// Package shard serves Search over one immutable segment (one partition).
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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/match"
	"github.com/anytroops/landmark/internal/segment"
)

// Limits on a single request, so one client cannot monopolize a shard.
const (
	MaxLandmarks = 50_000 // ~10x a 10 s query with 4 shifts
	MaxTopK      = 1_000
	DefaultTopK  = 20 // must exceed the largest duplicate cluster (D14, D22)
)

// Config tunes a Server.
type Config struct {
	Workers      int           // concurrent searches; one Matcher each
	MaxQueueWait time.Duration // longest a request waits for a free Matcher
}

// Server implements landmarkv1.ShardServiceServer.
type Server struct {
	landmarkv1.UnimplementedShardServiceServer
	seg     *segment.Segment
	cfg     Config
	pool    chan *match.Matcher
	metrics *metrics
}

// New returns a Server over seg. Metrics are registered on reg.
func New(seg *segment.Segment, cfg Config, reg prometheus.Registerer) (*Server, error) {
	if cfg.Workers < 1 || cfg.MaxQueueWait <= 0 {
		return nil, errors.New("shard: Workers must be >= 1 and MaxQueueWait > 0")
	}
	pool := make(chan *match.Matcher, cfg.Workers)
	for range cfg.Workers {
		pool <- &match.Matcher{}
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
	switch {
	case len(req.GetLandmarks()) == 0:
		return nil, status.Error(codes.InvalidArgument, "no landmarks")
	case len(req.GetLandmarks()) > MaxLandmarks:
		return nil, status.Errorf(codes.InvalidArgument, "%d landmarks exceeds limit %d", len(req.GetLandmarks()), MaxLandmarks)
	case topK > MaxTopK:
		return nil, status.Errorf(codes.InvalidArgument, "top_k %d exceeds limit %d", topK, MaxTopK)
	case topK == 0:
		topK = DefaultTopK
	}

	m, err := s.acquire(ctx)
	s.metrics.queueWait.Observe(time.Since(start).Seconds())
	if err != nil {
		return nil, err
	}
	defer func() { s.pool <- m }()

	query := make([]fingerprint.Landmark, len(req.GetLandmarks()))
	for i, l := range req.GetLandmarks() {
		query[i] = fingerprint.Landmark{Hash: l.GetHash(), T: l.GetT()}
	}
	res, err := m.Search(s.seg, query, topK)
	if err != nil {
		// Only a corrupt segment gets here; it passed validation at load, so
		// this is a bug or bad hardware, not bad input.
		return nil, status.Errorf(codes.Internal, "search: %v", err)
	}
	s.metrics.postings.Observe(float64(res.PostingsScanned))

	partition, _ := s.seg.Partition()
	out := &landmarkv1.SearchResponse{
		PartitionId:     partition,
		PostingsScanned: res.PostingsScanned,
		Candidates:      make([]*landmarkv1.Candidate, len(res.Candidates)),
	}
	for i, c := range res.Candidates {
		out.Candidates[i] = &landmarkv1.Candidate{
			TrackId: s.seg.GlobalTrack(c.Track),
			Score:   c.Score,
			Offset:  c.Offset,
		}
	}
	return out, nil
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

// acquire takes a Matcher, waiting at most until ctx ends or MaxQueueWait.
func (s *Server) acquire(ctx context.Context) (*match.Matcher, error) {
	select { // fast path: a Matcher is free, no timer needed
	case m := <-s.pool:
		return m, nil
	default:
	}
	timer := time.NewTimer(s.cfg.MaxQueueWait)
	defer timer.Stop()
	select {
	case m := <-s.pool:
		return m, nil
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	case <-timer.C:
		return nil, status.Errorf(codes.ResourceExhausted,
			"overloaded: no free worker within %v", s.cfg.MaxQueueWait)
	}
}
