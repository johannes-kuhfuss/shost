package appstate

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type RuntimeState struct {
	Router                 *gin.Engine
	ListenAddr             string
	StartDateDate          time.Time
	Mu                     sync.RWMutex
	lastCertRenewDate      time.Time
	lastStartupProbeDate   time.Time
	lastLivenessProbeDate  time.Time
	lastReadinessProbeDate time.Time
	startupProbeCount      int
	livenessProbeCount     int
	readinessProbeCount    int
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

// ProbeStats is a consistent snapshot of a probe's count and latest request time.
// LastProbeDate is zero until the first request.
type ProbeStats struct {
	Count         int
	LastProbeDate time.Time
}

func (s *RuntimeState) RecordStartupProbe() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.startupProbeCount++
	s.lastStartupProbeDate = time.Now().UTC()
}

func (s *RuntimeState) StartupProbeStats() ProbeStats {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return ProbeStats{Count: s.startupProbeCount, LastProbeDate: s.lastStartupProbeDate}
}

func (s *RuntimeState) RecordLivenessProbe() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.livenessProbeCount++
	s.lastLivenessProbeDate = time.Now().UTC()
}

func (s *RuntimeState) LivenessProbeStats() ProbeStats {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return ProbeStats{Count: s.livenessProbeCount, LastProbeDate: s.lastLivenessProbeDate}
}

func (s *RuntimeState) RecordReadinessProbe() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.readinessProbeCount++
	s.lastReadinessProbeDate = time.Now().UTC()
}

func (s *RuntimeState) ReadinessProbeStats() ProbeStats {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return ProbeStats{Count: s.readinessProbeCount, LastProbeDate: s.lastReadinessProbeDate}
}

func New() *AppState {
	return &AppState{
		Runtime: RuntimeState{},
	}
}
