// Command shard serves Search for one partition over gRPC: one segment file
// (-segment), or an index directory's partition (-index, -partition), whose
// newest manifest version it serves and polls for, swapping in each new
// version (songs added while serving, D42) without a restart.
//
// Ports: -listen for gRPC (Search + grpc.health.v1), -admin for Prometheus
// /metrics and pprof. The admin port is for operators only and must never be
// exposed publicly.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/manifest"
	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/shard"
)

func main() {
	segPath := flag.String("segment", "", "segment file to serve (or -index)")
	indexDir := flag.String("index", "", "index directory: serve its newest manifest version (D42)")
	partition := flag.Uint("partition", 0, "with -index: the partition to serve")
	poll := flag.Duration("poll", time.Second, "with -index: how often to look for a newer version")
	listen := flag.String("listen", ":7001", "gRPC listen address")
	admin := flag.String("admin", "127.0.0.1:9101", "metrics and pprof address")
	workers := flag.Int("workers", runtime.NumCPU(), "concurrent searches")
	maxWait := flag.Duration("max-queue-wait", 100*time.Millisecond, "reject requests waiting longer than this for a worker")
	flag.Parse()
	if (*segPath == "") == (*indexDir == "") {
		slog.Error("give -segment or -index")
		os.Exit(2)
	}
	src := source{segment: *segPath, index: *indexDir, partition: uint32(*partition), poll: *poll}
	if err := run(src, *listen, *admin, shard.Config{Workers: *workers, MaxQueueWait: *maxWait}); err != nil {
		slog.Error("shard failed", "err", err)
		os.Exit(1)
	}
}

// source is where the shard's content comes from.
type source struct {
	segment   string
	index     string
	partition uint32
	poll      time.Duration
}

// load opens the content to serve: the segment file, or the index's newest
// version newer than after.
func (s source) load(after uint64) (shard.Content, bool, error) {
	if s.segment != "" {
		seg, err := segment.Open(s.segment)
		if err != nil {
			return shard.Content{}, false, err
		}
		c, err := shard.Single(seg)
		return c, true, err
	}
	m, err := manifest.Latest(s.index)
	if err != nil || m.Version <= after {
		return shard.Content{}, false, err
	}
	sum, err := m.Checksum(s.partition)
	if err != nil {
		return shard.Content{}, false, err
	}
	set, err := manifest.Open(s.index, m, s.partition)
	if err != nil {
		return shard.Content{}, false, err
	}
	return shard.Content{Set: set, Version: m.Version, Checksum: sum}, true, nil
}

// follow swaps in each newer index version until ctx ends.
func follow(ctx context.Context, src source, srv *shard.Server, log *slog.Logger) {
	t := time.NewTicker(src.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		start := time.Now()
		c, ok, err := src.load(srv.Version())
		if err != nil {
			log.Warn("new index version not loaded", "err", err) // retried next tick
			continue
		}
		if !ok {
			continue
		}
		if err := srv.Swap(c); err != nil {
			_ = c.Set.Close()
			log.Error("index version refused", "version", c.Version, "err", err)
			continue
		}
		log.Info("index version swapped in", "version", c.Version, "segments", len(c.Set.Segments()),
			"postings", c.Set.NPostings(), "load_ms", time.Since(start).Milliseconds())
	}
}

func run(src source, listen, admin string, cfg shard.Config) error {
	start := time.Now()
	content, _, err := src.load(0)
	if err != nil {
		return err
	}
	part, nParts := content.Set.Partition()
	log := slog.With("partition", part, "n_partitions", nParts)
	log.Info("index loaded", "segment", src.segment, "index", src.index, "version", content.Version,
		"segments", len(content.Set.Segments()), "keys", content.Set.NKeys(), "postings", content.Set.NPostings(),
		"format_version", content.Set.Version(), "load_ms", time.Since(start).Milliseconds())

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	srv, err := shard.New(content, cfg, reg)
	if err != nil {
		return err
	}

	gs := grpc.NewServer()
	landmarkv1.RegisterShardServiceServer(gs, srv)
	hs := health.NewServer()
	healthpb.RegisterHealthServer(gs, hs)

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	adminSrv := &http.Server{Addr: admin, Handler: adminMux(reg), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("admin server", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if src.index != "" {
		go follow(ctx, src, srv, log)
	}
	shutdownDone := make(chan struct{})
	forced := false // written before shutdownDone closes, read after: no race
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		// Report NOT_SERVING first so health-checking clients route away,
		// then finish in-flight requests (bounded) and exit.
		log.Info("shutting down")
		hs.Shutdown()
		done := make(chan struct{})
		go func() { gs.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			gs.Stop()
			forced = true
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = adminSrv.Shutdown(shutdownCtx)
	}()

	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	hs.SetServingStatus(landmarkv1.ShardService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	log.Info("serving", "grpc", lis.Addr().String(), "admin", admin, "workers", cfg.Workers,
		"max_queue_wait", cfg.MaxQueueWait)
	err = gs.Serve(lis)
	// Serve returns as soon as shutdown begins, while in-flight Searches may
	// still be reading the mapped segment. Wait for GracefulStop to drain them
	// before unmapping; unmapping under a running reader is a segfault, not an
	// error. After a forced Stop, handlers may still be running, so leave the
	// mapping for the OS to reclaim at exit.
	stop()
	<-shutdownDone
	if !forced {
		srv.Close()
	}
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

func adminMux(reg *prometheus.Registry) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	return mux
}
