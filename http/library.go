package http

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

	"github.com/bstkhq/go-dmxcast"
	"github.com/bstkhq/go-dmxcast/olashow"
)

var showFileRe = regexp.MustCompile(`^(\d+)\.(.+)\.show$`)

type ShowInfo struct {
	olashow.OlaShow
	path string

	ID       int
	FileName string
	Playing  *PlayInfo
}

type PlayInfo struct {
	dmxcast.PlayingShow

	HandleID int
	ShowID   int
}

type Library struct {
	folder string
	player *dmxcast.Player

	mu          sync.Mutex
	showsByID   map[int]ShowInfo             // id -> cached show info (Playing always nil)
	showsByName map[string]int               // lower(name) -> id
	orderedIDs  []int                        // ids sorted ascending
	showHandles map[int][]dmxcast.ShowHandle // id -> handles started via API
}

func NewLibrary(player *dmxcast.Player, path string) (*Library, error) {
	l := &Library{
		folder:      path,
		player:      player,
		showsByID:   make(map[int]ShowInfo),
		showsByName: make(map[string]int),
		showHandles: make(map[int][]dmxcast.ShowHandle),
	}

	if err := l.loadAll(); err != nil {
		return nil, err
	}

	return l, nil
}

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
		m := showFileRe.FindStringSubmatch(fn)
		if m == nil {
			continue
		}

		idRaw := m[1]
		nameFromFile := m[2]
		path := filepath.Join(l.folder, fn)

		id, err := strconv.Atoi(idRaw)
		if err != nil {
			return fmt.Errorf("invalid id in filename %q", fn)
		}

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

func (l *Library) resolveShowID(key string) (int, error) {
	needle := strings.ToLower(strings.TrimSpace(key))
	needle = strings.TrimSuffix(needle, ".show")

	// id?
	if n, err := strconv.Atoi(needle); err == nil {
		l.mu.Lock()
		_, ok := l.showsByID[n]
		l.mu.Unlock()
		if ok {
			return n, nil
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if id, ok := l.showsByName[needle]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("show not found: %q", key)
}

func (l *Library) resolveProgID(num string) (int, error) {
	num = strings.TrimSpace(num)
	if num == "" {
		return 0, fmt.Errorf("invalid program")
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid program")
	}

	l.mu.Lock()
	_, ok := l.showsByID[n]
	l.mu.Unlock()

	if !ok {
		return 0, fmt.Errorf("show not found")
	}
	return n, nil
}

func (l *Library) gcShowHandlesLocked() {
	for id, hs := range l.showHandles {
		out := hs[:0]
		for _, h := range hs {
			if l.player.IsPlaying(h) {
				out = append(out, h)
			}
		}
		if len(out) == 0 {
			delete(l.showHandles, id)
		} else {
			l.showHandles[id] = out
		}
	}
}

func (l *Library) listShows() []ShowInfo {
	playing := l.playingByShowID()

	l.mu.Lock()
	ids := make([]int, len(l.orderedIDs))
	copy(ids, l.orderedIDs)
	showsByID := l.showsByID
	l.mu.Unlock()

	out := make([]ShowInfo, 0, len(ids))
	for _, id := range ids {
		si := showsByID[id] // copy by value
		si.Playing = playing[si.ID]
		out = append(out, si)
	}
	return out
}

func (l *Library) getShow(id int) (ShowInfo, bool) {
	playing := l.playingByShowID()

	l.mu.Lock()
	si, ok := l.showsByID[id]
	l.mu.Unlock()
	if !ok {
		return ShowInfo{}, false
	}
	si.Playing = playing[si.ID]
	return si, true
}

func (l *Library) playShow(id int) (dmxcast.ShowHandle, error) {
	l.mu.Lock()
	si, ok := l.showsByID[id]
	l.mu.Unlock()
	if !ok {
		return dmxcast.ShowHandle{}, fmt.Errorf("show not found: %d", id)
	}

	showCopy := si.OlaShow
	h := l.player.Play(context.Background(), &showCopy)

	l.mu.Lock()
	l.showHandles[id] = append(l.showHandles[id], h)
	l.mu.Unlock()

	return h, nil
}

func (l *Library) stopShow(id int) (wasPlaying bool) {
	l.mu.Lock()
	l.gcShowHandlesLocked()

	hs := l.showHandles[id]
	delete(l.showHandles, id)
	l.mu.Unlock()

	for _, h := range hs {
		if l.player.IsPlaying(h) {
			wasPlaying = true
		}
		l.player.Stop(h)
	}

	return wasPlaying
}

func (l *Library) stopAll() {
	l.player.StopAll()
	l.mu.Lock()
	l.showHandles = make(map[int][]dmxcast.ShowHandle)
	l.mu.Unlock()
}

func (l *Library) isShowPlaying(id int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.gcShowHandlesLocked()
	for _, h := range l.showHandles[id] {
		if l.player.IsPlaying(h) {
			return true
		}
	}
	return false
}

func (l *Library) playingByShowID() map[int]*PlayInfo {
	playingShows := l.player.ListPlaying()
	byHandle := make(map[uint64]dmxcast.PlayingShow, len(playingShows))
	for _, ps := range playingShows {
		byHandle[ps.ID] = ps
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.gcShowHandlesLocked()

	out := make(map[int]*PlayInfo)
	for id, hs := range l.showHandles {
		for _, h := range hs {
			ps, ok := byHandle[h.ID()]
			if !ok {
				continue
			}
			out[id] = &PlayInfo{
				HandleID:    int(h.ID()),
				ShowID:      id,
				PlayingShow: ps,
			}
			break
		}
	}
	return out
}
