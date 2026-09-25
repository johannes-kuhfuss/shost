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
	startupProbe      ProbeStats
	livenessProbe     ProbeStats
	readinessProbe    ProbeStats
	livenessDisabled  bool
}

// LivenessDisabled reports whether liveness failures have been enabled for the demo.
func (s *RuntimeState) LivenessDisabled() bool {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.livenessDisabled
}

func (s *RuntimeState) SetLivenessDisabled(disabled bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.livenessDisabled = disabled
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
	SuccessCount  int
	FailureCount  int
	LastProbeDate time.Time
}

// record is called while the runtime state's mutex is held.
func (p *ProbeStats) record(success bool) {
	p.Count++
	if success {
		p.SuccessCount++
	} else {
		p.FailureCount++
	}
	p.LastProbeDate = time.Now().UTC()
}

func (s *RuntimeState) RecordStartupProbe(success bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.startupProbe.record(success)
}

func (s *RuntimeState) StartupProbeStats() ProbeStats {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.startupProbe
}

func (s *RuntimeState) RecordLivenessProbe(success bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.livenessProbe.record(success)
}

func (s *RuntimeState) LivenessProbeStats() ProbeStats {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.livenessProbe
}

func (s *RuntimeState) RecordReadinessProbe(success bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.readinessProbe.record(success)
}

func (s *RuntimeState) ReadinessProbeStats() ProbeStats {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.readinessProbe
}

func New() *AppState {
	return &AppState{
		Runtime: RuntimeState{},
	}
}
