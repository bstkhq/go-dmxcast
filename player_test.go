// engine_test.go
package dmxcast

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bstkhq/go-dmxcast/olashow"
	"github.com/stretchr/testify/require"
)

type sendEvent struct {
	at  time.Time
	dmx [512]byte
}

type mockTransport struct {
	mu    sync.Mutex
	sends []sendEvent
}

func (m *mockTransport) Send(dmx [512]byte) error {
	m.mu.Lock()
	m.sends = append(m.sends, sendEvent{
		at:  time.Now(),
		dmx: dmx,
	})
	m.mu.Unlock()
	return nil
}

func (m *mockTransport) Close() error { return nil }

func (m *mockTransport) last() (sendEvent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sends) == 0 {
		return sendEvent{}, false
	}
	return m.sends[len(m.sends)-1], true
}

func (m *mockTransport) all() []sendEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sendEvent, len(m.sends))
	copy(out, m.sends)
	return out
}

func makeShow(loop bool, frames ...olashow.Frame) *olashow.OlaShow {
	return &olashow.OlaShow{
		Loop:   loop,
		Name:   "test",
		Frames: frames,
	}
}

func frame(universe uint16, delay time.Duration, vals map[int]byte) olashow.Frame {
	var f olashow.Frame
	f.Universe = universe
	f.Delay = delay
	f.Length = 512
	for ch, v := range vals {
		f.Data[ch] = v
	}
	return f
}

func waitForAtLeastSends(t *testing.T, mt *mockTransport, n int, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		return len(mt.all()) >= n
	}, timeout, 1*time.Millisecond)
}

func TestPlayer_PlayStopAndList(t *testing.T) {
	mt := &mockTransport{}
	e := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 2 * time.Millisecond,
	})
	defer e.Close()

	s := makeShow(true, frame(99, 0, map[int]byte{0: 10}))
	h := e.Play(context.Background(), s)
	defer e.Stop(h)

	require.True(t, e.IsPlaying(h))

	playing := e.ListPlaying()
	require.Len(t, playing, 1)
	require.Equal(t, h.ID(), playing[0].ID)
	require.Same(t, s, playing[0].Show)
	require.Equal(t, s.Loop, playing[0].Loop)

	e.Stop(h)

	require.Eventually(t, func() bool {
		return !e.IsPlaying(h)
	}, 300*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_IgnoresFrameUniverse_UsesEngineUniverse(t *testing.T) {
	mt := &mockTransport{}
	e := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 2 * time.Millisecond,
	})
	defer e.Close()

	s := makeShow(true, frame(999, 0, map[int]byte{0: 7}))
	h := e.Play(context.Background(), s)
	defer e.Stop(h)

	require.Eventually(t, func() bool {
		ev, ok := mt.last()
		return ok && ev.dmx[0] == 7
	}, 300*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_HTP_MaxAndFallback(t *testing.T) {
	mt := &mockTransport{}
	e := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 2 * time.Millisecond,
	})
	defer e.Close()

	// Both start at 0 to remove start-order flakiness.
	showB := makeShow(true,
		frame(1, 0, map[int]byte{0: 0}),
		frame(1, 10*time.Millisecond, map[int]byte{0: 5}),
		frame(1, 50*time.Millisecond, map[int]byte{0: 5}),
	)
	hB := e.Play(context.Background(), showB)
	defer e.Stop(hB)

	showA := makeShow(true,
		frame(1, 0, map[int]byte{0: 0}),
		frame(1, 15*time.Millisecond, map[int]byte{0: 10}),
		frame(1, 20*time.Millisecond, map[int]byte{0: 0}),
		frame(1, 50*time.Millisecond, map[int]byte{0: 0}),
	)
	hA := e.Play(context.Background(), showA)
	defer e.Stop(hA)

	require.Eventually(t, func() bool {
		ev, ok := mt.last()
		return ok && ev.dmx[0] == 10
	}, 600*time.Millisecond, 1*time.Millisecond)

	require.Eventually(t, func() bool {
		ev, ok := mt.last()
		return ok && ev.dmx[0] == 5
	}, 800*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_LTP_LastWriterWins(t *testing.T) {
	mt := &mockTransport{}
	e := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeLTP,
		FlushInterval: 2 * time.Millisecond,
	})
	defer e.Close()

	showA := makeShow(true,
		frame(1, 0, map[int]byte{0: 10}),
		frame(1, 50*time.Millisecond, map[int]byte{0: 10}),
	)
	hA := e.Play(context.Background(), showA)
	defer e.Stop(hA)

	require.Eventually(t, func() bool {
		ev, ok := mt.last()
		return ok && ev.dmx[0] == 10
	}, 300*time.Millisecond, 1*time.Millisecond)

	showB := makeShow(true,
		frame(1, 10*time.Millisecond, map[int]byte{0: 5}),
		frame(1, 50*time.Millisecond, map[int]byte{0: 5}),
	)
	hB := e.Play(context.Background(), showB)
	defer e.Stop(hB)

	require.Eventually(t, func() bool {
		ev, ok := mt.last()
		return ok && ev.dmx[0] == 5
	}, 700*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_StopRemovesContribution(t *testing.T) {
	mt := &mockTransport{}
	e := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 2 * time.Millisecond,
	})
	defer e.Close()

	showA := makeShow(true, frame(1, 10*time.Millisecond, map[int]byte{0: 200}))
	hA := e.Play(context.Background(), showA)
	defer e.Stop(hA)

	showB := makeShow(true, frame(1, 10*time.Millisecond, map[int]byte{0: 10}))
	hB := e.Play(context.Background(), showB)
	defer e.Stop(hB)

	require.Eventually(t, func() bool {
		ev, ok := mt.last()
		return ok && ev.dmx[0] == 200
	}, 500*time.Millisecond, 1*time.Millisecond)

	e.Stop(hA)

	require.Eventually(t, func() bool {
		ev, ok := mt.last()
		return ok && ev.dmx[0] == 10
	}, 700*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_HTP_ExactMergedSequence(t *testing.T) {
	mt := &mockTransport{}
	e := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer e.Close()

	// B establishes the baseline first.
	showB := makeShow(true,
		frame(1, 0, map[int]byte{0: 50}),
		frame(1, 50*time.Millisecond, map[int]byte{0: 50}),
	)
	hB := e.Play(context.Background(), showB)
	defer e.Stop(hB)

	// Wait until the baseline is observable.
	require.Eventually(t, func() bool {
		ev, ok := mt.last()
		return ok && ev.dmx[0] == 50
	}, 300*time.Millisecond, 1*time.Millisecond)

	// Start A only after baseline is observable.
	showA := makeShow(true,
		frame(1, 0, map[int]byte{0: 0}),
		frame(1, 8*time.Millisecond, map[int]byte{0: 100}),
		frame(1, 5*time.Millisecond, map[int]byte{0: 0}),
		frame(1, 5*time.Millisecond, map[int]byte{0: 100}),
	)
	hA := e.Play(context.Background(), showA)
	defer e.Stop(hA)

	waitForAtLeastSends(t, mt, 40, 600*time.Millisecond)

	sends := mt.all()

	// Find first occurrence of 50 to anchor the sequence.
	start := 0
	for start < len(sends) && sends[start].dmx[0] != 50 {
		start++
	}
	require.Less(t, start, len(sends), "never observed baseline 50")

	var got []byte
	for _, ev := range sends[start:] {
		if len(got) == 0 || got[len(got)-1] != ev.dmx[0] {
			got = append(got, ev.dmx[0])
		}
		if len(got) >= 4 {
			break
		}
	}

	require.GreaterOrEqual(t, len(got), 4, "not enough transitions captured")
	require.Equal(t, []byte{50, 100, 50, 100}, got[:4])
}
