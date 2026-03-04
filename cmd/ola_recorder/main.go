package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bstkhq/go-dmxcast"
)

func main() {
	var (
		outPath = flag.String("out", "", "Output .show file path (required)")
		srcIP   = flag.String("src", "", "Optional local bind IP (empty = OS default)")
		netID   = flag.Int("net", 0, "Art-Net Net (0..127) to record")
		subUni  = flag.Int("subuni", 0, "Art-Net SubUni (0..255) to record")

		stats = flag.Duration("stats", 1*time.Second, "Print stats every interval (0 = disable)")
	)
	flag.Parse()

	if *outPath == "" {
		fmt.Fprintln(os.Stderr, "missing -out")
		os.Exit(2)
	}
	if *netID < 0 || *netID > 127 {
		fmt.Fprintln(os.Stderr, "-net must be 0..127")
		os.Exit(2)
	}
	if *subUni < 0 || *subUni > 255 {
		fmt.Fprintln(os.Stderr, "-subuni must be 0..255")
		os.Exit(2)
	}

	var bindIP net.IP
	if *srcIP != "" {
		bindIP = net.ParseIP(*srcIP)
		if bindIP == nil {
			fmt.Fprintf(os.Stderr, "invalid -src: %q\n", *srcIP)
			os.Exit(2)
		}
	}

	out, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create output file: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	base, err := dmxcast.NewArtNetListener(dmxcast.ArtNetListenerConfig{
		BindIP: bindIP,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to bind Art-Net listener: %v\n", err)
		os.Exit(1)
	}
	defer base.Close()

	wantUniverse := (uint16(*netID) << 8) | uint16(*subUni)

	var (
		frames atomic.Uint64
		lastAt atomic.Int64 // unix nano
	)

	l := &filterListener{
		base: base,
		keep: func(universe uint16) bool {
			return universe == wantUniverse
		},
		onFrame: func(now time.Time) {
			frames.Add(1)
			lastAt.Store(now.UnixNano())
		},
	}

	rec := dmxcast.NewRecorder(l)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("Recording Art-Net Net=%d SubUni=%d (universe=%d) -> %s\n", *netID, *subUni, wantUniverse, *outPath)
	if bindIP != nil {
		fmt.Printf("Bind IP: %s\n", bindIP.String())
	} else {
		fmt.Printf("Bind IP: (os default)\n")
	}

	var statsDone chan struct{}
	if *stats > 0 {
		statsDone = make(chan struct{})
		go func() {
			defer close(statsDone)
			t := time.NewTicker(*stats)
			defer t.Stop()

			var prev uint64
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					cur := frames.Load()
					delta := cur - prev
					prev = cur

					la := lastAt.Load()
					var ago time.Duration
					if la != 0 {
						ago = time.Since(time.Unix(0, la)).Truncate(time.Millisecond)
					}

					fmt.Printf("frames=%d (+%d) last=%s ago\n", cur, delta, ago)
				}
			}
		}()
	}

	err = rec.Record(ctx, out)

	if statsDone != nil {
		<-statsDone
	}

	_ = base.Close()

	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "recording error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Done. frames=%d\n", frames.Load())
}

type filterListener struct {
	base dmxcast.Listener

	keep    func(universe uint16) bool
	onFrame func(now time.Time)
}

func (l *filterListener) Run(ctx context.Context, h dmxcast.FrameHandler) error {
	return l.base.Run(ctx, func(now time.Time, universe uint16, dmx [512]byte) error {
		if l.keep != nil && !l.keep(universe) {
			return nil
		}
		if l.onFrame != nil {
			l.onFrame(now)
		}
		return h(now, universe, dmx)
	})
}

func (l *filterListener) Close() error {
	return l.base.Close()
}
