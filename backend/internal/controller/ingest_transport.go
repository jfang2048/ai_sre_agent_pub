package controller

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"google.golang.org/grpc/credentials"
)

type controllerIngestTransportStatus struct {
	TLSEnabled                 bool   `json:"tls_enabled"`
	MTLSEnabled                bool   `json:"mtls_enabled"`
	PlaintextActive            bool   `json:"plaintext_active"`
	PlaintextOverride          bool   `json:"plaintext_override"`
	IdentityBindingEnabled     bool   `json:"identity_binding_enabled"`
	AllowlistEnabled           bool   `json:"allowlist_enabled"`
	AllowlistCollectorIDs      int    `json:"allowlist_collector_ids"`
	AllowlistClusterNames      int    `json:"allowlist_cluster_names"`
	AllowlistPeerSubjects      int    `json:"allowlist_peer_subjects"`
	AuthenticationFailures     uint64 `json:"authentication_failures"`
	AuthorizationFailures      uint64 `json:"authorization_failures"`
	AllowlistRejections        uint64 `json:"allowlist_rejections"`
	IdentityMismatchRejections uint64 `json:"identity_mismatch_rejections"`
}

func (c *Controller) ingestAccessPolicy() ingest.AccessPolicy {
	if c == nil {
		return ingest.AccessPolicy{}
	}
	return ingest.AccessPolicy{
		AllowedCollectorIDs: append([]string(nil), c.config.Ingest.Transport.AllowedCollectorIDs...),
		AllowedClusterNames: append([]string(nil), c.config.Ingest.Transport.AllowedClusterNames...),
		AllowedPeerSubjects: append([]string(nil), c.config.Ingest.Transport.AllowedPeerSubjects...),
	}
}

func (c *Controller) ingestTransportStatus() controllerIngestTransportStatus {
	status := controllerIngestTransportStatus{}
	if c == nil {
		return status
	}
	stats := ingest.Stats{}
	if c.ingestServer != nil {
		stats = c.ingestServer.Stats()
		status.AuthenticationFailures = stats.AuthnRejectedTotal
		status.AuthorizationFailures = stats.AuthzRejectedTotal
		status.AllowlistRejections = stats.AllowlistRejectedTotal
		status.IdentityMismatchRejections = stats.IdentityMismatchTotal
	}
	status.TLSEnabled = c.config.Ingest.Transport.TLS.Enabled
	status.MTLSEnabled = c.config.Ingest.Transport.TLS.Enabled && c.config.Ingest.Transport.TLS.RequireClientCert
	status.PlaintextActive = !status.TLSEnabled
	status.PlaintextOverride = !status.TLSEnabled && c.config.Ingest.Transport.AllowPlaintext
	status.IdentityBindingEnabled = c.auth.IngestAuthEnabled || status.MTLSEnabled
	status.AllowlistCollectorIDs = len(c.config.Ingest.Transport.AllowedCollectorIDs)
	status.AllowlistClusterNames = len(c.config.Ingest.Transport.AllowedClusterNames)
	status.AllowlistPeerSubjects = len(c.config.Ingest.Transport.AllowedPeerSubjects)
	status.AllowlistEnabled = status.AllowlistCollectorIDs > 0 || status.AllowlistClusterNames > 0 || status.AllowlistPeerSubjects > 0
	return status
}

func loadIngestServerTransportCredentials(cfg IngestTransportConfig) (credentials.TransportCredentials, error) {
	if !cfg.TLS.Enabled {
		return nil, nil
	}
	certFile := strings.TrimSpace(cfg.TLS.CertFile)
	keyFile := strings.TrimSpace(cfg.TLS.KeyFile)
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("ingest transport tls requires cert_file and key_file")
	}
	serverCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load ingest server cert/key: %w", err)
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCert},
	}
	if cfg.TLS.RequireClientCert {
		caFile := strings.TrimSpace(cfg.TLS.CAFile)
		if caFile == "" {
			return nil, fmt.Errorf("ingest transport tls.ca_file is required when require_client_cert=true")
		}
		caBytes, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read ingest client ca bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, fmt.Errorf("parse ingest client ca bundle %q", caFile)
		}
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
		tlsCfg.ClientCAs = pool
	}
	return credentials.NewTLS(tlsCfg), nil
}
