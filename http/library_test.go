package http

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bstkhq/go-dmxcast"
	"github.com/stretchr/testify/require"
)

type mockTransport struct{}

func (m *mockTransport) Send(_ [512]byte) error { return nil }
func (m *mockTransport) Close() error           { return nil }

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

func newTestPlayer(t *testing.T) *dmxcast.Player {
	t.Helper()
	tx := &mockTransport{}
	p := dmxcast.NewPlayer(tx, &dmxcast.PlayerConfig{
		Mode:          dmxcast.MergeHTP,
		FlushInterval: 2 * time.Millisecond,
	})
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestLibrary_LoadResolve(t *testing.T) {
	dir := t.TempDir()

	writeShowFile(t, dir, "001.alpha.show", 10, 50)
	writeShowFile(t, dir, "002.beta.show", 20, 50)

	lib, err := NewLibrary(newTestPlayer(t), dir)
	require.NoError(t, err)

	id, err := lib.resolveShowID("001")
	require.NoError(t, err)
	require.Equal(t, 1, id)

	id, err = lib.resolveShowID("alpha")
	require.NoError(t, err)
	require.Equal(t, 1, id)

	shows := lib.listShows()
	require.Len(t, shows, 2)
	require.Equal(t, 1, shows[0].ID)
	require.Equal(t, "alpha", shows[0].Name)
	require.Equal(t, 2, shows[1].ID)
	require.Equal(t, "beta", shows[1].Name)
}

func TestLibrary_PlayStopShow(t *testing.T) {
	dir := t.TempDir()
	writeShowFile(t, dir, "001.alpha.show", 10, 100)

	player := newTestPlayer(t)
	lib, err := NewLibrary(player, dir)
	require.NoError(t, err)

	id, err := lib.resolveShowID("001")
	require.NoError(t, err)

	h, err := lib.playShow(id)
	require.NoError(t, err)
	require.NotZero(t, h.ID())

	require.Eventually(t, func() bool {
		return lib.isShowPlaying(id)
	}, 200*time.Millisecond, 1*time.Millisecond)

	was := lib.stopShow(id)
	require.True(t, was)

	require.Eventually(t, func() bool {
		return !lib.isShowPlaying(id)
	}, 300*time.Millisecond, 1*time.Millisecond)
}

func TestLibrary_StopAll(t *testing.T) {
	dir := t.TempDir()
	writeShowFile(t, dir, "001.alpha.show", 10, 100)
	writeShowFile(t, dir, "002.beta.show", 20, 100)

	player := newTestPlayer(t)
	lib, err := NewLibrary(player, dir)
	require.NoError(t, err)

	_, err = lib.playShow(1)
	require.NoError(t, err)
	_, err = lib.playShow(2)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return lib.isShowPlaying(1) && lib.isShowPlaying(2)
	}, 200*time.Millisecond, 1*time.Millisecond)

	lib.stopAll()

	require.Eventually(t, func() bool {
		return !lib.isShowPlaying(1) && !lib.isShowPlaying(2)
	}, 400*time.Millisecond, 1*time.Millisecond)
}

func TestLibrary_ProgResolve_Normalizes(t *testing.T) {
	dir := t.TempDir()
	writeShowFile(t, dir, "001.alpha.show", 10, 50)

	lib, err := NewLibrary(newTestPlayer(t), dir)
	require.NoError(t, err)

	id, err := lib.resolveProgID("1")
	require.NoError(t, err)
	require.Equal(t, 1, id)

	id, err = lib.resolveProgID("001")
	require.NoError(t, err)
	require.Equal(t, 1, id)

	_, err = lib.resolveProgID("999")
	require.Error(t, err)
}
