package dto

type State struct {
	PodName              string
	PodIP                string
	PodNamespace         string
	NodeName             string
	ListeningAddr        string
	TlsPort              string
	GracefulShutdownTime string
	DrainRequestTime     string
	UseTls               string
	CertFile             string
	KeyFile              string
	ServiceStartDate     string
	LastCertRenewDate    string
}
