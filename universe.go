package dmxcast

import "sync"

// Universe stores the latest DMX buffer per source and can merge them on demand.
type Universe struct {
	mu   sync.Mutex
	mode MergeMode

	// showID -> last known state
	sources map[uint64]*universeSource
}

type universeSource struct {
	dmx [512]byte
	seq uint64 // global monotonic sequence (used for LTP)
}

// NewUniverse creates a Universe using the given merge mode.
func NewUniverse(mode MergeMode) *Universe {
	return &Universe{
		mode:    mode,
		sources: make(map[uint64]*universeSource),
	}
}

// SetMode changes the merge mode.
func (u *Universe) SetMode(mode MergeMode) {
	u.mu.Lock()
	u.mode = mode
	u.mu.Unlock()
}

// Mode returns the current merge mode.
func (u *Universe) Mode() MergeMode {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.mode
}

// Apply stores the latest DMX for showID.
// seq must be monotonic across all shows (engine-global).
func (u *Universe) Apply(showID uint64, seq uint64, dmx [512]byte) {
	u.mu.Lock()
	s := u.sources[showID]
	if s == nil {
		s = &universeSource{}
		u.sources[showID] = s
	}
	s.dmx = dmx
	s.seq = seq
	u.mu.Unlock()
}

// Remove removes a show from the universe.
func (u *Universe) Remove(showID uint64) {
	u.mu.Lock()
	delete(u.sources, showID)
	u.mu.Unlock()
}

// Merge returns the merged DMX snapshot for the current sources.
func (u *Universe) Merge() [512]byte {
	u.mu.Lock()
	mode := u.mode

	tmp := make([]universeSource, 0, len(u.sources))
	for _, s := range u.sources {
		tmp = append(tmp, *s)
	}
	u.mu.Unlock()

	var out [512]byte

	switch mode {
	case MergeHTP:
		for _, s := range tmp {
			for i := range 512 {
				if s.dmx[i] > out[i] {
					out[i] = s.dmx[i]
				}
			}
		}

		return out
	case MergeLTP:
		var winSeq [512]uint64
		for _, s := range tmp {
			for i := range 512 {
				if s.seq >= winSeq[i] {
					winSeq[i] = s.seq
					out[i] = s.dmx[i]
				}
			}
		}

		return out
	default:
		// Fallback to HTP semantics.
		for _, s := range tmp {
			for i := range 512 {
				if s.dmx[i] > out[i] {
					out[i] = s.dmx[i]
				}
			}
		}

		return out
	}
}

// SourcesCount returns the number of sources (useful for tests/metrics).
func (u *Universe) SourcesCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.sources)
}
