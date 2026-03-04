package dmxcast

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bstkhq/go-dmxcast/olashow"
)

// MergeMode defines how multiple shows are combined into a single DMX output.
type MergeMode int

const (
	// MergeHTP (Highest Takes Precedence) merges per channel using the maximum value.
	MergeHTP MergeMode = iota
	// MergeLTP (Latest Takes Precedence) merges per channel using the most recently
	// updated value.
	MergeLTP
)

// PlayerConfig configures a Player/Engine instance.
//
// Mode selects how multiple concurrent shows are merged (HTP or LTP).
// FlushInterval controls the output refresh period.
type PlayerConfig struct {
	// Mode selects the merge method used when multiple shows are playing.
	Mode MergeMode
	// FlushInterval is the period between output frames.
	// If zero, it defaults to 44 Hz (time.Second/44).
	FlushInterval time.Duration
}

// Player mixes multiple shows and outputs merged DMX through a Transport.
type Player struct {
	tx Transport
	u  *Universe

	mu      sync.Mutex
	players map[uint64]*showPlayer
	closed  bool
	wg      sync.WaitGroup

	nextID uint64

	flushInterval time.Duration
	mergeSeq      uint64
}

const defaultFlushInterval = time.Second / 44

// NewPlayer creates a Player that merges concurrent shows and sends the result
// through tx.
func NewPlayer(tx Transport, cfg *PlayerConfig) *Player {
	flush := cfg.FlushInterval
	if flush <= 0 {
		flush = defaultFlushInterval
	}

	e := &Player{
		tx:            tx,
		u:             NewUniverse(cfg.Mode),
		players:       make(map[uint64]*showPlayer),
		flushInterval: flush,
	}

	e.wg.Go(e.outputLoop)
	return e
}

// Close stops all running shows, waits for internal goroutines to exit, and
// closes the underlying Transport.
func (e *Player) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}

	e.closed = true

	var toStop []*showPlayer
	for _, p := range e.players {
		toStop = append(toStop, p)
	}

	e.mu.Unlock()

	for _, p := range toStop {
		p.stop()
	}

	e.wg.Wait()
	return e.tx.Close()
}

type ShowHandle struct{ id uint64 }

// ID returns the unique identifier of the handle.
func (h ShowHandle) ID() uint64 { return h.id }

// PlayingShow describes a currently running show.
type PlayingShow struct {
	ID        uint64
	Show      *olashow.OlaShow
	StartedAt time.Time
}

// Play starts playing show until it finishes, ctx is cancelled, or Stop is called.
//
// The OLA frame Universe field is ignored. All frames contribute to the single
// merged output routed by the Transport.
//
// If show.Exclusive is true, the player stops all other running shows before
// starting playback of this one.
func (e *Player) Play(ctx context.Context, show *olashow.OlaShow) ShowHandle {
	id := atomic.AddUint64(&e.nextID, 1)

	p := &showPlayer{
		id:        id,
		e:         e,
		show:      show,
		ctx:       ctx,
		startedAt: time.Now(),
		stopCh:    make(chan struct{}),
	}

	p.running.Store(true)

	e.mu.Lock()
	e.players[id] = p
	e.mu.Unlock()

	e.wg.Go(func() {
		defer p.running.Store(false)
		p.run()
		e.onShowExit(p)
	})

	return ShowHandle{id: id}
}

// Stop requests the show identified by h to stop.
//
// Stop is asynchronous: it signals the show goroutine, which will exit shortly
// after (immediately for zero-delay frames, or after the current frame delay).
// Calling Stop for an unknown handle is a no-op.
func (e *Player) Stop(h ShowHandle) {
	e.mu.Lock()
	p := e.players[h.id]
	e.mu.Unlock()

	if p != nil {
		p.stop()
	}
}

// StopAll requests all currently running shows to stop.
//
// StopAll is asynchronous; use IsPlaying or ListPlaying to observe completion.
func (e *Player) StopAll() {
	e.doStopAll(nil)
}

func (e *Player) doStopAll(except map[uint64]bool) {
	e.mu.Lock()
	ids := make([]uint64, 0, len(e.players))
	for id := range e.players {
		if !except[id] {
			ids = append(ids, id)
		}
	}
	e.mu.Unlock()

	for _, id := range ids {
		e.Stop(ShowHandle{id: id})
	}
}

// IsPlaying reports whether the show identified by h is currently running.
func (e *Player) IsPlaying(h ShowHandle) bool {
	e.mu.Lock()
	p := e.players[h.id]
	e.mu.Unlock()

	return p != nil && p.running.Load()
}

// ListPlaying returns a snapshot of currently running shows.
//
// The returned slice is a point-in-time view; shows may start/stop concurrently.
func (e *Player) ListPlaying() []PlayingShow {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]PlayingShow, 0, len(e.players))
	for _, p := range e.players {
		out = append(out, PlayingShow{
			ID:        p.id,
			Show:      p.show,
			StartedAt: p.startedAt,
		})
	}

	return out
}

func (e *Player) onShowExit(p *showPlayer) {
	e.mu.Lock()
	delete(e.players, p.id)
	e.mu.Unlock()

	e.u.Remove(p.id)
}

func (e *Player) outputLoop() {
	tick := time.NewTicker(e.flushInterval)
	defer tick.Stop()

	for {
		e.mu.Lock()
		closed := e.closed
		e.mu.Unlock()

		if closed {
			return
		}

		dmx := e.u.Merge()
		_ = e.tx.Send(dmx)

		<-tick.C
	}
}

type showPlayer struct {
	id   uint64
	e    *Player
	show *olashow.OlaShow

	ctx context.Context

	stopOnce sync.Once
	stopCh   chan struct{}
	running  atomic.Bool

	startedAt time.Time
}

func (p *showPlayer) stop() {
	p.stopOnce.Do(func() { close(p.stopCh) })
}

func (p *showPlayer) run() {
	// Note: OLA show frames include a Universe field, but this engine ignores it.
	// All frames are mixed into the Engine's single output and routed by the Transport.

	if p.show.Exclusive {
		p.e.doStopAll(map[uint64]bool{p.id: true})
	}

	loopsLeft := p.show.Loop
	for {
		for i := 0; i < len(p.show.Frames); i++ {
			fr := p.show.Frames[i]

			dmx := fr.Data
			if fr.Length >= 0 && fr.Length < 512 {
				for j := fr.Length; j < 512; j++ {
					dmx[j] = 0
				}
			}

			seq := atomic.AddUint64(&p.e.mergeSeq, 1)
			p.e.u.Apply(p.id, seq, dmx)

			if fr.Delay <= 0 {
				select {
				case <-p.ctx.Done():
					return
				case <-p.stopCh:
					return
				default:
				}
				continue
			}

			t := time.NewTimer(fr.Delay)
			select {
			case <-p.ctx.Done():
				t.Stop()
				return
			case <-p.stopCh:
				t.Stop()
				return
			case <-t.C:
			}
		}

		if loopsLeft == -1 {
			continue
		}
		if loopsLeft == 0 {
			return
		}

		loopsLeft--
		if loopsLeft == 0 {
			return
		}
	}
}
