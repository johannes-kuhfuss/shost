// package handlers sets up the handlers for the Web UI
package handlers

import (
	"demo-service/appconfig"
	"demo-service/appstate"
	"demo-service/certstore"
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

func NewStatsUiHandler(cfg *appconfig.AppConfig, state *appstate.AppState) StatsUiHandler {
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
	c.Header("Cache-Control", "no-store")
	until := uh.State.Runtime.ReadinessDisabledUntil()
	remaining := time.Until(until)
	if remaining < 0 {
		remaining = 0
	}
	probes := make([]dto.ProbeStatus, 0, 3)
	for _, probe := range []struct {
		name  string
		stats appstate.ProbeStats
	}{
		{"Startup", uh.State.Runtime.StartupProbeStats()},
		{"Liveness", uh.State.Runtime.LivenessProbeStats()},
		{"Readiness", uh.State.Runtime.ReadinessProbeStats()},
	} {
		lastStatus := "N/A"
		if probe.stats.Count > 0 {
			lastStatus = "Failed"
			if probe.stats.LastProbeSuccessful {
				lastStatus = "Successful"
			}
		}
		probes = append(probes, dto.ProbeStatus{
			Name:            probe.name,
			Count:           probe.stats.Count,
			SuccessCount:    probe.stats.SuccessCount,
			FailureCount:    probe.stats.FailureCount,
			LastProbeDate:   formatDate(probe.stats.LastProbeDate),
			LastProbeStatus: lastStatus,
		})
	}
	c.HTML(http.StatusOK, "probes.page.tmpl", gin.H{
		"title":                     "Probe Status",
		"probes":                    probes,
		"livenessDisabled":          uh.State.Runtime.LivenessDisabled(),
		"readinessDisabled":         remaining > 0,
		"readinessUntil":            until.UTC().Format(time.RFC3339),
		"readinessRemainingMS":      remaining.Milliseconds(),
		"readinessRemainingSeconds": int64(remaining.Seconds() + 0.999),
	})
}

// DisableReadiness returns the confirmation page directly, before service
// routing is removed; recovery requires no further browser requests.
func (uh *StatsUiHandler) DisableReadiness(c *gin.Context) {
	seconds, err := strconv.Atoi(c.PostForm("seconds"))
	if err != nil || seconds < 1 || seconds > 3600 {
		c.String(http.StatusBadRequest, "Readiness duration must be a whole number from 1 to 3600 seconds")
		return
	}
	uh.State.Runtime.DisableReadinessFor(time.Duration(seconds) * time.Second)
	uh.ProbesPage(c)
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
	currentState.ServiceStartDate = formatDate(uh.State.Runtime.StartDate)

	return currentState
}

func formatDate(d time.Time) string {
	if d.IsZero() {
		return "N/A"
	} else {
		return d.Format(time.RFC3339)
	}
}

func CertificatePage(c *gin.Context, store *certstore.CertificateStore, lastRenewed time.Time) {
	data := gin.H{"title": "Certificate"}
	if store == nil {
		data["error"] = "TLS is disabled"
		c.HTML(http.StatusServiceUnavailable, "certificate.page.tmpl", data)
		return
	}

	info := store.Info()
	if info == nil {
		data["error"] = "No certificate loaded"
		c.HTML(http.StatusServiceUnavailable, "certificate.page.tmpl", data)
		return
	}

	data["certificate"] = info
	data["lastRenewed"] = formatDate(lastRenewed)
	data["notBefore"] = info.NotBefore.UTC().Format(time.RFC3339)
	data["notAfter"] = info.NotAfter.UTC().Format(time.RFC3339)
	c.HTML(http.StatusOK, "certificate.page.tmpl", data)
}
