// Command loadgen drives a coordinator with an open-loop query load.
//
//	loadgen queries -out data/queries/test_10s.gob        # fingerprint once
//	loadgen run -queries data/queries/test_10s.gob -rate 100 -duration 60s -out bench/results/run1
//
// Open loop: request i is due at start + i/rate (or a Poisson arrival time)
// whether or not earlier requests returned, like independent users. Latency
// is measured from the due time, not the actual send time, so a stalled
// system (or a stalled generator) shows up as latency instead of silently
// sending fewer requests. That mistake is coordinated omission.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loadgen {queries|run} [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "queries":
		err = queriesCmd(os.Args[2:])
	case "run":
		err = runCmd(os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}
}
