// Command shard serves Search for one partition's segment over gRPC.
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
	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/shard"
)

func main() {
	segPath := flag.String("segment", "", "segment file to serve (required)")
	listen := flag.String("listen", ":7001", "gRPC listen address")
	admin := flag.String("admin", "127.0.0.1:9101", "metrics and pprof address")
	workers := flag.Int("workers", runtime.NumCPU(), "concurrent searches")
	maxWait := flag.Duration("max-queue-wait", 100*time.Millisecond, "reject requests waiting longer than this for a worker")
	flag.Parse()
	if *segPath == "" {
		slog.Error("-segment is required")
		os.Exit(2)
	}
	if err := run(*segPath, *listen, *admin, shard.Config{Workers: *workers, MaxQueueWait: *maxWait}); err != nil {
		slog.Error("shard failed", "err", err)
		os.Exit(1)
	}
}

func run(segPath, listen, admin string, cfg shard.Config) error {
	start := time.Now()
	seg, err := segment.Open(segPath)
	if err != nil {
		return err
	}
	part, nParts := seg.Partition()
	log := slog.With("partition", part, "n_partitions", nParts)
	log.Info("segment loaded", "path", segPath, "keys", seg.NKeys(), "postings", seg.NPostings(),
		"bytes", seg.SizeBytes(), "load_ms", time.Since(start).Milliseconds())

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	srv, err := shard.New(seg, cfg, reg)
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
		_ = seg.Close()
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
