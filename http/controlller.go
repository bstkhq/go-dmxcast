package http

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

type ShowController struct {
	lib *Library
}

func NewShowController(lib *Library) *ShowController {
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
	return c.JSON(http.StatusOK, sc.lib.listShows())
}

func (sc *ShowController) Get(c echo.Context) error {
	key := strings.TrimSpace(c.Param("key"))
	id, err := sc.lib.resolveShowID(key)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
	}

	si, ok := sc.lib.getShow(id)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"error": fmt.Sprintf("show not found: %q", key)})
	}
	return c.JSON(http.StatusOK, si)
}

func (sc *ShowController) Play(c echo.Context) error {
	key := strings.TrimSpace(c.Param("key"))
	id, err := sc.lib.resolveShowID(key)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
	}

	h, err := sc.lib.playShow(id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"handleId": int(h.ID())})
}

func (sc *ShowController) Stop(c echo.Context) error {
	key := strings.TrimSpace(c.Param("key"))
	id, err := sc.lib.resolveShowID(key)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
	}

	wasPlaying := sc.lib.stopShow(id)
	return c.JSON(http.StatusOK, map[string]any{"wasPlaying": wasPlaying})
}

func (sc *ShowController) StopAll(c echo.Context) error {
	sc.lib.stopAll()
	return c.JSON(http.StatusOK, map[string]any{"stopped": true})
}
