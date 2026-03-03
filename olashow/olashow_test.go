package olashow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRead_ParsesSample(t *testing.T) {
	input := strings.Join([]string{
		"OLA Show",
		"1 172,10,180",
		"58",
		"1 0,0,255",
		"57",
		"",
	}, "\n")

	show, err := Read(strings.NewReader(input))
	require.NoError(t, err)
	require.NotNil(t, show)
	require.Len(t, show.Frames, 2)

	f0 := show.Frames[0]
	require.Equal(t, uint16(1), f0.Universe)
	require.Equal(t, 3, f0.Length)
	require.Equal(t, byte(172), f0.Data[0])
	require.Equal(t, byte(10), f0.Data[1])
	require.Equal(t, byte(180), f0.Data[2])
	require.Equal(t, 58*time.Millisecond, f0.Delay)

	f1 := show.Frames[1]
	require.Equal(t, uint16(1), f1.Universe)
	require.Equal(t, 3, f1.Length)
	require.Equal(t, byte(0), f1.Data[0])
	require.Equal(t, byte(0), f1.Data[1])
	require.Equal(t, byte(255), f1.Data[2])
	require.Equal(t, 57*time.Millisecond, f1.Delay)
}

func TestWrite_ThenRead_RoundTrip(t *testing.T) {
	var show OlaShow
	show.Frames = make([]Frame, 0, 2)

	var f0 Frame
	f0.Universe = 1
	f0.Length = 5
	f0.Data[0] = 1
	f0.Data[1] = 2
	f0.Data[2] = 3
	f0.Data[3] = 4
	f0.Data[4] = 5
	f0.Delay = 33 * time.Millisecond
	show.Frames = append(show.Frames, f0)

	var f1 Frame
	f1.Universe = 2
	f1.Length = 1
	f1.Data[0] = 255
	f1.Delay = 0
	show.Frames = append(show.Frames, f1)

	var buf bytes.Buffer
	require.NoError(t, Write(&show, &buf))

	got, err := Read(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Len(t, got.Frames, 2)

	require.Equal(t, show.Frames[0].Universe, got.Frames[0].Universe)
	require.Equal(t, show.Frames[0].Length, got.Frames[0].Length)
	require.Equal(t, show.Frames[0].Delay, got.Frames[0].Delay)
	require.Equal(t, show.Frames[0].Data, got.Frames[0].Data)

	require.Equal(t, show.Frames[1].Universe, got.Frames[1].Universe)
	require.Equal(t, show.Frames[1].Length, got.Frames[1].Length)
	require.Equal(t, show.Frames[1].Delay, got.Frames[1].Delay)
	require.Equal(t, show.Frames[1].Data, got.Frames[1].Data)
}

func TestRead_BadHeader(t *testing.T) {
	_, err := Read(strings.NewReader("NOT OLA\n1 1,2,3\n10\n"))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrBadHeader)
}

func TestRead_MissingDelay(t *testing.T) {
	_, err := Read(strings.NewReader("NOT OLA\n1 1,2,3\n1 1,2,3\n10\n"))
	require.Error(t, err)
}

func TestWrite_InvalidLength(t *testing.T) {
	show := &OlaShow{
		Frames: []Frame{
			{Universe: 1, Length: 999, Delay: 1 * time.Millisecond},
		},
	}
	var buf bytes.Buffer
	require.Error(t, Write(show, &buf))
}

func TestRead_OficialTestdata(t *testing.T) {
	fixtures := []string{
		"dos_line_endings",
		"multiple_unis",
		"partial_frames",
		"single_uni",
		"trailing_timeout",
	}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			b := readFixture(t, name)

			show, err := Read(bytes.NewReader(b))
			require.NoError(t, err, "fixture should be valid according to ola_recorder --verify")
			assertShowSane(t, show)

			var out bytes.Buffer
			require.NoError(t, Write(show, &out))

			show2, err := Read(bytes.NewReader(out.Bytes()))
			require.NoError(t, err)
			assertShowSane(t, show2)

			require.Equal(t, len(show.Frames), len(show2.Frames))
		})
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	p := filepath.Join("testdata", name)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		t.Skipf("fixture %q not found at %s (copy from OLA examples/testdata)", name, p)
	}
	require.NoError(t, err)
	require.NotEmpty(t, b, "fixture %q is empty", name)
	return b
}

func assertShowSane(t *testing.T, show *OlaShow) {
	t.Helper()

	require.NotNil(t, show)
	require.NotEmpty(t, show.Frames)

	for i, f := range show.Frames {
		require.GreaterOrEqualf(t, f.Length, 0, "frame %d length", i)
		require.LessOrEqualf(t, f.Length, 512, "frame %d length", i)
		require.GreaterOrEqualf(t, f.Delay, time.Duration(0), "frame %d delay", i)
	}
}

func TestRead_Metadata_ParsesNameAndLoop(t *testing.T) {
	in := strings.Join([]string{
		"# name=My Show",
		"# loop=true",
		"OLA Show",
		"1 1,2,3",
		"10",
		"",
	}, "\n")

	s, err := Read(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, "My Show", s.Name)
	require.True(t, s.Loop)
	require.Len(t, s.Frames, 1)
	require.Equal(t, 10*time.Millisecond, s.Frames[0].Delay)
}

func TestRead_Metadata_UnknownKeyFails(t *testing.T) {
	in := strings.Join([]string{
		"# foo=bar",
		"OLA Show",
		"1 1,2,3",
		"10",
	}, "\n")

	_, err := Read(strings.NewReader(in))
	require.Error(t, err)
}

func TestRead_Metadata_NotAllowedInBodyFails(t *testing.T) {
	in := strings.Join([]string{
		"OLA Show",
		"1 1,2,3",
		"10",
		"# name=Nope",
	}, "\n")

	_, err := Read(strings.NewReader(in))
	require.Error(t, err)
}

func TestWrite_WritesMetadataAtTop(t *testing.T) {
	show := &OlaShow{
		Name: "Hello",
		Loop: true,
		Frames: []Frame{
			func() Frame {
				var f Frame
				f.Universe = 1
				f.Length = 3
				f.Data[0] = 1
				f.Data[1] = 2
				f.Data[2] = 3
				f.Delay = 10 * time.Millisecond
				return f
			}(),
		},
	}

	var buf bytes.Buffer
	require.NoError(t, Write(show, &buf))

	out := buf.String()
	require.True(t, strings.HasPrefix(out, "# name=Hello\n# loop=true\nOLA Show\n"))

	// roundtrip preserves fields
	got, err := Read(strings.NewReader(out))
	require.NoError(t, err)
	require.Equal(t, "Hello", got.Name)
	require.True(t, got.Loop)
	require.Len(t, got.Frames, 1)
}
