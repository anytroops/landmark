// Command msgsize compares the wire size of a query under the old
// repeated-Landmark-message encoding and the packed arrays of D28.
//
// The old layout is computed from protobuf's encoding rules (tag, length,
// and a two-field submessage per landmark) since that message type no
// longer exists; the new size comes from proto.Size on real requests.
package main

import (
	"encoding/gob"
	"encoding/json"
	"flag"
	"log"
	"os"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/fingerprint"
)

type query struct {
	FMAID     int
	Expected  int32
	Landmarks []fingerprint.Landmark
}

func main() {
	path := flag.String("queries", "data/queries/test_10s_seed0.gob", "loadgen query file")
	out := flag.String("out", "bench/results/msgsize.json", "output JSON")
	flag.Parse()
	f, err := os.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	var qs []query
	if err := gob.NewDecoder(f).Decode(&qs); err != nil {
		log.Fatal(err)
	}
	var oldTotal, newTotal, landmarks int
	for _, q := range qs {
		req := &landmarkv1.SearchRequest{TopK: 20}
		old := protowire.SizeTag(2) + protowire.SizeVarint(20) // top_k
		for _, l := range q.Landmarks {
			req.Hashes = append(req.Hashes, l.Hash)
			req.Times = append(req.Times, l.T)
			sub := 0
			if l.Hash != 0 {
				sub += protowire.SizeTag(1) + protowire.SizeVarint(uint64(l.Hash))
			}
			if l.T != 0 {
				sub += protowire.SizeTag(2) + protowire.SizeVarint(uint64(l.T))
			}
			old += protowire.SizeTag(1) + protowire.SizeBytes(sub) // repeated Landmark landmarks = 1
		}
		oldTotal += old
		newTotal += proto.Size(req)
		landmarks += len(q.Landmarks)
	}
	res := map[string]float64{
		"queries":                     float64(len(qs)),
		"mean_landmarks":              float64(landmarks) / float64(len(qs)),
		"old_mean_bytes":              float64(oldTotal) / float64(len(qs)),
		"new_mean_bytes":              float64(newTotal) / float64(len(qs)),
		"old_bytes_per_landmark":      float64(oldTotal) / float64(landmarks),
		"new_bytes_per_landmark":      float64(newTotal) / float64(landmarks),
		"objects_decoded_per_req_old": float64(landmarks) / float64(len(qs)),
		"objects_decoded_per_req_new": 1,
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stdout.Write(append(b, '\n')); err != nil {
		log.Fatal(err)
	}
}
