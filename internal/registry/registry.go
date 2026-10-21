// Package registry serves live monitoring's RegistryService (D39, D40): the
// stream list, leases that assign streams to workers, and each stream's
// event log.
//
// A lease lasts TTL past the registry's receipt of the heartbeat that
// granted or last renewed it. Every grant raises the stream's epoch (a
// fencing token) and stores it before replying, and Append accepts events
// only at the stream's current epoch, under the same lock as the grants: a
// worker that lost its lease, however late it wakes up, cannot log next to
// its replacement. Leases are not stored, so after a restart every stream
// is free; the registry then grants nothing for one TTL, so holders from
// before the restart stop on their own first (their epochs are refused in
// any case).
//
// Files in Dir: registry.json (streams and epochs; replaced atomically) and
// events/<stream>.jsonl (one Event per line, fsynced per Append).
package registry

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
)

// Options configures a Registry.
type Options struct {
	Dir string
	TTL time.Duration // lease time to live (D39: 6 s)
	// Now is the clock (nil: time.Now); tests replace it.
	Now func() time.Time
	// SubscriberBuffer is how many events a subscriber may fall behind
	// before it is dropped (0: 1024).
	SubscriberBuffer int
}

// Registry implements landmarkv1.RegistryServiceServer.
type Registry struct {
	landmarkv1.UnimplementedRegistryServiceServer

	dir        string
	ttl        time.Duration
	now        func() time.Time
	bufferSize int
	m          *metrics

	mu         sync.Mutex
	grantAfter time.Time
	streams    map[string]*stream
	ids        []string // sorted
	subs       map[*subscriber]struct{}
}

type stream struct {
	id, url string
	epoch   uint64
	holder  string // worker id; empty: free
	expires time.Time
	events  []*landmarkv1.Event // events[i].Seq == i+1
	open    *landmarkv1.Event   // last start without an end
	log     *os.File
	size    int64 // bytes of complete lines in log
}

type subscriber struct {
	want map[string]bool // empty: all streams
	ch   chan *landmarkv1.Event
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}$`)

// Open loads the registry in opts.Dir, creating it if needed, and truncates
// a log line torn by a crash.
func Open(opts Options, reg prometheus.Registerer) (*Registry, error) {
	if opts.TTL <= 0 {
		return nil, errors.New("registry: TTL must be positive")
	}
	r := &Registry{dir: opts.Dir, ttl: opts.TTL, now: opts.Now, bufferSize: cmp.Or(opts.SubscriberBuffer, 1024),
		streams: map[string]*stream{}, subs: map[*subscriber]struct{}{}}
	if r.now == nil {
		r.now = time.Now
	}
	m, err := newMetrics(reg)
	if err != nil {
		return nil, err
	}
	r.m = m
	if err := os.MkdirAll(filepath.Join(r.dir, "events"), 0o755); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	b, err := os.ReadFile(filepath.Join(r.dir, "registry.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("registry: %w", err)
	default:
		var st state
		if err := json.Unmarshal(b, &st); err != nil {
			return nil, fmt.Errorf("registry: parse registry.json: %w", err)
		}
		for _, s := range st.Streams {
			if err := r.load(s); err != nil {
				_ = r.Close()
				return nil, err
			}
		}
	}
	r.grantAfter = r.now().Add(r.ttl)
	return r, nil
}

// Close closes the logs. Subscribers stay open until their contexts end.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for _, s := range r.streams {
		errs = append(errs, s.log.Close())
	}
	return errors.Join(errs...)
}

// state is registry.json.
type state struct {
	Streams []storedStream `json:"streams"`
}

type storedStream struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Epoch uint64 `json:"epoch"`
}

// load opens a stream's log, reads its events, and cuts a torn last line.
func (r *Registry) load(st storedStream) error {
	path := filepath.Join(r.dir, "events", st.ID+".jsonl")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("registry: %w", err)
	}
	s := &stream{id: st.ID, url: st.URL, epoch: st.Epoch, log: f}
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break // a line without its newline was torn by a crash: dropped below
		}
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("registry: read %s: %w", path, err)
		}
		e := &landmarkv1.Event{}
		if err := protojson.Unmarshal(bytes.TrimSpace(line), e); err != nil {
			_ = f.Close()
			return fmt.Errorf("registry: %s line %d: %w", path, len(s.events)+1, err)
		}
		if e.GetSeq() != uint64(len(s.events))+1 || e.GetEpoch() > s.epoch {
			_ = f.Close()
			return fmt.Errorf("registry: %s line %d: seq %d epoch %d out of order", path, len(s.events)+1, e.GetSeq(), e.GetEpoch())
		}
		s.addEvent(e)
		s.size += int64(len(line))
	}
	if err := f.Truncate(s.size); err != nil {
		_ = f.Close()
		return fmt.Errorf("registry: truncate %s: %w", path, err)
	}
	if _, err := f.Seek(s.size, io.SeekStart); err != nil {
		_ = f.Close()
		return fmt.Errorf("registry: %w", err)
	}
	r.streams[s.id] = s
	r.ids = append(r.ids, s.id)
	slices.Sort(r.ids)
	return nil
}

func (s *stream) addEvent(e *landmarkv1.Event) {
	s.events = append(s.events, e)
	switch e.GetKind() {
	case landmarkv1.EventKind_EVENT_KIND_START:
		s.open = e
	case landmarkv1.EventKind_EVENT_KIND_END:
		s.open = nil
	}
}

// saveState writes registry.json: temp file, fsync, rename, fsync the
// directory, so a crash leaves the old or the new file, never a mix.
func (r *Registry) saveState() error {
	var st state
	for _, id := range r.ids {
		s := r.streams[id]
		st.Streams = append(st.Streams, storedStream{ID: s.id, URL: s.url, Epoch: s.epoch})
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(r.dir, "registry.json.tmp")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(r.dir, "registry.json")); err != nil {
		return err
	}
	d, err := os.Open(r.dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// AddStream adds a stream; adding one that exists with the same URL is a
// no-op.
func (r *Registry) AddStream(_ context.Context, req *landmarkv1.AddStreamRequest) (*landmarkv1.AddStreamResponse, error) {
	id, url := req.GetStream().GetId(), req.GetStream().GetUrl()
	if !validID.MatchString(id) || url == "" {
		return nil, status.Errorf(codes.InvalidArgument, "stream id %q (letters, digits, '.', '_', '-'; up to 64) and a url are required", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.streams[id]; ok {
		if s.url != url {
			return nil, status.Errorf(codes.AlreadyExists, "stream %s exists with url %s", id, s.url)
		}
		return &landmarkv1.AddStreamResponse{}, nil
	}
	if err := r.load(storedStream{ID: id, URL: url}); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	if err := r.saveState(); err != nil {
		_ = r.streams[id].log.Close()
		delete(r.streams, id)
		r.ids = slices.DeleteFunc(r.ids, func(x string) bool { return x == id })
		return nil, status.Errorf(codes.Unavailable, "save state: %v", err)
	}
	return &landmarkv1.AddStreamResponse{}, nil
}

// Heartbeat renews the worker's leases, frees expired ones, grants free
// streams up to the worker's capacity, and returns the worker's leases.
func (r *Registry) Heartbeat(_ context.Context, req *landmarkv1.HeartbeatRequest) (*landmarkv1.HeartbeatResponse, error) {
	w := req.GetWorkerId()
	if w == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.expire(now)
	for _, h := range req.GetHeld() {
		// Renew at the same epoch only: a lease that expired but was not
		// granted again can be taken back, since no one else held it.
		if s := r.streams[h.GetStreamId()]; s != nil && s.epoch == h.GetEpoch() && (s.holder == "" || s.holder == w) {
			s.holder, s.expires = w, now.Add(r.ttl)
		}
	}
	held := 0
	for _, s := range r.streams {
		if s.holder == w {
			held++
		}
	}
	var grants []*stream
	if !now.Before(r.grantAfter) {
		for _, id := range r.ids {
			if held >= int(req.GetCapacity()) {
				break
			}
			if s := r.streams[id]; s.holder == "" {
				grants = append(grants, s)
				held++
			}
		}
	}
	if len(grants) > 0 {
		for _, s := range grants {
			s.epoch++
		}
		if err := r.saveState(); err != nil {
			for _, s := range grants {
				s.epoch--
			}
			return nil, status.Errorf(codes.Unavailable, "save state: %v", err)
		}
		for _, s := range grants {
			s.holder, s.expires = w, now.Add(r.ttl)
		}
		r.m.grants.Add(float64(len(grants)))
	}
	resp := &landmarkv1.HeartbeatResponse{TtlMs: uint32(r.ttl / time.Millisecond)}
	for _, id := range r.ids {
		if s := r.streams[id]; s.holder == w {
			resp.Leases = append(resp.Leases, &landmarkv1.Lease{StreamId: s.id, Url: s.url, Epoch: s.epoch,
				NextSeq: uint64(len(s.events)) + 1, Open: s.open})
		}
	}
	return resp, nil
}

// expire frees leases whose TTL has passed.
func (r *Registry) expire(now time.Time) {
	for _, s := range r.streams {
		if s.holder != "" && now.After(s.expires) {
			s.holder = ""
			r.m.expired.Inc()
		}
	}
}

// Append logs events at the stream's current epoch (see the package
// comment). Events already logged may be re-sent unchanged.
func (r *Registry) Append(_ context.Context, req *landmarkv1.AppendRequest) (*landmarkv1.AppendResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.streams[req.GetStreamId()]
	if s == nil {
		return nil, status.Errorf(codes.NotFound, "no stream %q", req.GetStreamId())
	}
	if req.GetEpoch() != s.epoch {
		r.m.rejected.WithLabelValues("epoch").Inc()
		return nil, status.Errorf(codes.FailedPrecondition, "stale epoch %d: stream %s is at epoch %d", req.GetEpoch(), s.id, s.epoch)
	}
	next := uint64(len(s.events)) + 1
	var fresh []*landmarkv1.Event
	for i, e := range req.GetEvents() {
		seq := e.GetSeq()
		if seq == 0 || (i > 0 && seq != req.GetEvents()[i-1].GetSeq()+1) {
			r.m.rejected.WithLabelValues("sequence").Inc()
			return nil, status.Errorf(codes.InvalidArgument, "event sequence numbers must be consecutive from 1")
		}
		switch {
		case seq < next:
			if !sameEvent(e, s.events[seq-1]) {
				r.m.rejected.WithLabelValues("conflict").Inc()
				return nil, status.Errorf(codes.FailedPrecondition, "event %d differs from the logged one", seq)
			}
		case seq == next+uint64(len(fresh)):
			fresh = append(fresh, e)
		default:
			r.m.rejected.WithLabelValues("sequence").Inc()
			return nil, status.Errorf(codes.FailedPrecondition, "sequence gap: event %d, next is %d", seq, next)
		}
	}
	if len(fresh) == 0 {
		return &landmarkv1.AppendResponse{NextSeq: next}, nil
	}
	logged := float64(r.now().UnixNano()) / 1e9
	var buf bytes.Buffer
	out := make([]*landmarkv1.Event, len(fresh))
	for i, e := range fresh {
		c := proto.CloneOf(e)
		c.StreamId, c.Epoch, c.LoggedUnix = s.id, s.epoch, logged
		line, err := protojson.Marshal(c)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "encode event: %v", err)
		}
		buf.Write(compact(line))
		buf.WriteByte('\n')
		out[i] = c
	}
	if err := s.write(buf.Bytes()); err != nil {
		return nil, status.Errorf(codes.Unavailable, "write log: %v", err)
	}
	for _, e := range out {
		s.addEvent(e)
		r.notify(e)
	}
	r.m.events.Add(float64(len(out)))
	return &landmarkv1.AppendResponse{NextSeq: uint64(len(s.events)) + 1}, nil
}

// write appends complete lines and fsyncs; on failure it cuts the file back
// so no partial line stays.
func (s *stream) write(b []byte) error {
	_, err := s.log.Write(b)
	if err == nil {
		err = s.log.Sync()
	}
	if err != nil {
		_ = s.log.Truncate(s.size)
		_, _ = s.log.Seek(s.size, io.SeekStart)
		return err
	}
	s.size += int64(len(b))
	return nil
}

// compact puts protojson output on one line (it may add spaces, never
// newlines inside strings, which it escapes).
func compact(b []byte) []byte {
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		return b
	}
	return out.Bytes()
}

// sameEvent compares a re-sent event with the logged one, ignoring the
// fields the registry sets.
func sameEvent(sent, logged *landmarkv1.Event) bool {
	c := proto.CloneOf(sent)
	c.StreamId, c.Epoch, c.LoggedUnix = logged.GetStreamId(), logged.GetEpoch(), logged.GetLoggedUnix()
	return proto.Equal(c, logged)
}

// notify sends a logged event to subscribers; one that has fallen
// SubscriberBuffer events behind is dropped (its stream ends with an error
// and it can subscribe again, replaying the log). Callers hold r.mu.
func (r *Registry) notify(e *landmarkv1.Event) {
	for sub := range r.subs {
		if len(sub.want) > 0 && !sub.want[e.GetStreamId()] {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			close(sub.ch)
			delete(r.subs, sub)
			r.m.dropped.Inc()
		}
	}
}

// Subscribe sends the logged events of the requested streams (unless
// live_only), in the order they were logged, then new ones as they are
// logged.
func (r *Registry) Subscribe(req *landmarkv1.SubscribeRequest, out grpc.ServerStreamingServer[landmarkv1.SubscribeResponse]) error {
	sub := &subscriber{want: map[string]bool{}, ch: make(chan *landmarkv1.Event, r.bufferSize)}
	for _, id := range req.GetStreamIds() {
		sub.want[id] = true
	}
	r.mu.Lock()
	var replay []*landmarkv1.Event
	if !req.GetLiveOnly() {
		for _, id := range r.ids {
			if len(sub.want) == 0 || sub.want[id] {
				replay = append(replay, r.streams[id].events...)
			}
		}
		slices.SortStableFunc(replay, func(a, b *landmarkv1.Event) int { return cmp.Compare(a.GetLoggedUnix(), b.GetLoggedUnix()) })
	}
	r.subs[sub] = struct{}{} // under the same lock as the snapshot: no gap, no repeat
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.subs, sub)
		r.mu.Unlock()
	}()
	for _, e := range replay {
		if err := out.Send(&landmarkv1.SubscribeResponse{Event: e}); err != nil {
			return err
		}
	}
	ctx := out.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-sub.ch:
			if !ok {
				return status.Error(codes.ResourceExhausted, "subscriber fell behind; subscribe again to replay")
			}
			if err := out.Send(&landmarkv1.SubscribeResponse{Event: e}); err != nil {
				return err
			}
		}
	}
}

// ListStreams reports every stream's holder, epoch, and next sequence
// number.
func (r *Registry) ListStreams(context.Context, *landmarkv1.ListStreamsRequest) (*landmarkv1.ListStreamsResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire(r.now())
	resp := &landmarkv1.ListStreamsResponse{}
	for _, id := range r.ids {
		s := r.streams[id]
		resp.Streams = append(resp.Streams, &landmarkv1.StreamStatus{Stream: &landmarkv1.Stream{Id: s.id, Url: s.url},
			Holder: s.holder, Epoch: s.epoch, NextSeq: uint64(len(s.events)) + 1})
	}
	return resp, nil
}

type metrics struct {
	grants, expired, events, dropped prometheus.Counter
	rejected                         *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		grants: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_registry_grants_total", Help: "Leases granted (each raises an epoch)."}),
		expired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_registry_leases_expired_total", Help: "Leases freed because the holder stopped renewing."}),
		events: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_registry_events_total", Help: "Events logged."}),
		dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "landmark_registry_subscribers_dropped_total", Help: "Subscribers dropped for falling behind."}),
		rejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "landmark_registry_appends_rejected_total", Help: "Appends refused, by reason."}, []string{"reason"}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{m.grants, m.expired, m.events, m.dropped, m.rejected} {
			if err := reg.Register(c); err != nil {
				return nil, fmt.Errorf("registry: metrics: %w", err)
			}
		}
	}
	return m, nil
}
