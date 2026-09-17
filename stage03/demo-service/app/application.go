package app

import (
	"context"
	"crypto/tls"
	"demo-service/appconfig"
	"demo-service/appstate"
	"demo-service/certstore"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johannes-kuhfuss/services_utils/date"
	"github.com/johannes-kuhfuss/services_utils/logger"
)

type Application struct {
	cfg              appconfig.AppConfig
	state            *appstate.AppState
	server           http.Server
	certificateStore *certstore.CertificateStore
	shuttingDown     atomic.Bool
}

func StartApp(ctx context.Context) error {
	application := &Application{}
	return application.Start(ctx)
}

func (a *Application) Start(ctx context.Context) error {
	a.state = appstate.New()
	err := appconfig.InitConfig(appconfig.EnvFile, &a.cfg)
	if err != nil {
		return err
	}
	logger.Info("Starting application...")
	if a.cfg.Server.UseTLS {
		a.certificateStore = certstore.New(a.cfg.Server.CertFile, a.cfg.Server.KeyFile)
		if err := a.certificateStore.Reload(); err != nil {
			return fmt.Errorf("initial TLS certificate load failed: %w", err)
		}
	}
	a.initRouter()
	a.initServer()
	if err := a.mapUrls(); err != nil {
		return err
	}

	appCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		watchDone <-chan struct{}
		watchErr  <-chan error
	)
	if a.cfg.Server.UseTLS {
		watcherDone := make(chan struct{})
		watcherErrors := make(chan error, 1)
		watchDone = watcherDone
		watchErr = watcherErrors

		go func() {
			defer close(watcherDone)
			if err := a.certificateStore.WatchCertFolder(appCtx); err != nil {
				watcherErrors <- err
			}
		}()
	}

	serverErr := a.runServer(
		appCtx,
		a.startServer,
		watchErr,
		time.Duration(a.cfg.Server.DrainRequestsTime)*time.Second,
		time.Duration(a.cfg.Server.GracefulShutdownTime)*time.Second,
	)

	cancel()
	if watchDone != nil {
		<-watchDone
	}
	return serverErr
}

func (a *Application) runServer(ctx context.Context, serve func() error, watchErr <-chan error, drainDuration, shutdownTimeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- serve()
	}()

	var lifecycleErr error
	select {
	case err := <-serveErr:
		return err
	case err := <-watchErr:
		lifecycleErr = fmt.Errorf("certificate watcher failed: %w", err)
		logger.Error("Certificate watcher failed. Shutting down.", err)
	case <-ctx.Done():
	}

	logger.Infof("Allowing traffic to drain for %v...", drainDuration)
	a.shuttingDown.Store(true)
	drainTimer := time.NewTimer(drainDuration)
	defer drainTimer.Stop()

	select {
	case <-drainTimer.C:
		logger.Info("Request draining period completed.")

	case err := <-serveErr:
		// The server failed while we were draining.
		return err
	}
	logger.Info("Shutdown requested. Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	shutdownErr := a.server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		logger.Error("Graceful server shutdown failed.", shutdownErr)
		if closeErr := a.server.Close(); closeErr != nil {
			logger.Error("Forced server shutdown failed", closeErr)
		}
	}

	serveResult := <-serveErr
	if shutdownErr != nil {
		return fmt.Errorf("graceful shutdown: %w", shutdownErr)
	}
	if serveResult != nil {
		return serveResult
	}
	return lifecycleErr
}

// initRouter initializes gin-gonic as the router
func (a *Application) initRouter() {
	gin.SetMode(a.cfg.Gin.Mode)
	router := gin.New()
	if a.cfg.Gin.LogToLogger {
		gin.DefaultWriter = logger.GetLogger()
		router.Use(gin.Logger())
	}
	router.Use(gin.Recovery())
	router.SetTrustedProxies(nil)
	//globPath := filepath.Join(a.cfg.Gin.TemplatePath, "*.tmpl")
	//router.LoadHTMLGlob(globPath)

	a.state.Runtime.Router = router
}

// initServer checks whether https is enabled and initializes the web server accordingly
func (a *Application) initServer() {
	var tlsConfig tls.Config

	if a.cfg.Server.UseTLS {
		tlsConfig = tls.Config{
			MinVersion: tls.VersionTLS13,
			CurvePreferences: []tls.CurveID{
				tls.X25519,
				tls.CurveP256,
				tls.CurveP384,
			},
			GetCertificate: a.certificateStore.GetCertificate,
		}
	}
	if a.cfg.Server.UseTLS {
		a.state.Runtime.ListenAddr = fmt.Sprintf("%s:%s", a.cfg.Server.Host, a.cfg.Server.TLSPort)
	} else {
		a.state.Runtime.ListenAddr = fmt.Sprintf("%s:%s", a.cfg.Server.Host, a.cfg.Server.Port)
	}

	a.server = http.Server{
		Addr:              a.state.Runtime.ListenAddr,
		Handler:           a.state.Runtime.Router,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    http.DefaultMaxHeaderBytes,
	}
	if a.cfg.Server.UseTLS {
		a.server.TLSConfig = &tlsConfig
	}
}

// mapUrls defines the handlers for the available URLs
func (a *Application) mapUrls() error {
	a.state.Runtime.Router.GET("/", a.pong)
	a.state.Runtime.Router.GET("/health/startup", a.startup)
	a.state.Runtime.Router.GET("/health/ready", a.ready)
	a.state.Runtime.Router.GET("/health/live", a.live)
	a.state.Runtime.Router.GET("/certificate", a.certificate)
	return nil
}

func (a *Application) pong(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ping": "pong"})
}

func (a *Application) startup(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"endpoint": "startup probe",
		"status":   "ok"})
}

func (a *Application) ready(c *gin.Context) {
	if !a.shuttingDown.Load() {
		c.JSON(http.StatusOK, gin.H{
			"endpoint": "ready probe",
			"status":   "ok"})
	} else {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"endpoint": "ready probe",
			"status":   "shutting down"})
	}
}

func (a *Application) live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"endpoint": "live probe",
		"status":   "ok"})
}

func (a *Application) certificate(c *gin.Context) {
	if a.certificateStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "TLS is disabled",
		})
		return
	}

	info := a.certificateStore.Info()
	if info == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "no certificate loaded",
		})
		return
	}

	c.JSON(http.StatusOK, info)
}

// startServer starts the preconfigured web server
func (a *Application) startServer() error {
	var (
		err error
	)
	logger.Infof("Listening on %v", a.state.Runtime.ListenAddr)
	a.state.Runtime.StartDate = date.GetNowUtc()
	if a.cfg.Server.UseTLS {
		err = a.server.ListenAndServeTLS("", "")
	} else {
		err = a.server.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve HTTP: %w", err)
}
