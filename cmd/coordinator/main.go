// Command coordinator serves Identify by scatter-gather over the shards in a
// cluster config (see internal/cluster for the file format).
//
// Ports: -listen for gRPC (Identify + grpc.health.v1), -admin for Prometheus
// /metrics and pprof. Never expose the admin port publicly.
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
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/cluster"
	"github.com/anytroops/landmark/internal/coordinator"
)

func main() {
	cfgPath := flag.String("config", "", "cluster config JSON (required)")
	listen := flag.String("listen", ":7000", "gRPC listen address")
	admin := flag.String("admin", "127.0.0.1:9100", "metrics and pprof address")
	flag.Parse()
	if *cfgPath == "" {
		slog.Error("-config is required")
		os.Exit(2)
	}
	if err := run(*cfgPath, *listen, *admin); err != nil {
		slog.Error("coordinator failed", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath, listen, admin string) error {
	cl, err := cluster.Load(cfgPath)
	if err != nil {
		return err
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	coord, err := coordinator.New(cl, coordinator.Options{}, reg)
	if err != nil {
		return err
	}
	defer func() { _ = coord.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	coord.Start(ctx) // first health round runs before we accept traffic

	gs := grpc.NewServer()
	landmarkv1.RegisterCoordinatorServiceServer(gs, coord)
	hs := health.NewServer()
	healthpb.RegisterHealthServer(gs, hs)

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	adminSrv := &http.Server{Addr: admin, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("admin server", "err", err)
		}
	}()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		slog.Info("shutting down")
		hs.Shutdown()
		done := make(chan struct{})
		go func() { gs.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			gs.Stop()
		}
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = adminSrv.Shutdown(sctx)
	}()

	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	hs.SetServingStatus(landmarkv1.CoordinatorService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	slog.Info("serving", "grpc", lis.Addr().String(), "admin", admin, "partitions", len(cl.Partitions),
		"deadline", cl.Deadline(), "t_abs", cl.TAbs, "t_ratio", cl.TRatio,
		"stop_fraction", cl.StopFraction, "stop_limit", cl.StopLimit)
	err = gs.Serve(lis)
	stop()
	<-shutdownDone
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}
