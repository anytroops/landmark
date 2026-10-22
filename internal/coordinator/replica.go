package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
)

// replica is one shard server holding a copy of one partition.
//
// It is available for traffic when it is verified (serves an index version
// the coordinator knows, with that version's content, D26 and D42),
// healthy (last health check said SERVING), and not ejected (D25). The
// health loop owns served and healthy; request outcomes drive ejection; mu
// guards them.
type replica struct {
	addr      string
	partition uint32
	conn      *grpc.ClientConn
	shard     landmarkv1.ShardServiceClient
	health    healthpb.HealthClient

	outstanding atomic.Int64 // in-flight Searches, for least-outstanding picks

	mu           sync.Mutex
	served       map[uint64]bool // verified index versions it can search
	healthy      bool
	consecFails  int
	ejected      bool
	ejectedUntil time.Time
	lastProblem  string // last reported verification problem, to log changes only
}

func (r *replica) available() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.served) > 0 && r.healthy && !r.ejected
}

func (r *replica) isVerified() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.served) > 0
}

// serves reports whether the replica was verified to search version v.
func (r *replica) serves(v uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.served[v]
}

// versions returns the verified versions it serves.
func (r *replica) versions() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uint64, 0, len(r.served))
	for v := range r.served {
		out = append(out, v)
	}
	return out
}

func (r *replica) recordSuccess() {
	r.mu.Lock()
	r.consecFails = 0
	r.mu.Unlock()
}

// recordFailure counts a failed request and reports whether it just caused
// an ejection.
func (r *replica) recordFailure(now time.Time, opts Options) (ejectedNow bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consecFails++
	if !r.ejected && r.consecFails >= opts.EjectAfter {
		r.ejected = true
		r.ejectedUntil = now.Add(opts.EjectFor)
		return true
	}
	return false
}

// markUnverified is called when a response proves the replica serves the
// wrong partition; it stays out until the health loop re-verifies it.
func (r *replica) markUnverified(reason string) {
	r.mu.Lock()
	r.served = nil
	r.mu.Unlock()
	slog.Warn("replica excluded", "addr", r.addr, "partition", r.partition, "reason", reason)
}

// check runs one health round for this replica: verify which index
// versions it serves with Info (each one's checksum must equal what the
// coordinator expects for that version and partition), then read its
// serving status. A passing check after the minimum ejection time
// reinstates an ejected replica.
func (r *replica) check(ctx context.Context, expect func(version uint64, p uint32) (uint32, bool), nParts uint32,
	now func() time.Time, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	served, problem := map[uint64]bool{}, ""
	info, err := r.shard.Info(ctx, &landmarkv1.InfoRequest{})
	switch {
	case err != nil:
		problem = fmt.Sprintf("info: %v", err)
	case info.GetPartitionId() != r.partition || info.GetNPartitions() != nParts:
		problem = fmt.Sprintf("serves partition %d of %d, want %d of %d",
			info.GetPartitionId(), info.GetNPartitions(), r.partition, nParts)
	default:
		versions := info.GetVersions()
		if len(versions) == 0 { // a shard from before versions: its one segment
			versions = []*landmarkv1.ServedVersion{{Checksum: info.GetSegmentCrc32C()}}
		}
		for _, sv := range versions {
			want, ok := expect(sv.GetVersion(), r.partition)
			switch {
			case !ok:
				problem = fmt.Sprintf("serves index version %d, unknown here", sv.GetVersion())
			case sv.GetChecksum() != want:
				problem = fmt.Sprintf("version %d checksum %08x, want %08x", sv.GetVersion(), sv.GetChecksum(), want)
			default:
				served[sv.GetVersion()] = true
			}
		}
		if len(served) > 0 {
			problem = "" // a version it can serve correctly is enough
		}
	}
	verified := len(served) > 0

	healthy := false
	if verified {
		resp, err := r.health.Check(ctx, &healthpb.HealthCheckRequest{
			Service: landmarkv1.ShardService_ServiceDesc.ServiceName,
		})
		healthy = err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING
		if !healthy && problem == "" {
			problem = fmt.Sprintf("not serving (err=%v)", err)
		}
	}

	r.mu.Lock()
	r.served, r.healthy = served, healthy
	if healthy && r.ejected && !now().Before(r.ejectedUntil) {
		r.ejected, r.consecFails = false, 0
	}
	changed := problem != r.lastProblem
	r.lastProblem = problem
	r.mu.Unlock()

	if changed {
		if problem == "" {
			slog.Info("replica ok", "addr", r.addr, "partition", r.partition)
		} else {
			slog.Warn("replica unavailable", "addr", r.addr, "partition", r.partition, "reason", problem)
		}
	}
}
