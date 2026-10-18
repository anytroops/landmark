// Package shard serves Search over one immutable segment (one partition).
//
// The segment's version picks the matcher: version 1 (pair landmarks) uses
// offset voting, version 2 (triplet landmarks, D34) the tempo-invariant
// triplet matcher, and requests must carry the matching landmark kind.
//
// Concurrency model (D23): a fixed pool of Matchers, one per worker (default
// one per CPU core). A request must take a Matcher before it runs. It waits
// until one is free, its own deadline passes, or MaxQueueWait passes,
// whichever comes first. Waiting past MaxQueueWait is rejected as
// RESOURCE_EXHAUSTED (load shedding): under overload the shard keeps a
// bounded number of searches on the CPU, so the ones it accepts stay fast,
// instead of running everything at once and slowing every request down.
package shard

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/match"
	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/triplet"
)

// Limits on a single request, so one client cannot monopolize a shard.
const (
	MaxLandmarks = 50_000 // ~10x a 10 s query with 4 shifts
	MaxTopK      = 1_000
	DefaultTopK  = 20 // must exceed the largest duplicate cluster (D14, D22)
)

// Config tunes a Server.
type Config struct {
	Workers      int           // concurrent searches; one matcher each
	MaxQueueWait time.Duration // longest a request waits for a free matcher
}

// worker is one search slot: the matcher kind the segment needs.
type worker struct {
	pairs    *match.Matcher
	triplets *match.TripletMatcher
}

// Server implements landmarkv1.ShardServiceServer.
type Server struct {
	landmarkv1.UnimplementedShardServiceServer
	seg     *segment.Segment
	cfg     Config
	pool    chan *worker
	metrics *metrics
}

// New returns a Server over seg. Metrics are registered on reg.
func New(seg *segment.Segment, cfg Config, reg prometheus.Registerer) (*Server, error) {
	if cfg.Workers < 1 || cfg.MaxQueueWait <= 0 {
		return nil, errors.New("shard: Workers must be >= 1 and MaxQueueWait > 0")
	}
	pool := make(chan *worker, cfg.Workers)
	for range cfg.Workers {
		if seg.HasAux() {
			pool <- &worker{triplets: match.NewTripletMatcher()}
		} else {
			pool <- &worker{pairs: &match.Matcher{}}
		}
	}
	partition, _ := seg.Partition()
	m, err := newMetrics(reg, partition)
	if err != nil {
		return nil, err
	}
	return &Server{seg: seg, cfg: cfg, pool: pool, metrics: m}, nil
}

// Search scores this partition's tracks against the query landmarks and
// returns the local top-k with catalog-wide track ids.
func (s *Server) Search(ctx context.Context, req *landmarkv1.SearchRequest) (*landmarkv1.SearchResponse, error) {
	start := time.Now()
	resp, err := s.search(ctx, req, start)
	s.metrics.observe(start, err)
	return resp, err
}

func (s *Server) search(ctx context.Context, req *landmarkv1.SearchRequest, start time.Time) (*landmarkv1.SearchResponse, error) {
	topK := int(req.GetTopK())
	hashes, times, spans, bands := req.GetHashes(), req.GetTimes(), req.GetSpans(), req.GetBands()
	triplets := s.seg.HasAux()
	switch {
	case len(hashes) != len(times):
		return nil, status.Errorf(codes.InvalidArgument, "%d hashes but %d times", len(hashes), len(times))
	case triplets && (len(spans) != len(hashes) || len(bands) != len(hashes)):
		return nil, status.Errorf(codes.InvalidArgument,
			"triplet index: %d hashes but %d spans and %d bands", len(hashes), len(spans), len(bands))
	case !triplets && (len(spans) != 0 || len(bands) != 0):
		return nil, status.Error(codes.InvalidArgument, "pair index: spans and bands are for a triplet index")
	case len(hashes) == 0:
		return nil, status.Error(codes.InvalidArgument, "no landmarks")
	case len(hashes) > MaxLandmarks:
		return nil, status.Errorf(codes.InvalidArgument, "%d landmarks exceeds limit %d", len(hashes), MaxLandmarks)
	case topK > MaxTopK:
		return nil, status.Errorf(codes.InvalidArgument, "top_k %d exceeds limit %d", topK, MaxTopK)
	case topK == 0:
		topK = DefaultTopK
	}

	w, err := s.acquire(ctx)
	s.metrics.queueWait.Observe(time.Since(start).Seconds())
	if err != nil {
		return nil, err
	}
	defer func() { s.pool <- w }()

	partition, _ := s.seg.Partition()
	out := &landmarkv1.SearchResponse{PartitionId: partition}
	if triplets {
		err = s.searchTriplets(w.triplets, req, topK, out)
	} else {
		err = s.searchPairs(w.pairs, req, topK, out)
	}
	if err != nil {
		// Only a corrupt segment gets here; it passed validation at load, so
		// this is a bug or bad hardware, not bad input.
		return nil, status.Errorf(codes.Internal, "search: %v", err)
	}
	s.metrics.postings.Observe(float64(out.PostingsScanned))
	return out, nil
}

func (s *Server) searchPairs(m *match.Matcher, req *landmarkv1.SearchRequest, topK int, out *landmarkv1.SearchResponse) error {
	hashes, times := req.GetHashes(), req.GetTimes()
	query := make([]fingerprint.Landmark, len(hashes))
	for i := range hashes {
		query[i] = fingerprint.Landmark{Hash: hashes[i], T: times[i]}
	}
	res, err := m.Search(s.seg, query, topK)
	if err != nil {
		return err
	}
	out.PostingsScanned = res.PostingsScanned
	out.Candidates = make([]*landmarkv1.Candidate, len(res.Candidates))
	for i, c := range res.Candidates {
		out.Candidates[i] = &landmarkv1.Candidate{
			TrackId: s.seg.GlobalTrack(c.Track),
			Score:   c.Score,
			Offset:  c.Offset,
		}
	}
	return nil
}

func (s *Server) searchTriplets(m *match.TripletMatcher, req *landmarkv1.SearchRequest, topK int, out *landmarkv1.SearchResponse) error {
	hashes, times, spans, bands := req.GetHashes(), req.GetTimes(), req.GetSpans(), req.GetBands()
	query := make([]triplet.Landmark, len(hashes))
	for i := range hashes {
		query[i] = triplet.Landmark{Hash: hashes[i], T: times[i], Span: spans[i], Band: bands[i]}
	}
	res, err := m.Search(s.seg, query, topK)
	if err != nil {
		return err
	}
	out.PostingsScanned = res.PostingsScanned
	out.Candidates = make([]*landmarkv1.Candidate, len(res.Candidates))
	for i, c := range res.Candidates {
		out.Candidates[i] = &landmarkv1.Candidate{
			TrackId: s.seg.GlobalTrack(c.Track),
			Score:   c.Score,
			Offset:  roundFrames(c.Offset),
			Scale:   float32(c.Scale),
		}
	}
	return nil
}

// roundFrames rounds a fitted offset to whole frames, clamped to int32.
func roundFrames(x float64) int32 {
	return int32(max(min(math.Round(x), math.MaxInt32), math.MinInt32))
}

// Info reports which segment this shard serves. It does not take a worker,
// so it answers even when the shard is saturated.
func (s *Server) Info(context.Context, *landmarkv1.InfoRequest) (*landmarkv1.InfoResponse, error) {
	partition, nParts := s.seg.Partition()
	return &landmarkv1.InfoResponse{
		PartitionId:   partition,
		NPartitions:   nParts,
		NTracks:       s.seg.NTracks(),
		NKeys:         uint64(s.seg.NKeys()),
		NPostings:     s.seg.NPostings(),
		SegmentCrc32C: s.seg.Checksum(),
		FormatVersion: uint32(s.seg.Version()),
	}, nil
}

// acquire takes a worker, waiting at most until ctx ends or MaxQueueWait.
func (s *Server) acquire(ctx context.Context) (*worker, error) {
	select { // fast path: a worker is free, no timer needed
	case w := <-s.pool:
		return w, nil
	default:
	}
	timer := time.NewTimer(s.cfg.MaxQueueWait)
	defer timer.Stop()
	select {
	case w := <-s.pool:
		return w, nil
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	case <-timer.C:
		return nil, status.Errorf(codes.ResourceExhausted,
			"overloaded: no free worker within %v", s.cfg.MaxQueueWait)
	}
}
