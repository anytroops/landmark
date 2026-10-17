// Command equiv sends the same queries to two coordinators and checks that
// they return identical answers: the same candidates (track, score, offset)
// in the same order, and the same accept decision. It backs the claim that
// splitting the index P ways never changes an answer, with or without the
// stop list (D30, which filters before fan-out using global counts).
package main

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/runinfo"
)

type query struct {
	FMAID     int
	Expected  int32
	Landmarks []fingerprint.Landmark
}

type result struct {
	A             string            `json:"a"`
	B             string            `json:"b"`
	Label         string            `json:"label"`
	Queries       int               `json:"queries"`
	Identical     int               `json:"identical"`
	Mismatches    int               `json:"mismatches"`
	Partial       int               `json:"partial_answers"`
	Matched       int               `json:"matched"`
	FirstMismatch []string          `json:"first_mismatches"`
	Machine       map[string]string `json:"machine"`
}

func main() {
	a := flag.String("a", "", "first coordinator address (required)")
	b := flag.String("b", "", "second coordinator address (required)")
	qPath := flag.String("queries", "data/queries/medium_test_10s_seed0.gob", "loadgen query file")
	topK := flag.Int("top-k", 20, "candidates to compare per query")
	label := flag.String("label", "", "what is being compared, for the output")
	out := flag.String("out", "", "JSON result path (optional)")
	flag.Parse()
	if *a == "" || *b == "" {
		log.Fatal("-a and -b are required")
	}
	res, err := run(*a, *b, *qPath, *topK)
	if err != nil {
		log.Fatal(err)
	}
	res.Label, res.Machine = *label, runinfo.Machine(".")
	fmt.Printf("%s: %d queries, %d identical, %d mismatched, %d partial, %d matched\n",
		*label, res.Queries, res.Identical, res.Mismatches, res.Partial, res.Matched)
	for _, m := range res.FirstMismatch {
		fmt.Println("  " + m)
	}
	if *out != "" {
		bs, err := json.MarshalIndent(res, "", " ")
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*out, append(bs, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	if res.Mismatches > 0 || res.Partial > 0 {
		os.Exit(1)
	}
}

func run(a, b, qPath string, topK int) (*result, error) {
	f, err := os.Open(qPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var qs []query
	if err := gob.NewDecoder(f).Decode(&qs); err != nil {
		return nil, fmt.Errorf("decode %s: %w", qPath, err)
	}
	var conns []*grpc.ClientConn
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	clients := make([]landmarkv1.CoordinatorServiceClient, 2)
	for i, addr := range []string{a, b} {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		conns = append(conns, conn)
		clients[i] = landmarkv1.NewCoordinatorServiceClient(conn)
	}

	res := &result{A: a, B: b, Queries: len(qs)}
	for _, q := range qs {
		req := &landmarkv1.IdentifyRequest{TopK: uint32(topK)}
		for _, l := range q.Landmarks {
			req.Hashes = append(req.Hashes, l.Hash)
			req.Times = append(req.Times, l.T)
		}
		var resp [2]*landmarkv1.IdentifyResponse
		for i, c := range clients {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			resp[i], err = c.Identify(ctx, req)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("query fma %d on %s: %w", q.FMAID, []string{a, b}[i], err)
			}
		}
		if resp[0].GetPartial() || resp[1].GetPartial() {
			res.Partial++
			continue
		}
		if diff := compare(resp[0], resp[1]); diff != "" {
			res.Mismatches++
			if len(res.FirstMismatch) < 10 {
				res.FirstMismatch = append(res.FirstMismatch, fmt.Sprintf("fma %d: %s", q.FMAID, diff))
			}
			continue
		}
		res.Identical++
		if resp[0].GetMatched() {
			res.Matched++
		}
	}
	return res, nil
}

// compare describes the first difference between two answers, or "".
func compare(x, y *landmarkv1.IdentifyResponse) string {
	if x.GetMatched() != y.GetMatched() {
		return fmt.Sprintf("matched %v vs %v", x.GetMatched(), y.GetMatched())
	}
	cx, cy := x.GetCandidates(), y.GetCandidates()
	if len(cx) != len(cy) {
		return fmt.Sprintf("%d vs %d candidates", len(cx), len(cy))
	}
	for i := range cx {
		if cx[i].GetTrackId() != cy[i].GetTrackId() || cx[i].GetScore() != cy[i].GetScore() || cx[i].GetOffset() != cy[i].GetOffset() {
			return fmt.Sprintf("candidate %d: %v vs %v", i, cx[i], cy[i])
		}
	}
	return ""
}
