package hybrid

import (
	"context"
	"fmt"
	"sync"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/tracklist"
	"github.com/anytroops/landmark/internal/triplet"
)

// Front is the shared front end of one stream (D48): the stream resampled
// once per extra tempo factor (audio.Resampler) and, at each speed, its
// triplet spectrogram frames computed once (triplet.Stream), so a window's
// triplet landmarks are cut out of shared frames instead of computed again
// for every overlapping window. Windows must start on a multiple of Align
// samples so that every speed's sub-hop grids line up.
//
// Landmarks equals Window's per-window fingerprints for the window as is.
// At the extra factors the stream, not the window, is resampled, which
// changes only a window's outer few samples (the resampling filter reaches
// 20 samples past an edge, where the spectrogram's Hann window is near
// zero; BENCHMARKS B14 found the same landmarks for every window). Offline
// and live use Front alike, so live still equals offline.
//
// A Front is not safe for concurrent use.
type Front struct {
	window int
	align  int
	speeds []frontSpeed
}

type frontSpeed struct {
	p, q int
	rs   *audio.Resampler // nil: the stream as is
	st   *triplet.Stream
}

// NewFront returns a front end for this searcher's windows of window
// samples (the stream's rate).
func (s *Searcher) NewFront(window int) (*Front, error) {
	f := &Front{window: window, align: 1}
	add := func(p, q int, rs *audio.Resampler) error {
		st, err := triplet.NewStream(triplet.DefaultParams(), s.Shifts)
		if err != nil {
			return err
		}
		if window*p%q != 0 && p != q {
			// ResamplePoly's output length is ceil(n p / q); keep it exact.
			return fmt.Errorf("hybrid: a %d-sample window has no whole length at %d/%d", window, p, q)
		}
		f.speeds = append(f.speeds, frontSpeed{p: p, q: q, rs: rs, st: st})
		// A start S lands on the grid at this speed when S p / q is a
		// multiple of the grid: S a multiple of grid q / gcd(p, grid q).
		g := st.Step() * q
		f.align = lcm(f.align, g/gcd(p, g))
		return nil
	}
	if err := add(1, 1, nil); err != nil {
		return nil, err
	}
	for _, x := range s.Extra {
		p, q, ok := Ratio(x)
		if !ok {
			return nil, fmt.Errorf("tempo factor %v is not a ratio of integers up to 1000", x)
		}
		if err := add(p, q, audio.NewResampler(p, q)); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// Align is the multiple of samples every window start must be.
func (f *Front) Align() int { return f.align }

// Push adds the stream's next samples.
func (f *Front) Push(x []float32) {
	for _, sp := range f.speeds {
		if sp.rs == nil {
			sp.st.Push(x)
		} else {
			sp.st.Push(sp.rs.Push(x))
		}
	}
}

// Close ends the stream: the resamplers produce their last samples.
func (f *Front) Close() {
	for _, sp := range f.speeds {
		if sp.rs != nil {
			sp.st.Push(sp.rs.Close())
		}
	}
}

func (sp frontSpeed) span(start, window int) (int, int) {
	return start * sp.p / sp.q, (window*sp.p + sp.q - 1) / sp.q
}

// Ready reports whether the window starting at sample start can be cut.
func (f *Front) Ready(start int) bool {
	for _, sp := range f.speeds {
		if !sp.st.Ready(sp.span(start, f.window)) {
			return false
		}
	}
	return true
}

// Landmarks returns the window's triplet landmarks: [0] as is, [i+1] for
// the searcher's Extra[i], as WindowFrom takes them.
func (f *Front) Landmarks(start int) ([][]triplet.Landmark, error) {
	if start%f.align != 0 {
		return nil, fmt.Errorf("hybrid: window start %d is not a multiple of %d", start, f.align)
	}
	out := make([][]triplet.Landmark, len(f.speeds))
	for i, sp := range f.speeds {
		lms, err := sp.st.Query(sp.span(start, f.window))
		if err != nil {
			return nil, err
		}
		out[i] = lms
	}
	return out, nil
}

// Forget drops what only windows starting before start need.
func (f *Front) Forget(start int) {
	for _, sp := range f.speeds {
		sp.st.Forget(start * sp.p / sp.q)
	}
}

// WindowStarts returns the start sample of each full window of an n-sample
// signal.
func WindowStarts(n, window, hop int) []int {
	var out []int
	for a := 0; a+window <= n; a += hop {
		out = append(out, a)
	}
	return out
}

// SearchMix runs every window's searches over a whole recording (window and
// hop in samples) on workers goroutines and calls emit with each window's
// rows in window order; an error from emit stops it. With front, triplet
// landmarks come from a shared front end (D48; hop must be a multiple of
// its Align), computed in order as the workers search earlier windows;
// otherwise each window fingerprints itself (Window). A window whose
// searches fail is emitted with its error.
func (s *Searcher) SearchMix(ctx context.Context, pcm []float32, window, hop, workers int, front bool,
	emit func(i, start int, rows []tracklist.Row, partial int, err error) error) error {
	starts := WindowStarts(len(pcm), window, hop)
	var fe *Front
	if front {
		var err error
		if fe, err = s.NewFront(window); err != nil {
			return err
		}
		if hop%fe.Align() != 0 {
			return fmt.Errorf("hybrid: a %d-sample hop is not a multiple of the front end's %d", hop, fe.Align())
		}
	}
	type job struct {
		i     int
		trips [][]triplet.Landmark
	}
	type result struct {
		rows    []tracklist.Row
		partial int
		err     error
	}
	results := make([]chan result, len(starts))
	for i := range results {
		results[i] = make(chan result, 1) // each written once: workers never block
	}
	jobs := make(chan job, max(workers, 1))
	var wg sync.WaitGroup
	defer wg.Wait() // after cancel, below: it stops the feeder and every search
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for range max(workers, 1) {
		wg.Go(func() {
			fp := NewFingerprinters()
			for j := range jobs {
				x := pcm[starts[j.i] : starts[j.i]+window]
				t0 := WindowT0(starts[j.i])
				var r result
				if j.trips != nil {
					r.rows, r.partial, r.err = s.WindowFrom(ctx, x, t0, j.trips, fp)
				} else {
					r.rows, r.partial, r.err = s.Window(ctx, x, t0, fp)
				}
				results[j.i] <- r
			}
		})
	}
	var feedErr error
	feedDone := make(chan struct{})
	wg.Go(func() {
		defer close(feedDone)
		defer close(jobs)
		err := feed(ctx, pcm, starts, fe, func(i int, trips [][]triplet.Landmark) bool {
			select {
			case jobs <- job{i, trips}:
				return true
			case <-ctx.Done():
				return false
			}
		})
		if err != nil && ctx.Err() == nil {
			feedErr = err
			cancel()
		}
	})
	for i := range starts {
		var r result
		select {
		case r = <-results[i]:
		case <-ctx.Done():
			<-feedDone
			if feedErr != nil {
				return feedErr
			}
			return ctx.Err()
		}
		if err := emit(i, starts[i], r.rows, r.partial, r.err); err != nil {
			return err
		}
	}
	return nil
}

// feed hands out the windows in order: with a front end, after pushing the
// audio each needs and cutting its landmarks.
func feed(ctx context.Context, pcm []float32, starts []int, fe *Front, send func(int, [][]triplet.Landmark) bool) error {
	const chunk = audio.SampleRate // samples pushed at a time
	pushed := 0
	for i, start := range starts {
		var trips [][]triplet.Landmark
		if fe != nil {
			for !fe.Ready(start) {
				if pushed == len(pcm) {
					return fmt.Errorf("hybrid: window at sample %d never completed", start)
				}
				n := min(chunk, len(pcm)-pushed)
				fe.Push(pcm[pushed : pushed+n])
				if pushed += n; pushed == len(pcm) {
					fe.Close()
				}
			}
			var err error
			if trips, err = fe.Landmarks(start); err != nil {
				return err
			}
			if i+1 < len(starts) {
				fe.Forget(starts[i+1])
			}
		}
		if !send(i, trips) || ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func lcm(a, b int) int { return a / gcd(a, b) * b }
