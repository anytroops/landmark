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
// It is available for traffic when it is verified (serves the expected
// segment, D26), healthy (last health check said SERVING), and not ejected
// (D25). The health loop owns verified and healthy; request outcomes drive
// ejection; mu guards all three.
type replica struct {
	addr      string
	partition uint32
	conn      *grpc.ClientConn
	shard     landmarkv1.ShardServiceClient
	health    healthpb.HealthClient

	outstanding atomic.Int64 // in-flight Searches, for least-outstanding picks

	mu           sync.Mutex
	verified     bool
	healthy      bool
	consecFails  int
	ejected      bool
	ejectedUntil time.Time
	lastProblem  string // last reported verification problem, to log changes only
}

func (r *replica) available() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.verified && r.healthy && !r.ejected
}

func (r *replica) isVerified() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.verified
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
	r.verified = false
	r.mu.Unlock()
	slog.Warn("replica excluded", "addr", r.addr, "partition", r.partition, "reason", reason)
}

// check runs one health round for this replica: verify its segment identity
// with Info, then read its serving status. A passing check after the
// minimum ejection time reinstates an ejected replica.
func (r *replica) check(ctx context.Context, wantCRC uint32, nParts uint32, now func() time.Time, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	verified, problem := false, ""
	info, err := r.shard.Info(ctx, &landmarkv1.InfoRequest{})
	switch {
	case err != nil:
		problem = fmt.Sprintf("info: %v", err)
	case info.GetPartitionId() != r.partition || info.GetNPartitions() != nParts:
		problem = fmt.Sprintf("serves partition %d of %d, want %d of %d",
			info.GetPartitionId(), info.GetNPartitions(), r.partition, nParts)
	case info.GetSegmentCrc32C() != wantCRC:
		problem = fmt.Sprintf("segment crc32c %08x, want %08x", info.GetSegmentCrc32C(), wantCRC)
	default:
		verified = true
	}

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
	r.verified, r.healthy = verified, healthy
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
