package olashow

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrBadHeader indicates the input is not an OLA Show file.
	ErrBadHeader = errors.New(`invalid header: expected "OLA Show"`)
)

// Frame is a DMX snapshot for a universe plus the delay to the next frame.
//
// Length is how many slots in Data are meaningful (0..512).
// Delay is stored in the file as an integer number of milliseconds.
type Frame struct {
	Universe uint16
	Data     [512]byte
	Length   int
	Delay    time.Duration
}

// OlaShow is a decoded OLA Show file.
type OlaShow struct {
	Frames []Frame
}

// Open reads an OLA Show file from disk.
func Open(filepath string) (*OlaShow, error) {
	f, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

// Read parses an OLA Show file from r and loads it into memory.
func Read(r io.Reader) (*OlaShow, error) {
	sc := bufio.NewScanner(r)

	buf := make([]byte, 0, 16*1024)
	sc.Buffer(buf, 2*1024*1024)

	lineNo := 0
	seenHeader := false

	expectDelay := false
	var pending Frame

	show := &OlaShow{Frames: make([]Frame, 0, 256)}

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if !seenHeader {
			if line != "OLA Show" {
				return nil, fmt.Errorf("%w (line %d: %q)", ErrBadHeader, lineNo, line)
			}
			seenHeader = true
			continue
		}

		if !expectDelay {
			f, err := parseFrameLine(line)
			if err != nil {
				return nil, fmt.Errorf("parse frame line %d: %w", lineNo, err)
			}
			pending = f
			expectDelay = true
			continue
		}

		delayMs, err := parseDelayLine(line)
		if err != nil {
			return nil, fmt.Errorf("parse delay line %d: %w", lineNo, err)
		}
		pending.Delay = time.Duration(delayMs) * time.Millisecond

		show.Frames = append(show.Frames, pending)
		pending = Frame{}
		expectDelay = false
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !seenHeader {
		return nil, fmt.Errorf("%w (file ended before header)", ErrBadHeader)
	}

	if expectDelay {
		pending.Delay = 0
		show.Frames = append(show.Frames, pending)
	}

	return show, nil
}

// Write encodes show to w using the OLA Show text format.
func Write(show *OlaShow, w io.Writer) error {
	if show == nil {
		return fmt.Errorf("show is nil")
	}

	bw := bufio.NewWriter(w)

	if _, err := bw.WriteString("OLA Show\n"); err != nil {
		return err
	}

	for i, f := range show.Frames {
		if f.Length < 0 || f.Length > 512 {
			return fmt.Errorf("frame %d: invalid Length %d (must be 0..512)", i, f.Length)
		}

		delayMs := f.Delay.Milliseconds()
		if delayMs < 0 {
			return fmt.Errorf("frame %d: negative Delay %v", i, f.Delay)
		}

		if err := writeFrameLine(bw, f); err != nil {
			return fmt.Errorf("frame %d: %w", i, err)
		}
		if _, err := fmt.Fprintf(bw, "%d\n", delayMs); err != nil {
			return err
		}
	}

	return bw.Flush()
}

func writeFrameLine(w io.Writer, f Frame) error {
	if _, err := fmt.Fprintf(w, "%d ", f.Universe); err != nil {
		return err
	}

	for i := 0; i < f.Length; i++ {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%d", f.Data[i]); err != nil {
			return err
		}
	}

	_, err := io.WriteString(w, "\n")
	return err
}

func parseFrameLine(line string) (Frame, error) {
	i := strings.IndexFunc(line, func(r rune) bool { return r == ' ' || r == '\t' })
	if i < 0 {
		return Frame{}, fmt.Errorf("expected '<universe> <dmx_csv>', got %q", line)
	}

	uStr := strings.TrimSpace(line[:i])
	dmxStr := strings.TrimSpace(line[i:])

	u64, err := strconv.ParseUint(uStr, 10, 16)
	if err != nil {
		return Frame{}, fmt.Errorf("invalid universe %q: %v", uStr, err)
	}

	data, n, err := parseDMXCSV(dmxStr)
	if err != nil {
		return Frame{}, err
	}

	return Frame{
		Universe: uint16(u64),
		Data:     data,
		Length:   n,
	}, nil
}

func parseDelayLine(line string) (int64, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, fmt.Errorf("empty delay")
	}
	ms, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid delay %q: %v", line, err)
	}
	if ms < 0 {
		return 0, fmt.Errorf("delay must be >= 0, got %d", ms)
	}
	return ms, nil
}

func parseDMXCSV(s string) (data [512]byte, n int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return data, 0, nil
	}

	parts := strings.Split(s, ",")
	if len(parts) > 512 {
		return data, 0, fmt.Errorf("too many DMX slots: %d (max 512)", len(parts))
	}

	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			data[i] = 0
			n++
			continue
		}

		v, e := strconv.ParseUint(p, 10, 8)
		if e != nil {
			return data, 0, fmt.Errorf("invalid DMX value %q at index %d: %v", p, i, e)
		}
		data[i] = byte(v)
		n++
	}

	return data, n, nil
}
