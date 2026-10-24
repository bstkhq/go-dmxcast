package dmxcast

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUniverse_LiveHoldsItsChannelsOverTheShows(t *testing.T) {
	u := NewUniverse(MergeHTP)
	show := dmx0(200)
	show[1] = 50
	u.Apply(1, 1, show)

	var dmx [512]byte
	var hold [512]bool
	dmx[0], hold[0] = 10, true // lower than the show, still wins
	u.ApplyLive(9, 2, dmx, hold)

	out := u.Merge()
	require.Equal(t, byte(10), out[0], "a held channel is the live value")
	require.Equal(t, byte(50), out[1], "a channel the live source doesn't hold stays the show's")

	u.RemoveLive(9)
	require.Equal(t, byte(200), u.Merge()[0], "releasing hands the channel back to the show")
}

func TestUniverse_LatestLiveWinsASharedChannel(t *testing.T) {
	u := NewUniverse(MergeHTP)
	var a, b [512]byte
	var hold [512]bool
	hold[3] = true
	a[3], b[3] = 1, 2
	u.ApplyLive(1, 5, a, hold)
	u.ApplyLive(2, 6, b, hold)
	require.Equal(t, byte(2), u.Merge()[3])
	u.ApplyLive(1, 7, a, hold)
	require.Equal(t, byte(1), u.Merge()[3])
}

func TestLive_ConcurrentSendsKeepTheLatestFrame(t *testing.T) {
	p := NewPlayer(&mockTransport{}, &PlayerConfig{Mode: MergeHTP, FlushInterval: time.Millisecond})
	defer p.Close()
	l := p.Live()
	seq := func() uint64 { return atomic.LoadUint64(&p.mergeSeq) }

	var dmx [512]byte
	var hold [512]bool
	hold[0] = true

	// a first frame takes its sequence and waits for the universe
	p.u.mu.Lock()
	locked := true
	defer func() {
		if locked {
			p.u.mu.Unlock()
		}
	}()
	var wg sync.WaitGroup
	wg.Go(func() { l.Send(dmx, hold) })
	require.Eventually(t, func() bool { return seq() == 1 }, time.Second, time.Millisecond)

	// a second one, sent meanwhile, waits for it before taking its own
	wg.Go(func() { l.Send(dmx, hold) })
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, uint64(1), seq(), "a frame takes no sequence while an older one is on its way")

	p.u.mu.Unlock()
	locked = false
	wg.Wait()
	p.u.mu.Lock()
	stored := p.u.live[l.id].seq
	p.u.mu.Unlock()
	require.Equal(t, uint64(2), stored, "the frame stored is the latest")
}
