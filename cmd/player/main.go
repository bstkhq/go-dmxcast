package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bstkhq/go-dmxcast"
	"github.com/bstkhq/go-dmxcast/olashow"
)

// countingTransport wraps a Transport and counts frames sent.
type countingTransport struct {
	tx     dmxcast.Transport
	frames atomic.Uint64
}

func (t *countingTransport) Send(dmx [512]byte) error {
	t.frames.Add(1)
	return t.tx.Send(dmx)
}

func (t *countingTransport) Close() error { return t.tx.Close() }

func (t *countingTransport) Frames() uint64 { return t.frames.Load() }

func main() {
	var (
		filePath = flag.String("file", "", "Path to OLA show file")
		dstIP    = flag.String("ip", "", "Destination IP (Art-Net unicast)")
		srcIP    = flag.String("src", "", "Optional local bind IP (empty = OS default)")
		netID    = flag.Int("net", 0, "Art-Net Net (0..127)")
		subUni   = flag.Int("subuni", 0, "Art-Net SubUni (0..255)")

		loop = flag.Bool("loop", false, "Force loop playback (overrides show metadata)")
		once = flag.Bool("once", false, "Force play once (overrides show metadata)")

		modeStr = flag.String("mode", "htp", "Merge mode: htp or ltp")
		hz      = flag.Float64("hz", 44, "Output refresh rate in Hz (default 44)")
		stats   = flag.Duration("stats", 1*time.Second, "Print stats every interval (0 = disable)")
	)
	flag.Parse()

	if *filePath == "" {
		fmt.Fprintln(os.Stderr, "Missing -file")
		flag.Usage()
		os.Exit(2)
	}
	if *dstIP == "" {
		fmt.Fprintln(os.Stderr, "Missing -ip")
		flag.Usage()
		os.Exit(2)
	}
	if *loop && *once {
		fmt.Fprintln(os.Stderr, "Use only one of -loop or -once")
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

	show, err := olashow.Open(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read show: %v\n", err)
		os.Exit(1)
	}
	if len(show.Frames) == 0 {
		fmt.Fprintln(os.Stderr, "Show has no frames")
		os.Exit(1)
	}

	if *loop {
		show.Loop = true
	}
	if *once {
		show.Loop = false
	}

	mode := parseMode(*modeStr)

	var flush time.Duration
	if *hz > 0 {
		flush = time.Duration(float64(time.Second) / *hz)
	} else {
		flush = 0 // NewPlayer will default to 44 Hz if <= 0
	}

	totalShowTime := estimateShowDuration(show)

	fmt.Printf("DMXCast Player\n")
	fmt.Printf("  File:      %s\n", *filePath)
	fmt.Printf("  Show name: %q\n", show.Name)
	fmt.Printf("  Loop:      %v\n", show.Loop)
	fmt.Printf("  Frames:    %d\n", len(show.Frames))
	if totalShowTime > 0 {
		fmt.Printf("  Duration:  ~%s\n", totalShowTime.Truncate(time.Millisecond))
	} else {
		fmt.Printf("  Duration:  (unknown)\n")
	}
	fmt.Printf("  Output:    Art-Net unicast %s  net=%d subuni=%d  hz=%.2f  mode=%s\n",
		*dstIP, *netID, *subUni, effHz(flush), strings.ToUpper(*modeStr))
	fmt.Printf("  Ctrl+C to stop\n\n")

	tx, err := dmxcast.NewArtNetTransport(&dmxcast.ArtNetConfig{
		DstIP:  net.ParseIP(*dstIP),
		SrcIP:  parseIPOrNil(*srcIP),
		Net:    uint8(*netID),
		SubUni: uint8(*subUni),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Art-Net transport: %v\n", err)
		os.Exit(1)
	}
	defer tx.Close()

	countingTx := &countingTransport{tx: tx}

	player := dmxcast.NewPlayer(countingTx, &dmxcast.PlayerConfig{
		Mode:          mode,
		FlushInterval: flush,
	})
	defer player.Close()

	ctx, cancel := signalContext()
	defer cancel()

	h := player.Play(ctx, show)

	// Stats loop
	var last uint64
	lastAt := time.Now()
	done := make(chan struct{})
	if *stats > 0 {
		go func() {
			t := time.NewTicker(*stats)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					now := time.Now()
					cur := countingTx.Frames()
					delta := cur - last
					sec := now.Sub(lastAt).Seconds()
					rate := float64(delta) / sec
					fmt.Printf("[stats] sent=%d (+%d) rate=%.1f fps playing=%v\n",
						cur, delta, rate, player.IsPlaying(h))
					last, lastAt = cur, now
				}
			}
		}()
	}

	// Wait until finished or cancelled.
	for {
		if ctx.Err() != nil {
			player.Stop(h)
			break
		}
		if !player.IsPlaying(h) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(done)

	fmt.Printf("\nDone. Total frames sent: %d\n", countingTx.Frames())
}

func parseMode(s string) dmxcast.MergeMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ltp":
		return dmxcast.MergeLTP
	default:
		return dmxcast.MergeHTP
	}
}

func parseIPOrNil(s string) net.IP {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return net.ParseIP(s)
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}

func estimateShowDuration(show *olashow.OlaShow) time.Duration {
	var d time.Duration
	for _, f := range show.Frames {
		if f.Delay > 0 {
			d += f.Delay
		}
	}
	return d
}

func effHz(flush time.Duration) float64 {
	if flush <= 0 {
		flush = time.Second / 44
	}
	return float64(time.Second) / float64(flush)
}
