package dto

type ProbeStatus struct {
	Name          string
	Count         int
	SuccessCount  int
	FailureCount  int
	LastProbeDate string
}
