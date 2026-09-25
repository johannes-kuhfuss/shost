package dto

type State struct {
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
