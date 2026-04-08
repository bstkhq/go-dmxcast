package dmxcast

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bstkhq/go-dmxcast/olashow"
	"github.com/stretchr/testify/require"
)

type mockTransport struct {
	mu   sync.Mutex
	allv [][512]byte
}

func (m *mockTransport) Send(dmx [512]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allv = append(m.allv, dmx)
	return nil
}

func (m *mockTransport) Close() error { return nil }

func (m *mockTransport) all() [][512]byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([][512]byte, len(m.allv))
	copy(out, m.allv)
	return out
}

func frame(universe uint16, delay time.Duration, vals map[int]byte) olashow.Frame {
	var f olashow.Frame
	f.Universe = universe
	f.Delay = delay

	max := 0
	for ch, v := range vals {
		if ch >= 0 && ch < 512 {
			f.Data[ch] = v
			if ch+1 > max {
				max = ch + 1
			}
		}
	}
	f.Length = max

	return f
}

func makeShow(loop int, frames ...olashow.Frame) *olashow.OlaShow {
	return &olashow.OlaShow{
		Loop:   loop,
		Name:   "test",
		Frames: frames,
	}
}

func makeShowWithDuration(duration time.Duration, frames ...olashow.Frame) *olashow.OlaShow {
	return &olashow.OlaShow{
		Loop:     -1,
		Duration: duration,
		Name:     "test",
		Frames:   frames,
	}
}

func TestPlayer_SingleShow_Output(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	show := makeShow(0,
		frame(1, 20*time.Millisecond, map[int]byte{0: 10, 1: 20}),
		frame(1, 20*time.Millisecond, map[int]byte{0: 30, 1: 40}),
	)

	h := p.Play(context.Background(), show)

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h)
	}, 500*time.Millisecond, 1*time.Millisecond)

	all := mt.all()
	require.NotEmpty(t, all)

	var sawFirst, sawSecond bool
	for _, dmx := range all {
		if dmx[0] == 10 && dmx[1] == 20 {
			sawFirst = true
		}
		if dmx[0] == 30 && dmx[1] == 40 {
			sawSecond = true
		}
	}

	require.True(t, sawFirst)
	require.True(t, sawSecond)
}

func TestPlayer_Loop(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	show := makeShow(50, frame(1, 1*time.Millisecond, map[int]byte{0: 50}))

	done := make(chan uint64, 1)
	p.OnShowExited(func(h ShowHandle) { done <- h.ID() })

	h := p.Play(context.Background(), show)

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h)
	}, 2*time.Second, 2*time.Millisecond)

	select {
	case got := <-done:
		require.Equal(t, h.ID(), got)
		require.GreaterOrEqual(t, len(mt.all()), 50)
	case <-time.After(500 * time.Millisecond):
		require.FailNow(t, "timeout waiting for OnShowExited")
	}
}

func TestPlayer_DurationCompletesNaturally(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	show := makeShowWithDuration(60*time.Millisecond,
		frame(1, 10*time.Millisecond, map[int]byte{0: 50}),
	)

	done := make(chan uint64, 1)
	p.OnShowExited(func(h ShowHandle) { done <- h.ID() })

	h := p.Play(context.Background(), show)

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h)
	}, 500*time.Millisecond, 1*time.Millisecond)

	select {
	case got := <-done:
		require.Equal(t, h.ID(), got)
	case <-time.After(500 * time.Millisecond):
		require.FailNow(t, "timeout waiting for OnShowExited")
	}
}

func TestPlayer_DurationOverridesLoop(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	show := &olashow.OlaShow{
		Loop:     -1,
		Duration: 50 * time.Millisecond,
		Name:     "test",
		Frames: []olashow.Frame{
			frame(1, 10*time.Millisecond, map[int]byte{0: 50}),
		},
	}

	h := p.Play(context.Background(), show)

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h)
	}, 500*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_DurationZeroFallsBackToLoop(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	show := &olashow.OlaShow{
		Loop:     3,
		Duration: 0,
		Name:     "test",
		Frames: []olashow.Frame{
			frame(1, 10*time.Millisecond, map[int]byte{0: 50}),
		},
	}

	h := p.Play(context.Background(), show)

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h)
	}, 500*time.Millisecond, 1*time.Millisecond)

	require.GreaterOrEqual(t, len(mt.all()), 3)
}

func TestPlayer_Stop(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	show := makeShow(-1, frame(1, 50*time.Millisecond, map[int]byte{0: 99}))

	h := p.Play(context.Background(), show)

	require.Eventually(t, func() bool {
		return p.IsPlaying(h)
	}, 200*time.Millisecond, 1*time.Millisecond)

	p.Stop(h)

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h)
	}, 500*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_StopAll(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	h1 := p.Play(context.Background(), makeShow(-1, frame(1, 50*time.Millisecond, map[int]byte{0: 10})))
	h2 := p.Play(context.Background(), makeShow(-1, frame(1, 50*time.Millisecond, map[int]byte{1: 20})))

	require.Eventually(t, func() bool {
		return p.IsPlaying(h1) && p.IsPlaying(h2)
	}, 200*time.Millisecond, 1*time.Millisecond)

	p.StopAll()

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h1) && !p.IsPlaying(h2)
	}, 500*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_ExclusiveStopsOthers(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	normal := makeShow(-1, frame(1, 50*time.Millisecond, map[int]byte{0: 10}))
	h1 := p.Play(context.Background(), normal)

	require.Eventually(t, func() bool {
		return p.IsPlaying(h1)
	}, 200*time.Millisecond, 1*time.Millisecond)

	exclusive := &olashow.OlaShow{
		Name:      "exclusive",
		Exclusive: true,
		Frames: []olashow.Frame{
			frame(1, 50*time.Millisecond, map[int]byte{0: 100}),
		},
	}

	h2 := p.Play(context.Background(), exclusive)

	require.Eventually(t, func() bool {
		return p.IsPlaying(h2) && !p.IsPlaying(h1)
	}, 500*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_ListPlaying(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	h1 := p.Play(context.Background(), makeShow(-1, frame(1, 50*time.Millisecond, map[int]byte{0: 1})))
	h2 := p.Play(context.Background(), makeShow(-1, frame(1, 50*time.Millisecond, map[int]byte{1: 2})))

	require.Eventually(t, func() bool {
		ps := p.ListPlaying()
		return len(ps) >= 2
	}, 200*time.Millisecond, 1*time.Millisecond)

	ps := p.ListPlaying()
	require.Len(t, ps, 2)

	ids := map[uint64]bool{}
	for _, s := range ps {
		ids[s.ID] = true
		require.NotNil(t, s.Show)
		require.False(t, s.StartedAt.IsZero())
	}

	require.True(t, ids[h1.ID()])
	require.True(t, ids[h2.ID()])
}

func TestPlayer_ContextCancelStopsShow(t *testing.T) {
	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 1 * time.Millisecond,
	})
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	h := p.Play(ctx, makeShow(-1, frame(1, 50*time.Millisecond, map[int]byte{0: 42})))

	require.Eventually(t, func() bool {
		return p.IsPlaying(h)
	}, 200*time.Millisecond, 1*time.Millisecond)

	cancel()

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h)
	}, 500*time.Millisecond, 1*time.Millisecond)
}

func TestPlayer_SingleFrameDuration400ms_UsesImplicitDelayAndSendsExpectedPackets(t *testing.T) {
	in := `# name=smoke-soft
# duration=400ms
OLA Show
1 172,10,180,123,0,0,0,0`

	show, err := olashow.Read(strings.NewReader(in), nil)
	require.NoError(t, err)

	require.Equal(t, "smoke-soft", show.Name)
	require.Equal(t, 400*time.Millisecond, show.Duration)
	require.Len(t, show.Frames, 1)
	require.Equal(t, olashow.DefaultImplicitFrameDelay, show.Frames[0].Delay)

	mt := &mockTransport{}
	p := NewPlayer(mt, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: defaultFlushInterval,
	})
	defer p.Close()

	h := p.Play(context.Background(), show)

	require.Eventually(t, func() bool {
		return !p.IsPlaying(h)
	}, 2*time.Second, 5*time.Millisecond)

	// Wait one extra flush interval so the last useful merged frame can be sent.
	time.Sleep(defaultFlushInterval)

	all := mt.all()

	// Count only packets that contain the expected frame content.
	matching := 0
	for _, dmx := range all {
		if dmx[0] == 172 &&
			dmx[1] == 10 &&
			dmx[2] == 180 &&
			dmx[3] == 123 {
			matching++
		}
	}

	// 400ms / (1/44)s = 17.6, so we expect about 18 useful packets.
	// Allow a small tolerance because goroutine scheduling and ticker timing are not exact.
	require.InDelta(t, 18, matching, 1)
}
