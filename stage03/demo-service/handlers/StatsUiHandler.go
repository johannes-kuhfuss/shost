// package handlers sets up the handlers for the Web UI
package handlers

import (
	"context"
	"demo-service/appconfig"
	"demo-service/appstate"
	"demo-service/dto"
	"net"
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

// ProbesPage displays the latest probe statistics using thread-safe snapshots.
func (uh *StatsUiHandler) ProbesPage(c *gin.Context) {
	probes := make([]dto.ProbeStatus, 0, 3)
	for _, probe := range []struct {
		name  string
		stats appstate.ProbeStats
	}{
		{"Startup", uh.State.Runtime.StartupProbeStats()},
		{"Liveness", uh.State.Runtime.LivenessProbeStats()},
		{"Readiness", uh.State.Runtime.ReadinessProbeStats()},
	} {
		probes = append(probes, dto.ProbeStatus{
			Name:          probe.name,
			Count:         probe.stats.Count,
			SuccessCount:  probe.stats.SuccessCount,
			FailureCount:  probe.stats.FailureCount,
			LastProbeDate: formatDate(probe.stats.LastProbeDate),
		})
	}
	c.HTML(http.StatusOK, "probes.page.tmpl", gin.H{
		"title":            "Probe Status",
		"probes":           probes,
		"livenessDisabled": uh.State.Runtime.LivenessDisabled(),
	})
}

// SetLiveness controls the in-memory liveness failure simulation.
func (uh *StatsUiHandler) SetLiveness(c *gin.Context) {
	switch c.PostForm("action") {
	case "disable":
		uh.State.Runtime.SetLivenessDisabled(true)
	case "enable":
		uh.State.Runtime.SetLivenessDisabled(false)
	default:
		c.String(http.StatusBadRequest, "Invalid liveness action")
		return
	}
	c.Redirect(http.StatusSeeOther, "/probes")
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
	if host, port, err := net.SplitHostPort(currentState.ListeningAddr); err == nil && host == "" {
		currentState.ListeningAddr = net.JoinHostPort("0.0.0.0", port)
	}
	currentState.PodName = uh.Cfg.Kubernetes.PodName
	currentState.PodIP = uh.Cfg.Kubernetes.PodIP
	currentState.PodNamespace = uh.Cfg.Kubernetes.PodNamespace
	currentState.NodeName = uh.Cfg.Kubernetes.NodeName
	currentState.TlsPort = uh.Cfg.Server.TLSPort
	currentState.GracefulShutdownTime = strconv.FormatInt(int64(uh.Cfg.Server.GracefulShutdownTime), 10)
	currentState.DrainRequestTime = strconv.FormatInt(int64(uh.Cfg.Server.DrainRequestsTime), 10)
	currentState.UseTls = strconv.FormatBool(uh.Cfg.Server.UseTLS)
	currentState.CertFile = uh.Cfg.Server.CertFile
	currentState.KeyFile = uh.Cfg.Server.KeyFile
	currentState.ServiceStartDate = formatDate(uh.State.Runtime.StartDateDate)
	currentState.LastCertRenewDate = formatDate(uh.State.Runtime.LastCertRenewDate())

	return currentState
}

func formatDate(d time.Time) string {
	if d.IsZero() {
		return "N/A"
	} else {
		return d.Format(time.RFC3339)
	}
}
