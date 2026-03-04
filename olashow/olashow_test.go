// olashow_test.go
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

func TestOLA_Fixtures_AllVerifyFilesParse(t *testing.T) {
	// These are the exact files used by OLA's examples/RecorderVerifyTest.sh
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

func TestWrite_InvalidLength(t *testing.T) {
	s := &OlaShow{
		Frames: []Frame{
			func() Frame {
				var f Frame
				f.Universe = 1
				f.Length = 513
				f.Delay = 0
				return f
			}(),
		},
	}

	var buf bytes.Buffer
	require.Error(t, Write(s, &buf))
}

func TestRead_Metadata_Block_ParsesNameLoopExclusive(t *testing.T) {
	in := strings.Join([]string{
		"# name=My Show",
		"# loop=true",
		"# exclusive=true",
		"OLA Show",
		"1 1,2,3",
		"10",
	}, "\n")

	s, err := Read(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, "My Show", s.Name)
	require.Equal(t, -1, s.Loop)
	require.True(t, s.Exclusive)
	require.Len(t, s.Frames, 1)
	require.Equal(t, 10*time.Millisecond, s.Frames[0].Delay)
}

func TestRead_Metadata_LoopBoolFalseIsZero(t *testing.T) {
	in := strings.Join([]string{
		"# loop=false",
		"OLA Show",
		"1 1,2,3",
		"0",
	}, "\n")

	s, err := Read(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, 0, s.Loop)
}

func TestRead_Metadata_LoopInt(t *testing.T) {
	in := strings.Join([]string{
		"# loop=3",
		"OLA Show",
		"1 1,2,3",
		"0",
	}, "\n")

	s, err := Read(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, 3, s.Loop)
}

func TestRead_Metadata_UnknownKeyFails(t *testing.T) {
	in := strings.Join([]string{
		"# foo=bar",
		"OLA Show",
		"1 1,2,3",
		"0",
	}, "\n")

	_, err := Read(strings.NewReader(in))
	require.Error(t, err)
}

func TestRead_Metadata_BlockMustBeConsecutive_EmptyLineFails(t *testing.T) {
	in := strings.Join([]string{
		"# name=X",
		"",
		"# loop=true",
		"OLA Show",
		"1 1,2,3",
		"0",
	}, "\n")

	_, err := Read(strings.NewReader(in))
	require.Error(t, err)
}

func TestRead_Metadata_MustBeImmediatelyFollowedByHeader(t *testing.T) {
	in := strings.Join([]string{
		"# name=X",
		"not header",
		"OLA Show",
		"1 1,2,3",
		"0",
	}, "\n")

	_, err := Read(strings.NewReader(in))
	require.Error(t, err)
}

func TestRead_Metadata_NotAllowedAfterHeaderFails(t *testing.T) {
	in := strings.Join([]string{
		"OLA Show",
		"1 1,2,3",
		"0",
		"# name=Nope",
	}, "\n")

	_, err := Read(strings.NewReader(in))
	require.Error(t, err)
}

func TestWrite_Metadata_FormatsLoopAndExclusive(t *testing.T) {
	show := &OlaShow{
		Name:      "Hello",
		Loop:      -1,
		Exclusive: true,
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

	require.True(t, strings.HasPrefix(out, "# name=Hello\n# loop=true\n# exclusive=true\nOLA Show\n"))

	// Roundtrip preserves fields.
	got, err := Read(strings.NewReader(out))
	require.NoError(t, err)
	require.Equal(t, "Hello", got.Name)
	require.Equal(t, -1, got.Loop)
	require.True(t, got.Exclusive)
	require.Len(t, got.Frames, 1)
}

func TestWrite_Metadata_LoopInt(t *testing.T) {
	show := &OlaShow{
		Name: "X",
		Loop: 5,
		Frames: []Frame{
			func() Frame {
				var f Frame
				f.Universe = 1
				f.Length = 1
				f.Data[0] = 7
				f.Delay = 0
				return f
			}(),
		},
	}

	var buf bytes.Buffer
	require.NoError(t, Write(show, &buf))
	out := buf.String()

	require.Contains(t, out, "# loop=5\n")
	require.Contains(t, out, "OLA Show\n")
}
