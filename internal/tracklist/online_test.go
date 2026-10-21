package tracklist

import (
	"math"
	"math/rand/v2"
	"strconv"
	"testing"
)

// windowsOf splits sorted rows into windows.
func windowsOf(rows []Row) [][]Row {
	var out [][]Row
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].T0 == rows[i].T0 {
			j++
		}
		out = append(out, rows[i:j])
		i = j
	}
	return out
}

// offline is what cmd/tracklist does with the smoothing decoder.
func offline(rows []Row, window, tAbs, tRatio float64, k int, gap float64) []Entry {
	e := Smooth(rows, window, tAbs, tRatio, k)
	if gap >= 0 {
		e = MergeRepeats(e, gap)
	}
	return e
}

type timedEvent struct {
	Event
	at int // index of the window whose Push returned it; -1: Close
}

func runOnline(t *testing.T, o *Online, windows [][]Row) []timedEvent {
	t.Helper()
	var out []timedEvent
	for i, w := range windows {
		ev, err := o.Push(w)
		if err != nil {
			t.Fatalf("window %d: %v", i, err)
		}
		for _, e := range ev {
			out = append(out, timedEvent{e, i})
		}
	}
	for _, e := range o.Close() {
		out = append(out, timedEvent{e, -1})
	}
	return out
}

// checkOnline requires the online end events to equal the offline entries,
// each preceded by its start event, sent as soon as the entry's first window
// had k windows after it.
func checkOnline(t *testing.T, name string, rows []Row, window, tAbs, tRatio float64, k int, gap float64) {
	t.Helper()
	SortRows(rows)
	windows := windowsOf(rows)
	index := map[float64]int{}
	for i, w := range windows {
		index[w[0].T0] = i
	}
	want := offline(rows, window, tAbs, tRatio, k, gap)
	events := runOnline(t, NewOnline(window, tAbs, tRatio, k, gap), windows)
	var ends []Entry
	open := -1 // index into events of the start without an end
	for i, e := range events {
		switch e.Kind {
		case Start:
			if open >= 0 {
				t.Fatalf("%s: start %+v while %+v is open", name, e.Entry, events[open].Entry)
			}
			open = i
			first := index[e.Entry.Start]
			if wantAt := first + k; wantAt < len(windows) && e.at != wantAt {
				t.Errorf("%s: start of %+v sent after window %d, want %d", name, e.Entry, e.at, wantAt)
			}
		case End:
			if open < 0 || events[open].Entry.Track != e.Entry.Track || events[open].Entry.Start != e.Entry.Start {
				t.Fatalf("%s: end %+v without its start", name, e.Entry)
			}
			if e.Resumed || e.Interrupted {
				t.Errorf("%s: end %+v flagged without a resume", name, e.Entry)
			}
			open = -1
			ends = append(ends, e.Entry)
		}
	}
	if open >= 0 {
		t.Fatalf("%s: %+v never ended", name, events[open].Entry)
	}
	if len(ends) != len(want) {
		t.Fatalf("%s: %d entries online, %d offline:\n got %+v\nwant %+v", name, len(ends), len(want), ends, want)
	}
	for i := range ends {
		if ends[i] != want[i] {
			t.Errorf("%s: entry %d online %+v, offline %+v", name, i, ends[i], want[i])
		}
	}
}

// randomRows makes a mix-like sequence: a true track that changes now and
// then, windows that find it, a rival, or nothing, under several
// hypotheses, with small integer scores so ties occur.
func randomRows(rng *rand.Rand, n int) []Row {
	modes := []string{"b2", "b2_speed", "keylock", "none"}
	var rows []Row
	truth := rng.IntN(6)
	for i := range n {
		if rng.IntN(8) == 0 {
			truth = rng.IntN(6) - 1 // -1: unknown music
		}
		t0 := math.Round(float64(i)*2.5*1000) / 1000
		for h := range 1 + rng.IntN(4) {
			r := Row{T0: t0, Mode: modes[h], Factor: 1 + float64(rng.IntN(9)-4)/100, Track: -1,
				Offset: float64(rng.IntN(5000))}
			switch x := rng.IntN(10); {
			case x < 6 && truth >= 0:
				r.Track = truth
			case x < 9:
				r.Track = rng.IntN(6)
			}
			if r.Track >= 0 {
				r.Score = float64(rng.IntN(40))
				r.RunnerUp = float64(rng.IntN(15))
			}
			rows = append(rows, r)
		}
	}
	return rows
}

func TestOnlineEqualsOffline(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := range 300 {
		rows := randomRows(rng, 1+rng.IntN(80))
		for _, k := range []int{0, 1, 2, 3} {
			for _, gap := range []float64{-1, 0, 2.5, 7.5, 20} {
				name := "trial " + itoa(trial) + " k " + itoa(k) + " gap " + ftoa(gap)
				checkOnline(t, name, append([]Row(nil), rows...), 10, 10, 2, k, gap)
			}
		}
	}
}

func TestOnlineEqualsOfflineOnGoldens(t *testing.T) {
	for _, g := range loadGoldens(t) {
		p := g.SmoothParams
		for _, gap := range []float64{-1, 2.5} {
			checkOnline(t, g.Source+" gap "+ftoa(gap), g.rows(), g.WindowS, p.TAbs, p.TRatio, p.K, gap)
		}
	}
}

// steady is n windows from t0 = 0 where track wins clearly (-1: nothing).
func steady(tracks ...int) [][]Row {
	var out [][]Row
	for i, tr := range tracks {
		r := Row{T0: 2.5 * float64(i), Mode: "b2", Factor: 1.02, Track: tr}
		if tr >= 0 {
			r.Score = 50
		}
		out = append(out, []Row{r})
	}
	return out
}

func TestOnlineResume(t *testing.T) {
	prev := Entry{Track: 7, Start: -60, End: -50, Factor: 1.01, Evidence: 100}
	tests := []struct {
		name    string
		windows [][]Row
		want    []Event
	}{
		{
			name:    "same track continues: one end, original start",
			windows: steady(7, 7, 7, 3, 3, 3),
			want: []Event{
				{Kind: End, Entry: Entry{Track: 7, Start: -60, End: 15, Factor: 1.02, Evidence: 150}, Resumed: true},
				{Kind: Start, Entry: Entry{Track: 3, Start: 7.5, End: 17.5, Factor: 1.02, Evidence: 50}},
				{Kind: End, Entry: Entry{Track: 3, Start: 7.5, End: 22.5, Factor: 1.02, Evidence: 150}},
			},
		},
		{
			name:    "another track: interrupted at 0",
			windows: steady(3, 3, 3),
			want: []Event{
				{Kind: End, Entry: Entry{Track: 7, Start: -60, End: 0, Factor: 1.01}, Interrupted: true},
				{Kind: Start, Entry: Entry{Track: 3, Start: 0, End: 10, Factor: 1.02, Evidence: 50}},
				{Kind: End, Entry: Entry{Track: 3, Start: 0, End: 15, Factor: 1.02, Evidence: 150}},
			},
		},
		{
			name:    "nothing found within the resume time: interrupted",
			windows: steady(-1, -1, -1, -1, -1, 7, 7, 7),
			want: []Event{
				{Kind: End, Entry: Entry{Track: 7, Start: -60, End: 0, Factor: 1.01}, Interrupted: true},
				{Kind: Start, Entry: Entry{Track: 7, Start: 12.5, End: 22.5, Factor: 1.02, Evidence: 50}},
				{Kind: End, Entry: Entry{Track: 7, Start: 12.5, End: 27.5, Factor: 1.02, Evidence: 150}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOnline(10, 10, 2, 1, 2.5)
			if err := o.Resume(prev, 10); err != nil {
				t.Fatal(err)
			}
			var got []Event
			for _, e := range runOnline(t, o, tt.windows) {
				got = append(got, e.Event)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("events %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestOnlineRejectsBadWindows(t *testing.T) {
	o := NewOnline(10, 10, 2, 1, 2.5)
	if _, err := o.Push(nil); err == nil {
		t.Error("empty window: want an error")
	}
	if _, err := o.Push([]Row{{T0: 5}, {T0: 7.5}}); err == nil {
		t.Error("rows of two windows: want an error")
	}
	if _, err := o.Push([]Row{{T0: 5, Track: -1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Push([]Row{{T0: 5, Track: -1}}); err == nil {
		t.Error("repeated window start: want an error")
	}
	if err := o.Resume(Entry{}, 10); err == nil {
		t.Error("Resume after a window: want an error")
	}
	o.Close()
	if _, err := o.Push([]Row{{T0: 10, Track: -1}}); err == nil {
		t.Error("Push after Close: want an error")
	}
}

func itoa(i int) string     { return ftoa(float64(i)) }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// TestOnlineEndsAsSoonAsFinal: an entry ends when a window starting at its
// end plus the merge gap is final, the first that could not continue it.
func TestOnlineEndsAsSoonAsFinal(t *testing.T) {
	windows := steady(7, 7, -1, -1, -1, -1, -1, -1, -1, -1) // entry 0 to 12.5 s
	for _, e := range runOnline(t, NewOnline(10, 10, 2, 1, 2.5), windows) {
		// The window at 15 s (index 6) is final once index 7 is in.
		if e.Kind == End && e.at != 7 {
			t.Errorf("end %+v sent after window %d, want 7", e.Entry, e.at)
		}
	}
}
