// Command events follows live monitoring's event logs (D40): it subscribes
// to the registry and prints each song as workers report it, after
// replaying what is already logged (unless -live).
//
//	events -registry 127.0.0.1:7700                 # every stream
//	events -registry 127.0.0.1:7700 -stream st001   # one stream
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
)

func main() {
	addr := flag.String("registry", "127.0.0.1:7700", "registry address")
	streams := flag.String("stream", "", "comma list of streams to follow (empty: all)")
	live := flag.Bool("live", false, "skip events already logged")
	asJSON := flag.Bool("json", false, "print events as JSON lines")
	flag.Parse()
	var ids []string
	if *streams != "" {
		ids = strings.Split(*streams, ",")
	}
	if err := run(*addr, ids, *live, *asJSON); err != nil {
		fmt.Fprintln(os.Stderr, "events:", err)
		os.Exit(1)
	}
}

func run(addr string, ids []string, live, asJSON bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	return follow(ctx, landmarkv1.NewRegistryServiceClient(conn), ids, live, asJSON, os.Stdout)
}

// follow prints events until ctx ends. A subscriber dropped for falling
// behind subscribes again; the replay repeats nothing it printed, because
// it skips events at or below the last sequence number seen per stream.
func follow(ctx context.Context, client landmarkv1.RegistryServiceClient, ids []string, liveOnly, asJSON bool, w io.Writer) error {
	seen := map[string]uint64{}
	for {
		sub, err := client.Subscribe(ctx, &landmarkv1.SubscribeRequest{StreamIds: ids, LiveOnly: liveOnly})
		if err != nil {
			return err
		}
		for {
			m, err := sub.Recv()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if status.Code(err) == codes.ResourceExhausted || errors.Is(err, io.EOF) {
					break // dropped or closed: subscribe again
				}
				return err
			}
			e := m.GetEvent()
			if e.GetSeq() <= seen[e.GetStreamId()] {
				continue
			}
			seen[e.GetStreamId()] = e.GetSeq()
			if err := printEvent(w, e, asJSON); err != nil {
				return err
			}
		}
		liveOnly = false // the replay fills what a drop missed
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func printEvent(w io.Writer, e *landmarkv1.Event, asJSON bool) error {
	if asJSON {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", b)
		return err
	}
	at := time.Unix(0, int64(e.GetStartUnix()*1e9)).Format("15:04:05")
	var b strings.Builder
	switch e.GetKind() {
	case landmarkv1.EventKind_EVENT_KIND_START:
		fmt.Fprintf(&b, "%s  %-8s  playing  %s", at, e.GetStreamId(), e.GetName())
	case landmarkv1.EventKind_EVENT_KIND_END:
		dur := e.GetEndUnix() - e.GetStartUnix()
		fmt.Fprintf(&b, "%s  %-8s  played   %s for %.0f s", at, e.GetStreamId(), e.GetName(), dur)
	default:
		return nil
	}
	if t := e.GetTempo(); t > 0 && (t > 1.005 || t < 0.995) {
		fmt.Fprintf(&b, " (%+.1f%%)", 100*(t-1))
	}
	if e.GetResumed() {
		b.WriteString(" [continued after a failover or reconnect]")
	}
	if e.GetInterrupted() {
		b.WriteString(" [ended during a failover or reconnect]")
	}
	_, err := fmt.Fprintln(w, b.String())
	return err
}
