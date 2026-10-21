package tracklist

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// EventKind says what an Online event reports.
type EventKind int

const (
	// Start reports that a new entry's first window is final. Factor and
	// Evidence cover that window only.
	Start EventKind = iota + 1
	// End reports a finished entry, equal to the offline decoder's.
	End
)

func (k EventKind) String() string {
	switch k {
	case Start:
		return "start"
	case End:
		return "end"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Event is one decision of an Online decoder.
type Event struct {
	Kind  EventKind
	Entry Entry
	// Resumed: the entry began before a failover (see Resume) and this
	// decoder continued it. Interrupted: such an entry did not continue; its
	// End is this decoder's first audio, a bound on when it really ended.
	Resumed, Interrupted bool
}

// Online is the streaming form of Smooth followed by MergeRepeats (D38). A
// window's label is final once k more windows are in; a start event is sent
// when an entry's first window is final, and an end event when the entry
// can no longer grow or merge. The end events are the offline entries,
// value for value, and no event is ever retracted: whether a run of windows
// continues the previous entry (MergeRepeats) is known when the run begins.
type Online struct {
	window, tAbs, tRatio, gap float64
	k                         int

	// Windows not yet final, after up to k final ones (the majority vote's
	// left context): raw labels and best rows, oldest first.
	labels []int
	best   []Row
	final  int // how many of the buffered windows are final

	run  *openRun
	open *Entry // the entry being built: its start is sent, its end not
	// pending: open came from Resume and no run has continued it yet;
	// carried: it came from Resume and a run did.
	pending, carried bool
	resumeWithin     float64
	lastT0           float64
	pushed           int
	closed           bool
}

// openRun is a run of windows with the same final label.
type openRun struct {
	label      int
	start, end float64
	factors    []float64
	evidence   float64
	continues  bool // part of the open entry rather than a new one
	announced  bool // its first window has been handled
}

// NewOnline returns a decoder with Smooth's parameters and MergeRepeats'
// gap (negative: never merge, as when the merge step is off).
func NewOnline(window, tAbs, tRatio float64, k int, mergeGap float64) *Online {
	return &Online{window: window, tAbs: tAbs, tRatio: tRatio, k: max(k, 0), gap: mergeGap}
}

// Resume makes an entry that began before this decoder's first audio (a
// failover: its start event is already logged) the entry in progress, in
// this decoder's time (its Start is then negative). A run of its track that
// begins at a window start up to within continues it, and the end event
// keeps e's Start; otherwise the entry ends, flagged Interrupted, at time 0.
// Call it before the first Push.
func (o *Online) Resume(e Entry, within float64) error {
	if o.pushed > 0 || o.open != nil {
		return errors.New("tracklist: Resume after the first window")
	}
	e.Evidence, e.End = 0, 0
	o.open, o.pending, o.resumeWithin = &e, true, within
	return nil
}

// Push adds every row of the next window (one per hypothesis, all with the
// same T0, later than the previous window's) and returns the events it
// makes final.
func (o *Online) Push(rows []Row) ([]Event, error) {
	if o.closed {
		return nil, errors.New("tracklist: Push after Close")
	}
	if len(rows) == 0 {
		return nil, errors.New("tracklist: window without rows")
	}
	t0 := rows[0].T0
	for _, r := range rows {
		if r.T0 != t0 {
			return nil, fmt.Errorf("tracklist: window rows start at %v and %v", t0, r.T0)
		}
	}
	if o.pushed > 0 && t0 <= o.lastT0 {
		return nil, fmt.Errorf("tracklist: window at %v after one at %v", t0, o.lastT0)
	}
	sorted := slices.Clone(rows)
	SortRows(sorted)
	b := bestPerWindow(sorted)[0]
	label := -1
	if accepted(b, o.tAbs, o.tRatio) {
		label = b.Track
	}
	o.labels, o.best = append(o.labels, label), append(o.best, b)
	o.lastT0 = t0
	o.pushed++
	var ev []Event
	for o.final+o.k < len(o.labels) {
		ev = o.finalize(ev)
	}
	if drop := o.final - o.k; drop > 0 {
		o.labels, o.best, o.final = o.labels[drop:], o.best[drop:], o.k
	}
	return ev, nil
}

// Close ends the stream: the remaining windows are decided with the right
// context they have (as offline decoding does at the end of a mix) and
// every open entry ends.
func (o *Online) Close() []Event {
	if o.closed {
		return nil
	}
	o.closed = true
	var ev []Event
	for o.final < len(o.labels) {
		ev = o.finalize(ev)
	}
	if o.run != nil {
		o.closeRun()
	}
	if o.open != nil {
		ev = append(ev, o.endOpen())
	}
	return ev
}

// finalize decides the oldest non-final window.
func (o *Online) finalize(ev []Event) []Event {
	i := o.final
	label := majority(o.labels[max(0, i-o.k):min(len(o.labels), i+o.k+1)])
	o.final++
	b := o.best[i]
	if o.run != nil && o.run.label != label {
		o.closeRun()
	}
	if label >= 0 {
		if o.run == nil {
			ev = o.beginRun(ev, label, b.T0)
		}
		o.run.end = b.T0 + o.window
		if b.Track == label {
			o.run.factors = append(o.run.factors, b.Factor)
			o.run.evidence += b.Score
		}
		if !o.run.announced {
			o.run.announced = true
			if !o.run.continues {
				*o.open = o.runEntry()
				ev = append(ev, Event{Kind: Start, Entry: *o.open})
			}
		}
	}
	if o.run == nil && o.open != nil && b.T0 >= o.limit() {
		ev = append(ev, o.endOpen())
	}
	return ev
}

// beginRun starts a run at t0 and decides whether it continues the open
// entry (MergeRepeats' rule: same track, starting at most gap seconds after
// the entry's end); if not, the open entry is finished.
func (o *Online) beginRun(ev []Event, label int, t0 float64) []Event {
	cont := o.open != nil && o.open.Track == label && o.gap >= 0 && t0 <= o.limit()
	if !cont && o.open != nil {
		ev = append(ev, o.endOpen())
	}
	if cont && o.pending {
		o.pending, o.carried = false, true
	}
	o.run = &openRun{label: label, start: t0, continues: cont}
	if !cont {
		o.open = &Entry{Track: label}
	}
	return ev
}

// limit is the latest window start at which a run can still continue the
// open entry.
func (o *Online) limit() float64 {
	if o.pending {
		return o.resumeWithin
	}
	if o.gap < 0 {
		return math.Inf(-1)
	}
	return o.open.End + o.gap
}

// runEntry is the open run as an entry, as Smooth builds it.
func (o *Online) runEntry() Entry {
	r := o.run
	f := 1.0
	if len(r.factors) > 0 {
		f = median(r.factors)
	}
	return Entry{Track: r.label, Start: r.start, End: r.end, Factor: f, Evidence: r.evidence}
}

// closeRun ends the open run and folds it into the open entry.
func (o *Online) closeRun() {
	e := o.runEntry()
	if o.run.continues {
		o.open.End = math.Max(o.open.End, e.End)
		if e.Evidence > o.open.Evidence {
			o.open.Evidence, o.open.Factor = e.Evidence, e.Factor
		}
	} else {
		*o.open = e
	}
	o.run = nil
}

func (o *Online) endOpen() Event {
	e := Event{Kind: End, Entry: *o.open, Resumed: o.carried, Interrupted: o.pending}
	o.open, o.pending, o.carried = nil, false, false
	return e
}
