// Command analysisparity checks internal/analysis (the Go port of DJ mix
// analysis, D43) against the Python prototype's committed results on real
// UnmixDB mixes. For each mix it reads the window answers the eval used
// (the triplet rows and the hybrid's pair rows, scaled by the calibrated
// weight), decodes the tracklist with the Go decoder, analyzes the mix and
// its songs' references with the Go code, and compares each song's
// turntable call and ramp-fitted fades with eval/dj_analysis.py's.
//
//	go run ./bench/analysisparity -songs eval/results/dj_analysis/test/songs.json -limit 40
package main

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/anytroops/landmark/internal/analysis"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/tracklist"
	"github.com/anytroops/landmark/internal/triplet"
)

const resDir = "eval/results/tracklist"

type pySong struct {
	Mix       int       `json:"mix"`
	Name      string    `json:"name"`
	Track     int       `json:"track"`
	Rate      float64   `json:"rate"`
	Turntable bool      `json:"turntable"`
	FadeIn    []float64 `json:"ramp_fade_in"`
	FadeOut   []float64 `json:"ramp_fade_out"`
}

func main() {
	songsPath := flag.String("songs", "eval/results/dj_analysis/test/songs.json", "dj_analysis songs.json")
	limit := flag.Int("limit", 40, "mixes to check")
	out := flag.String("out", "", "write the comparison as JSON here")
	flag.Parse()
	if err := run(*songsPath, *limit, *out); err != nil {
		log.Fatal(err)
	}
}

func run(songsPath string, limit int, outPath string) error {
	var py []pySong
	if err := readJSON(songsPath, &py); err != nil {
		return err
	}
	var gt struct {
		Mixes []struct {
			File string `json:"file"`
			Name string `json:"name"`
		} `json:"mixes"`
	}
	if err := readJSON("eval/mixes/unmixdb_s8.json", &gt); err != nil {
		return err
	}
	var cal struct {
		Smooth struct {
			Params struct {
				TAbs   float64 `json:"t_abs"`
				TRatio float64 `json:"t_ratio"`
				K      int     `json:"k"`
				Alpha  float64 `json:"alpha"`
			} `json:"params"`
		} `json:"smooth"`
	}
	if err := readJSON(filepath.Join(resDir, "calibration_unmixdb_s8_b2dhp_hybrid_set222-set275.json"), &cal); err != nil {
		return err
	}
	p := cal.Smooth.Params
	b2, err := readRows(filepath.Join(resDir, "windows_unmixdb_s8_b2dhp.csv.gz"), nil, 1)
	if err != nil {
		return err
	}
	pairs, err := readRows(filepath.Join(resDir, "windows_unmixdb_s8_gridA2.csv.gz"), func(mode string, f float64) bool {
		return (mode == "none" && f == 1) || (mode == "keylock" && (f == 2 || f == 0.5))
	}, p.Alpha)
	if err != nil {
		return err
	}
	refs, err := refPaths("data/segments/medium_unmixdb_b2")
	if err != nil {
		return err
	}

	byMix := map[int][]pySong{}
	var mixes []int
	for _, s := range py {
		if _, ok := byMix[s.Mix]; !ok {
			mixes = append(mixes, s.Mix)
		}
		byMix[s.Mix] = append(byMix[s.Mix], s)
	}
	mixes = mixes[:min(limit, len(mixes))]
	fp := triplet.New(triplet.DefaultParams())
	spec := func(path string) (analysis.Spectrum, error) {
		pcm, err := audio.Decode(context.Background(), path, audio.SampleRate)
		if err != nil {
			return analysis.Spectrum{}, err
		}
		return analysis.Power(fp.Spectrogram(pcm)), nil
	}
	type cmp struct {
		Mix         int     `json:"mix"`
		Track       int     `json:"track"`
		TurntableOK bool    `json:"turntable_same"`
		FadeInDiff  float64 `json:"fade_in_max_diff_s"`
		FadeOutDiff float64 `json:"fade_out_max_diff_s"`
		RateDiff    float64 `json:"rate_diff"`
	}
	var rows []cmp
	for _, m := range mixes {
		rs := slices.Concat(b2[m], pairs[m])
		tracklist.SortRows(rs)
		entries := tracklist.MergeRepeats(tracklist.Smooth(rs, 10, p.TAbs, p.TRatio, p.K), 2.5)
		mixSpec, err := spec(gt.Mixes[m].File)
		if err != nil {
			return err
		}
		var songs []analysis.Song
		for _, e := range entries {
			a, rate, ok := analysis.Alignment(rs, e.Track, e.Start, e.End)
			path, known := refs[e.Track]
			if !ok || !known {
				continue
			}
			ref, err := spec(path)
			if err != nil {
				return err
			}
			songs = append(songs, analysis.Song{Track: e.Track, Start: e.Start, End: e.End, A: a, Rate: rate, Ref: ref})
		}
		got := analysis.Analyze(mixSpec, songs)
		used := make([]bool, len(songs)) // by input song: a song can drop out of the results
		for _, want := range byMix[m] {
			// A song the decoder split into two entries appears twice: pair
			// the occurrences in order.
			i := slices.IndexFunc(got, func(r analysis.Result) bool { return r.Track == want.Track && !used[r.Song] })
			if i < 0 {
				return fmt.Errorf("mix %d (%s): track %d analyzed in Python only", m, want.Name, want.Track)
			}
			used[got[i].Song] = true
			g := got[i]
			c := cmp{Mix: m, Track: want.Track, TurntableOK: g.Turntable == want.Turntable,
				RateDiff: math.Abs(g.Rate - want.Rate)}
			if len(want.FadeIn) == 2 && g.FadeInOK {
				c.FadeInDiff = math.Max(math.Abs(g.FadeIn[0]-want.FadeIn[0]), math.Abs(g.FadeIn[1]-want.FadeIn[1]))
			}
			if len(want.FadeOut) == 2 && g.FadeOutOK {
				c.FadeOutDiff = math.Max(math.Abs(g.FadeOut[0]-want.FadeOut[0]), math.Abs(g.FadeOut[1]-want.FadeOut[1]))
			}
			rows = append(rows, c)
		}
	}
	same, within := 0, 0
	var worst float64
	for _, r := range rows {
		if r.TurntableOK {
			same++
		}
		d := math.Max(r.FadeInDiff, r.FadeOutDiff)
		if d <= 0.1+1e-9 {
			within++
		}
		worst = math.Max(worst, d)
	}
	fmt.Printf("%d mixes, %d songs: turntable call same %d, fades within 0.1 s %d, largest fade difference %.2f s\n",
		len(mixes), len(rows), same, within, worst)
	if outPath != "" {
		b, err := json.MarshalIndent(rows, "", " ")
		if err != nil {
			return err
		}
		return os.WriteFile(outPath, append(b, '\n'), 0o644)
	}
	return nil
}

// readRows reads a windows CSV into rows by mix, keeping those keep accepts
// (nil: all), scores and runner-ups multiplied by weight.
func readRows(path string, keep func(string, float64) bool, weight float64) (map[int][]tracklist.Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(gz)
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	out := map[int][]tracklist.Row{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		num := func(k string) float64 { v, _ := strconv.ParseFloat(rec[col[k]], 64); return v }
		mode, factor := rec[col["mode"]], num("factor")
		if keep != nil && !keep(mode, factor) {
			continue
		}
		m := int(num("mix"))
		out[m] = append(out[m], tracklist.Row{T0: num("t0"), Mode: mode, Factor: factor, Track: int(num("top1_dense")),
			Score: weight * num("score"), RunnerUp: weight * num("runner_up"), Offset: num("offset")})
	}
}

// refPaths maps the UnmixDB references' dense ids to their audio.
func refPaths(dir string) (map[int]string, error) {
	var cat []struct {
		Dense int `json:"dense"`
		FMAID int `json:"fma_id"`
	}
	if err := readJSON(filepath.Join(dir, "catalog.json"), &cat); err != nil {
		return nil, err
	}
	var extras []struct {
		FMAID int    `json:"fma_id"`
		Path  string `json:"path"`
	}
	if err := readJSON(filepath.Join(dir, "extras.json"), &extras); err != nil {
		return nil, err
	}
	paths := map[int]string{}
	for _, e := range extras {
		paths[e.FMAID] = e.Path
	}
	out := map[int]string{}
	for _, t := range cat {
		if p, ok := paths[t.FMAID]; ok {
			out[t.Dense] = p
		}
	}
	return out, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
