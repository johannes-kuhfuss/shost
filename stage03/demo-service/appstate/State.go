package appstate

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type RuntimeState struct {
	Router            *gin.Engine
	ListenAddr        string
	StartDateDate     time.Time
	Mu                sync.RWMutex
	lastCertRenewDate time.Time
}

// LastCertRenewDate returns zero until a certificate renewal has been observed.
func (s *RuntimeState) LastCertRenewDate() time.Time {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.lastCertRenewDate
}

// SetLastCertRenewDate records a successful renewal safely alongside status reads.
func (s *RuntimeState) SetLastCertRenewDate(date time.Time) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.lastCertRenewDate = date.UTC()
}

type AppState struct {
	Runtime RuntimeState
}

func New() *AppState {
	return &AppState{
		Runtime: RuntimeState{},
	}
}
