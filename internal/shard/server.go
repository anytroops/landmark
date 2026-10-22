// Package shard serves Search over one partition: an immutable segment, or
// a manifest version's segment set (a base and its deltas, D42) that Swap
// replaces with a newer version while searches run. It keeps the version
// before the current one until its searches drain and a newer one replaces
// it, so a coordinator can pin every partition of a query to one version.
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
	"fmt"
	"math"
	"sync"
	"sync/atomic"
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

// Content is what a shard searches: a segment set with its index version
// and the partition's content checksum for that version (D26, D42).
type Content struct {
	Set      *segment.Set
	Version  uint64
	Checksum uint32
}

// Single wraps one segment as version 0, identified by its own CRC: a
// shard started on a segment file rather than a manifest.
func Single(seg *segment.Segment) (Content, error) {
	set, err := segment.NewSet([]*segment.Segment{seg})
	if err != nil {
		return Content{}, err
	}
	return Content{Set: set, Checksum: seg.Checksum()}, nil
}

// view is one served Content and the searches using it. A view is retired
// when a newer one replaces it and closed once retired with no searches.
type view struct {
	Content
	mu      sync.Mutex
	refs    int
	retired bool
	closed  bool
}

func (v *view) release() {
	v.mu.Lock()
	v.refs--
	done := v.retired && v.refs == 0 && !v.closed
	v.closed = v.closed || done
	v.mu.Unlock()
	if done {
		_ = v.Set.Close()
	}
}

func (v *view) retire() {
	v.mu.Lock()
	v.retired = true
	done := v.refs == 0 && !v.closed
	v.closed = v.closed || done
	v.mu.Unlock()
	if done {
		_ = v.Set.Close()
	}
}

// Server implements landmarkv1.ShardServiceServer.
type Server struct {
	landmarkv1.UnimplementedShardServiceServer
	cfg     Config
	pool    chan *worker
	metrics *metrics
	aux     bool   // triplet landmarks: fixed for the server's life
	part    uint32 // partition and partition count: fixed too
	nParts  uint32

	swapMu   sync.Mutex // serializes Swap
	cur      atomic.Pointer[view]
	previous atomic.Pointer[view] // the version before cur, until replaced
}

// New returns a Server over c. Metrics are registered on reg.
func New(c Content, cfg Config, reg prometheus.Registerer) (*Server, error) {
	if cfg.Workers < 1 || cfg.MaxQueueWait <= 0 {
		return nil, errors.New("shard: Workers must be >= 1 and MaxQueueWait > 0")
	}
	aux := c.Set.HasAux()
	pool := make(chan *worker, cfg.Workers)
	for range cfg.Workers {
		if aux {
			pool <- &worker{triplets: match.NewTripletMatcher()}
		} else {
			pool <- &worker{pairs: &match.Matcher{}}
		}
	}
	partition, nParts := c.Set.Partition()
	m, err := newMetrics(reg, partition)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, pool: pool, metrics: m, aux: aux, part: partition, nParts: nParts}
	s.cur.Store(&view{Content: c})
	m.version.Set(float64(c.Version))
	return s, nil
}

// Swap makes c the current content. Searches already running finish on the
// content they started with; the previous current version stays searchable
// (by version) until the next Swap, and each retired version is closed once
// its last search ends. c must be the same partition and landmark kind, at a
// newer version.
func (s *Server) Swap(c Content) error {
	s.swapMu.Lock()
	defer s.swapMu.Unlock()
	part, nParts := c.Set.Partition()
	cur := s.cur.Load()
	switch {
	case part != s.part || nParts != s.nParts:
		return fmt.Errorf("shard: swap to partition %d of %d, serving %d of %d", part, nParts, s.part, s.nParts)
	case c.Set.HasAux() != s.aux:
		return errors.New("shard: swap to another landmark kind")
	case c.Version <= cur.Version:
		return fmt.Errorf("shard: swap to version %d, serving %d", c.Version, cur.Version)
	}
	s.cur.Store(&view{Content: c})
	if old := s.previous.Swap(cur); old != nil {
		old.retire()
	}
	s.metrics.version.Set(float64(c.Version))
	return nil
}

// Version is the current content's index version.
func (s *Server) Version() uint64 { return s.cur.Load().Version }

// Close retires every view (closing each once its searches end).
func (s *Server) Close() {
	s.swapMu.Lock()
	defer s.swapMu.Unlock()
	if v := s.previous.Swap(nil); v != nil {
		v.retire()
	}
	s.cur.Load().retire()
}

// hold returns the view serving version (0: the current one) with a
// reference taken, or nil if this shard does not hold that version. Swap
// never retires the current view (it becomes the previous one), so a
// retired current view means the server is closed.
func (s *Server) hold(version uint64) *view {
	for _, p := range []*atomic.Pointer[view]{&s.cur, &s.previous} {
		v := p.Load()
		if v == nil || (version != 0 && v.Version != version) {
			continue
		}
		v.mu.Lock()
		ok := !v.retired
		if ok {
			v.refs++
		}
		v.mu.Unlock()
		if ok {
			return v
		}
		if version == 0 {
			return nil
		}
	}
	return nil
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
	triplets := s.aux
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

	v := s.hold(req.GetManifestVersion())
	if v == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "index version %d is not served here", req.GetManifestVersion())
	}
	defer v.release()

	w, err := s.acquire(ctx)
	s.metrics.queueWait.Observe(time.Since(start).Seconds())
	if err != nil {
		return nil, err
	}
	defer func() { s.pool <- w }()

	out := &landmarkv1.SearchResponse{PartitionId: s.part, ManifestVersion: v.Version}
	if triplets {
		err = s.searchTriplets(v.Set, w.triplets, req, topK, out)
	} else {
		err = s.searchPairs(v.Set, w.pairs, req, topK, out)
	}
	if err != nil {
		// Only a corrupt segment gets here; it passed validation at load, so
		// this is a bug or bad hardware, not bad input.
		return nil, status.Errorf(codes.Internal, "search: %v", err)
	}
	s.metrics.postings.Observe(float64(out.PostingsScanned))
	return out, nil
}

func (s *Server) searchPairs(set *segment.Set, m *match.Matcher, req *landmarkv1.SearchRequest, topK int, out *landmarkv1.SearchResponse) error {
	hashes, times := req.GetHashes(), req.GetTimes()
	query := make([]fingerprint.Landmark, len(hashes))
	for i := range hashes {
		query[i] = fingerprint.Landmark{Hash: hashes[i], T: times[i]}
	}
	res, err := m.Search(set, query, topK)
	if err != nil {
		return err
	}
	out.PostingsScanned = res.PostingsScanned
	out.Candidates = make([]*landmarkv1.Candidate, len(res.Candidates))
	for i, c := range res.Candidates {
		out.Candidates[i] = &landmarkv1.Candidate{
			TrackId: set.GlobalTrack(c.Track),
			Score:   c.Score,
			Offset:  c.Offset,
		}
	}
	return nil
}

func (s *Server) searchTriplets(set *segment.Set, m *match.TripletMatcher, req *landmarkv1.SearchRequest, topK int, out *landmarkv1.SearchResponse) error {
	hashes, times, spans, bands := req.GetHashes(), req.GetTimes(), req.GetSpans(), req.GetBands()
	query := make([]triplet.Landmark, len(hashes))
	for i := range hashes {
		query[i] = triplet.Landmark{Hash: hashes[i], T: times[i], Span: spans[i], Band: bands[i]}
	}
	res, err := m.Search(set, query, topK)
	if err != nil {
		return err
	}
	out.PostingsScanned = res.PostingsScanned
	out.Candidates = make([]*landmarkv1.Candidate, len(res.Candidates))
	for i, c := range res.Candidates {
		out.Candidates[i] = &landmarkv1.Candidate{
			TrackId: set.GlobalTrack(c.Track),
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

// Info reports what this shard serves: the current content and every
// version it can search. It does not take a worker, so it answers even
// when the shard is saturated.
func (s *Server) Info(context.Context, *landmarkv1.InfoRequest) (*landmarkv1.InfoResponse, error) {
	cur := s.hold(0)
	if cur == nil {
		return nil, status.Error(codes.Unavailable, "shard closed")
	}
	defer cur.release()
	resp := &landmarkv1.InfoResponse{
		PartitionId:   s.part,
		NPartitions:   s.nParts,
		NTracks:       cur.Set.NTracks(),
		NKeys:         cur.Set.NKeys(),
		NPostings:     cur.Set.NPostings(),
		SegmentCrc32C: cur.Checksum,
		FormatVersion: uint32(cur.Set.Version()),
		Versions:      []*landmarkv1.ServedVersion{{Version: cur.Version, Checksum: cur.Checksum}},
	}
	if prev := s.hold(s.previousVersion()); prev != nil && prev != cur {
		resp.Versions = append(resp.Versions, &landmarkv1.ServedVersion{Version: prev.Version, Checksum: prev.Checksum})
		prev.release()
	}
	return resp, nil
}

func (s *Server) previousVersion() uint64 {
	if v := s.previous.Load(); v != nil {
		return v.Version
	}
	return ^uint64(0) // no version has this number
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
