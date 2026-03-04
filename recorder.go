package dmxcast

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// FrameHandler is a transport-agnostic callback for timestamped DMX frames.
type FrameHandler func(now time.Time, universe uint16, dmx [512]byte) error

// Listener is a source of timestamped DMX frames.
// Implementations should stop when ctx is done and return nil in that case.
type Listener interface {
	Run(ctx context.Context, h FrameHandler) error
	Close() error
}

// Recorder consumes DMX frames from a Listener and writes an OLA Show to an
// io.Writer.
//
// It records using the same timing strategy as OLA's ola_recorder:
//   - Each received frame becomes one OLA show frame.
//   - The delay line is written when the *next* frame arrives (ms, truncated).
//   - The output ends with a frame line (no trailing delay line).
type Recorder struct {
	l  Listener
	bw *bufio.Writer

	started     bool
	firstFrame  bool
	lastArrival time.Time
}

// NewRecorder creates a Recorder bound to a listener.
func NewRecorder(l Listener) *Recorder {
	return &Recorder{
		l:          l,
		firstFrame: true,
	}
}

// Record runs the listener loop and writes an OLA Show to w until ctx is done
// or the listener returns an error.
//
// This method does not start any goroutines. Cancel ctx to stop recording.
func (r *Recorder) Record(ctx context.Context, w io.Writer) error {
	r.bw = bufio.NewWriterSize(w, 64*1024)
	r.started = false
	r.firstFrame = true
	r.lastArrival = time.Time{}

	if err := r.start(); err != nil {
		return err
	}

	err := r.l.Run(ctx, func(now time.Time, universe uint16, dmx [512]byte) error {
		return r.recordFrame(now, universe, dmx)
	})

	_ = r.bw.Flush()
	return err
}

func (r *Recorder) start() error {
	if r.started {
		return nil
	}
	if _, err := r.bw.WriteString("OLA Show\n"); err != nil {
		return err
	}
	r.started = true
	return r.bw.Flush()
}

func (r *Recorder) recordFrame(now time.Time, universe uint16, dmx [512]byte) error {
	if r.firstFrame {
		if _, err := r.bw.WriteString(buildFrameLine(universe, dmx)); err != nil {
			return err
		}
		r.firstFrame = false
		r.lastArrival = now
		return r.bw.Flush()
	}

	delayMs := now.Sub(r.lastArrival).Milliseconds()
	if delayMs < 0 {
		delayMs = 0
	}

	if _, err := fmt.Fprintf(r.bw, "%d\n", delayMs); err != nil {
		return err
	}
	if _, err := r.bw.WriteString(buildFrameLine(universe, dmx)); err != nil {
		return err
	}

	r.lastArrival = now
	return r.bw.Flush()
}

func buildFrameLine(universe uint16, dmx [512]byte) string {
	var sb strings.Builder
	sb.Grow(4096)

	fmt.Fprintf(&sb, "%d ", universe)
	for i := range 512 {
		if i > 0 {
			sb.WriteByte(',')
		}

		fmt.Fprintf(&sb, "%d", dmx[i])
	}

	sb.WriteByte('\n')
	return sb.String()
}
