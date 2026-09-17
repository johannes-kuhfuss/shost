package certstore

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/johannes-kuhfuss/services_utils/logger"
)

type certificateSnapshot struct {
	certificate tls.Certificate
	info        CertificateInfo
}

type CertificateStore struct {
	current atomic.Pointer[certificateSnapshot]

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
	newSnap := certificateSnapshot{
		certificate: next,
		info:        info,
	}
	s.current.Store(&newSnap)

	return nil
}

func (s *CertificateStore) Info() *CertificateInfo {
	snapshot := s.current.Load()
	if snapshot == nil {
		return nil
	}
	info := snapshot.info
	return &info
}

func buildCertificateInfo(leaf *x509.Certificate) CertificateInfo {
	var (
		ci CertificateInfo
	)
	ci.Subject = leaf.Subject.CommonName
	ci.Issuer = leaf.Issuer.CommonName
	ci.SerialNumber = leaf.SerialNumber.String()
	ci.DNSNames = leaf.DNSNames
	ci.NotBefore = leaf.NotBefore
	ci.NotAfter = leaf.NotAfter
	fingerprint := sha256.Sum256(leaf.Raw)
	ci.SHA256Fingerprint = strings.ToUpper(hex.EncodeToString(fingerprint[:]))
	return ci
}

func (s *CertificateStore) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	snapshot := s.current.Load()
	if snapshot == nil {
		return nil, errors.New("no TLS certificate loaded")
	}

	return &snapshot.certificate, nil
}

func (s *CertificateStore) WatchCertFolder(ctx context.Context) error {
	return s.watchCertFolder(ctx, 500*time.Millisecond, nil)
}

func (s *CertificateStore) watchCertFolder(ctx context.Context, debounceDuration time.Duration, ready chan<- struct{}) error {
	var (
		debounceTimer *time.Timer
		debounceC     <-chan time.Time
	)

	certFolder := filepath.Dir(s.certFile)
	keyFolder := filepath.Dir(s.keyFile)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("could not start filesystem watcher: %w", err)
	}
	defer watcher.Close()

	watchFolders := []string{certFolder}
	if keyFolder != certFolder {
		watchFolders = append(watchFolders, keyFolder)
	}
	for _, folder := range watchFolders {
		if err := watcher.Add(folder); err != nil {
			return fmt.Errorf("could not add directory %q to watcher: %w", folder, err)
		}
		logger.Infof("watching for certificate changes in %v", folder)
	}

	if ready != nil {
		close(ready)
	}

	defer func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
	}()

	scheduleReload := func() {
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(debounceDuration)
		} else {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer.Reset(debounceDuration)
		}
		debounceC = debounceTimer.C
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("filesystem watcher event channel closed unexpectedly")
			}

			relevantOps := fsnotify.Create | fsnotify.Write | fsnotify.Rename | fsnotify.Remove
			if event.Op&relevantOps == 0 {
				continue
			}
			scheduleReload()

		case <-debounceC:
			debounceC = nil
			oldInfo := s.Info()

			if err := s.Reload(); err != nil {
				logger.Warnf("could not reload certificate; continuing with old certificate: %v", err)
				continue
			}
			newInfo := s.Info()
			logger.Infof(
				"reloaded TLS certificate: old fingerprint=%s, new fingerprint=%s, expires=%s",
				certificateFingerprint(oldInfo),
				certificateFingerprint(newInfo),
				newInfo.NotAfter,
			)

		case watcherErr, ok := <-watcher.Errors:
			if !ok {
				return errors.New("filesystem watcher error channel closed unexpectedly")
			}
			return fmt.Errorf("filesystem watcher failed: %w", watcherErr)

		case <-ctx.Done():
			return nil
		}
	}
}

func certificateFingerprint(info *CertificateInfo) string {
	if info == nil {
		return "<none>"
	}
	return info.SHA256Fingerprint
}
