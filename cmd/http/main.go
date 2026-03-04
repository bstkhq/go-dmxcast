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
	"github.com/bstkhq/go-dmxcast/http"
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

	// Ctrl-C context
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

	lib, err := http.NewLibrary(player, *showFolder)
	if err != nil {
		log.Fatal(err)
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	http.NewShowController(lib).RegisterRoutes(e.Group("/show"), nil)
	http.NewProgController(lib).RegisterRoutes(e.Group("/prog"), nil)

	fmt.Println("  Routes:")
	fmt.Println("    GET  /show")
	fmt.Println("    GET  /show/:key")
	fmt.Println("    GET  /show/:key/play")
	fmt.Println("    GET  /show/:key/stop")
	fmt.Println("    GET  /show/stop")

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
