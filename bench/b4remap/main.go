// Command b4remap measures segment size with global track ids (D19) by
// rewriting existing local-id segments, without re-fingerprinting.
// Postings are identical; only the stored track ids change.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/anytroops/landmark/internal/runinfo"
	"github.com/anytroops/landmark/internal/segment"
)

func main() {
	in := flag.String("in", "", "directory of local-id part-*.lmrk segments")
	out := flag.String("out", "", "output JSON path")
	flag.Parse()
	paths, err := filepath.Glob(filepath.Join(*in, "part-*.lmrk"))
	if err != nil || len(paths) == 0 {
		log.Fatalf("no segments in %q: %v", *in, err)
	}

	type segSize struct {
		Partition   uint32 `json:"partition"`
		LocalBytes  int    `json:"local_bytes"`
		GlobalBytes int    `json:"global_bytes"`
		Postings    uint64 `json:"postings"`
	}
	var res struct {
		Source           string            `json:"source"`
		Partitions       int               `json:"partitions"`
		Segments         []segSize         `json:"segments"`
		TotalPostings    uint64            `json:"total_postings"`
		LocalBytes       int               `json:"local_bytes"`
		GlobalBytes      int               `json:"global_bytes"`
		LocalBPerPosting float64           `json:"local_bytes_per_posting"`
		GlobalBPerPost   float64           `json:"global_bytes_per_posting"`
		Machine          map[string]string `json:"machine"`
	}
	res.Source, res.Partitions, res.Machine = *in, len(paths), runinfo.Machine(".")

	var nTracks uint32
	segs := make([]*segment.Segment, len(paths))
	for i, p := range paths {
		if segs[i], err = segment.Open(p); err != nil {
			log.Fatal(err)
		}
		nTracks += segs[i].NTracks()
	}
	tmp := filepath.Join(os.TempDir(), "b4remap.lmrk")
	for _, s := range segs {
		part, _ := s.Partition()
		var entries []segment.Entry
		var buf []segment.Posting
		for k := 0; k < s.NKeys(); k++ {
			h := s.Key(k)
			if buf, err = s.AppendPostings(buf[:0], h); err != nil {
				log.Fatal(err)
			}
			for _, p := range buf {
				entries = append(entries, segment.Entry{Hash: h, Track: s.GlobalTrack(p.Track), T: p.T})
			}
		}
		meta := segment.Meta{Partition: 0, NPartitions: 1, NTracks: nTracks, Encoding: s.Encoding()}
		if err := segment.Write(tmp, meta, entries); err != nil {
			log.Fatal(err)
		}
		st, err := os.Stat(tmp)
		if err != nil {
			log.Fatal(err)
		}
		res.Segments = append(res.Segments, segSize{part, s.SizeBytes(), int(st.Size()), s.NPostings()})
		res.LocalBytes += s.SizeBytes()
		res.GlobalBytes += int(st.Size())
		res.TotalPostings += s.NPostings()
		_ = s.Close()
	}
	_ = os.Remove(tmp)
	res.LocalBPerPosting = float64(res.LocalBytes) / float64(res.TotalPostings)
	res.GlobalBPerPost = float64(res.GlobalBytes) / float64(res.TotalPostings)
	b, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("P=%d local %.3f B/posting, global %.3f B/posting (+%.1f%%)\n", len(paths),
		res.LocalBPerPosting, res.GlobalBPerPost, 100*(res.GlobalBPerPost/res.LocalBPerPosting-1))
}
