// package handlers sets up the handlers for the Web UI
package handlers

import (
	"context"
	"demo-service/appconfig"
	"demo-service/appstate"
	"net/http"

	"github.com/gin-gonic/gin"
)

type StatsUiHandler struct {
	Cfg   *appconfig.AppConfig
	State *appstate.AppState
}

func NewStatsUiHandlerWithState(cfg *appconfig.AppConfig, state *appstate.AppState) StatsUiHandler {
	return NewStatsUiHandlerWithContext(context.Background(), cfg, state)
}

func NewStatsUiHandlerWithContext(ctx context.Context, cfg *appconfig.AppConfig, state *appstate.AppState) StatsUiHandler {
	return StatsUiHandler{
		Cfg:   cfg,
		State: state,
	}
}

// StatusPage is the handler for the status page
func (uh *StatsUiHandler) StatusPage(c *gin.Context) {
	configData := ""
	c.HTML(http.StatusOK, "status.page.tmpl", gin.H{
		"title":      "Status",
		"configdata": configData,
	})
}

// AboutPage is the handler for the page displaying a short description of the program and its license
func (uh *StatsUiHandler) AboutPage(c *gin.Context) {
	c.HTML(http.StatusOK, "about.page.tmpl", gin.H{
		"title": "About",
		"data":  nil,
	})
}
