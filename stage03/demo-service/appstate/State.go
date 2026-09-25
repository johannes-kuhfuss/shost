package appstate

import (
	"time"

	"github.com/gin-gonic/gin"
)

type RuntimeState struct {
	Router            *gin.Engine
	ListenAddr        string
	StartDateDate     time.Time
	LastCertRenewDate time.Time
}

type AppState struct {
	Runtime RuntimeState
}

func New() *AppState {
	return &AppState{
		Runtime: RuntimeState{},
	}
}
