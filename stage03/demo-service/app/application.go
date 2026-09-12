package app

import (
	"crypto/tls"
	"demo-service/appconfig"
	"demo-service/appstate"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johannes-kuhfuss/services_utils/date"
	"github.com/johannes-kuhfuss/services_utils/logger"
)

type Application struct {
	cfg    appconfig.AppConfig
	state  *appstate.AppState
	server http.Server
}

func StartApp() error {
	application := &Application{}
	return application.Start()
}

func (a *Application) Start() error {
	a.state = appstate.New()
	err := appconfig.InitConfig(appconfig.EnvFile, &a.cfg)
	if err != nil {
		return err
	}
	logger.Info("Starting application...")
	a.initRouter()
	a.initServer()
	a.wireApp()
	if err := a.mapUrls(); err != nil {
		return err
	}
	a.startServer()
	logger.Info("Ending application.")
	return nil
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
			PreferServerCipherSuites: true,
			MinVersion:               tls.VersionTLS13,
			CurvePreferences: []tls.CurveID{
				tls.X25519,
				tls.CurveP256,
				tls.CurveP384,
			},
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
		a.server.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))
	}
}

// wireApp initializes the services in the right order and injects the dependencies
func (a *Application) wireApp() {
}

// mapUrls defines the handlers for the available URLs
func (a *Application) mapUrls() error {
	a.state.Runtime.Router.GET("/", a.pong)
	a.state.Runtime.Router.GET("/healthz", a.healthz)
	return nil
}

func (a *Application) pong(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ping": "pong"})
}

func (a *Application) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// startServer starts the preconfigured web server
func (a *Application) startServer() {
	logger.Infof("Listening on %v", a.state.Runtime.ListenAddr)
	a.state.Runtime.StartDate = date.GetNowUtc()
	if a.cfg.Server.UseTLS {
		if err := a.server.ListenAndServeTLS(a.cfg.Server.CertFile, a.cfg.Server.KeyFile); err != nil && err != http.ErrServerClosed {
			logger.Error("Error while starting https server", err)
		}
	} else {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Error while starting http server", err)
		}
	}
}
