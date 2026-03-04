package olashow

import (
	"bytes"
	"fmt"
	"io"
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

			show, err := Read(bytes.NewReader(b), nil)
			require.NoError(t, err, "fixture should be valid according to ola_recorder --verify")
			assertShowSane(t, show)

			var out bytes.Buffer
			require.NoError(t, Write(show, &out))

			show2, err := Read(bytes.NewReader(out.Bytes()), nil)
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

	s, err := Read(strings.NewReader(in), nil)
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

	s, err := Read(strings.NewReader(in), nil)
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

	s, err := Read(strings.NewReader(in), nil)
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

	_, err := Read(strings.NewReader(in), nil)
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

	_, err := Read(strings.NewReader(in), nil)
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

	_, err := Read(strings.NewReader(in), nil)
	require.Error(t, err)
}

func TestRead_Metadata_NotAllowedAfterHeaderFails(t *testing.T) {
	in := strings.Join([]string{
		"OLA Show",
		"1 1,2,3",
		"0",
		"# name=Nope",
	}, "\n")

	_, err := Read(strings.NewReader(in), nil)
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

	got, err := Read(strings.NewReader(out), nil)
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

type memResolver struct {
	files    map[string]string
	seen     map[string]bool
	metadata string
}

func (r *memResolver) Open(file string) (io.ReadCloser, error) {
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	if r.seen[file] {
		return nil, fmt.Errorf("cyclic include %q", file)
	}
	r.seen[file] = true

	s, ok := r.files[file]
	if !ok {
		return nil, os.ErrNotExist
	}

	return io.NopCloser(strings.NewReader(s)), nil
}

func (r *memResolver) Metadata() (io.ReadCloser, error) {
	if r.metadata == "" {
		return nil, fmt.Errorf("no metadata")
	}

	return io.NopCloser(strings.NewReader(r.metadata)), nil
}

func (r *memResolver) FileResolver(name string) FileResolver {
	nr := NewDefaultResolver(name)
	for k, v := range r.seen {
		nr.seen[k] = v
	}

	return nr
}

func TestRead_Include_PrependsFrames(t *testing.T) {
	root := strings.Join([]string{
		"# include=a.show",
		"OLA Show",
		"1 9",
		"0",
	}, "\n")

	a := strings.Join([]string{
		"OLA Show",
		"1 1",
		"0",
		"1 2",
		"0",
	}, "\n")

	res := &memResolver{
		files: map[string]string{
			"a.show": a,
		},
	}

	s, err := Read(strings.NewReader(root), res)
	require.NoError(t, err)
	require.Len(t, s.Frames, 3)
	require.Equal(t, byte(1), s.Frames[0].Data[0])
	require.Equal(t, byte(2), s.Frames[1].Data[0])
	require.Equal(t, byte(9), s.Frames[2].Data[0])
}

func TestRead_Include_DuplicateFails(t *testing.T) {
	root := strings.Join([]string{
		"# include=a.show",
		"# include=a.show",
		"OLA Show",
		"1 9",
		"0",
	}, "\n")

	a := strings.Join([]string{
		"OLA Show",
		"1 1",
		"0",
	}, "\n")

	res := &memResolver{
		files: map[string]string{
			"a.show": a,
		},
	}

	_, err := Read(strings.NewReader(root), res)
	require.Error(t, err)
}

func TestRead_SidecarMetadata_Applies(t *testing.T) {
	root := strings.Join([]string{
		"OLA Show",
		"1 9",
		"0",
	}, "\n")

	meta := strings.Join([]string{
		"name=FromMeta",
		"loop=true",
		"exclusive=true",
	}, "\n")

	res := &memResolver{
		metadata: meta,
		files:    map[string]string{},
	}

	s, err := Read(strings.NewReader(root), res)
	require.NoError(t, err)
	require.Equal(t, "FromMeta", s.Name)
	require.Equal(t, -1, s.Loop)
	require.True(t, s.Exclusive)
}

func TestRead_SidecarMetadata_BothFails(t *testing.T) {
	root := strings.Join([]string{
		"# name=Inline",
		"# loop=3",
		"# exclusive=false",
		"OLA Show",
		"1 9",
		"0",
	}, "\n")

	meta := strings.Join([]string{
		"name=FromMeta",
		"loop=true",
		"exclusive=true",
	}, "\n")

	res := &memResolver{
		metadata: meta,
		files:    map[string]string{},
	}

	_, err := Read(strings.NewReader(root), res)
	require.Error(t, err)
}

func TestRead_SidecarMetadata_IncludeWorks(t *testing.T) {
	root := strings.Join([]string{
		"OLA Show",
		"1 9",
		"0",
	}, "\n")

	a := strings.Join([]string{
		"OLA Show",
		"1 1",
		"0",
	}, "\n")

	meta := "include=a.show\n"
	res := &memResolver{
		metadata: meta,
		files: map[string]string{
			"a.show": a,
		},
	}

	s, err := Read(strings.NewReader(root), res)
	require.NoError(t, err)
	require.Len(t, s.Frames, 2)
	require.Equal(t, byte(1), s.Frames[0].Data[0])
	require.Equal(t, byte(9), s.Frames[1].Data[0])
}
