package hybrid

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/tracklist"
	"github.com/anytroops/landmark/internal/triplet"
)

const (
	testWindow = 10 * audio.SampleRate
	testHop    = 27520 // the shared front end's hop (D48)
)

// TestFrontCutsTheStreamsFingerprints: at every speed, a window's
// landmarks are the query fingerprint of that window of the stream (at its
// speed, the stream resampled as a whole), pushed in uneven pieces.
func TestFrontCutsTheStreamsFingerprints(t *testing.T) {
	x := noise(45 * audio.SampleRate)
	s := &Searcher{Shifts: 4, Extra: []float64{2, 0.5}}
	f, err := s.NewFront(testWindow)
	if err != nil {
		t.Fatal(err)
	}
	if f.Align() != 128 {
		t.Fatalf("align %d, want 128 (half time needs S/2 on the 64-sample grid)", f.Align())
	}
	fp := triplet.New(triplet.DefaultParams())
	whole := [][]float32{x, audio.ResamplePoly(x, 2, 1), audio.ResamplePoly(x, 1, 2)}
	ratios := [][2]int{{1, 1}, {2, 1}, {1, 2}}
	want := func(start, speed int) []triplet.Landmark {
		p, q := ratios[speed][0], ratios[speed][1]
		a, n := start*p/q, (testWindow*p+q-1)/q
		return fp.FingerprintQuery(whole[speed][a:a+n], 4)
	}
	rng := rand.New(rand.NewPCG(1, 1))
	next, pushed, checked := 0, 0, 0
	for pushed < len(x) {
		n := min(1+rng.IntN(20000), len(x)-pushed)
		f.Push(x[pushed : pushed+n])
		if pushed += n; pushed == len(x) {
			f.Close()
		}
		for next+testWindow <= len(x) && f.Ready(next) {
			got, err := f.Landmarks(next)
			if err != nil {
				t.Fatal(err)
			}
			for speed := range got {
				if w := want(next, speed); !slices.Equal(got[speed], w) {
					t.Fatalf("window at %d, speed %d: %d landmarks, want %d", next, speed, len(got[speed]), len(w))
				}
			}
			next += testHop
			f.Forget(next)
			checked++
		}
	}
	if checked != len(WindowStarts(len(x), testWindow, testHop)) {
		t.Fatalf("checked %d windows of %d", checked, len(WindowStarts(len(x), testWindow, testHop)))
	}
	if _, err := f.Landmarks(64); err == nil {
		t.Error("a start off the 128-sample grid was accepted")
	}
}

// hashAnswer answers by the request's content, so rows show whether two
// paths sent the same landmarks.
func hashAnswer(_ context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
	h := fnv.New32a()
	for i, x := range req.GetHashes() {
		_, _ = h.Write([]byte{byte(x), byte(x >> 8), byte(x >> 16), byte(req.GetTimes()[i])})
	}
	return &landmarkv1.IdentifyResponse{Candidates: []*landmarkv1.Candidate{
		{TrackId: h.Sum32() % 1000, Score: uint32(len(req.GetHashes()))}}}, nil
}

// TestSearchMix: windows come back in order, the shared front end sends
// the same triplet searches as fingerprinting each window, and the pair
// searches are the same either way.
func TestSearchMix(t *testing.T) {
	x := noise(40 * audio.SampleRate)
	s := &Searcher{B2: hashAnswer, Pairs: hashAnswer, Cluster: func(d uint32) uint32 { return d }, Shifts: 4,
		Extra: []float64{2, 0.5}, Alpha: 3, Timeout: 10 * time.Second}
	collect := func(front bool) [][]tracklist.Row {
		var out [][]tracklist.Row
		err := s.SearchMix(context.Background(), x, testWindow, testHop, 3, front,
			func(i, start int, rows []tracklist.Row, _ int, err error) error {
				if err != nil {
					return err
				}
				if i != len(out) || start != i*testHop {
					t.Fatalf("window %d at %d out of order", i, start)
				}
				out = append(out, rows)
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	shared, own := collect(true), collect(false)
	if len(shared) != len(WindowStarts(len(x), testWindow, testHop)) || len(own) != len(shared) {
		t.Fatalf("%d and %d windows", len(shared), len(own))
	}
	for i := range shared {
		if !slices.Equal(shared[i], own[i]) {
			t.Errorf("window %d: shared front end %v, per window %v", i, shared[i], own[i])
		}
	}
	if err := s.SearchMix(context.Background(), x, testWindow, 27562, 2, true,
		func(int, int, []tracklist.Row, int, error) error { return nil }); err == nil {
		t.Error("a hop off the front end's grid was accepted")
	}
	stop := errors.New("stop")
	if err := s.SearchMix(context.Background(), x, testWindow, testHop, 2, true,
		func(i int, _ int, _ []tracklist.Row, _ int, _ error) error {
			if i == 2 {
				return stop
			}
			return nil
		}); !errors.Is(err, stop) {
		t.Errorf("emit's error: got %v", err)
	}
}

// BenchmarkLiveWindow is one live window's fingerprinting as the worker
// does it with the shared front end: a hop of audio pushed in quarter
// seconds, the window's triplet landmarks cut at every speed, and its pair
// landmarks (WindowFrom). One op is a 60 s stream; the metrics are per
// window.
func BenchmarkLiveWindow(b *testing.B) {
	x := noise(60 * audio.SampleRate)
	s := &Searcher{Shifts: 4, Extra: []float64{2, 0.5}}
	fp := NewFingerprinters()
	windows := len(WindowStarts(len(x), testWindow, testHop))
	b.ReportAllocs()
	for b.Loop() {
		f, err := s.NewFront(testWindow)
		if err != nil {
			b.Fatal(err)
		}
		next := 0
		for pushed := 0; pushed < len(x); {
			n := min(audio.SampleRate/4, len(x)-pushed)
			f.Push(x[pushed : pushed+n])
			if pushed += n; pushed == len(x) {
				f.Close()
			}
			for next+testWindow <= len(x) && f.Ready(next) {
				if _, err := f.Landmarks(next); err != nil {
					b.Fatal(err)
				}
				fp.pair.FingerprintQuery(x[next:next+testWindow], s.Shifts)
				next += testHop
				f.Forget(next)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Microseconds())/1000/float64(b.N*windows), "ms/window")
	b.ReportMetric(0, "ns/op")
}
