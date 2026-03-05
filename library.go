package dmxcast

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bstkhq/go-dmxcast/olashow"
)

var showFileRe = regexp.MustCompile(`^(\d+)\.(.+)\.show$`)

// ShowInfo describes a show discovered on disk plus its runtime state.
type ShowInfo struct {
	olashow.OlaShow
	// ID is the numeric identifier extracted from the filename.
	ID int
	// FileName is the show filename (e.g. "001.alpha.show").
	FileName string
	// Playing is the current runtime information, if the show is running.
	Playing *PlayInfo

	path string
}

// PlayInfo describes a running show instance.
type PlayInfo struct {
	PlayingShow
	// HandleID is the player handle ID for this run.
	HandleID int
	// ShowID is the numeric show ID.
	ShowID int
}

// IDFromFilename extracts a show ID and a default name from a filename.
//
// It must return ok=false for filenames that are not shows.
type IDFromFilename func(filename string) (id int, nameFromFile string, ok bool)

// LibraryEventType identifies the kind of library event.
type LibraryEventType int

const (
	// LibraryStateChanged is emitted when the running set changes.
	LibraryStateChanged LibraryEventType = iota
)

// LibraryEventReason explains why the state changed.
type LibraryEventReason string

const (
	// PlayLibraryEvent a show started
	PlayLibraryEvent LibraryEventReason = "play"
	// RestartLibraryEvent a show was restarted
	RestartLibraryEvent LibraryEventReason = "restart"
	// StopLibraryEvent a show was stopped
	StopLibraryEvent LibraryEventReason = "stop"
	// StopAllLibraryEvent all shows were stopped
	StopAllLibraryEvent LibraryEventReason = "stopall"
	// FinishedLibraryEvent a show ended naturally
	FinishedLibraryEvent LibraryEventReason = "finished"
)

// LibraryEvent is emitted when the library state changes.
type LibraryEvent struct {
	// Type is the event category (currently only LibraryStateChanged).
	Type LibraryEventType
	// At is the time the event was created.
	At time.Time
	// Reason explains why the state changed.
	Reason LibraryEventReason
	// Show is the show related to this event when applicable.
	// It is nil for events that don't target a single show (e.g. stopall).
	Show *ShowInfo
	// Running is a snapshot of all currently running shows after the change.
	Running []PlayInfo
}

// LibraryConfig configures a Library instance.
type LibraryConfig struct {
	// Path is the folder containing show files.
	Path string

	// IDFromFilename extracts (id, nameFromFile) from the filename.
	// If nil, it defaults to parsing "<id>.<name>.show".
	IDFromFilename IDFromFilename

	// OnEvent is an optional callback invoked when the library state changes.
	// It is intended to be set once during initialization.
	OnEvent func(LibraryEvent)
}

// Library loads OLA show files from a folder and controls playback via Player.
type Library struct {
	folder string
	player *Player
	cfg    *LibraryConfig

	mu           sync.Mutex
	showsByID    map[int]ShowInfo
	showsByName  map[string]int
	orderedIDs   []int
	running      map[int]ShowHandle
	handleToShow map[uint64]int
}

// NewLibrary loads shows from cfg.Path and wires the library to player.
func NewLibrary(player *Player, cfg *LibraryConfig) (*Library, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("missing LibraryConfig.Path")
	}

	if cfg.IDFromFilename == nil {
		cfg.IDFromFilename = defaultIDFromFilename
	}

	l := &Library{
		folder:       cfg.Path,
		player:       player,
		cfg:          cfg,
		showsByID:    make(map[int]ShowInfo),
		showsByName:  make(map[string]int),
		running:      make(map[int]ShowHandle),
		handleToShow: make(map[uint64]int),
	}

	if err := l.loadAll(); err != nil {
		return nil, err
	}

	player.OnShowExited(func(h ShowHandle) {
		l.onPlayerExited(h)
	})

	return l, nil
}

func defaultIDFromFilename(filename string) (id int, nameFromFile string, ok bool) {
	m := showFileRe.FindStringSubmatch(filename)
	if m == nil {
		return 0, "", false
	}

	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", false
	}

	return n, m[2], true
}

func (l *Library) emitState(r LibraryEventReason, showID *int) {
	if l.cfg.OnEvent == nil {
		return
	}

	ev := LibraryEvent{
		Type:   LibraryStateChanged,
		At:     time.Now(),
		Reason: r,
	}

	if showID != nil {
		if si, ok := l.getShowInfoNoPlaying(*showID); ok {
			tmp := si
			ev.Show = &tmp
		}
	}

	ev.Running = l.snapshotRunning()
	l.cfg.OnEvent(ev)
}

func (l *Library) getShowInfoNoPlaying(id int) (ShowInfo, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	si, ok := l.showsByID[id]
	return si, ok
}

func (l *Library) snapshotRunning() []PlayInfo {
	playingShows := l.player.ListPlaying()
	byHandle := make(map[uint64]PlayingShow, len(playingShows))
	for _, ps := range playingShows {
		byHandle[ps.ID] = ps
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]PlayInfo, 0, len(l.running))
	for showID, h := range l.running {
		ps, ok := byHandle[h.ID()]
		if !ok {
			continue
		}
		out = append(out, PlayInfo{
			HandleID:    int(h.ID()),
			ShowID:      showID,
			PlayingShow: ps,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ShowID < out[j].ShowID })
	return out
}

func (l *Library) onPlayerExited(h ShowHandle) {
	l.mu.Lock()
	showID, ok := l.handleToShow[h.ID()]
	if ok {
		delete(l.handleToShow, h.ID())
		cur, ok2 := l.running[showID]
		if ok2 && cur.ID() == h.ID() {
			delete(l.running, showID)
		}
	}
	l.mu.Unlock()

	if ok {
		l.emitState("finished", &showID)
	}
}

// loadAll scans the folder and loads all shows into memory.
func (l *Library) loadAll() error {
	dirEntries, err := os.ReadDir(l.folder)
	if err != nil {
		return err
	}

	type rec struct {
		id       int
		fileName string
		path     string
		nameFrom string
		show     *olashow.OlaShow
	}

	var recs []rec

	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}

		fn := de.Name()
		id, nameFromFile, ok := l.cfg.IDFromFilename(fn)
		if !ok {
			continue
		}

		path := filepath.Join(l.folder, fn)

		show, err := olashow.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", fn, err)
		}

		if show.Name == "" {
			show.Name = nameFromFile
		}

		recs = append(recs, rec{
			id:       id,
			fileName: fn,
			path:     path,
			nameFrom: nameFromFile,
			show:     show,
		})
	}

	sort.Slice(recs, func(i, j int) bool {
		if recs[i].id == recs[j].id {
			return recs[i].fileName < recs[j].fileName
		}
		return recs[i].id < recs[j].id
	})

	l.mu.Lock()
	defer l.mu.Unlock()

	l.showsByID = make(map[int]ShowInfo, len(recs))
	l.showsByName = make(map[string]int, len(recs)*2)
	l.orderedIDs = l.orderedIDs[:0]

	for _, r := range recs {
		si := ShowInfo{
			ID:       r.id,
			FileName: r.fileName,
			OlaShow:  *r.show,
			path:     r.path,
		}

		l.showsByID[r.id] = si
		l.orderedIDs = append(l.orderedIDs, r.id)

		l.showsByName[strings.ToLower(r.nameFrom)] = r.id
		if si.OlaShow.Name != "" {
			l.showsByName[strings.ToLower(si.OlaShow.Name)] = r.id
		}
	}

	return nil
}

// List returns all known shows ordered by ID.
func (l *Library) List() []ShowInfo {
	playing := l.RunningShows()

	l.mu.Lock()
	ids := make([]int, len(l.orderedIDs))
	copy(ids, l.orderedIDs)
	showsByID := l.showsByID
	l.mu.Unlock()

	out := make([]ShowInfo, 0, len(ids))
	for _, id := range ids {
		si := showsByID[id]
		si.Playing = playing[si.ID]
		out = append(out, si)
	}
	return out
}

// Get returns show info for a id.
func (l *Library) Get(id int) (*ShowInfo, bool) {
	playing := l.RunningShows()

	l.mu.Lock()
	si, ok := l.showsByID[id]
	l.mu.Unlock()
	if !ok {
		return nil, false
	}

	si.Playing = playing[si.ID]
	return &si, true
}

// Get returns show info for a name.
func (l *Library) GetByName(name string) (*ShowInfo, bool) {
	if id, ok := l.showsByName[name]; ok {
		return l.Get(id)
	}

	return nil, false
}

// Play starts playing a show by ID.
//
// If the show is already running, Play stops the current instance and starts
// it again.
func (l *Library) Play(id int) (ShowHandle, error) {
	l.mu.Lock()
	si, ok := l.showsByID[id]
	if !ok {
		l.mu.Unlock()
		return ShowHandle{}, fmt.Errorf("show not found: %d", id)
	}

	prev, hadPrev := l.running[id]
	if hadPrev {
		delete(l.running, id)
		delete(l.handleToShow, prev.ID())
	}
	l.mu.Unlock()

	if hadPrev {
		l.player.Stop(prev)
		l.emitState("restart", &id)
	}

	showCopy := si.OlaShow
	h := l.player.Play(context.Background(), &showCopy)

	l.mu.Lock()
	l.running[id] = h
	l.handleToShow[h.ID()] = id
	l.mu.Unlock()

	l.emitState("play", &id)
	return h, nil
}

// Stop stops a running show by ID.
//
// It returns true if the show was running at the time Stop was requested.
func (l *Library) Stop(id int) (wasPlaying bool) {
	l.mu.Lock()
	h, ok := l.running[id]
	if ok {
		delete(l.running, id)
		delete(l.handleToShow, h.ID())
	}
	l.mu.Unlock()

	if !ok {
		return false
	}

	wasPlaying = l.player.IsPlaying(h)
	l.player.Stop(h)

	l.emitState("stop", &id)
	return wasPlaying
}

// StopAll stops all currently running shows and clears library runtime state.
func (l *Library) StopAll() {
	l.mu.Lock()
	l.running = make(map[int]ShowHandle)
	l.handleToShow = make(map[uint64]int)
	l.mu.Unlock()

	l.player.StopAll()
	l.emitState("stopall", nil)
}

// IsShowPlaying reports whether the given show ID is currently playing.
func (l *Library) IsShowPlaying(id int) bool {
	l.mu.Lock()
	h, ok := l.running[id]
	l.mu.Unlock()

	if !ok {
		return false
	}

	return l.player.IsPlaying(h)
}

// RunningShows currently running shows, returns a map show ID -> PlayInfo.
func (l *Library) RunningShows() map[int]*PlayInfo {
	playingShows := l.player.ListPlaying()
	byHandle := make(map[uint64]PlayingShow, len(playingShows))
	for _, ps := range playingShows {
		byHandle[ps.ID] = ps
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	out := make(map[int]*PlayInfo)
	for showID, h := range l.running {
		ps, ok := byHandle[h.ID()]
		if !ok {
			continue
		}
		out[showID] = &PlayInfo{
			HandleID:    int(h.ID()),
			ShowID:      showID,
			PlayingShow: ps,
		}
	}
	return out
}
