package certstore

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

type CertificateStore struct {
	cert atomic.Pointer[tls.Certificate]
	info atomic.Pointer[CertificateInfo]

	certFile string
	keyFile  string
}

type CertificateInfo struct {
	Subject           string    `json:"subject"`
	Issuer            string    `json:"issuer"`
	SerialNumber      string    `json:"serialNumber"`
	DNSNames          []string  `json:"dnsNames"`
	NotBefore         time.Time `json:"notBefore"`
	NotAfter          time.Time `json:"notAfter"`
	SHA256Fingerprint string    `json:"sha256Fingerprint"`
}

func New(certFile, keyFile string) *CertificateStore {
	return &CertificateStore{
		certFile: certFile,
		keyFile:  keyFile,
	}
}

func (s *CertificateStore) Reload() error {
	next, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
	if err != nil {
		return err
	}

	leaf, err := x509.ParseCertificate(next.Certificate[0])
	if err != nil {
		return err
	}

	next.Leaf = leaf

	info := buildCertificateInfo(leaf)

	// Publish only after everything was loaded successfully.
	s.cert.Store(&next)
	s.info.Store(&info)

	return nil
}

func (s *CertificateStore) Info() *CertificateInfo {
	return s.info.Load()
}

func buildCertificateInfo(leaf *x509.Certificate) CertificateInfo {
	var (
		ci CertificateInfo
	)
	ci.Subject = leaf.Subject.CommonName
	ci.Issuer = leaf.Issuer.CommonName
	ci.SerialNumber = leaf.Issuer.SerialNumber
	ci.DNSNames = leaf.DNSNames
	ci.NotBefore = leaf.NotBefore
	ci.NotAfter = leaf.NotAfter
	fingerprint := sha256.Sum256(leaf.Raw)
	ci.SHA256Fingerprint = strings.ToUpper(hex.EncodeToString(fingerprint[:]))
	return ci
}

func (s *CertificateStore) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := s.cert.Load()
	if cert == nil {
		return nil, errors.New("no TLS certificate loaded")
	}

	return cert, nil
}

func WatchCertFolder() {

}
