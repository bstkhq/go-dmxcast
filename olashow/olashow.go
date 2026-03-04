// olashow.go
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
type Frame struct {
	Universe uint16
	Data     [512]byte
	Length   int
	Delay    time.Duration
}

// OlaShow is a decoded OLA Show file.
type OlaShow struct {
	Name string
	// Loop times; if -1 it's infinite.
	Loop int
	// Exclusive indicates the show should take exclusive control (header metadata).
	Exclusive bool
	Frames    []Frame
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
//
// Optional leading metadata lines are supported (only at the beginning, as a
// consecutive block with no blank lines inside):
//
//	# name=<string>
//	# loop=<bool|int>   (true => -1, false => 0, int => that value)
//	# exclusive=<bool>
//
// If any metadata line is present, the next line after the metadata block must
// be "OLA Show". Lines starting with '#' are not allowed elsewhere.
//
// The show body is plain text:
//   - "OLA Show"
//   - Then repeating pairs:
//     "<universe> <dmx_csv>"
//     "<delay_ms>"
//
// Notes:
//   - Empty DMX CSV fields (",,") are treated as 0.
//   - If the file ends right after a frame line, the last frame Delay is 0.
func Read(r io.Reader) (*OlaShow, error) {
	sc := bufio.NewScanner(r)

	buf := make([]byte, 0, 16*1024)
	sc.Buffer(buf, 2*1024*1024)

	show := &OlaShow{Frames: make([]Frame, 0, 256)}

	lineNo, ok, err := readMetaBlock(sc, show)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w (file ended before header)", ErrBadHeader)
	}

	expectDelay := false
	var pending Frame

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			return nil, fmt.Errorf("comments are only allowed at the beginning (line %d)", lineNo)
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

	if expectDelay {
		pending.Delay = 0
		show.Frames = append(show.Frames, pending)
	}

	return show, nil
}

// Write encodes show to w using the OLA Show text format.
func Write(show *OlaShow, w io.Writer) error {
	bw := bufio.NewWriter(w)

	if show.Name != "" {
		if _, err := fmt.Fprintf(bw, "# name=%s\n", show.Name); err != nil {
			return err
		}
	}

	if show.Loop == -1 {
		if _, err := bw.WriteString("# loop=true\n"); err != nil {
			return err
		}
	} else if show.Loop != 0 {
		if _, err := fmt.Fprintf(bw, "# loop=%d\n", show.Loop); err != nil {
			return err
		}
	}

	if show.Exclusive {
		if _, err := bw.WriteString("# exclusive=true\n"); err != nil {
			return err
		}
	}

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

// readMetaBlock reads an optional consecutive metadata block at the beginning.
// If a metadata line is present, the next line after the block must be "OLA Show".
// No blank lines are allowed inside the block.
func readMetaBlock(sc *bufio.Scanner, show *OlaShow) (lineNo int, headerRead bool, err error) {
	// Skip leading empty lines.
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		// No metadata block: first non-empty line must be header.
		if !strings.HasPrefix(line, "#") {
			if line != "OLA Show" {
				return lineNo, false, fmt.Errorf("%w (line %d: %q)", ErrBadHeader, lineNo, line)
			}
			return lineNo, true, nil
		}

		// Metadata block (must be consecutive).
		for {
			if err := parseMetaLine(show, line); err != nil {
				return lineNo, false, fmt.Errorf("parse metadata line %d: %w", lineNo, err)
			}

			if !sc.Scan() {
				if sc.Err() != nil {
					return lineNo, false, sc.Err()
				}
				return lineNo, false, fmt.Errorf("%w (file ended before header)", ErrBadHeader)
			}

			lineNo++
			line = strings.TrimSpace(sc.Text())

			// No blank lines inside metadata block.
			if line == "" {
				return lineNo, false, fmt.Errorf("metadata block must be consecutive (empty line at %d)", lineNo)
			}

			if strings.HasPrefix(line, "#") {
				continue
			}

			// End of metadata block: must be exact header.
			if line != "OLA Show" {
				return lineNo, false, fmt.Errorf("%w (line %d: %q)", ErrBadHeader, lineNo, line)
			}
			return lineNo, true, nil
		}
	}

	if sc.Err() != nil {
		return lineNo, false, sc.Err()
	}
	return lineNo, false, fmt.Errorf("%w (file ended before header)", ErrBadHeader)
}

func parseMetaLine(show *OlaShow, line string) error {
	s := strings.TrimSpace(strings.TrimPrefix(line, "#"))
	if s == "" {
		return fmt.Errorf("empty metadata line")
	}

	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("expected # key=value")
	}
	k = strings.TrimSpace(k)
	v = strings.TrimSpace(v)

	switch k {
	case "name":
		show.Name = v
		return nil

	case "loop":
		// bool or int
		if b, err := strconv.ParseBool(v); err == nil {
			if b {
				show.Loop = -1
			} else {
				show.Loop = 0
			}
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid loop value %q", v)
		}
		show.Loop = n
		return nil

	case "exclusive":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid exclusive value %q", v)
		}
		show.Exclusive = b
		return nil

	default:
		return fmt.Errorf("unknown variable %q", k)
	}
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
