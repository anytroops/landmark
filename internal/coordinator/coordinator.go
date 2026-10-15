// Package coordinator answers Identify by scatter-gather over partitions.
//
// Each query goes to one replica of every partition concurrently under one
// deadline. Per partition (D24): pick the available replica with the fewest
// requests in flight; give the first attempt half the remaining budget when
// another replica exists, so a hung replica cannot consume the whole
// deadline; on failure or per-attempt timeout, try the other replica with
// whatever budget is left. Replicas are ejected after consecutive failures
// and reinstated by health checks (D25), and are only used once verified to
// serve the expected segment (D26). Partitions are disjoint by track, so the
// global top-k is an exact merge of per-partition top-k lists. A partition
// that never answers makes the response partial rather than an error.
package coordinator

import (
	"cmp"
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/cluster"
	"github.com/anytroops/landmark/internal/match"
	"github.com/anytroops/landmark/internal/shard"
)

const (
	// DefaultTopK is used when IdentifyRequest.top_k is 0.
	DefaultTopK = 10
	// MaxTopK is the largest IdentifyRequest.top_k accepted.
	MaxTopK = 100
	// shardTopK is what each partition is asked for. It must exceed the
	// largest duplicate cluster so the runner-up outside the top-1's cluster
	// survives the merge (D14, D22).
	shardTopK = 20
)

// Options tunes failure handling. Zero fields take defaults.
type Options struct {
	HealthInterval   time.Duration // time between health rounds
	HealthTimeout    time.Duration // per replica per round
	EjectAfter       int           // consecutive failures before ejection
	EjectFor         time.Duration // minimum time an ejected replica sits out
	MinAttemptBudget time.Duration // do not start an attempt with less time left
	Now              func() time.Time
	DialOptions      []grpc.DialOption
}

func (o Options) withDefaults() Options {
	o.HealthInterval = cmp.Or(o.HealthInterval, time.Second)
	o.HealthTimeout = cmp.Or(o.HealthTimeout, 250*time.Millisecond)
	o.EjectAfter = cmp.Or(o.EjectAfter, 3)
	o.EjectFor = cmp.Or(o.EjectFor, 5*time.Second)
	o.MinAttemptBudget = cmp.Or(o.MinAttemptBudget, 5*time.Millisecond)
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.DialOptions == nil {
		o.DialOptions = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	return o
}

// Coordinator implements landmarkv1.CoordinatorServiceServer.
type Coordinator struct {
	landmarkv1.UnimplementedCoordinatorServiceServer
	cl      *cluster.Cluster
	opts    Options
	parts   [][]*replica // parts[p] = replicas of partition p
	metrics *metrics
	stopped chan struct{}
	wg      sync.WaitGroup
}

// New connects to every replica. Connections are lazy, so New does no I/O;
// call Start before serving.
func New(cl *cluster.Cluster, opts Options, reg prometheus.Registerer) (*Coordinator, error) {
	opts = opts.withDefaults()
	c := &Coordinator{cl: cl, opts: opts, parts: make([][]*replica, len(cl.Partitions)), stopped: make(chan struct{})}
	for p, part := range cl.Partitions {
		for _, addr := range part.Replicas {
			conn, err := grpc.NewClient(addr, opts.DialOptions...)
			if err != nil {
				_ = c.Close()
				return nil, err
			}
			c.parts[p] = append(c.parts[p], &replica{
				addr: addr, partition: uint32(p), conn: conn,
				shard: landmarkv1.NewShardServiceClient(conn), health: healthpb.NewHealthClient(conn),
			})
		}
	}
	m, err := newMetrics(reg, c)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	c.metrics = m
	return c, nil
}

// Start runs one health round synchronously, so replicas are verified before
// the first query, then keeps checking every HealthInterval until Close.
func (c *Coordinator) Start(ctx context.Context) {
	c.healthRound(ctx)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		t := time.NewTicker(c.opts.HealthInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.healthRound(ctx)
			case <-ctx.Done():
				return
			case <-c.stopped:
				return
			}
		}
	}()
}

func (c *Coordinator) healthRound(ctx context.Context) {
	var wg sync.WaitGroup
	for p, reps := range c.parts {
		for _, r := range reps {
			wg.Go(func() {
				r.check(ctx, c.cl.ExpectedCRC[p], uint32(len(c.parts)), c.opts.Now, c.opts.HealthTimeout)
			})
		}
	}
	wg.Wait()
}

// Close stops the health loop and closes every connection.
func (c *Coordinator) Close() error {
	select {
	case <-c.stopped:
	default:
		close(c.stopped)
	}
	c.wg.Wait()
	var errs []error
	for _, reps := range c.parts {
		for _, r := range reps {
			errs = append(errs, r.conn.Close())
		}
	}
	return errors.Join(errs...)
}

// Identify scatters the query to every partition and merges the answers.
func (c *Coordinator) Identify(ctx context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
	start := c.opts.Now()
	resp, err := c.identify(ctx, req)
	c.metrics.observeIdentify(start, c.opts.Now(), resp, err)
	return resp, err
}

func (c *Coordinator) identify(ctx context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
	n := uint32(len(c.parts))
	topK := int(req.GetTopK())
	hashes, times := req.GetHashes(), req.GetTimes()
	switch {
	case len(hashes) != len(times):
		return nil, status.Errorf(codes.InvalidArgument, "%d hashes but %d times", len(hashes), len(times))
	case len(hashes) == 0:
		// Silent audio fingerprints to nothing: a complete answer of "no match".
		return &landmarkv1.IdentifyResponse{PartitionsResponded: n, PartitionsTotal: n}, nil
	case len(hashes) > shard.MaxLandmarks:
		return nil, status.Errorf(codes.InvalidArgument, "%d landmarks exceeds limit %d", len(hashes), shard.MaxLandmarks)
	case topK > MaxTopK:
		return nil, status.Errorf(codes.InvalidArgument, "top_k %d exceeds limit %d", topK, MaxTopK)
	case topK == 0:
		topK = DefaultTopK
	}

	// The caller's deadline wins if it is earlier than ours.
	ctx, cancel := context.WithTimeout(ctx, c.cl.Deadline())
	defer cancel()
	// The same arrays go to every partition; the request is only read.
	shardReq := &landmarkv1.SearchRequest{Hashes: hashes, Times: times, TopK: shardTopK}

	results := make([]*landmarkv1.SearchResponse, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for p := range n {
		wg.Go(func() { results[p], errs[p] = c.searchPartition(ctx, p, shardReq) })
	}
	wg.Wait()

	var merged []*landmarkv1.Candidate
	responded := uint32(0)
	for p := range n {
		if errs[p] != nil {
			if status.Code(errs[p]) == codes.InvalidArgument {
				return nil, errs[p] // the request is bad on every replica
			}
			continue
		}
		responded++
		merged = append(merged, results[p].GetCandidates()...)
	}
	if responded == 0 {
		if err := ctx.Err(); err != nil { // out of time, or the caller left
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Errorf(codes.Unavailable, "no partition answered: %v", errors.Join(errs...))
	}

	slices.SortFunc(merged, func(a, b *landmarkv1.Candidate) int {
		return cmp.Or(cmp.Compare(b.GetScore(), a.GetScore()), cmp.Compare(a.GetTrackId(), b.GetTrackId()))
	})
	cands := make([]match.Candidate, len(merged))
	for i, m := range merged {
		cands[i] = match.Candidate{Track: m.GetTrackId(), Score: m.GetScore(), Offset: m.GetOffset()}
	}
	matched := false
	if len(cands) > 0 {
		runnerUp := match.RunnerUp(cands, c.clusterOf)
		matched = match.Accept(cands[0].Score, runnerUp, c.cl.TAbs, c.cl.TRatio)
	}
	return &landmarkv1.IdentifyResponse{
		Candidates:          merged[:min(topK, len(merged))],
		Matched:             matched,
		Partial:             responded < n,
		PartitionsResponded: responded,
		PartitionsTotal:     n,
	}, nil
}

func (c *Coordinator) clusterOf(track uint32) uint32 {
	if int(track) < len(c.cl.Catalog) {
		return c.cl.Catalog[track].Cluster
	}
	return track // unknown id: its own cluster
}

// searchPartition gets partition p's top-k from one of its replicas,
// retrying once on another replica within ctx's deadline (D24).
func (c *Coordinator) searchPartition(ctx context.Context, p uint32, req *landmarkv1.SearchRequest) (*landmarkv1.SearchResponse, error) {
	deadline, _ := ctx.Deadline()
	var tried *replica
	lastErr := status.Error(codes.Unavailable, "no usable replica")
	for attempt := range len(c.parts[p]) {
		r, alternative := c.pick(p, tried)
		if r == nil {
			break
		}
		remaining := deadline.Sub(c.opts.Now())
		if remaining < c.opts.MinAttemptBudget {
			break
		}
		budget := remaining
		if alternative && remaining/2 >= c.opts.MinAttemptBudget {
			budget = remaining / 2 // leave room to retry elsewhere
		}
		if attempt > 0 {
			c.metrics.retries.Inc()
		}

		actx, cancel := context.WithTimeout(ctx, budget)
		r.outstanding.Add(1)
		resp, err := r.shard.Search(actx, req)
		r.outstanding.Add(-1)
		cancel()

		switch {
		case err == nil && resp.GetPartitionId() != p:
			r.markUnverified("answered for the wrong partition")
			c.metrics.attempt(r, "wrong_partition")
			lastErr = status.Errorf(codes.Internal, "replica %s answered for partition %d", r.addr, resp.GetPartitionId())
		case err == nil:
			r.recordSuccess()
			c.metrics.attempt(r, "ok")
			return resp, nil
		case ctx.Err() != nil:
			// The whole query is out of time (or the caller left); this is
			// not the replica's fault, and there is nothing left to retry with.
			c.metrics.attempt(r, "query_deadline")
			return nil, status.FromContextError(ctx.Err()).Err()
		default:
			code := status.Code(err)
			c.metrics.attempt(r, code.String())
			lastErr = err
			switch code {
			case codes.InvalidArgument:
				return nil, err // same answer from every replica
			case codes.ResourceExhausted:
				// Overload is not failure: ejecting a busy replica would push
				// its load onto its partner and cascade. Just try the other.
			default:
				if r.recordFailure(c.opts.Now(), c.opts) {
					c.metrics.ejections.WithLabelValues(r.addr).Inc()
				}
			}
		}
		tried = r
	}
	return nil, lastErr
}

// pick returns the replica of partition p to try next, skipping exclude,
// and whether another candidate would remain for a retry.
//
// Preference: available replicas (verified, healthy, not ejected) by fewest
// in-flight requests, ties broken randomly. If none is available, fall back
// to any verified replica ("panic mode"): health state can lag reality, and
// trying a possibly-recovered replica beats giving up on the partition.
// Unverified replicas are never used; a partial answer is better than an
// answer from the wrong index.
func (c *Coordinator) pick(p uint32, exclude *replica) (*replica, bool) {
	var avail, verified []*replica
	for _, r := range c.parts[p] {
		if r == exclude {
			continue
		}
		if r.available() {
			avail = append(avail, r)
		} else if r.isVerified() {
			verified = append(verified, r)
		}
	}
	pool := avail
	if len(pool) == 0 {
		pool = verified
	}
	if len(pool) == 0 {
		return nil, false
	}
	best := pool[0]
	ties := 1
	for _, r := range pool[1:] {
		switch o, b := r.outstanding.Load(), best.outstanding.Load(); {
		case o < b:
			best, ties = r, 1
		case o == b:
			ties++
			if rand.IntN(ties) == 0 { // reservoir: uniform among ties
				best = r
			}
		}
	}
	return best, len(avail)+len(verified) > 1
}
