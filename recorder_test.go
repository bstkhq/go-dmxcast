// recorder_test.go
package dmxcast

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/bstkhq/go-dmxcast/olashow"
	"github.com/stretchr/testify/require"
)

type testFrame struct {
	now      time.Time
	universe uint16
	dmx      [512]byte
}

type fakeListener struct {
	frames []testFrame
}

func (l *fakeListener) Run(ctx context.Context, h FrameHandler) error {
	for _, fr := range l.frames {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := h(fr.now, fr.universe, fr.dmx); err != nil {
			return err
		}
	}
	return nil
}

func (l *fakeListener) Close() error { return nil }

func TestRecorder_WritesHeaderAndFramesWithDelay(t *testing.T) {
	t0 := time.Unix(100, 0)
	t1 := t0.Add(1234*time.Millisecond + 900*time.Microsecond) // truncated to 1234ms

	var dmx1, dmx2 [512]byte
	dmx1[0], dmx1[1] = 5, 6
	dmx2[0], dmx2[1] = 7, 8

	u := uint16(0x0102)

	l := &fakeListener{
		frames: []testFrame{
			{now: t0, universe: u, dmx: dmx1},
			{now: t1, universe: u, dmx: dmx2},
		},
	}

	rec := NewRecorder(l)

	var out bytes.Buffer
	err := rec.Record(context.Background(), &out)
	require.NoError(t, err)

	want := "OLA Show\n" +
		buildFrameLine(u, dmx1) +
		"1234\n" +
		buildFrameLine(u, dmx2)

	require.Equal(t, want, out.String())
}

func TestRecorder_SingleFrame_NoTrailingDelay(t *testing.T) {
	t0 := time.Unix(100, 0)

	var dmx [512]byte
	dmx[0] = 42

	u := uint16(7)

	l := &fakeListener{
		frames: []testFrame{
			{now: t0, universe: u, dmx: dmx},
		},
	}

	rec := NewRecorder(l)

	var out bytes.Buffer
	err := rec.Record(context.Background(), &out)
	require.NoError(t, err)

	want := "OLA Show\n" + buildFrameLine(u, dmx)
	require.Equal(t, want, out.String())
}

func TestRecorder_ContextCancelStops(t *testing.T) {
	t0 := time.Unix(100, 0)
	u := uint16(1)

	var dmx [512]byte
	dmx[0] = 1

	frames := make([]testFrame, 0, 100)
	for i := 0; i < 100; i++ {
		frames = append(frames, testFrame{
			now:      t0.Add(time.Duration(i) * time.Millisecond),
			universe: u,
			dmx:      dmx,
		})
	}

	l := &fakeListener{frames: frames}
	rec := NewRecorder(l)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	err := rec.Record(ctx, &out)
	require.NoError(t, err)

	require.Equal(t, "OLA Show\n", out.String())
}

func TestRecorder_ArtNet_RecordVsOriginal(t *testing.T) {
	flush := 200 * time.Millisecond
	want := []byte{10, 20, 30}

	l, err := NewArtNetListener(ArtNetListenerConfig{
		BindIP: net.ParseIP("127.0.0.1"),
	})

	if err != nil {
		t.Skipf("cannot bind Art-Net UDP/6454 on 127.0.0.1: %v", err)
		return
	}

	defer l.Close()

	rec := NewRecorder(l)
	var recorded bytes.Buffer

	recCtx, recCancel := context.WithCancel(context.Background())
	recDone := make(chan error, 1)
	go func() {
		recDone <- rec.Record(recCtx, &recorded)
	}()

	time.Sleep(30 * time.Millisecond)

	tx, err := NewArtNetTransport(&ArtNetConfig{
		DstIP:  net.ParseIP("127.0.0.1"),
		SrcIP:  nil,
		Net:    0,
		SubUni: 1,
	})
	require.NoError(t, err)
	defer tx.Close()

	player := NewPlayer(tx, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: flush,
	})
	defer player.Close()

	original := makeFixtureShow(flush)

	time.Sleep(20 * time.Millisecond)

	h := player.Play(context.Background(), original)

	require.Eventually(t, func() bool {
		return !player.IsPlaying(h)
	}, 6*time.Second, 10*time.Millisecond)

	time.Sleep(50 * time.Millisecond)

	recCancel()
	_ = l.Close()

	select {
	case err := <-recDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timeout waiting for recorder to stop")
	}

	gotShow, err := olashow.Read(bytes.NewReader(recorded.Bytes()), nil)
	require.NoError(t, err)
	require.NotEmpty(t, gotShow.Frames)

	gotVals := make([]byte, 0, len(gotShow.Frames))
	gotDelays := make([]time.Duration, 0, len(gotShow.Frames))
	for _, fr := range gotShow.Frames {
		gotVals = append(gotVals, fr.Data[0])
		gotDelays = append(gotDelays, fr.Delay)
	}

	start := -1
	for i := 0; i+len(want) <= len(gotVals); i++ {
		ok := true
		for j := 0; j < len(want); j++ {
			if gotVals[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			start = i
			break
		}
	}

	require.NotEqual(t, -1, start, "expected sequence %v not found in recorded values %v", want, gotVals)

	require.Equal(t, want, gotVals[start:start+len(want)])

	const tol = 120 * time.Millisecond
	require.InDelta(t, float64(flush), float64(gotDelays[start+0]), float64(tol))
	require.InDelta(t, float64(flush), float64(gotDelays[start+1]), float64(tol))
}

func makeFixtureShow(step time.Duration) *olashow.OlaShow {
	var f1, f2, f3 olashow.Frame
	f1.Universe, f2.Universe, f3.Universe = 1, 1, 1
	f1.Length, f2.Length, f3.Length = 512, 512, 512

	f1.Data[0] = 10
	f2.Data[0] = 20
	f3.Data[0] = 30

	f1.Delay = step
	f2.Delay = step
	f3.Delay = step

	return &olashow.OlaShow{
		Name:      "fixture",
		Loop:      0,
		Exclusive: false,
		Frames:    []olashow.Frame{f1, f2, f3},
	}
}
