// Package local serves an index inside one process, for tools that run on
// one machine (D54: the set reconstructor works from the DJ's own library).
// Each partition gets a shard server on a loopback port and the coordinator
// runs in the process: the code a cluster runs, so a search gets the answer
// a cluster gives.
package local

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/cluster"
	"github.com/anytroops/landmark/internal/coordinator"
	"github.com/anytroops/landmark/internal/manifest"
	"github.com/anytroops/landmark/internal/shard"
)

// Options configure an Index.
type Options struct {
	// StopFraction turns on the coordinator's stop list (D30); the hybrid
	// tracklister was calibrated with 0.04.
	StopFraction float64
	// Deadline bounds each search; a shard queues a search this long rather
	// than shed it, since a local tool would rather wait than lose a window.
	Deadline time.Duration
	// Workers is each shard's matcher pool (0: one per CPU).
	Workers int
}

// Index is an index directory served in process. Identify is safe for
// concurrent use.
type Index struct {
	coord  *coordinator.Coordinator
	stop   context.CancelFunc
	grpcs  []*grpc.Server
	shards []*shard.Server
}

// Open serves the newest version of the index in dir.
func Open(ctx context.Context, dir string, o Options) (_ *Index, err error) {
	if o.Deadline <= 0 {
		return nil, errors.New("local: deadline must be positive")
	}
	if o.Workers == 0 {
		o.Workers = runtime.NumCPU()
	}
	m, err := manifest.Latest(dir)
	if err != nil {
		return nil, err
	}
	ix := &Index{}
	defer func() {
		if err != nil {
			ix.Close()
		}
	}()
	parts := make([]cluster.Partition, m.NPartitions)
	for p := range m.NPartitions {
		sum, err := m.Checksum(p)
		if err != nil {
			return nil, err
		}
		set, err := manifest.Open(dir, m, p)
		if err != nil {
			return nil, err
		}
		srv, err := shard.New(shard.Content{Set: set, Version: m.Version, Checksum: sum},
			shard.Config{Workers: o.Workers, MaxQueueWait: o.Deadline}, prometheus.NewRegistry())
		if err != nil {
			_ = set.Close()
			return nil, err
		}
		ix.shards = append(ix.shards, srv)
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		gs := grpc.NewServer()
		landmarkv1.RegisterShardServiceServer(gs, srv)
		hs := health.NewServer()
		hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		hs.SetServingStatus(landmarkv1.ShardService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
		healthpb.RegisterHealthServer(gs, hs)
		ix.grpcs = append(ix.grpcs, gs)
		go func() { _ = gs.Serve(lis) }()
		parts[p] = cluster.Partition{ID: p, Replicas: []string{lis.Addr().String()}}
	}
	// t_abs and t_ratio only set the response's Matched flag, which the
	// tracklisters do not read; these are the eval harness's values.
	cl, err := cluster.New(cluster.Config{IndexDir: dir, DeadlineMs: int(o.Deadline.Milliseconds()),
		TAbs: 30, TRatio: 1, StopFraction: o.StopFraction, Partitions: parts})
	if err != nil {
		return nil, err
	}
	if ix.coord, err = coordinator.New(cl, coordinator.Options{}, prometheus.NewRegistry()); err != nil {
		return nil, err
	}
	var cctx context.Context
	cctx, ix.stop = context.WithCancel(ctx)
	ix.coord.Start(cctx) // the first health round checks every shard's identity (D26)
	return ix, nil
}

// Identify searches the index, as a cluster's coordinator does.
func (ix *Index) Identify(ctx context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
	resp, err := ix.coord.Identify(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("local index: %w", err)
	}
	return resp, nil
}

// Close stops the coordinator and the shards.
func (ix *Index) Close() {
	if ix.stop != nil {
		ix.stop()
	}
	if ix.coord != nil {
		_ = ix.coord.Close()
	}
	for _, gs := range ix.grpcs {
		gs.Stop()
	}
	for _, s := range ix.shards {
		s.Close()
	}
}
