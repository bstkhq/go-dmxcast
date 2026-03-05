package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/bstkhq/go-dmxcast"
	"github.com/labstack/echo/v4"
)

type ShowController struct {
	lib *dmxcast.Library
}

func NewShowController(lib *dmxcast.Library) *ShowController {
	return &ShowController{lib: lib}
}

func (sc *ShowController) RegisterRoutes(g *echo.Group, auth echo.MiddlewareFunc) {
	g.GET("", sc.List)
	g.GET("/:key", sc.Get)
	g.GET("/:key/play", sc.Play)
	g.GET("/:key/stop", sc.Stop)
	g.GET("/stop", sc.StopAll)
}

func (sc *ShowController) List(c echo.Context) error {
	return c.JSON(http.StatusOK, sc.lib.List())
}

func (sc *ShowController) Get(c echo.Context) error {
	key := strings.TrimSpace(c.Param("key"))
	id, err := sc.resolveShowID(key)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
	}

	si, ok := sc.lib.Get(id)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"error": fmt.Sprintf("show not found: %q", key)})
	}
	return c.JSON(http.StatusOK, si)
}

func (sc *ShowController) Play(c echo.Context) error {
	key := strings.TrimSpace(c.Param("key"))
	id, err := sc.resolveShowID(key)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
	}

	h, err := sc.lib.Play(id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"handleId": int(h.ID())})
}

func (sc *ShowController) Stop(c echo.Context) error {
	key := strings.TrimSpace(c.Param("key"))
	id, err := sc.resolveShowID(key)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
	}

	wasPlaying := sc.lib.Stop(id)
	return c.JSON(http.StatusOK, map[string]any{"wasPlaying": wasPlaying})
}

func (sc *ShowController) StopAll(c echo.Context) error {
	sc.lib.StopAll()
	return c.JSON(http.StatusOK, map[string]any{"stopped": true})
}

func (sc *ShowController) resolveShowID(key string) (int, error) {
	needle := strings.ToLower(strings.TrimSpace(key))

	if n, err := strconv.Atoi(needle); err == nil {
		if s, ok := sc.lib.Get(n); ok {
			return s.ID, nil
		}
	}

	if s, ok := sc.lib.GetByName(needle); ok {
		return s.ID, nil
	}

	return 0, fmt.Errorf("show not found: %q", key)
}

type ProgController struct {
	lib *dmxcast.Library
}

func NewProgController(lib *dmxcast.Library) *ProgController {
	return &ProgController{lib: lib}
}

func (pc *ProgController) RegisterRoutes(g *echo.Group, auth echo.MiddlewareFunc) {
	g.GET("/:num", pc.Status)
	g.GET("/:num/play", pc.Play)
	g.GET("/:num/stop", pc.Stop)
	g.GET("/:num/stop/all", pc.StopAll)
}

func (pc *ProgController) Status(c echo.Context) error {
	id, err := pc.checkExists(c.Param("num"))
	if err != nil {
		return c.String(http.StatusOK, "Stop")
	}

	if pc.lib.IsShowPlaying(id) {
		return c.String(http.StatusOK, "Play")
	}

	return c.String(http.StatusOK, "Stop")
}

func (pc *ProgController) Play(c echo.Context) error {
	id, err := pc.checkExists(c.Param("num"))
	if err != nil {
		return c.String(http.StatusNotFound, "stop")
	}

	_, err = pc.lib.Play(id)
	if err != nil {
		return c.String(http.StatusInternalServerError, "stop")
	}

	return c.String(http.StatusOK, "play 255")
}

func (pc *ProgController) Stop(c echo.Context) error {
	id, err := pc.checkExists(c.Param("num"))
	if err != nil {
		return c.String(http.StatusOK, "stop")
	}

	pc.lib.Stop(id)
	return c.String(http.StatusOK, "stop")
}

func (pc *ProgController) StopAll(c echo.Context) error {
	pc.lib.StopAll()
	return c.String(http.StatusOK, "stop all")
}

func (pc *ProgController) checkExists(num string) (int, error) {
	num = strings.TrimSpace(num)
	if num == "" {
		return 0, fmt.Errorf("invalid program")
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid program")
	}

	s, ok := pc.lib.Get(n)
	if !ok {
		return 0, fmt.Errorf("show not found")
	}

	return s.ID, nil
}
