// Package hybrid runs the searches of one mix window for the hybrid matcher
// (D36), shared by cmd/tracklist (a whole mix) and cmd/monitor (live
// streams).
//
// A window is searched with the tempo-invariant triplet fingerprint (dual
// filterbank, D35) on a triplet index, which fits each match's tempo, and
// with the plain pair fingerprint on a pair index of the same catalog, which
// survives heavy effects better. Each extra factor (default double and half
// time, beyond the triplet matcher's range) adds a triplet search of the
// window resampled by that factor (turntable) and a pair search of its
// landmarks rescaled by it (keylock). Pair scores are weighted by Alpha to
// put them on the triplet scale.
package hybrid

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/tracklist"
	"github.com/anytroops/landmark/internal/triplet"
)

// IdentifyFunc sends one Identify request to a cluster.
type IdentifyFunc func(context.Context, *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error)

// Dial connects to a coordinator. The returned function closes the
// connection.
func Dial(addr string) (IdentifyFunc, func(), error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	client := landmarkv1.NewCoordinatorServiceClient(conn)
	return func(ctx context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
		return client.Identify(ctx, req)
	}, func() { _ = conn.Close() }, nil
}

// Searcher runs every search of a window. It is safe for concurrent use;
// each goroutine needs its own Fingerprinters.
type Searcher struct {
	B2      IdentifyFunc
	Pairs   IdentifyFunc // nil: triplet index only
	Cluster func(uint32) uint32
	Shifts  int       // sub-hop query grids (D15)
	Extra   []float64 // extra tempo factors, each a ratio of small integers
	Alpha   float64   // weight of pair scores
	Timeout time.Duration
}

// Fingerprinters holds one goroutine's fingerprinters (not shareable).
type Fingerprinters struct {
	trip *triplet.Fingerprinter
	pair *fingerprint.Fingerprinter
}

// NewFingerprinters returns fingerprinters with the default parameters.
func NewFingerprinters() *Fingerprinters {
	return &Fingerprinters{trip: triplet.New(triplet.DefaultParams()), pair: fingerprint.New(fingerprint.DefaultParams())}
}

// Window runs one window's searches: triplets as is and resampled by each
// extra factor; with a pair index, pairs as is and with landmarks rescaled
// by each extra factor. It returns one row per search and how many answers
// came from only some partitions.
func (s *Searcher) Window(ctx context.Context, x []float32, t0 float64, fp *Fingerprinters) ([]tracklist.Row, int, error) {
	var rows []tracklist.Row
	partial := 0
	ask := func(identify IdentifyFunc, req *landmarkv1.IdentifyRequest, mode string, factor, weight float64) error {
		if len(req.GetHashes()) == 0 {
			rows = append(rows, tracklist.Row{T0: t0, Mode: mode, Factor: factor, Track: -1})
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, s.Timeout)
		defer cancel()
		resp, err := identify(ctx, req)
		if err != nil {
			return err
		}
		if resp.GetPartial() {
			partial++
		}
		rows = append(rows, row(t0, mode, factor, weight, resp, s.Cluster))
		return nil
	}
	if err := ask(s.B2, request(fp.trip.FingerprintQuery(x, s.Shifts)), "b2", 1, 1); err != nil {
		return nil, 0, err
	}
	for _, f := range s.Extra {
		p, q, ok := Ratio(f)
		if !ok {
			return nil, 0, fmt.Errorf("tempo factor %v is not a ratio of integers up to 1000", f)
		}
		y := audio.ResamplePoly(x, p, q) // play it 1/f as fast: undoes a turntable change of f
		if err := ask(s.B2, request(fp.trip.FingerprintQuery(y, s.Shifts)), "b2_speed", f, 1); err != nil {
			return nil, 0, err
		}
	}
	if s.Pairs == nil {
		return rows, partial, nil
	}
	lms := fp.pair.FingerprintQuery(x, s.Shifts)
	if err := ask(s.Pairs, pairRequest(lms), "none", 1, s.Alpha); err != nil {
		return nil, 0, err
	}
	for _, f := range s.Extra {
		if err := ask(s.Pairs, pairRequest(fingerprint.RescaleTempo(lms, f)), "keylock", f, s.Alpha); err != nil {
			return nil, 0, err
		}
	}
	return rows, partial, nil
}

// WindowT0 is a window's start in seconds, rounded to ms as eval.tracklist
// does, so window starts compare exactly across tools.
func WindowT0(startSample int) float64 {
	return math.Round(float64(startSample)/audio.SampleRate*1000) / 1000
}

// ParseFactors reads a comma list of tempo factors, each a ratio of small
// integers (so a turntable undo is an exact resampling).
func ParseFactors(s string) ([]float64, error) {
	var out []float64
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("bad tempo factor %q", f)
		}
		if _, _, ok := Ratio(v); !ok {
			return nil, fmt.Errorf("tempo factor %v is not a ratio of integers up to 1000", v)
		}
		out = append(out, v)
	}
	return out, nil
}

// Ratio returns p/q = f with q <= 1000.
func Ratio(f float64) (p, q int, ok bool) {
	for q = 1; q <= 1000; q++ {
		p = int(math.Round(f * float64(q)))
		if p > 0 && math.Abs(float64(p)/float64(q)-f) < 1e-9 {
			return p, q, true
		}
	}
	return 0, 0, false
}

// request asks for enough candidates to find a runner-up outside the top
// candidate's duplicate cluster (D14).
func request(lms []triplet.Landmark) *landmarkv1.IdentifyRequest {
	n := len(lms)
	r := &landmarkv1.IdentifyRequest{TopK: 20, Hashes: make([]uint32, n), Times: make([]uint32, n),
		Spans: make([]byte, n), Bands: make([]byte, n)}
	for i, l := range lms {
		r.Hashes[i], r.Times[i], r.Spans[i], r.Bands[i] = l.Hash, l.T, l.Span, l.Band
	}
	return r
}

func pairRequest(lms []fingerprint.Landmark) *landmarkv1.IdentifyRequest {
	n := len(lms)
	r := &landmarkv1.IdentifyRequest{TopK: 20, Hashes: make([]uint32, n), Times: make([]uint32, n)}
	for i, l := range lms {
		r.Hashes[i], r.Times[i] = l.Hash, l.T
	}
	return r
}

// row is one search's answer: the top candidate, the tempo factor (the
// hypothesis's factor times the triplet matcher's fitted scale, when it
// reports one), its offset, and the best score outside its duplicate
// cluster, with scores multiplied by weight.
func row(t0 float64, mode string, factor, weight float64, resp *landmarkv1.IdentifyResponse,
	cluster func(uint32) uint32) tracklist.Row {
	r := tracklist.Row{T0: t0, Mode: mode, Factor: factor, Track: -1}
	cands := resp.GetCandidates()
	if len(cands) == 0 {
		return r
	}
	top := cands[0]
	r.Track, r.Score, r.Offset = int(top.GetTrackId()), weight*float64(top.GetScore()), float64(top.GetOffset())
	if sc := top.GetScale(); sc > 0 {
		r.Factor = factor * float64(sc)
	}
	for _, c := range cands[1:] {
		if cluster(c.GetTrackId()) != cluster(top.GetTrackId()) {
			r.RunnerUp = weight * float64(c.GetScore())
			break
		}
	}
	return r
}
