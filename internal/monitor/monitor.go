// Package monitor is a live monitoring worker (D38-D40,
// docs/design/live-monitoring.md). It leases streams from the registry by
// heartbeat and runs each one: audio as it arrives, 10 s windows every
// 2.5 s, the hybrid searches of each window (internal/hybrid), the online
// decoder (tracklist.Online), and its events appended to the stream's log
// at the lease's epoch.
//
// A worker stops a stream when a heartbeat reply no longer lists it, when
// the registry refuses an append for its epoch (fenced: someone else holds
// the stream), and stops every stream when no heartbeat has succeeded for
// TTL minus one heartbeat interval (it may have lost its leases without
// hearing about it).
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/hybrid"
	"github.com/anytroops/landmark/internal/tracklist"
	"github.com/anytroops/landmark/internal/triplet"
)

// StartHeader is the HTTP response header in which a source may give the
// Unix time (seconds) of the first sample it sends; cmd/streamserver does.
// Without it, a stream's time starts when its first bytes arrive.
const StartHeader = "X-Landmark-Start"

// Config configures a Worker.
type Config struct {
	WorkerID string
	// Capacity: streams run at once. A source that has ended (a file read
	// to its end, a broadcast gone off the air) keeps its lease (so it is
	// not played again) but does not count.
	Capacity  int
	Heartbeat time.Duration // D39: 2 s
	Window    float64       // seconds
	Hop       float64       // seconds (HopSamples, if set, wins)
	// HopSamples: the hop in samples, for a hop that seconds cannot carry
	// exactly (the shared front end's 27,520).
	HopSamples int
	// Front: cut windows' triplet landmarks from a shared front end per
	// stream (D48) instead of fingerprinting each window; the hop must be a
	// multiple of its alignment.
	Front bool
	// Decoder: Smooth's parameters and the merge gap (D38).
	TAbs, TRatio float64
	K            int
	MergeGap     float64
	// ResumeWithin: after a failover (or a live source joined again after
	// it dropped), a run of the open entry's track that begins this many
	// seconds into the new audio continues it.
	ResumeWithin float64
	// ReadRate: playback speed of file sources (1: real time; 0: as fast
	// as possible).
	ReadRate float64
	Searcher *hybrid.Searcher
	Names    func(int) string
	// WindowLog, if set, receives one JSON line per searched window
	// (timings for the capacity benchmark).
	WindowLog io.Writer
	// SearchRetries: attempts per window before it counts as unanswered,
	// with a jittered backoff between them (D49).
	SearchRetries int
	// MaxInFlight: windows of all streams searched at once (D49; 0: no
	// limit). Streams that start together want their searches at the same
	// moment; past this many, a window waits here rather than at the
	// shards, which shed what waits over 100 ms.
	MaxInFlight int
	// DropAfter: a window of a live source not yet given a search slot this
	// long after its last sample arrived is not searched (D50; 0: never),
	// and the decoder treats it as unanswered: an overloaded worker stays in
	// real time instead of falling further behind. Files are read ahead of
	// their searches on purpose, so their windows are never dropped.
	DropAfter time.Duration
}

// Worker runs leased streams.
type Worker struct {
	cfg Config
	reg landmarkv1.RegistryServiceClient
	m   *metrics

	logMu sync.Mutex // WindowLog writes

	jobs map[string]*job // by stream id; used by Run's goroutine only
	wg   sync.WaitGroup

	inFlight chan struct{} // MaxInFlight slots; nil: no limit
}

type job struct {
	lease    *landmarkv1.Lease
	cancel   context.CancelFunc
	done     chan struct{}
	finished atomic.Bool // the source ended and every event is logged
}

// New returns a worker that talks to the registry through reg.
func New(cfg Config, reg landmarkv1.RegistryServiceClient, promReg prometheus.Registerer) (*Worker, error) {
	if cfg.WorkerID == "" || cfg.Searcher == nil || cfg.Heartbeat <= 0 || cfg.Window <= 0 || (cfg.Hop <= 0 && cfg.HopSamples <= 0) {
		return nil, errors.New("monitor: WorkerID, Searcher, Heartbeat, Window, and Hop are required")
	}
	if cfg.Names == nil {
		cfg.Names = func(d int) string { return fmt.Sprintf("track %d", d) }
	}
	cfg.SearchRetries = max(cfg.SearchRetries, 1)
	m, err := newMetrics(promReg)
	if err != nil {
		return nil, err
	}
	w := &Worker{cfg: cfg, reg: reg, m: m, jobs: map[string]*job{}}
	if cfg.MaxInFlight > 0 {
		w.inFlight = make(chan struct{}, cfg.MaxInFlight)
	}
	return w, nil
}

// Run heartbeats and runs streams until ctx ends, then stops them all.
func (w *Worker) Run(ctx context.Context) error {
	defer w.stopAll()
	tick := time.NewTicker(w.cfg.Heartbeat)
	defer tick.Stop()
	// expiry fires when the leases may be gone: the registry counts the TTL
	// from its receipt of a heartbeat, after our send, so sent + TTL minus
	// one interval is safely early.
	expiry := time.NewTimer(time.Hour)
	expiry.Stop()
	for {
		sent := time.Now()
		resp, err := w.heartbeat(ctx)
		switch {
		case err == nil:
			ttl := time.Duration(resp.GetTtlMs()) * time.Millisecond
			if ttl <= w.cfg.Heartbeat {
				return fmt.Errorf("monitor: heartbeat interval %v is not shorter than the lease TTL %v", w.cfg.Heartbeat, ttl)
			}
			expiry.Reset(time.Until(sent.Add(ttl - w.cfg.Heartbeat)))
			w.apply(ctx, resp.GetLeases())
		case ctx.Err() != nil:
			return nil
		default:
			w.m.heartbeatFailures.Inc()
			slog.Warn("heartbeat failed", "err", err)
		}
		w.reap()
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case <-expiry.C:
			if len(w.jobs) > 0 {
				slog.Warn("no heartbeat within the lease time: stopping every stream")
				for id := range w.jobs {
					w.stop(id)
				}
			}
			<-tick.C
		}
	}
}

func (w *Worker) heartbeat(ctx context.Context) (*landmarkv1.HeartbeatResponse, error) {
	capacity := w.cfg.Capacity
	req := &landmarkv1.HeartbeatRequest{WorkerId: w.cfg.WorkerID}
	for _, id := range slices.Sorted(maps.Keys(w.jobs)) {
		j := w.jobs[id]
		req.Held = append(req.Held, &landmarkv1.HeldLease{StreamId: id, Epoch: j.lease.GetEpoch()})
		if j.finished.Load() {
			capacity++
		}
	}
	req.Capacity = uint32(capacity)
	hctx, cancel := context.WithTimeout(ctx, w.cfg.Heartbeat)
	defer cancel()
	return w.reg.Heartbeat(hctx, req)
}

// apply starts newly granted streams and stops streams no longer leased
// (or leased again at another epoch).
func (w *Worker) apply(ctx context.Context, leases []*landmarkv1.Lease) {
	want := map[string]*landmarkv1.Lease{}
	for _, l := range leases {
		want[l.GetStreamId()] = l
	}
	for id, j := range w.jobs {
		if l, ok := want[id]; !ok || l.GetEpoch() != j.lease.GetEpoch() {
			w.stop(id)
		}
	}
	for id, l := range want {
		if _, ok := w.jobs[id]; !ok {
			w.start(ctx, l)
		}
	}
}

func (w *Worker) start(ctx context.Context, l *landmarkv1.Lease) {
	jctx, cancel := context.WithCancel(ctx)
	j := &job{lease: l, cancel: cancel, done: make(chan struct{})}
	w.jobs[l.GetStreamId()] = j
	w.m.running.Inc()
	slog.Info("stream start", "stream", l.GetStreamId(), "epoch", l.GetEpoch(), "next_seq", l.GetNextSeq())
	w.wg.Go(func() {
		defer w.m.running.Dec()
		defer close(j.done)
		err := w.runStream(jctx, l, &j.finished)
		switch {
		case jctx.Err() != nil:
		case errors.Is(err, errFenced):
			w.m.fenced.Inc()
			slog.Warn("stream fenced", "stream", l.GetStreamId(), "epoch", l.GetEpoch(), "err", err)
		case err != nil:
			slog.Error("stream failed", "stream", l.GetStreamId(), "err", err)
		}
	})
}

// stop cancels a stream without waiting for it (a search in flight ends at
// its timeout), so the heartbeat loop never stalls; a cancelled stream
// appends nothing more.
func (w *Worker) stop(id string) {
	j := w.jobs[id]
	j.cancel()
	delete(w.jobs, id)
	slog.Info("stream stop", "stream", id, "epoch", j.lease.GetEpoch())
}

// reap forgets streams that ended by themselves (fenced or failed), so the
// next heartbeat does not renew them; a failed stream is granted again.
func (w *Worker) reap() {
	for id, j := range w.jobs {
		select {
		case <-j.done:
			w.stop(id)
		default:
		}
	}
}

// stopAll cancels every stream and waits for them.
func (w *Worker) stopAll() {
	for id := range w.jobs {
		w.stop(id)
	}
	w.wg.Wait()
}

var errFenced = errors.New("lease epoch refused")

// window is one window's audio, its triplet landmarks when a shared front
// end cut them (nil: fingerprint x), and when its last sample arrived.
type window struct {
	t0    float64
	x     []float32
	trips [][]triplet.Landmark
	ready time.Time
	drop  time.Time // not searched once this passes (zero: never; D50)
}

// hopSamples is the window hop in samples.
func (c Config) hopSamples() int {
	if c.HopSamples > 0 {
		return c.HopSamples
	}
	return int(c.Hop * audio.SampleRate)
}

// runStream runs one leased stream until ctx ends, the stream is fenced,
// or the source fails. A file source is decoded to its end and the stream
// then idles, holding its lease. A live (http) source never ends by itself,
// so when it drops it is joined again (with backoff while that fails), and
// the song in progress continues as after a failover (Resume); it ends when
// the server says the broadcast is over (410 Gone).
func (w *Worker) runStream(ctx context.Context, l *landmarkv1.Lease, finished *atomic.Bool) error {
	ctx, cancel := context.WithCancel(ctx) // also ends the reader on an early return
	defer cancel()
	id, url := l.GetStreamId(), l.GetUrl()
	a := &appender{reg: w.reg, stream: id, epoch: l.GetEpoch(), next: l.GetNextSeq(), m: w.m, open: l.GetOpen()}
	src, base, err := w.connect(ctx, id, url, true)
	switch {
	case errors.Is(err, errGone):
		src, base = nil, unix(time.Now()) // over before we joined: at most an open entry to end
	case err != nil:
		return err
	}
	dec, err := w.decoder(a.open, base)
	if err != nil {
		if src != nil {
			_ = src.Close()
		}
		return err
	}
	fp := hybrid.NewFingerprinters()
	var dropAfter time.Duration
	if isLive(url) {
		dropAfter = w.cfg.DropAfter
	}
	quick := 0 // live connections in a row that ended before their first window
	for src != nil {
		n, dropped, err := w.decode(ctx, id, src, base, dropAfter, dec, a, fp)
		_ = src.Close()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil { // fenced, or the decoder refused a window
			return err
		}
		if !isLive(url) {
			if dropped != nil {
				return dropped
			}
			break // read to its end
		}
		slog.Warn("live source dropped, joining again", "stream", id, "err", dropped, "windows", n)
		if n > 0 {
			quick = 0
		} else { // a server that accepts and drops at once must not be hammered
			quick++
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff(quick)):
			}
		}
		next, nextBase, err := w.connect(ctx, id, url, false)
		if errors.Is(err, errGone) {
			break // the broadcast is over: dec ends the last song at its last audio
		}
		if err != nil {
			return nil // ctx ended
		}
		w.m.reconnects.Inc()
		if dec, err = w.decoder(a.open, nextBase); err != nil {
			_ = next.Close()
			return err
		}
		src, base = next, nextBase
	}
	if err := a.send(ctx, w.events(dec.Close(), base)); err != nil {
		return err
	}
	finished.Store(true)
	w.m.finished.Inc()
	slog.Info("stream source ended", "stream", id)
	<-ctx.Done()
	return nil
}

// Joining a live source again after it dropped: first at once, then with
// this backoff between failures (and between connections that end before
// their first window).
const (
	reconnectMin = 500 * time.Millisecond
	reconnectMax = 10 * time.Second
)

// backoff is the wait after the n-th failure in a row (from 1).
func backoff(n int) time.Duration {
	d := reconnectMin
	for i := 1; i < n && d < reconnectMax; i++ {
		d *= 2
	}
	return min(d, reconnectMax)
}

// connect opens the source. The first open of a lease returns any failure
// (the stream fails and is started again on the next heartbeat); joining
// again after a drop retries until it works, the broadcast is over
// (errGone), or ctx ends.
func (w *Worker) connect(ctx context.Context, id, url string, first bool) (*audio.Stream, float64, error) {
	for failures := 1; ; failures++ {
		src, base, err := openSource(ctx, url, w.cfg.ReadRate)
		if err == nil || first || errors.Is(err, errGone) {
			return src, base, err
		}
		delay := backoff(failures)
		slog.Warn("joining the live source failed", "stream", id, "err", err, "retry_in", delay)
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// decoder returns an online decoder for a source whose first sample is at
// base. An entry still open (its start logged by an earlier holder, or
// before the source dropped) continues as after a failover.
func (w *Worker) decoder(open *landmarkv1.Event, base float64) (*tracklist.Online, error) {
	dec := tracklist.NewOnline(w.cfg.Window, w.cfg.TAbs, w.cfg.TRatio, w.cfg.K, w.cfg.MergeGap)
	if open != nil {
		e := tracklist.Entry{Track: int(open.GetTrackId()), Start: open.GetStartUnix() - base, Factor: open.GetTempo()}
		if err := dec.Resume(e, w.cfg.ResumeWithin); err != nil {
			return nil, err
		}
	}
	return dec, nil
}

// decode searches the source's windows and feeds them to dec, appending
// its events, until the source ends or fails, and returns how many windows
// it decoded and the source's error (nil at its end). err is any other
// failure: the decoder refusing a window, or an append (fenced). A window
// not given a search slot within dropAfter of its audio arriving is
// dropped (0: never).
func (w *Worker) decode(ctx context.Context, id string, src *audio.Stream, base float64, dropAfter time.Duration,
	dec *tracklist.Online, a *appender, fp *hybrid.Fingerprinters) (n int, dropped, err error) {
	ctx, cancel := context.WithCancel(ctx) // ends the reader on an early return
	defer cancel()
	windows := make(chan window, 64)
	readErr := make(chan error, 1)
	go func() { readErr <- w.readWindows(ctx, src, dropAfter, windows) }()
	for win := range windows {
		rows := w.search(ctx, id, win, fp)
		if ctx.Err() != nil {
			break
		}
		ev, err := dec.Push(rows)
		if err != nil {
			return n, nil, err
		}
		n++
		if err := a.send(ctx, w.events(ev, base)); err != nil {
			return n, nil, err
		}
	}
	return n, <-readErr, nil
}

// readWindows cuts the source into windows and sends them in order; it
// closes out when the source ends. With a shared front end, a window waits
// until the front end can cut its landmarks (the resamplers look a few
// samples ahead); its ready time stays when its last sample arrived.
func (w *Worker) readWindows(ctx context.Context, src *audio.Stream, dropAfter time.Duration, out chan<- window) error {
	defer close(out)
	size, hop := int(w.cfg.Window*audio.SampleRate), w.cfg.hopSamples()
	cut := windower{size: size, hop: hop}
	var fe *hybrid.Front
	if w.cfg.Front {
		var err error
		if fe, err = w.cfg.Searcher.NewFront(size); err != nil {
			return err
		}
		if hop%fe.Align() != 0 {
			return fmt.Errorf("monitor: a %d-sample hop is not a multiple of the front end's %d", hop, fe.Align())
		}
	}
	type cutAt struct {
		cutWindow
		at time.Time
	}
	var pending []cutAt
	buf := make([]float32, audio.SampleRate/4)
	for {
		n, err := src.Read(buf)
		eof := errors.Is(err, io.EOF)
		now := time.Now()
		for _, win := range cut.add(buf[:n]) {
			pending = append(pending, cutAt{win, now})
		}
		if fe != nil {
			fe.Push(buf[:n])
			if eof {
				fe.Close()
			}
		}
		for len(pending) > 0 && (fe == nil || fe.Ready(pending[0].start)) {
			win := pending[0]
			pending = pending[1:]
			msg := window{t0: hybrid.WindowT0(win.start), x: win.x, ready: win.at}
			if dropAfter > 0 {
				msg.drop = win.at.Add(dropAfter)
			}
			if fe != nil {
				if msg.trips, err = fe.Landmarks(win.start); err != nil {
					return err
				}
				fe.Forget(win.start + hop)
			}
			select {
			case out <- msg:
			case <-ctx.Done():
				return nil
			}
		}
		if eof {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read stream: %w", err)
		}
	}
}

// search runs a window's searches, retrying; a window still unanswered
// counts as one with no candidates, so the decoder keeps going.
func (w *Worker) search(ctx context.Context, stream string, win window, fp *hybrid.Fingerprinters) []tracklist.Row {
	var rows []tracklist.Row
	partial := 0
	var err error
	for attempt := range w.cfg.SearchRetries {
		if attempt > 0 && !sleep(ctx, retryDelay(attempt)) {
			break
		}
		if err = w.acquire(ctx, win.drop); err != nil {
			break
		}
		if win.trips != nil {
			rows, partial, err = w.cfg.Searcher.WindowFrom(ctx, win.x, win.t0, win.trips, fp)
		} else {
			rows, partial, err = w.cfg.Searcher.Window(ctx, win.x, win.t0, fp)
		}
		w.release()
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	done := time.Now()
	late := errors.Is(err, errLate)
	switch {
	case late: // not logged one by one: an overloaded worker drops many
		w.m.late.Inc()
	case err != nil:
		w.m.searchFailures.Inc()
		slog.Warn("window unanswered", "stream", stream, "t0", win.t0, "err", err)
	}
	if err != nil {
		rows = []tracklist.Row{{T0: win.t0, Mode: "b2", Factor: 1, Track: -1}}
	}
	lag := done.Sub(win.ready)
	w.m.windows.Inc()
	w.m.lag.Observe(lag.Seconds())
	if w.cfg.WindowLog != nil {
		b, _ := json.Marshal(map[string]any{"stream": stream, "t0": win.t0, "ready_unix": unix(win.ready),
			"done_unix": unix(done), "searches": len(rows), "partial": partial, "failed": err != nil, "late": late})
		w.logMu.Lock()
		_, _ = w.cfg.WindowLog.Write(append(b, '\n'))
		w.logMu.Unlock()
	}
	return rows
}

// errLate: a window was not given a search slot before its drop time (D50).
var errLate = errors.New("monitor: window dropped, too late to search")

// acquire takes a search slot (D49), waiting while all are taken. It
// returns errLate once drop (unless zero) has passed, at once if it already
// has (D50), or ctx's error if ctx ends first.
func (w *Worker) acquire(ctx context.Context, drop time.Time) error {
	var late <-chan time.Time
	if !drop.IsZero() {
		d := time.Until(drop)
		if d <= 0 {
			return errLate
		}
		t := time.NewTimer(d)
		defer t.Stop()
		late = t.C
	}
	if w.inFlight == nil {
		return ctx.Err()
	}
	select {
	case w.inFlight <- struct{}{}:
		return nil
	case <-late:
		return errLate
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) release() {
	if w.inFlight != nil {
		<-w.inFlight
	}
}

// retryDelay is the wait before a window's attempt (from 1): 50 ms doubling
// per attempt, times a random factor from 0.5 to 1.5 (D49), so the retries
// of windows that failed together do not come back together.
func retryDelay(attempt int) time.Duration {
	base := 50 * time.Millisecond << min(attempt-1, 5)
	return time.Duration(float64(base) * (0.5 + rand.Float64())) //nolint:gosec // jitter, not security
}

// sleep waits d unless ctx ends first, and reports whether it waited.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// events converts decoder events to log events in broadcast time.
func (w *Worker) events(ev []tracklist.Event, base float64) []*landmarkv1.Event {
	out := make([]*landmarkv1.Event, len(ev))
	now := unix(time.Now())
	for i, e := range ev {
		kind := landmarkv1.EventKind_EVENT_KIND_START
		if e.Kind == tracklist.End {
			kind = landmarkv1.EventKind_EVENT_KIND_END
		}
		out[i] = &landmarkv1.Event{Kind: kind, TrackId: uint32(e.Entry.Track), Name: w.cfg.Names(e.Entry.Track),
			StartUnix: base + e.Entry.Start, EndUnix: base + e.Entry.End, Tempo: e.Entry.Factor,
			Evidence: e.Entry.Evidence, Resumed: e.Resumed, Interrupted: e.Interrupted,
			WorkerId: w.cfg.WorkerID, DecidedUnix: now, SourceStartUnix: base}
	}
	return out
}

// appender numbers events and appends them, retrying transient failures
// (a retry re-sends the same numbered events, which the log acknowledges
// once). It tracks the entry whose start is logged and end is not.
type appender struct {
	reg    landmarkv1.RegistryServiceClient
	stream string
	epoch  uint64
	next   uint64
	open   *landmarkv1.Event
	m      *metrics
}

func (a *appender) send(ctx context.Context, events []*landmarkv1.Event) error {
	if len(events) == 0 {
		return nil
	}
	for i, e := range events {
		e.Seq = a.next + uint64(i)
	}
	req := &landmarkv1.AppendRequest{StreamId: a.stream, Epoch: a.epoch, Events: events}
	backoff := 100 * time.Millisecond
	for {
		actx, cancel := context.WithTimeout(ctx, 5*time.Second)
		resp, err := a.reg.Append(actx, req)
		cancel()
		switch status.Code(err) {
		case codes.OK:
			a.next = resp.GetNextSeq()
			a.m.appended.Add(float64(len(events)))
			for _, e := range events { // a decoder has one open entry: an end always closes it
				if e.GetKind() == landmarkv1.EventKind_EVENT_KIND_START {
					a.open = e
				} else {
					a.open = nil
				}
			}
			return nil
		case codes.FailedPrecondition:
			return fmt.Errorf("%w: %w", errFenced, err)
		case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted:
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("append failed, retrying", "stream", a.stream, "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, 2*time.Second)
		default:
			return fmt.Errorf("append: %w", err)
		}
	}
}

// errGone: a live source answered 410 Gone, its broadcast is over.
var errGone = errors.New("source off the air")

// isLive reports whether url is a live (http) source rather than a file.
func isLive(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// openSource opens a stream URL: http(s) is fetched and decoded as it
// arrives; anything else is a local file read at readRate. It returns the
// broadcast Unix time of the first sample.
func openSource(ctx context.Context, url string, readRate float64) (*audio.Stream, float64, error) {
	if !isLive(url) {
		if _, err := os.Stat(url); err != nil {
			return nil, 0, fmt.Errorf("source: %w", err)
		}
		base := unix(time.Now())
		s, err := audio.NewFileStream(ctx, url, audio.SampleRate, readRate)
		return s, base, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("source: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("source: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusGone {
			return nil, 0, fmt.Errorf("source %s: %w", url, errGone)
		}
		return nil, 0, fmt.Errorf("source %s: %s", url, resp.Status)
	}
	base := unix(time.Now())
	if v := resp.Header.Get(StartHeader); v != "" {
		if base, err = strconv.ParseFloat(v, 64); err != nil {
			_ = resp.Body.Close()
			return nil, 0, fmt.Errorf("source %s: bad %s %q", url, StartHeader, v)
		}
	}
	s, err := audio.NewStream(ctx, resp.Body, audio.SampleRate)
	if err != nil {
		_ = resp.Body.Close()
		return nil, 0, err
	}
	return s, base, nil
}

// windower cuts a sample stream into windows of size samples every hop.
type windower struct {
	size, hop int
	buf       []float32 // samples from the next window's start on
	next      int       // absolute start of the next window
}

type cutWindow struct {
	start int
	x     []float32
}

func (c *windower) add(x []float32) []cutWindow {
	c.buf = append(c.buf, x...)
	var out []cutWindow
	for len(c.buf) >= c.size {
		out = append(out, cutWindow{start: c.next, x: slices.Clone(c.buf[:c.size])})
		c.buf = c.buf[c.hop:]
		c.next += c.hop
	}
	return out
}

func unix(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

type metrics struct {
	windows, searchFailures, late, appended, fenced, heartbeatFailures, finished, reconnects prometheus.Counter
	running                                                                                  prometheus.Gauge
	lag                                                                                      prometheus.Histogram
}

func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		windows: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_monitor_windows_total", Help: "Windows searched."}),
		searchFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_monitor_windows_unanswered_total", Help: "Windows whose searches failed after retries."}),
		late: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_monitor_windows_dropped_late_total", Help: "Live windows not searched: no search slot within -drop-after of their audio (D50)."}),
		appended: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_monitor_events_appended_total", Help: "Events logged."}),
		fenced: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_monitor_streams_fenced_total", Help: "Streams stopped because the log refused their epoch."}),
		heartbeatFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_monitor_heartbeat_failures_total", Help: "Failed heartbeats."}),
		finished: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_monitor_sources_ended_total", Help: "Sources decoded to their end (files, and broadcasts that went off the air)."}),
		reconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_monitor_source_reconnects_total", Help: "Live sources joined again after they dropped."}),
		running: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "landmark_monitor_streams_running", Help: "Streams this worker runs."}),
		lag: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "landmark_monitor_window_lag_seconds", Help: "From a window's last sample to its searches done.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 12)}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{m.windows, m.searchFailures, m.late, m.appended, m.fenced,
			m.heartbeatFailures, m.finished, m.reconnects, m.running, m.lag} {
			if err := reg.Register(c); err != nil {
				return nil, fmt.Errorf("monitor: metrics: %w", err)
			}
		}
	}
	return m, nil
}
