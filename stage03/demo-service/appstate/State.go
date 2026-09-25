package appstate

import (
	"sync"
	"time"
)

type RuntimeState struct {
	ListenAddr             string
	StartDate              time.Time
	mu                     sync.RWMutex
	lastCertRenewDate      time.Time
	startupProbe           ProbeStats
	livenessProbe          ProbeStats
	readinessProbe         ProbeStats
	livenessDisabled       bool
	readinessDisabledUntil time.Time
}

// DisableReadinessFor starts a new timed failure window. A deadline avoids
// background timers and ensures recovery even when no UI requests arrive.
func (s *RuntimeState) DisableReadinessFor(duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readinessDisabledUntil = time.Now().Add(duration)
}

func (s *RuntimeState) ReadinessDisabledUntil() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readinessDisabledUntil
}

// LivenessDisabled reports whether liveness failures have been enabled for the demo.
func (s *RuntimeState) LivenessDisabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.livenessDisabled
}

func (s *RuntimeState) SetLivenessDisabled(disabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.livenessDisabled = disabled
}

// LastCertRenewDate returns zero until a certificate renewal has been observed.
func (s *RuntimeState) LastCertRenewDate() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastCertRenewDate
}

// SetLastCertRenewDate records a successful renewal safely alongside status reads.
func (s *RuntimeState) SetLastCertRenewDate(date time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	// LastProbeSuccessful is meaningful only when Count is greater than zero.
	LastProbeSuccessful bool
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
	p.LastProbeSuccessful = success
}

func (s *RuntimeState) RecordStartupProbe(success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startupProbe.record(success)
}

func (s *RuntimeState) StartupProbeStats() ProbeStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.startupProbe
}

func (s *RuntimeState) RecordLivenessProbe(success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.livenessProbe.record(success)
}

func (s *RuntimeState) LivenessProbeStats() ProbeStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.livenessProbe
}

func (s *RuntimeState) RecordReadinessProbe(success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readinessProbe.record(success)
}

func (s *RuntimeState) ReadinessProbeStats() ProbeStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readinessProbe
}

func New() *AppState {
	return &AppState{
		Runtime: RuntimeState{},
	}
}
