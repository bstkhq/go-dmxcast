package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestShowController_ListGetPlayStopStopAll(t *testing.T) {
	dir := t.TempDir()
	writeShowFile(t, dir, "001.alpha.show", 10, 100)
	writeShowFile(t, dir, "002.beta.show", 20, 100)

	player := newTestPlayer(t)
	lib, err := NewLibrary(player, dir)
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
		return lib.isShowPlaying(1)
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
		return !lib.isShowPlaying(1)
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
		return lib.isShowPlaying(1) && lib.isShowPlaying(2)
	}, 200*time.Millisecond, 1*time.Millisecond)

	req = httptest.NewRequest(http.MethodGet, "/show/stop", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Eventually(t, func() bool {
		return !lib.isShowPlaying(1) && !lib.isShowPlaying(2)
	}, 400*time.Millisecond, 1*time.Millisecond)
}
