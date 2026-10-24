package dmxcast

import (
	"sync"
	"sync/atomic"
)

// Live is a source a program writes frame by frame, for lights computed as
// they play rather than recorded in a show. On the channels it holds it sets
// the output, whatever the shows send; every other channel stays theirs, and
// releasing a channel hands it back to them at the next flush.
type Live struct {
	e  *Player
	id uint64

	// mu keeps a source's frames in the order they took their sequence, so
	// that an older frame never replaces a newer one when two goroutines send
	mu sync.Mutex
}

// Live returns a new live source. It holds no channel until it sends a frame.
func (e *Player) Live() *Live {
	return &Live{e: e, id: atomic.AddUint64(&e.nextID, 1)}
}

// Send sets the channels hold marks to the values in dmx, and releases every
// other channel the source held. The next flush outputs it.
func (l *Live) Send(dmx [512]byte, hold [512]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := atomic.AddUint64(&l.e.mergeSeq, 1)
	l.e.u.ApplyLive(l.id, seq, dmx, hold)
}

// Release hands every channel back to the shows.
func (l *Live) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.e.u.RemoveLive(l.id)
}
