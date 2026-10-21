// Command registry serves live monitoring's RegistryService (D39, D40):
// stream leases for cmd/monitor workers, each stream's event log, and live
// subscriptions to it.
//
//	registry -dir data/monitor -streams streams.json
//
// -streams adds the streams in a JSON file ([{"id": "...", "url": "..."}])
// at startup; streams persist in -dir, as do epochs and logs. Ports:
// -listen for gRPC, -admin for Prometheus /metrics (never expose it).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/registry"
)

func main() {
	dir := flag.String("dir", "", "state and log directory (required)")
	streams := flag.String("streams", "", "JSON file of streams to add at startup")
	listen := flag.String("listen", ":7700", "gRPC listen address")
	admin := flag.String("admin", "127.0.0.1:9300", "metrics address")
	ttl := flag.Duration("ttl", 6*time.Second, "lease time to live (D39)")
	flag.Parse()
	if *dir == "" {
		slog.Error("-dir is required")
		os.Exit(2)
	}
	if err := run(*dir, *streams, *listen, *admin, *ttl); err != nil {
		slog.Error("registry failed", "err", err)
		os.Exit(1)
	}
}

func run(dir, streamsPath, listen, admin string, ttl time.Duration) error {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	r, err := registry.Open(registry.Options{Dir: dir, TTL: ttl}, reg)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if streamsPath != "" {
		if err := addStreams(r, streamsPath); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	gs := grpc.NewServer()
	landmarkv1.RegisterRegistryServiceServer(gs, r)
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
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
		done := make(chan struct{})
		go func() { gs.GracefulStop(); close(done) }()
		select { // subscriptions never end on their own
		case <-done:
		case <-time.After(2 * time.Second):
			gs.Stop()
		}
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = adminSrv.Shutdown(sctx)
	}()
	slog.Info("serving", "grpc", lis.Addr().String(), "admin", admin, "dir", dir, "ttl", ttl)
	err = gs.Serve(lis)
	stop()
	<-shutdownDone
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

func addStreams(r *registry.Registry, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read streams: %w", err)
	}
	var streams []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(b, &streams); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for _, s := range streams {
		if _, err := r.AddStream(context.Background(), &landmarkv1.AddStreamRequest{
			Stream: &landmarkv1.Stream{Id: s.ID, Url: s.URL}}); err != nil {
			return fmt.Errorf("add stream %s: %w", s.ID, err)
		}
	}
	return nil
}
