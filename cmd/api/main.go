// cmd/server/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/bstkhq/go-dmxcast"
	"github.com/bstkhq/go-dmxcast/api"
	"github.com/labstack/echo/v4"
)

func main() {
	var (
		showFolder = flag.String("shows", "", "Folder containing OLA show files (required)")
		dstIPStr   = flag.String("dst", "", "Art-Net destination IP (required)")
		subUni     = flag.Int("subuni", -1, "Art-Net SubUni (0..255) (required)")
		netID      = flag.Int("net", 0, "Art-Net Net (0..127)")
		srcIPStr   = flag.String("src", "", "Optional local bind IP (empty = OS default)")
		modeStr    = flag.String("mode", "htp", "Merge mode: htp or ltp")
	)
	flag.Parse()

	if *showFolder == "" {
		log.Fatal("missing -shows")
	}
	if *dstIPStr == "" {
		log.Fatal("missing -dst")
	}
	if *subUni < 0 || *subUni > 255 {
		log.Fatal("invalid -subuni (must be 0..255)")
	}
	if *netID < 0 || *netID > 127 {
		log.Fatal("invalid -net (must be 0..127)")
	}

	dst := net.ParseIP(*dstIPStr)
	if dst == nil {
		log.Fatalf("invalid -dst: %q", *dstIPStr)
	}

	var src net.IP
	if *srcIPStr != "" {
		src = net.ParseIP(*srcIPStr)
		if src == nil {
			log.Fatalf("invalid -src: %q", *srcIPStr)
		}
	}

	var mode dmxcast.MergeMode
	switch strings.ToLower(strings.TrimSpace(*modeStr)) {
	case "htp":
		mode = dmxcast.MergeHTP
	case "ltp":
		mode = dmxcast.MergeLTP
	default:
		log.Fatalf("invalid -mode: %q (expected htp or ltp)", *modeStr)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	fmt.Println("dmxplayer starting")
	fmt.Printf("  HTTP:        %s\n", ":8080")
	fmt.Printf("  Shows:       %s\n", *showFolder)
	fmt.Printf("  Art-Net dst: %s\n", dst.String())
	fmt.Printf("  Art-Net net: %d\n", *netID)
	fmt.Printf("  Art-Net sub: %d (0x%02X)\n", *subUni, *subUni)
	if src != nil {
		fmt.Printf("  Art-Net src: %s\n", src.String())
	} else {
		fmt.Printf("  Art-Net src: (os default)\n")
	}
	fmt.Printf("  Merge mode:  %s\n", strings.ToUpper(strings.TrimSpace(*modeStr)))

	tx, err := dmxcast.NewArtNetTransport(&dmxcast.ArtNetConfig{
		DstIP:  dst,
		SrcIP:  src,
		Net:    uint8(*netID),
		SubUni: uint8(*subUni),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer tx.Close()

	player := dmxcast.NewPlayer(tx, &dmxcast.PlayerConfig{
		Mode: mode,
	})
	defer player.Close()

	lib, err := dmxcast.NewLibrary(player, &dmxcast.LibraryConfig{
		Path: *showFolder,
		OnEvent: func(ev dmxcast.LibraryEvent) {
			var show string
			if ev.Show != nil {
				show = ev.Show.FileName
			}
			fmt.Printf("[%s] event=%s show=%s running=%s\n",
				ev.At.Format(time.RFC3339),
				ev.Reason,
				show,
				formatRunning(ev.Running),
			)
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	api.NewShowController(lib).RegisterRoutes(e.Group("/show"), nil)
	api.NewProgController(lib).RegisterRoutes(e.Group("/prog"), nil)

	fmt.Println("  Routes:")
	fmt.Println("    GET  /show")
	fmt.Println("    GET  /show/:key")
	fmt.Println("    GET  /show/:key/play")
	fmt.Println("    GET  /show/:key/stop")
	fmt.Println("    GET  /show/stop")
	fmt.Println("    GET  /prog/:num")
	fmt.Println("    GET  /prog/:num/play")
	fmt.Println("    GET  /prog/:num/stop")
	fmt.Println("    GET  /prog/:num/stop/all")

	go func() {
		defer cancel()
		if err := e.Start(":8080"); err != nil {
			log.Fatal(fmt.Errorf("error starting server: %w", err))
		}
	}()

	<-ctx.Done()
	fmt.Println("dmxplayer shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Fatal(fmt.Errorf("error shutting down server: %w", err))
	}

	fmt.Println("dmxplayer stopped")
}

func formatRunning(running []dmxcast.PlayInfo) string {
	if len(running) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(running))
	for _, p := range running {
		name := p.Show.Name
		if name == "" {
			name = "?"
		}
		parts = append(parts, fmt.Sprintf("%03d:%s(#%d)", p.ShowID, name, p.HandleID))
	}
	return strings.Join(parts, " ")
}

func eventReasonToString(reason string) string {
	if reason == "" {
		return "?"
	}
	return reason
}
