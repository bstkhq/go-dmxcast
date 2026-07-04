package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bstkhq/go-dmxcast"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type mockTransport struct{}

func (m *mockTransport) Send(_ [512]byte) error { return nil }
func (m *mockTransport) Close() error           { return nil }

func writeShowFile(t *testing.T, dir, name string, dmx0 byte, delayMs int) string {
	t.Helper()

	path := filepath.Join(dir, name)
	content := "OLA Show\n" +
		"1 " + strconv.Itoa(int(dmx0)) + "\n" +
		strconv.Itoa(delayMs) + "\n"

	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
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

func TestShowController_ListGetPlayStopStopAll(t *testing.T) {
	cfg := &dmxcast.LibraryConfig{
		Path: t.TempDir(),
	}

	writeShowFile(t, cfg.Path, "001.alpha.show", 10, 100)
	writeShowFile(t, cfg.Path, "002.beta.show", 20, 100)

	player := newTestPlayer(t)
	lib, err := dmxcast.NewLibrary(player, cfg)
	require.NoError(t, err)

	e := echo.New()
	sc := NewShowController(lib)
	sc.RegisterRoutes(e.Group("/show"), nil)

	// GET /show
	req := httptest.NewRequest(http.MethodGet, "/show", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2)

	// GET /show/001
	req = httptest.NewRequest(http.MethodGet, "/show/001", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, 1., got["ID"])

	// GET /show/001/play
	req = httptest.NewRequest(http.MethodGet, "/show/001/play", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var playResp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &playResp))
	_, ok := playResp["handleId"]
	require.True(t, ok)

	require.Eventually(t, func() bool {
		return lib.IsShowPlaying(1)
	}, 200*time.Millisecond, 1*time.Millisecond)

	// GET /show/001/stop
	req = httptest.NewRequest(http.MethodGet, "/show/001/stop", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var stopResp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &stopResp))
	require.Equal(t, true, stopResp["wasPlaying"])

	require.Eventually(t, func() bool {
		return !lib.IsShowPlaying(1)
	}, 300*time.Millisecond, 1*time.Millisecond)

	// Start two shows then stop all
	req = httptest.NewRequest(http.MethodGet, "/show/001/play", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/show/002/play", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Eventually(t, func() bool {
		return lib.IsShowPlaying(1) && lib.IsShowPlaying(2)
	}, 200*time.Millisecond, 1*time.Millisecond)

	req = httptest.NewRequest(http.MethodGet, "/show/stop", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Eventually(t, func() bool {
		return !lib.IsShowPlaying(1) && !lib.IsShowPlaying(2)
	}, 400*time.Millisecond, 1*time.Millisecond)
}

func TestProgController_StatusPlayStopStopAll(t *testing.T) {
	dir := &dmxcast.LibraryConfig{
		Path: t.TempDir(),
	}

	writeShowFile(t, dir.Path, "001.alpha.show", 10, 100)
	writeShowFile(t, dir.Path, "002.beta.show", 20, 100)

	player := newTestPlayer(t)
	lib, err := dmxcast.NewLibrary(player, dir)
	require.NoError(t, err)

	e := echo.New()
	pc := NewProgController(lib)
	pc.RegisterRoutes(e.Group("/prog"), nil)

	// GET /prog/1 -> Stop
	req := httptest.NewRequest(http.MethodGet, "/prog/1", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Stop", rec.Body.String())

	// GET /prog/001/play -> "play 255"
	req = httptest.NewRequest(http.MethodGet, "/prog/001/play", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "play 255", rec.Body.String())

	require.Eventually(t, func() bool {
		return lib.IsShowPlaying(1)
	}, 200*time.Millisecond, 1*time.Millisecond)

	// GET /prog/1 -> Play
	req = httptest.NewRequest(http.MethodGet, "/prog/1", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Play", rec.Body.String())

	// GET /prog/1/stop -> stop
	req = httptest.NewRequest(http.MethodGet, "/prog/1/stop", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "stop", rec.Body.String())

	require.Eventually(t, func() bool {
		return !lib.IsShowPlaying(1)
	}, 300*time.Millisecond, 1*time.Millisecond)

	// Start both then stop all
	req = httptest.NewRequest(http.MethodGet, "/prog/1/play", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/prog/2/play", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Eventually(t, func() bool {
		return lib.IsShowPlaying(1) && lib.IsShowPlaying(2)
	}, 200*time.Millisecond, 1*time.Millisecond)

	req = httptest.NewRequest(http.MethodGet, "/prog/1/stop/all", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "stop all", rec.Body.String())

	require.Eventually(t, func() bool {
		return !lib.IsShowPlaying(1) && !lib.IsShowPlaying(2)
	}, 400*time.Millisecond, 1*time.Millisecond)
}
