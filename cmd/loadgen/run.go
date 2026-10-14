package main

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/runinfo"
)

// record is one scheduled request.
type record struct {
	Due      time.Duration // scheduled send time, from run start
	Latency  time.Duration // completion minus Due; includes any send delay
	Code     string        // gRPC status code, or "DROPPED" if never sent
	Partial  bool
	Matched  bool
	Correct  bool // matched the expected track, or rejected a never-indexed song
	Expected int32
}

type runConfig struct {
	Target      string        `json:"target"`
	Queries     string        `json:"queries"`
	Rate        float64       `json:"rate_qps"`
	Duration    time.Duration `json:"duration_ns"`
	Warmup      time.Duration `json:"warmup_ns"`
	Poisson     bool          `json:"poisson"`
	Timeout     time.Duration `json:"timeout_ns"`
	MaxInFlight int           `json:"max_in_flight"`
	Conns       int           `json:"conns"`
	Seed        uint64        `json:"seed"`
	Label       string        `json:"label"`
	EventAt     time.Duration `json:"event_at_ns"`
	EventCmd    string        `json:"event_cmd"`
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var cfg runConfig
	fs.StringVar(&cfg.Target, "target", "127.0.0.1:7000", "coordinator address")
	fs.StringVar(&cfg.Queries, "queries", "", "query file from `loadgen queries` (required)")
	fs.Float64Var(&cfg.Rate, "rate", 100, "offered load, queries per second")
	fs.DurationVar(&cfg.Duration, "duration", 60*time.Second, "measured duration (after warmup)")
	fs.DurationVar(&cfg.Warmup, "warmup", 10*time.Second, "warmup, sent but excluded from summary percentiles")
	fs.BoolVar(&cfg.Poisson, "poisson", false, "Poisson arrivals instead of a fixed interval")
	fs.DurationVar(&cfg.Timeout, "timeout", time.Second, "client deadline per request")
	fs.IntVar(&cfg.MaxInFlight, "max-in-flight", 20000, "requests beyond this are recorded as DROPPED")
	fs.IntVar(&cfg.Conns, "conns", 4, "gRPC connections, used round-robin")
	fs.Uint64Var(&cfg.Seed, "seed", 1, "query order and arrival seed")
	fs.StringVar(&cfg.Label, "label", "", "free-form note stored in the summary")
	fs.DurationVar(&cfg.EventAt, "event-at", 0, "run -event-cmd this long after the run starts (failure injection)")
	fs.StringVar(&cfg.EventCmd, "event-cmd", "", "shell command to run at -event-at, e.g. 'docker kill -s KILL landmark-shard-1a-1'")
	out := fs.String("out", "", "output directory for requests.csv.gz and summary.json (required)")
	_ = fs.Parse(args)
	if cfg.Queries == "" || *out == "" || cfg.Rate <= 0 {
		return errors.New("need -queries, -out, and -rate > 0")
	}
	queries, err := loadQueries(cfg.Queries)
	if err != nil {
		return err
	}
	reqs := make([]*landmarkv1.IdentifyRequest, len(queries))
	for i, q := range queries {
		r := &landmarkv1.IdentifyRequest{TopK: 1, Landmarks: make([]*landmarkv1.Landmark, len(q.Landmarks))}
		for j, l := range q.Landmarks {
			r.Landmarks[j] = &landmarkv1.Landmark{Hash: l.Hash, T: l.T}
		}
		reqs[i] = r
	}

	clients := make([]landmarkv1.CoordinatorServiceClient, cfg.Conns)
	for i := range clients {
		conn, err := grpc.NewClient(cfg.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		clients[i] = landmarkv1.NewCoordinatorServiceClient(conn)
	}

	records, event := schedule(cfg, queries, reqs, clients)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	if err := writeRecords(filepath.Join(*out, "requests.csv.gz"), records); err != nil {
		return err
	}
	sum := summarize(cfg, records)
	sum.Event = event
	b, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "summary.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	m := sum.Measured
	slog.Info("done", "offered_qps", cfg.Rate, "sent", m.Count, "success", fmt.Sprintf("%.4f", m.SuccessRate),
		"p50_ms", m.P50Ms, "p99_ms", m.P99Ms, "p999_ms", m.P999Ms, "dropped", m.Dropped)
	return nil
}

// eventResult records when the injected event actually ran, on the same
// clock as request due times.
type eventResult struct {
	Cmd    string  `json:"cmd"`
	AtS    float64 `json:"at_s"`
	TookMs float64 `json:"took_ms"`
	Output string  `json:"output"`
	Error  string  `json:"error,omitempty"`
}

// schedule sends every request at its due time and waits for all of them.
func schedule(cfg runConfig, queries []Query, reqs []*landmarkv1.IdentifyRequest, clients []landmarkv1.CoordinatorServiceClient) ([]record, *eventResult) {
	rng := rand.New(rand.NewPCG(cfg.Seed, 7))
	total := cfg.Warmup + cfg.Duration
	var dues []time.Duration
	for t := time.Duration(0); t < total; {
		dues = append(dues, t)
		gap := 1 / cfg.Rate
		if cfg.Poisson {
			gap = rng.ExpFloat64() / cfg.Rate
		}
		t += time.Duration(gap * float64(time.Second))
	}
	order := rng.Perm(len(dues))

	records := make([]record, len(dues))
	var inflight atomic.Int64
	var wg sync.WaitGroup
	start := time.Now().Add(50 * time.Millisecond)

	var event *eventResult
	eventDone := make(chan struct{})
	if cfg.EventCmd == "" {
		close(eventDone)
	} else {
		go func() {
			defer close(eventDone)
			time.Sleep(time.Until(start.Add(cfg.EventAt)))
			t0 := time.Now()
			out, err := exec.Command("sh", "-c", cfg.EventCmd).CombinedOutput()
			event = &eventResult{Cmd: cfg.EventCmd, AtS: t0.Sub(start).Seconds(),
				TookMs: float64(time.Since(t0).Microseconds()) / 1000, Output: strings.TrimSpace(string(out))}
			if err != nil {
				event.Error = err.Error()
			}
			slog.Info("event ran", "at_s", event.AtS, "cmd", cfg.EventCmd, "err", err)
		}()
	}
	for i, due := range dues {
		time.Sleep(time.Until(start.Add(due)))
		qi := order[i] % len(queries)
		rec := &records[i]
		rec.Due, rec.Expected = due, queries[qi].Expected
		if inflight.Load() >= int64(cfg.MaxInFlight) {
			rec.Code = "DROPPED" // counted as a failure in the summary
			continue
		}
		inflight.Add(1)
		wg.Go(func() {
			defer inflight.Add(-1)
			ctx, cancel := context.WithDeadline(context.Background(), start.Add(due).Add(cfg.Timeout))
			defer cancel()
			resp, err := clients[i%len(clients)].Identify(ctx, reqs[qi])
			rec.Latency = time.Since(start.Add(due))
			rec.Code = status.Code(err).String()
			if err == nil {
				rec.Partial, rec.Matched = resp.GetPartial(), resp.GetMatched()
				top := int32(-1)
				if len(resp.GetCandidates()) > 0 {
					top = int32(resp.GetCandidates()[0].GetTrackId())
				}
				// Duplicates: a twin of the expected track also counts as wrong
				// here; the eval harness handles clusters properly. Load tests
				// care about availability, so this is a coarse check.
				rec.Correct = (rec.Expected >= 0 && rec.Matched && top == rec.Expected) ||
					(rec.Expected < 0 && !rec.Matched)
			}
		})
	}
	wg.Wait()
	<-eventDone
	return records, event
}

type stats struct {
	Count       int            `json:"count"`
	OK          int            `json:"ok"`
	Partial     int            `json:"partial"`
	Dropped     int            `json:"dropped"`
	Codes       map[string]int `json:"codes"`
	SuccessRate float64        `json:"success_rate"` // OK and complete (not partial)
	CorrectRate float64        `json:"correct_rate"`
	P50Ms       float64        `json:"p50_ms"`
	P90Ms       float64        `json:"p90_ms"`
	P99Ms       float64        `json:"p99_ms"`
	P999Ms      float64        `json:"p999_ms"`
	MaxMs       float64        `json:"max_ms"`
}

type bucket struct {
	StartS float64 `json:"start_s"`
	stats
}

type summary struct {
	Config   runConfig         `json:"config"`
	Machine  map[string]string `json:"machine"`
	Measured stats             `json:"measured"` // after warmup
	Timeline []bucket          `json:"timeline"` // 1 s buckets over the whole run
	Event    *eventResult      `json:"event,omitempty"`
	Note     string            `json:"note"`
}

// computeStats summarizes recs. Percentiles cover every sent request,
// failed ones included (at their failure time), so errors cannot make the
// tail look better; dropped requests count as failures without a latency.
func computeStats(recs []record) stats {
	s := stats{Count: len(recs), Codes: map[string]int{}}
	var lat []float64
	correct := 0
	for _, r := range recs {
		s.Codes[r.Code]++
		switch {
		case r.Code == "DROPPED":
			s.Dropped++
			continue
		case r.Code == "OK":
			s.OK++
			if r.Partial {
				s.Partial++
			}
			if r.Correct {
				correct++
			}
		}
		lat = append(lat, float64(r.Latency)/1e6)
	}
	if s.Count > 0 {
		s.SuccessRate = float64(s.OK-s.Partial) / float64(s.Count)
		s.CorrectRate = float64(correct) / float64(s.Count)
	}
	slices.Sort(lat)
	q := func(p float64) float64 {
		if len(lat) == 0 {
			return math.NaN()
		}
		return math.Round(lat[int(math.Ceil(p*float64(len(lat))))-1]*1000) / 1000
	}
	if len(lat) > 0 {
		s.P50Ms, s.P90Ms, s.P99Ms, s.P999Ms, s.MaxMs = q(0.5), q(0.9), q(0.99), q(0.999), q(1)
	}
	return s
}

func summarize(cfg runConfig, recs []record) summary {
	var measured []record
	byBucket := map[int][]record{}
	for _, r := range recs {
		if r.Due >= cfg.Warmup {
			measured = append(measured, r)
		}
		byBucket[int(r.Due/time.Second)] = append(byBucket[int(r.Due/time.Second)], r)
	}
	sum := summary{
		Config: cfg, Machine: runinfo.Machine("."), Measured: computeStats(measured),
		Note: "Latency is measured from each request's scheduled send time (open loop, no coordinated omission).",
	}
	for b := 0; b <= int((cfg.Warmup+cfg.Duration)/time.Second); b++ {
		if rs := byBucket[b]; len(rs) > 0 {
			sum.Timeline = append(sum.Timeline, bucket{StartS: float64(b), stats: computeStats(rs)})
		}
	}
	return sum
}

func writeRecords(path string, recs []record) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	gz := gzip.NewWriter(f)
	w := csv.NewWriter(gz)
	_ = w.Write([]string{"due_ms", "latency_ms", "code", "partial", "matched", "correct", "expected"})
	for _, r := range recs {
		_ = w.Write([]string{
			strconv.FormatFloat(float64(r.Due)/1e6, 'f', 3, 64),
			strconv.FormatFloat(float64(r.Latency)/1e6, 'f', 3, 64),
			r.Code, strconv.FormatBool(r.Partial), strconv.FormatBool(r.Matched),
			strconv.FormatBool(r.Correct), strconv.Itoa(int(r.Expected)),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return gz.Close()
}
