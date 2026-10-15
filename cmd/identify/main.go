// Command identify fingerprints an audio file (or an excerpt of it) and asks
// a shard server who it is.
//
//	identify -shard localhost:7001 -catalog data/segments/p1_varint/catalog.json \
//	    -start 7.3 -duration 10 song.mp3
//
// With -coordinator it asks the whole cluster (fan-out, failover, merge);
// with -shard it asks one shard directly.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/match"
)

type catalogTrack struct {
	Dense   uint32 `json:"dense"`
	FMAID   int    `json:"fma_id"`
	Cluster uint32 `json:"cluster"`
}

func main() {
	addr := flag.String("shard", "", "shard gRPC address (query one partition directly)")
	coordAddr := flag.String("coordinator", "", "coordinator gRPC address (query the cluster)")
	catalogPath := flag.String("catalog", "", "catalog.json written by ingest (required)")
	startSec := flag.Float64("start", 0, "excerpt start, seconds")
	durSec := flag.Float64("duration", 10, "excerpt length, seconds (0 = whole file)")
	shifts := flag.Int("shifts", 4, "sub-hop query grids (D15)")
	// Calibrated on the fma_small val split (eval/results/summary_seed0_f64.json).
	tAbs := flag.Uint("t-abs", 30, "minimum score to accept")
	tRatio := flag.Float64("t-ratio", 1.0, "minimum top / runner-up ratio")
	timeout := flag.Duration("timeout", 2*time.Second, "end-to-end deadline")
	flag.Parse()
	if flag.NArg() != 1 || *catalogPath == "" || (*addr == "") == (*coordAddr == "") {
		fmt.Fprintln(os.Stderr, "usage: identify -catalog catalog.json (-shard addr | -coordinator addr) [flags] audio-file")
		os.Exit(2)
	}
	if *coordAddr != "" {
		if err := runCoordinator(*coordAddr, *catalogPath, flag.Arg(0), *startSec, *durSec, *shifts, *timeout); err != nil {
			fmt.Fprintln(os.Stderr, "identify:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(*addr, *catalogPath, flag.Arg(0), *startSec, *durSec, *shifts, uint32(*tAbs), *tRatio, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "identify:", err)
		os.Exit(1)
	}
}

func run(addr, catalogPath, file string, startSec, durSec float64, shifts int, tAbs uint32, tRatio float64, timeout time.Duration) error {
	catalog, err := loadCatalog(catalogPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	t0 := time.Now()
	pcm, err := audio.Decode(ctx, file, audio.SampleRate)
	if err != nil {
		return err
	}
	lo := min(int(startSec*audio.SampleRate), len(pcm))
	hi := len(pcm)
	if durSec > 0 {
		hi = min(lo+int(durSec*audio.SampleRate), len(pcm))
	}
	pcm = pcm[lo:hi]
	tDecode := time.Since(t0)

	t1 := time.Now()
	lms := fingerprint.New(fingerprint.DefaultParams()).FingerprintQuery(pcm, shifts)
	tFP := time.Since(t1)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	req := &landmarkv1.SearchRequest{TopK: 20, Hashes: make([]uint32, len(lms)), Times: make([]uint32, len(lms))}
	for i, l := range lms {
		req.Hashes[i], req.Times[i] = l.Hash, l.T
	}
	t2 := time.Now()
	resp, err := landmarkv1.NewShardServiceClient(conn).Search(ctx, req)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	tSearch := time.Since(t2)

	cands := make([]match.Candidate, len(resp.GetCandidates()))
	for i, c := range resp.GetCandidates() {
		cands[i] = match.Candidate{Track: c.GetTrackId(), Score: c.GetScore(), Offset: c.GetOffset()}
	}
	clusterOf := func(tr uint32) uint32 {
		if int(tr) < len(catalog) {
			return catalog[tr].Cluster
		}
		return tr
	}
	runnerUp := match.RunnerUp(cands, clusterOf)

	fmt.Printf("excerpt %.1f-%.1f s, %d landmarks; decode %v, fingerprint %v, search %v (%d postings)\n",
		float64(lo)/audio.SampleRate, float64(hi)/audio.SampleRate, len(lms),
		tDecode.Round(time.Millisecond), tFP.Round(time.Millisecond), tSearch.Round(time.Millisecond),
		resp.GetPostingsScanned())
	if len(cands) == 0 {
		fmt.Println("no match: no shared hashes")
		return nil
	}
	top := cands[0]
	verdict := "NO MATCH"
	if match.Accept(top.Score, runnerUp, tAbs, tRatio) {
		verdict = "MATCH"
	}
	fmt.Printf("%s: fma_id %d (score %d, runner-up %d, offset %.2f s into the track)\n",
		verdict, catalog[top.Track].FMAID, top.Score, runnerUp, float64(top.Offset)*256/audio.SampleRate)
	for i, c := range cands[:min(5, len(cands))] {
		fmt.Printf("  %d. fma_id %-7d score %d\n", i+1, catalog[c.Track].FMAID, c.Score)
	}
	return nil
}

// runCoordinator asks the cluster; acceptance happens in the coordinator.
func runCoordinator(addr, catalogPath, file string, startSec, durSec float64, shifts int, timeout time.Duration) error {
	catalog, err := loadCatalog(catalogPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	pcm, err := audio.Decode(ctx, file, audio.SampleRate)
	if err != nil {
		return err
	}
	lo := min(int(startSec*audio.SampleRate), len(pcm))
	hi := len(pcm)
	if durSec > 0 {
		hi = min(lo+int(durSec*audio.SampleRate), len(pcm))
	}
	lms := fingerprint.New(fingerprint.DefaultParams()).FingerprintQuery(pcm[lo:hi], shifts)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	req := &landmarkv1.IdentifyRequest{TopK: 5, Hashes: make([]uint32, len(lms)), Times: make([]uint32, len(lms))}
	for i, l := range lms {
		req.Hashes[i], req.Times[i] = l.Hash, l.T
	}
	t0 := time.Now()
	resp, err := landmarkv1.NewCoordinatorServiceClient(conn).Identify(ctx, req)
	if err != nil {
		return fmt.Errorf("identify: %w", err)
	}
	fmt.Printf("%d landmarks; identify %v; partitions %d/%d%s\n", len(lms), time.Since(t0).Round(time.Millisecond),
		resp.GetPartitionsResponded(), resp.GetPartitionsTotal(), map[bool]string{true: " (PARTIAL)", false: ""}[resp.GetPartial()])
	verdict := "NO MATCH"
	if resp.GetMatched() {
		verdict = "MATCH"
	}
	for i, c := range resp.GetCandidates() {
		fma := -1
		if int(c.GetTrackId()) < len(catalog) {
			fma = catalog[c.GetTrackId()].FMAID
		}
		if i == 0 {
			fmt.Printf("%s: fma_id %d (score %d, offset %.2f s)\n", verdict, fma, c.GetScore(), float64(c.GetOffset())*256/audio.SampleRate)
		}
		fmt.Printf("  %d. fma_id %-7d score %d\n", i+1, fma, c.GetScore())
	}
	return nil
}

func loadCatalog(path string) ([]catalogTrack, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	var c []catalogTrack
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	for i, t := range c {
		if t.Dense != uint32(i) {
			return nil, fmt.Errorf("catalog entry %d has dense id %d", i, t.Dense)
		}
	}
	return c, nil
}
