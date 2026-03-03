package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
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

// multiStringFlag collects repeated -file flags.
type multiStringFlag []string

func (m *multiStringFlag) String() string {
	if m == nil {
		return ""
	}
	return strings.Join(*m, ",")
}

func (m *multiStringFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	var (
		files  multiStringFlag
		dstIP  = flag.String("ip", "", "Destination IP (Art-Net unicast)")
		srcIP  = flag.String("src", "", "Optional local bind IP (empty = OS default)")
		netID  = flag.Int("net", 0, "Art-Net Net (0..127)")
		subUni = flag.Int("subuni", 0, "Art-Net SubUni (0..255)")

		loop = flag.Bool("loop", false, "Force loop playback (overrides show metadata)")
		once = flag.Bool("once", false, "Force play once (overrides show metadata)")

		modeStr = flag.String("mode", "htp", "Merge mode: htp or ltp")
		hz      = flag.Float64("hz", 44, "Output refresh rate in Hz (default 44)")
		stats   = flag.Duration("stats", 1*time.Second, "Print stats every interval (0 = disable)")
	)

	// Multi-value -file flag
	flag.Var(&files, "file", "Path(s) or glob(s) to OLA show file(s). Repeat -file, use commas, or globs (e.g. -file 'shows/*.show').")
	flag.Parse()

	expandedFiles, err := expandFiles(files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid -file: %v\n", err)
		os.Exit(2)
	}
	if len(expandedFiles) == 0 {
		fmt.Fprintln(os.Stderr, "Missing -file (or glob matched nothing)")
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

	mode := parseMode(*modeStr)

	var flush time.Duration
	if *hz > 0 {
		flush = time.Duration(float64(time.Second) / *hz)
	} else {
		flush = 0 // NewPlayer will default to 44 Hz if <= 0
	}

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

	// ---- Load all shows first ----
	type loadedShow struct {
		path string
		show *olashow.OlaShow
	}

	loaded := make([]loadedShow, 0, len(expandedFiles))
	for _, path := range expandedFiles {
		s, err := olashow.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read show %q: %v\n", path, err)
			os.Exit(1)
		}
		if len(s.Frames) == 0 {
			fmt.Fprintf(os.Stderr, "Show %q has no frames\n", path)
			os.Exit(1)
		}

		// Overrides
		if *loop {
			s.Loop = true
		}
		if *once {
			s.Loop = false
		}

		loaded = append(loaded, loadedShow{path: path, show: s})
	}

	fmt.Printf("Output: Art-Net unicast %s  net=%d subuni=%d  hz=%.2f  mode=%s\n",
		*dstIP, *netID, *subUni, effHz(flush), strings.ToUpper(*modeStr))
	fmt.Printf("Shows:  %d (started concurrently)\n", len(loaded))
	for i, ls := range loaded {
		d := estimateShowDuration(ls.show)
		dStr := "(unknown)"
		if d > 0 {
			dStr = "~" + d.Truncate(time.Millisecond).String()
		}
		fmt.Printf("  [%2d] %s  name=%q loop=%v frames=%d duration=%s\n",
			i+1, filepath.Base(ls.path), ls.show.Name, ls.show.Loop, len(ls.show.Frames), dStr)
	}
	fmt.Printf("Ctrl+C to stop\n\n")

	// ---- Start ALL shows concurrently ----
	handles := make([]dmxcast.ShowHandle, 0, len(loaded))
	for _, ls := range loaded {
		h := player.Play(ctx, ls.show)
		handles = append(handles, h)
	}

	// ---- Stats loop (global merged output) ----
	var totalFramesStart uint64 = countingTx.Frames()
	var last uint64 = totalFramesStart
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

					playing := 0
					for _, h := range handles {
						if player.IsPlaying(h) {
							playing++
						}
					}

					fmt.Printf("[stats] sent=%d (+%d) rate=%.1f fps playing=%d/%d\n",
						cur, delta, rate, playing, len(handles))

					last, lastAt = cur, now
				}
			}
		}()
	}

	// ---- Wait until ALL finished or cancelled ----
	for {
		if ctx.Err() != nil {
			for _, h := range handles {
				player.Stop(h)
			}
			break
		}

		allDone := true
		for _, h := range handles {
			if player.IsPlaying(h) {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(done)

	fmt.Printf("\nDone. Total frames sent: %d (delta=%d)\n",
		countingTx.Frames(),
		countingTx.Frames()-totalFramesStart,
	)
}

func expandFiles(values []string) ([]string, error) {
	// Split comma-separated entries across repeated flags.
	var raw []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		parts := strings.Split(v, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				raw = append(raw, p)
			}
		}
	}

	// Expand globs; if no meta characters, treat as literal path.
	var out []string
	seen := map[string]bool{}
	for _, item := range raw {
		matches, err := globOrLiteral(item)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			abs, err := filepath.Abs(m)
			if err != nil {
				return nil, err
			}
			if !seen[abs] {
				seen[abs] = true
				out = append(out, abs)
			}
		}
	}

	return out, nil
}

func globOrLiteral(pattern string) ([]string, error) {
	if hasGlobMeta(pattern) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		// Make glob results deterministic across filesystems.
		sort.Strings(matches)
		return matches, nil
	}

	// Literal: require it to exist.
	if _, err := os.Stat(pattern); err != nil {
		return nil, err
	}
	return []string{pattern}, nil
}

func hasGlobMeta(s string) bool {
	// filepath.Glob treats *, ?, [] as meta.
	return strings.ContainsAny(s, "*?[")
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
