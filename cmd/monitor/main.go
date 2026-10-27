// Command monitor is a live monitoring worker (D38-D40): it leases streams
// from cmd/registry and reports the songs they play as they play, searching
// each window with the hybrid matcher on two clusters as cmd/tracklist does.
//
//	monitor -registry localhost:7700 -capacity 8 \
//	    -coordinator localhost:7500 -index data/segments/medium_b2 \
//	    -pairs-coordinator localhost:7600 -pairs-index data/segments/medium
//
// Decoder settings and -alpha default to cmd/tracklist's (the hybrid's
// calibration on UnmixDB, with both clusters' stop lists at 0.04). A stream URL that is not http(s) is a local file,
// read at -read-rate times real time (simulated live sources). Ports:
// -admin for Prometheus /metrics (never expose it).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/catalog"
	"github.com/anytroops/landmark/internal/hybrid"
	"github.com/anytroops/landmark/internal/monitor"
)

func main() {
	host, _ := os.Hostname()
	var (
		registryAddr, admin, windowLog, extra, fr  string
		coordinator, index, pairsCoord, pairsIndex string
		cfg                                        monitor.Config
		search                                     hybrid.Searcher
	)
	flag.StringVar(&registryAddr, "registry", "", "registry address (required)")
	flag.StringVar(&cfg.WorkerID, "id", fmt.Sprintf("%s-%d", host, os.Getpid()), "worker id")
	flag.IntVar(&cfg.Capacity, "capacity", 4, "streams run at once")
	flag.DurationVar(&cfg.Heartbeat, "heartbeat", 2*time.Second, "heartbeat interval (D39)")
	flag.StringVar(&coordinator, "coordinator", "", "coordinator of the triplet index (required)")
	flag.StringVar(&index, "index", "", "triplet index directory, for catalog.json (required)")
	flag.StringVar(&pairsCoord, "pairs-coordinator", "", "coordinator of the pair index (hybrid, D36)")
	flag.StringVar(&pairsIndex, "pairs-index", "", "pair index directory (with -pairs-coordinator)")
	flag.Float64Var(&cfg.Window, "window", 10, "window length, seconds")
	flag.Float64Var(&cfg.Hop, "hop", 0, "window hop, seconds (0: 27,520 samples with the shared front end, 2.5 s with -front window)")
	flag.StringVar(&fr, "front", "shared", "shared: compute each stream's spectrogram frames once for all windows (D48); window: fingerprint every window")
	flag.IntVar(&search.Shifts, "shifts", 4, "sub-hop query grids (D15)")
	flag.StringVar(&extra, "extra", "2,0.5", "extra tempo factors searched per window (empty: none)")
	flag.Float64Var(&search.Alpha, "alpha", 3, "weight of pair scores against triplet scores")
	flag.DurationVar(&search.Timeout, "timeout", 5*time.Second, "deadline per search")
	flag.IntVar(&cfg.SearchRetries, "search-retries", 3, "attempts per window before it counts as unanswered (jittered backoff between, D49)")
	flag.IntVar(&cfg.MaxInFlight, "max-in-flight", 32, "windows of all streams searched at once (D49; 0: no limit): bursts wait here, not at the shards")
	flag.Float64Var(&cfg.TAbs, "t-abs", 10, "minimum window score")
	flag.Float64Var(&cfg.TRatio, "t-ratio", 2, "minimum top / runner-up ratio")
	flag.IntVar(&cfg.K, "k", 1, "neighbors on each side in the majority vote; labels are final k windows later")
	flag.Float64Var(&cfg.MergeGap, "merge-gap", 2.5, "join entries of one track this close, seconds (-1: off)")
	flag.Float64Var(&cfg.ResumeWithin, "resume-within", 10, "after a failover, continue the open entry if its track is found this soon, seconds")
	flag.Float64Var(&cfg.ReadRate, "read-rate", 1, "file sources: playback speed (0: as fast as possible)")
	flag.StringVar(&windowLog, "window-log", "", "write one JSON line of timings per window to this file")
	flag.StringVar(&admin, "admin", "127.0.0.1:9400", "metrics address")
	flag.Parse()
	var err error
	if search.Extra, err = hybrid.ParseFactors(extra); err != nil {
		fmt.Fprintln(os.Stderr, "monitor:", err)
		os.Exit(2)
	}
	switch {
	case fr != "shared" && fr != "window":
		fmt.Fprintln(os.Stderr, "monitor: -front is shared or window")
		os.Exit(2)
	case cfg.Hop > 0:
	case fr == "shared":
		cfg.HopSamples = 27520 // a multiple of the 128 the three speeds need
	default:
		cfg.Hop = 2.5
	}
	cfg.Front = fr == "shared"
	if registryAddr == "" || coordinator == "" || index == "" || (pairsCoord == "") != (pairsIndex == "") {
		fmt.Fprintln(os.Stderr, "usage: monitor -registry addr -coordinator addr -index dir [-pairs-coordinator addr -pairs-index dir] [flags]")
		os.Exit(2)
	}
	if err := run(registryAddr, admin, windowLog, coordinator, index, pairsCoord, pairsIndex, cfg, &search); err != nil {
		slog.Error("monitor failed", "err", err)
		os.Exit(1)
	}
}

func run(registryAddr, admin, windowLog, coordinator, index, pairsCoord, pairsIndex string,
	cfg monitor.Config, search *hybrid.Searcher) error {
	cat, err := catalog.Load(index)
	if err != nil {
		return err
	}
	search.Cluster, cfg.Names, cfg.Searcher = cat.Cluster, cat.Name, search
	var closeFn func()
	if search.B2, closeFn, err = hybrid.Dial(coordinator); err != nil {
		return err
	}
	defer closeFn()
	if pairsCoord != "" {
		if err := catalog.Same(index, pairsIndex); err != nil {
			return err
		}
		if search.Pairs, closeFn, err = hybrid.Dial(pairsCoord); err != nil {
			return err
		}
		defer closeFn()
	}
	if windowLog != "" {
		f, err := os.Create(windowLog)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		cfg.WindowLog = io.Writer(f)
	}
	conn, err := grpc.NewClient(registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial registry: %w", err)
	}
	defer func() { _ = conn.Close() }()

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	w, err := monitor.New(cfg, landmarkv1.NewRegistryServiceClient(conn), reg)
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
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = adminSrv.Shutdown(sctx)
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("monitoring", "id", cfg.WorkerID, "registry", registryAddr, "capacity", cfg.Capacity,
		"hybrid", search.Pairs != nil, "read_rate", cfg.ReadRate, "shared_front_end", cfg.Front)
	return w.Run(ctx)
}
