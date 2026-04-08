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

const DefaultImplicitFrameDelay = time.Second / 44

// Frame is a DMX snapshot for a universe plus the delay to the next frame.
type Frame struct {
	Universe uint16
	Data     [512]byte
	Length   int
	Delay    time.Duration
}

// OlaShow is a decoded OLA Show file.
type OlaShow struct {
	// Name is an optional display name for the show.
	Name string
	// Loop is the requested repeat count:
	//   -1  infinite loop
	//    0  play once (default)
	//   >0  repeat the given number of times
	Loop int
	// Duration is the total amount of time the show should keep looping.
	// When > 0, it overrides Loop count-based repetition.
	Duration time.Duration
	// Exclusive indicates the show requests exclusive control while playing.
	Exclusive bool
	// Frames is the sequence of DMX snapshots that make up the show.
	Frames []Frame `json:"-"`
}

func (s *OlaShow) String() string {
	var b strings.Builder
	_ = Write(s, &b)
	return b.String()
}

// Open reads an OLA Show file from disk.
func Open(file string) (*OlaShow, error) {
	resolver := NewDefaultResolver(file)

	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}

	defer f.Close()
	return Read(f, resolver)
}

// Read parses an OLA Show file from r and loads it into memory.
func Read(r io.Reader, resolver FileResolver) (*OlaShow, error) {
	sc := bufio.NewScanner(r)

	buf := make([]byte, 0, 16*1024)
	sc.Buffer(buf, 2*1024*1024)

	show := &OlaShow{Frames: make([]Frame, 0, 256)}
	var includes []string

	lineNo, ok, err := readMetaBlock(sc, show, &includes, resolver)
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
		pending.Delay = DefaultImplicitFrameDelay
		show.Frames = append(show.Frames, pending)
	}

	if len(includes) > 0 {
		var prefix []Frame
		for _, inc := range includes {
			if resolver == nil {
				return nil, fmt.Errorf("include %q: no FileResolver provided", inc)
			}

			rc, err := resolver.Open(inc)
			if err != nil {
				return nil, err
			}

			incShow, err := Read(rc, resolver.FileResolver(inc))
			_ = rc.Close()
			if err != nil {
				return nil, err
			}

			prefix = append(prefix, incShow.Frames...)
		}

		if len(prefix) > 0 {
			frames := make([]Frame, 0, len(prefix)+len(show.Frames))
			frames = append(frames, prefix...)
			frames = append(frames, show.Frames...)
			show.Frames = frames
		}
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

	if show.Duration > 0 {
		if _, err := fmt.Fprintf(bw, "# duration=%s\n", show.Duration); err != nil {
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

func readMetaBlock(sc *bufio.Scanner, show *OlaShow, includes *[]string, resolver FileResolver) (lineNo int, headerRead bool, err error) {
	var sidecard bool
	if resolver != nil {
		if rc, err := resolver.Metadata(); err == nil {
			if err := readMetaFile(rc, show, includes); err != nil {
				return 0, false, err
			}

			sidecard = true
		}
	}

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "#") {
			if line != "OLA Show" {
				return lineNo, false, fmt.Errorf("%w (line %d: %q)", ErrBadHeader, lineNo, line)
			}

			return lineNo, true, nil
		}

		if sidecard {
			return lineNo, false, fmt.Errorf("header metadata detected with sidecard .metadata file")
		}

		for {
			if err := parseMetaLine(show, line, includes); err != nil {
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

			if line == "" {
				return lineNo, false, fmt.Errorf("metadata block must be consecutive (empty line at %d)", lineNo)
			}

			if strings.HasPrefix(line, "#") {
				continue
			}

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

func readMetaFile(r io.ReadCloser, show *OlaShow, includes *[]string) error {
	defer r.Close()

	sc := bufio.NewScanner(r)

	buf := make([]byte, 0, 8*1024)
	sc.Buffer(buf, 2*1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			return fmt.Errorf("parse metadata line %d: expected key=value", lineNo)
		}
		if err := parseMetaLine(show, "# "+line, includes); err != nil {
			return fmt.Errorf("parse metadata line %d: %w", lineNo, err)
		}
	}

	if err := sc.Err(); err != nil {
		return err
	}

	return nil
}

func parseMetaLine(show *OlaShow, line string, includes *[]string) error {
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

	case "duration":
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid duration value %q", v)
		}
		if d < 0 {
			return fmt.Errorf("invalid duration value %q", v)
		}
		show.Duration = d
		return nil

	case "exclusive":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid exclusive value %q", v)
		}
		show.Exclusive = b
		return nil

	case "include":
		if includes != nil {
			*includes = append(*includes, v)
		}
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
