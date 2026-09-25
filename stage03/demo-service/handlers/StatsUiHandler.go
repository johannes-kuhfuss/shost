// package handlers sets up the handlers for the Web UI
package handlers

import (
	"context"
	"demo-service/appconfig"
	"demo-service/appstate"
	"demo-service/dto"
	"net/http"
	"strconv"
	"time"

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
	configData := uh.getState()
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

func (uh *StatsUiHandler) getState() dto.State {
	var (
		currentState dto.State
	)
	currentState.ListeningAddr = uh.State.Runtime.ListenAddr
	currentState.TlsPort = uh.Cfg.Server.TLSPort
	currentState.GracefulShutdownTime = strconv.FormatInt(int64(uh.Cfg.Server.GracefulShutdownTime), 10)
	currentState.DrainRequestTime = strconv.FormatInt(int64(uh.Cfg.Server.DrainRequestsTime), 10)
	currentState.UseTls = strconv.FormatBool(uh.Cfg.Server.UseTLS)
	currentState.CertFile = uh.Cfg.Server.CertFile
	currentState.KeyFile = uh.Cfg.Server.KeyFile
	currentState.ServiceStartDate = formatDate(uh.State.Runtime.StartDateDate)
	currentState.LastCertRenewDate = formatDate(uh.State.Runtime.LastCertRenewDate)

	return currentState
}

func formatDate(d time.Time) string {
	if d.IsZero() {
		return "N/A"
	} else {
		return d.Format(time.RFC3339)
	}
}
