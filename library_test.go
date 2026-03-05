package dmxcast

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeShowFile(t *testing.T, dir, name string, dmx0 byte, delayMs int) string {
	t.Helper()

	path := filepath.Join(dir, name)
	content := "OLA Show\n" +
		"1 " + itoa(int(dmx0)) + "\n" +
		itoa(delayMs) + "\n"

	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [32]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + (n % 10))
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func newTestPlayer(t *testing.T) *Player {
	t.Helper()
	tx := &mockTransport{}
	p := NewPlayer(tx, &PlayerConfig{
		Mode:          MergeHTP,
		FlushInterval: 2 * time.Millisecond,
	})
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestLibrary_LoadResolve(t *testing.T) {
	cfg := &LibraryConfig{
		Path: t.TempDir(),
	}

	writeShowFile(t, cfg.Path, "001.alpha.show", 10, 50)
	writeShowFile(t, cfg.Path, "002.beta.show", 20, 50)

	lib, err := NewLibrary(newTestPlayer(t), cfg)
	require.NoError(t, err)

	show, ok := lib.Get(1)
	require.True(t, ok)
	require.Equal(t, "alpha", show.Name)

	show, ok = lib.GetByName("alpha")
	require.True(t, ok)
	require.Equal(t, 1, show.ID)

	shows := lib.List()
	require.Len(t, shows, 2)
	require.Equal(t, 1, shows[0].ID)
	require.Equal(t, "alpha", shows[0].Name)
	require.Equal(t, 2, shows[1].ID)
	require.Equal(t, "beta", shows[1].Name)
}

func TestLibrary_PlayStopShow(t *testing.T) {
	cfg := &LibraryConfig{
		Path: t.TempDir(),
	}
	writeShowFile(t, cfg.Path, "001.alpha.show", 10, 100)

	player := newTestPlayer(t)
	lib, err := NewLibrary(player, cfg)
	require.NoError(t, err)

	h, err := lib.Play(1)
	require.NoError(t, err)
	require.NotZero(t, h.ID())

	require.Eventually(t, func() bool {
		return lib.IsShowPlaying(1)
	}, 200*time.Millisecond, 1*time.Millisecond)

	was := lib.Stop(1)
	require.True(t, was)

	require.Eventually(t, func() bool {
		return !lib.IsShowPlaying(1)
	}, 300*time.Millisecond, 1*time.Millisecond)
}

func TestLibrary_StopAll(t *testing.T) {
	cfg := &LibraryConfig{
		Path: t.TempDir(),
	}
	writeShowFile(t, cfg.Path, "001.alpha.show", 10, 100)
	writeShowFile(t, cfg.Path, "002.beta.show", 20, 100)

	player := newTestPlayer(t)
	lib, err := NewLibrary(player, cfg)
	require.NoError(t, err)

	_, err = lib.Play(1)
	require.NoError(t, err)
	_, err = lib.Play(2)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return lib.IsShowPlaying(1) && lib.IsShowPlaying(2)
	}, 200*time.Millisecond, 1*time.Millisecond)

	lib.StopAll()

	require.Eventually(t, func() bool {
		return !lib.IsShowPlaying(1) && !lib.IsShowPlaying(2)
	}, 400*time.Millisecond, 1*time.Millisecond)
}

func TestLibrary_Events_OnEvent_PlayStopFinishedRestartStopAll(t *testing.T) {
	cfg := &LibraryConfig{
		Path: t.TempDir(),
	}

	// show 1: short, will finish by itself (Loop=0) -> should emit "finished"
	// show 2: longer/infinite, used for restart + stopall
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Path, "001.alpha.show"), []byte(
		"OLA Show\n"+
			"1 10\n"+
			"10\n"+
			"1 20\n"+
			"10\n",
	), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(cfg.Path, "002.beta.show"), []byte(
		"OLA Show\n"+
			"1 30\n"+
			"50\n",
	), 0o644))

	var events []LibraryEvent
	cfg.OnEvent = func(ev LibraryEvent) {
		events = append(events, ev)
	}

	player := newTestPlayer(t)
	lib, err := NewLibrary(player, cfg)
	require.NoError(t, err)

	// ---- play show 1 (should emit play, later finished) ----
	_, err = lib.Play(1)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(events) >= 1 && events[0].Reason == PlayLibraryEvent
	}, 300*time.Millisecond, 1*time.Millisecond)

	require.Eventually(t, func() bool {
		for _, ev := range events {
			if ev.Reason == FinishedLibraryEvent {
				return true
			}
		}
		return false
	}, 700*time.Millisecond, 1*time.Millisecond)

	// ---- play show 2 (play) ----
	_, err = lib.Play(2)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, ev := range events {
			if ev.Reason == PlayLibraryEvent {
				// ensure show 2 is reflected in a snapshot at least once
				for _, p := range ev.Running {
					if p.ShowID == 2 {
						return true
					}
				}
			}
		}
		return false
	}, 500*time.Millisecond, 1*time.Millisecond)

	// ---- restart show 2 (playShow again) -> should emit "restart" and then "play" ----
	_, err = lib.Play(2)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		hasRestart := false
		hasPlayAfter := false
		for _, ev := range events {
			if ev.Reason == RestartLibraryEvent {
				hasRestart = true
			}
			if hasRestart && ev.Reason == PlayLibraryEvent {
				// play after restart
				hasPlayAfter = true
			}
		}
		return hasRestart && hasPlayAfter
	}, 700*time.Millisecond, 1*time.Millisecond)

	// ---- stop show 2 -> should emit "stop" ----
	was := lib.Stop(2)
	require.True(t, was)

	require.Eventually(t, func() bool {
		for _, ev := range events {
			if ev.Reason == StopAllLibraryEvent {
				return true
			}
		}
		return false
	}, 700*time.Millisecond, 1*time.Millisecond)

	// ---- play both then stopall -> should emit "stopall" ----
	_, err = lib.Play(1)
	require.NoError(t, err)
	_, err = lib.Play(2)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return lib.IsShowPlaying(1) && lib.IsShowPlaying(2)
	}, 700*time.Millisecond, 1*time.Millisecond)

	lib.StopAll()

	require.Eventually(t, func() bool {
		for _, ev := range events {
			if ev.Reason == StopAllLibraryEvent {
				// snapshot should be empty or not contain 1/2 after stopall
				has := false
				for _, p := range ev.Running {
					if p.ShowID == 1 || p.ShowID == 2 {
						has = true
						break
					}
				}
				return !has
			}
		}
		return false
	}, 700*time.Millisecond, 1*time.Millisecond)
}
