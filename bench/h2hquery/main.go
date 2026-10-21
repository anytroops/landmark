// Command h2hquery answers query files with Landmark for the head-to-head
// against audfprint and Panako (D41). Each file is decoded with ffmpeg at
// 11,025 Hz, fingerprinted with the pair fingerprint and the B2 dual query
// (D35), and sent to the pair and triplet coordinators. One CSV row per file
// and system: the top candidate, the best score outside its duplicate
// cluster (D14), and the time spent decoding, fingerprinting, and searching.
//
//	h2hquery -list queries.txt -index data/segments/medium_unmixdb \
//	    -pairs-coordinator 127.0.0.1:7700 -b2-coordinator 127.0.0.1:7600 \
//	    -workers 8 -out rows.csv
//
// Track ids are written as the other tools name them: "fma:<id>" for FMA
// tracks and "ref:<name>" for extras (the UnmixDB references).
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/catalog"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/hybrid"
	"github.com/anytroops/landmark/internal/triplet"
)

func main() {
	list := flag.String("list", "", "file with one query path per line (required)")
	index := flag.String("index", "", "index directory, for catalog.json (required)")
	pairsAddr := flag.String("pairs-coordinator", "", "pair index coordinator")
	b2Addr := flag.String("b2-coordinator", "", "triplet index coordinator")
	workers := flag.Int("workers", 1, "queries at once")
	shifts := flag.Int("shifts", 4, "sub-hop query grids (D15)")
	timeout := flag.Duration("timeout", 10*time.Second, "deadline per search")
	out := flag.String("out", "", "CSV output (required)")
	flag.Parse()
	if *list == "" || *index == "" || *out == "" || (*pairsAddr == "" && *b2Addr == "") {
		fmt.Fprintln(os.Stderr, "usage: h2hquery -list file -index dir (-pairs-coordinator addr | -b2-coordinator addr) -out rows.csv")
		os.Exit(2)
	}
	if err := run(*list, *index, *pairsAddr, *b2Addr, *workers, *shifts, *timeout, *out); err != nil {
		log.Fatal(err)
	}
}

type row struct {
	file, system, top        string
	score, runnerUp          uint32
	scale                    float32
	landmarks                int
	decodeMs, fpMs, searchMs float64
	err                      string
}

func run(listPath, index, pairsAddr, b2Addr string, workers, shifts int, timeout time.Duration, outPath string) error {
	files, err := readList(listPath)
	if err != nil {
		return err
	}
	cat, err := catalog.Load(index)
	if err != nil {
		return err
	}
	var pairs, b2 hybrid.IdentifyFunc
	if pairsAddr != "" {
		var closeFn func()
		if pairs, closeFn, err = hybrid.Dial(pairsAddr); err != nil {
			return err
		}
		defer closeFn()
	}
	if b2Addr != "" {
		var closeFn func()
		if b2, closeFn, err = hybrid.Dial(b2Addr); err != nil {
			return err
		}
		defer closeFn()
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"file", "system", "top1", "top1_score", "runner_up_score", "scale",
		"n_landmarks", "decode_ms", "fp_ms", "search_ms", "error"}); err != nil {
		return err
	}
	var mu sync.Mutex
	write := func(r row) {
		mu.Lock()
		defer mu.Unlock()
		_ = w.Write([]string{r.file, r.system, r.top, strconv.Itoa(int(r.score)), strconv.Itoa(int(r.runnerUp)),
			strconv.FormatFloat(float64(r.scale), 'f', 5, 32), strconv.Itoa(r.landmarks),
			ms(r.decodeMs), ms(r.fpMs), ms(r.searchMs), r.err})
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range max(workers, 1) {
		wg.Go(func() {
			pfp := fingerprint.New(fingerprint.DefaultParams())
			tfp := triplet.New(triplet.DefaultParams())
			for file := range jobs {
				t0 := time.Now()
				x, err := audio.Decode(context.Background(), file, audio.SampleRate)
				decode := since(t0)
				if err != nil {
					for _, sys := range systems(pairs, b2) {
						write(row{file: file, system: sys, err: err.Error()})
					}
					continue
				}
				if pairs != nil {
					t1 := time.Now()
					lms := pfp.FingerprintQuery(x, shifts)
					fp := since(t1)
					req := &landmarkv1.IdentifyRequest{TopK: 20, Hashes: make([]uint32, len(lms)), Times: make([]uint32, len(lms))}
					for i, l := range lms {
						req.Hashes[i], req.Times[i] = l.Hash, l.T
					}
					write(ask(pairs, req, file, "landmark_pairs", len(lms), decode, fp, timeout, cat))
				}
				if b2 != nil {
					t1 := time.Now()
					lms := tfp.FingerprintQuery(x, shifts)
					fp := since(t1)
					n := len(lms)
					req := &landmarkv1.IdentifyRequest{TopK: 20, Hashes: make([]uint32, n), Times: make([]uint32, n),
						Spans: make([]byte, n), Bands: make([]byte, n)}
					for i, l := range lms {
						req.Hashes[i], req.Times[i], req.Spans[i], req.Bands[i] = l.Hash, l.T, l.Span, l.Band
					}
					write(ask(b2, req, file, "landmark_b2", n, decode, fp, timeout, cat))
				}
			}
		})
	}
	for _, file := range files {
		jobs <- file
	}
	close(jobs)
	wg.Wait()
	w.Flush()
	return w.Error()
}

func systems(pairs, b2 hybrid.IdentifyFunc) []string {
	var out []string
	if pairs != nil {
		out = append(out, "landmark_pairs")
	}
	if b2 != nil {
		out = append(out, "landmark_b2")
	}
	return out
}

// ask sends one query and reads the top candidate and the best score
// outside its duplicate cluster.
func ask(identify hybrid.IdentifyFunc, req *landmarkv1.IdentifyRequest, file, system string, n int,
	decode, fp float64, timeout time.Duration, cat *catalog.Catalog) row {
	r := row{file: file, system: system, landmarks: n, decodeMs: decode, fpMs: fp}
	if n == 0 {
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	t := time.Now()
	resp, err := identify(ctx, req)
	r.searchMs = since(t)
	if err != nil {
		r.err = err.Error()
		return r
	}
	cands := resp.GetCandidates()
	if len(cands) == 0 {
		return r
	}
	top := cands[0]
	r.top, r.score, r.scale = trackName(cat, top.GetTrackId()), top.GetScore(), top.GetScale()
	for _, c := range cands[1:] {
		if cat.Cluster(c.GetTrackId()) != cat.Cluster(top.GetTrackId()) {
			r.runnerUp = c.GetScore()
			break
		}
	}
	return r
}

// trackName names a dense id the way the other tools' outputs are mapped:
// FMA tracks by id, extras by their name.
func trackName(cat *catalog.Catalog, dense uint32) string {
	if int(dense) >= len(cat.Tracks) {
		return fmt.Sprintf("dense:%d", dense)
	}
	name := cat.Name(int(dense))
	if strings.HasPrefix(name, "FMA ") {
		return fmt.Sprintf("fma:%d", cat.Tracks[dense].FMAID)
	}
	return "ref:" + name
}

func readList(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if s := strings.TrimSpace(sc.Text()); s != "" {
			out = append(out, s)
		}
	}
	return out, sc.Err()
}

func since(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }

func ms(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
