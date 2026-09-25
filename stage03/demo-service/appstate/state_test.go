package appstate

import (
	"sync"
	"testing"
	"time"
)

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
