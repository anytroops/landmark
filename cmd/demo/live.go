package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
)

// liveEvent is one live monitoring event as the page shows it.
type liveEvent struct {
	Stream      string  `json:"stream"`
	Seq         uint64  `json:"seq"`
	Kind        string  `json:"kind"` // start or end
	Song        song    `json:"song"`
	StartUnix   float64 `json:"start_unix"`
	EndUnix     float64 `json:"end_unix"`
	TempoPct    float64 `json:"tempo_pct"`
	Resumed     bool    `json:"resumed,omitempty"`
	Interrupted bool    `json:"interrupted,omitempty"`
	Worker      string  `json:"worker"`
	DecidedUnix float64 `json:"decided_unix"`
}

// live streams live monitoring as server-sent events: "streams" (the
// registry's streams and holders) when the page connects and every 5 s,
// then every event already logged and each new one ("event"). A
// subscriber the registry drops for falling behind subscribes again,
// skipping what it has sent (as cmd/events does).
func (s *server) live(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil {
		httpError(w, http.StatusNotFound, "live monitoring is not running (start the demo with stations)")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	send := func(name string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	streams := func() error {
		list, err := s.reg.ListStreams(ctx, &landmarkv1.ListStreamsRequest{})
		if err != nil {
			return err
		}
		type streamOut struct {
			ID     string `json:"id"`
			URL    string `json:"url"`
			Holder string `json:"holder"`
			Epoch  uint64 `json:"epoch"`
			Events uint64 `json:"events"`
		}
		var out []streamOut
		for _, st := range list.GetStreams() {
			out = append(out, streamOut{ID: st.GetStream().GetId(), URL: st.GetStream().GetUrl(),
				Holder: st.GetHolder(), Epoch: st.GetEpoch(), Events: st.GetNextSeq() - 1})
		}
		return send("streams", out)
	}
	if err := streams(); err != nil {
		_ = send("problem", map[string]string{"error": err.Error()})
		return
	}
	events := make(chan *landmarkv1.Event, 256)
	subErr := make(chan error, 1)
	go func() { subErr <- s.follow(r, events) }()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-subErr:
			if err != nil {
				_ = send("problem", map[string]string{"error": err.Error()})
			}
			return
		case <-tick.C:
			if streams() != nil {
				return
			}
		case e := <-events:
			kind := "start"
			if e.GetKind() == landmarkv1.EventKind_EVENT_KIND_END {
				kind = "end"
			}
			if send("event", liveEvent{Stream: e.GetStreamId(), Seq: e.GetSeq(), Kind: kind,
				Song: s.song(int(e.GetTrackId())), StartUnix: e.GetStartUnix(), EndUnix: e.GetEndUnix(),
				TempoPct: 100 * (e.GetTempo() - 1), Resumed: e.GetResumed(), Interrupted: e.GetInterrupted(),
				Worker: e.GetWorkerId(), DecidedUnix: e.GetDecidedUnix()}) != nil {
				return
			}
		}
	}
}

// follow subscribes to every stream's events and sends each once, in
// order per stream, until the request ends.
func (s *server) follow(r *http.Request, out chan<- *landmarkv1.Event) error {
	ctx := r.Context()
	seen := map[string]uint64{}
	for {
		sub, err := s.reg.Subscribe(ctx, &landmarkv1.SubscribeRequest{})
		if err != nil {
			return err
		}
		for {
			m, err := sub.Recv()
			if err != nil {
				if ctx.Err() != nil || status.Code(err) == codes.Canceled {
					return nil // the page left, or this server is shutting down
				}
				if status.Code(err) == codes.ResourceExhausted || errors.Is(err, io.EOF) {
					break // dropped or closed: subscribe again
				}
				return err
			}
			e := m.GetEvent()
			if e.GetSeq() <= seen[e.GetStreamId()] {
				continue
			}
			seen[e.GetStreamId()] = e.GetSeq()
			select {
			case out <- e:
			case <-ctx.Done():
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}
