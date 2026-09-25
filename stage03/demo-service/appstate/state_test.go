package appstate

import (
	"sync"
	"testing"
	"time"
)

func TestConcurrentProbeStats(t *testing.T) {
	state := New()
	for _, probe := range []struct {
		name   string
		record func()
		stats  func() ProbeStats
	}{
		{"startup", state.Runtime.RecordStartupProbe, state.Runtime.StartupProbeStats},
		{"liveness", state.Runtime.RecordLivenessProbe, state.Runtime.LivenessProbeStats},
		{"readiness", state.Runtime.RecordReadinessProbe, state.Runtime.ReadinessProbeStats},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if got := probe.stats(); got.Count != 0 || !got.LastProbeDate.IsZero() {
				t.Fatalf("initial stats = %+v, want zero", got)
			}
			started := time.Now().UTC()
			var workers sync.WaitGroup
			for range 8 {
				workers.Go(func() {
					for range 1000 {
						probe.record()
						got := probe.stats()
						if got.Count < 1 || got.LastProbeDate.Before(started) || got.LastProbeDate.Location() != time.UTC {
							t.Errorf("invalid snapshot: %+v", got)
						}
					}
				})
			}
			workers.Wait()
			if got := probe.stats(); got.Count != 8000 || got.LastProbeDate.After(time.Now()) {
				t.Fatalf("final stats = %+v, want 8000 requests with a past timestamp", got)
			}
		})
	}
}

func TestConcurrentCertificateRenewalDate(t *testing.T) {
	state := New()
	if !state.Runtime.LastCertRenewDate().IsZero() {
		t.Fatal("renewal date should initially be zero")
	}
	date := time.Now().UTC()
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 1000 {
				state.Runtime.SetLastCertRenewDate(date)
				if got := state.Runtime.LastCertRenewDate(); !got.Equal(date) {
					t.Errorf("renewal date = %v, want %v", got, date)
				}
			}
		})
	}
	workers.Wait()
}
