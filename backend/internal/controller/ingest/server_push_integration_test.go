package ingest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/identity"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type recordingProcessor struct {
	mu          sync.Mutex
	calls       int
	collectorID string
	batchID     string
}

type panicProcessor struct{}

func (p *panicProcessor) ProcessBatch(string, *telemetryv1.TelemetryBatch, time.Time) {
	panic("processor panic")
}

func (p *recordingProcessor) ProcessBatch(collectorID string, batch *telemetryv1.TelemetryBatch, _ time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.collectorID = collectorID
	if batch != nil {
		p.batchID = batch.BatchId
	}
}

func (p *recordingProcessor) snapshot() (calls int, collectorID, batchID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.collectorID, p.batchID
}

func startIngestGRPCServer(t *testing.T, ingestServer *Server) (addr string, cleanup func()) {
	return startIngestGRPCServerWithOptions(t, ingestServer)
}

func startIngestGRPCServerWithOptions(t *testing.T, ingestServer *Server, opts ...grpc.ServerOption) (addr string, cleanup func()) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("skipping due to listen permission error: %v", err)
		}
		t.Fatalf("listen: %v", err)
	}

	grpcServer := grpc.NewServer(opts...)
	telemetryv1.RegisterTelemetryIngestServer(grpcServer, ingestServer)

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	return listener.Addr().String(), func() {
		grpcServer.Stop()
		_ = listener.Close()
	}
}

func dialIngestClient(t *testing.T, addr string) (*grpc.ClientConn, telemetryv1.TelemetryIngestClient) {
	return dialIngestClientWithCreds(t, addr, insecure.NewCredentials())
}

func dialIngestClientWithCreds(t *testing.T, addr string, creds credentials.TransportCredentials) (*grpc.ClientConn, telemetryv1.TelemetryIngestClient) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	require.NoError(t, err)

	return conn, telemetryv1.NewTelemetryIngestClient(conn)
}

func writePEMFile(t *testing.T, dir, name string, blockType string, der []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600))
	return path
}

func generateTLSFixture(t *testing.T, collectorID string) (serverCert, serverKey, clientCert, clientKey, caFile string) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "ai-sre-agent-test-ca",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	serverKeyPair, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "controller.internal",
		},
		DNSNames:    []string{"controller.internal"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		URIs:        []*url.URL{{Scheme: "spiffe", Host: "ai-sre-agent", Path: "/controller"}},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKeyPair.PublicKey, caKey)
	require.NoError(t, err)

	clientKeyPair, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			CommonName: collectorID,
		},
		URIs:        []*url.URL{{Scheme: "spiffe", Host: "ai-sre-agent", Path: "/collector/" + collectorID}},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKeyPair.PublicKey, caKey)
	require.NoError(t, err)

	dir := t.TempDir()
	caFile = writePEMFile(t, dir, "ca.pem", "CERTIFICATE", caDER)
	serverCert = writePEMFile(t, dir, "server.pem", "CERTIFICATE", serverDER)
	serverKey = writePEMFile(t, dir, "server-key.pem", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKeyPair))
	clientCert = writePEMFile(t, dir, "client.pem", "CERTIFICATE", clientDER)
	clientKey = writePEMFile(t, dir, "client-key.pem", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(clientKeyPair))
	return serverCert, serverKey, clientCert, clientKey, caFile
}

func withBearerToken(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func TestPushIngestsTelemetryAndUpdatesStoreStats(t *testing.T) {
	store := NewMemoryStore()
	processor := &recordingProcessor{}
	server := NewServer(store, zap.NewNop(), processor)

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	batch := &telemetryv1.TelemetryBatch{
		BatchId: "batch-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-a",
			Hostname:    "node-a",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 77.5},
		},
		Processes: []*telemetryv1.ProcessSample{
			{Pid: 42, Name: "trainer", CpuPercent: 88.0},
		},
		Logs: []*telemetryv1.LogFingerprint{
			{Fingerprint: "oom", Example: "OOM killer invoked", Count: 1},
		},
	}

	require.NoError(t, stream.Send(batch))

	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-1", ack.BatchId)
	require.NoError(t, stream.CloseSend())

	node := store.Node("collector-a")
	require.NotNil(t, node)
	require.Equal(t, "node-a", node.Hostname)
	require.Equal(t, 1, node.MetricCount)
	require.Equal(t, 1, len(node.Processes))
	require.Equal(t, 1, len(node.Logs))
	require.Equal(t, 77.5, node.Metrics["node_cpu_usage_percent"])

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.BatchesTotal)
	require.Equal(t, uint64(1), stats.MetricsTotal)
	require.Equal(t, uint64(1), stats.ProcessesTotal)
	require.Equal(t, uint64(1), stats.LogsTotal)
	require.Equal(t, "collector-a", stats.LastCollector)
	require.Equal(t, "batch-1", stats.LastBatchID)

	calls, collectorID, batchID := processor.snapshot()
	require.Equal(t, 1, calls)
	require.Equal(t, "collector-a", collectorID)
	require.Equal(t, "batch-1", batchID)
}

func TestPushDeduplicatesRetriedBatchIDs(t *testing.T) {
	store := NewMemoryStore()
	processor := &recordingProcessor{}
	server := NewServer(store, zap.NewNop(), processor)

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	batch := &telemetryv1.TelemetryBatch{
		BatchId: "batch-dup-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-dup",
			Hostname:    "node-dup",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 55},
		},
	}

	require.NoError(t, stream.Send(batch))
	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-dup-1", ack.BatchId)

	require.NoError(t, stream.Send(batch))
	ack, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-dup-1", ack.BatchId)
	require.NoError(t, stream.CloseSend())

	node := store.Node("collector-dup")
	require.NotNil(t, node)
	require.Equal(t, 1, node.MetricCount)

	history := store.MetricHistory("collector-dup", time.Now().Add(-5*time.Minute), 10)
	require.Len(t, history, 1)

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.BatchesTotal)
	require.Equal(t, uint64(1), stats.DuplicatesTotal)

	calls, _, _ := processor.snapshot()
	require.Equal(t, 1, calls)
}

func TestPushRejectsInvalidBatchAndTracksRejections(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	invalid := &telemetryv1.TelemetryBatch{
		BatchId: "",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-a",
			Hostname:    "node-a",
		},
	}
	require.NoError(t, stream.Send(invalid))

	_, err = stream.Recv()
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.NoError(t, stream.CloseSend())

	stats := server.Stats()
	require.Equal(t, uint64(0), stats.BatchesTotal)
	require.Equal(t, uint64(1), stats.RejectedTotal)
	require.Contains(t, stats.LastError, "batch_id is required")
}

func TestPushRejectsWritesWhenWriteGuardBlocks(t *testing.T) {
	store := NewMemoryStore()
	processor := &recordingProcessor{}
	server := NewServer(store, zap.NewNop(), processor)
	server.SetWriteGuard(func() error {
		return errors.New("controller is not the active leader for gRPC ingest writes")
	})

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	batch := &telemetryv1.TelemetryBatch{
		BatchId: "batch-blocked-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-blocked",
			Hostname:    "node-blocked",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 72},
		},
	}

	require.NoError(t, stream.Send(batch))
	_, err = stream.Recv()
	require.Error(t, err)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.NoError(t, stream.CloseSend())

	require.Nil(t, store.Node("collector-blocked"))

	stats := server.Stats()
	require.Equal(t, uint64(0), stats.BatchesTotal)
	require.Equal(t, uint64(1), stats.RejectedTotal)
	require.Equal(t, uint64(1), stats.WriteGuardRejectionsTotal)
	require.Contains(t, stats.LastError, "active leader")

	calls, _, _ := processor.snapshot()
	require.Equal(t, 0, calls)
}

func TestPushRejectsUnauthenticatedStreams(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())
	server.SetAuthenticator(func(ctx context.Context) (*identity.Identity, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("authorization")) == 0 {
			return nil, errors.New("missing ingest token")
		}
		return nil, nil
	})

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-authn-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-a",
			Hostname:    "node-a",
		},
	}))

	_, err = stream.Recv()
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.RejectedTotal)
	require.Equal(t, uint64(1), stats.AuthnRejectedTotal)
	require.Contains(t, stats.LastError, "missing ingest token")
}

func TestPushRejectsCollectorIdentityMismatch(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())
	server.SetAuthenticator(func(ctx context.Context) (*identity.Identity, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if got := strings.TrimSpace(firstOrEmpty(md.Get("authorization"))); got != "Bearer collector-a-token" {
			return nil, errors.New("unexpected ingest token")
		}
		return &identity.Identity{
			Subject:     "collector-a",
			ActorType:   identity.ActorTypeService,
			Roles:       []identity.Role{identity.RoleCollector},
			AuthnMethod: identity.AuthnMethodIngestAuth,
			CollectorID: "collector-a",
		}, nil
	})

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(withBearerToken(context.Background(), "collector-a-token"), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-authz-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-b",
			Hostname:    "node-b",
		},
	}))

	_, err = stream.Recv()
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.RejectedTotal)
	require.Equal(t, uint64(1), stats.AuthzRejectedTotal)
	require.Equal(t, "collector-a", stats.LastAuthSubject)
	require.Contains(t, stats.LastError, "collector-a")
}

func TestPushAcceptsExplicitWildcardCollectorServiceToken(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())
	server.SetAuthenticator(func(ctx context.Context) (*identity.Identity, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if got := strings.TrimSpace(firstOrEmpty(md.Get("authorization"))); got != "Bearer shared-collector-token" {
			return nil, errors.New("unexpected ingest token")
		}
		return &identity.Identity{
			Subject:     "collector-daemonset",
			ActorType:   identity.ActorTypeService,
			Roles:       []identity.Role{identity.RoleCollector},
			AuthnMethod: identity.AuthnMethodIngestAuth,
			CollectorID: "*",
		}, nil
	})

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(withBearerToken(context.Background(), "shared-collector-token"), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-authz-2",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-z",
			Hostname:    "node-z",
		},
	}))

	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-authz-2", ack.GetBatchId())

	stats := server.Stats()
	require.Equal(t, "collector-daemonset", stats.LastAuthSubject)
}

func TestPushMTLSAcceptsCollectorMatchingClientCert(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())

	serverCert, serverKey, clientCert, clientKey, caFile := generateTLSFixture(t, "collector-a")
	serverTLS, err := loadServerTLSConfigForTest(serverCert, serverKey, caFile, true)
	require.NoError(t, err)
	addr, cleanup := startIngestGRPCServerWithOptions(t, server, grpc.Creds(credentials.NewTLS(serverTLS)))
	defer cleanup()

	clientCreds, err := loadClientTLSConfigForTest(clientCert, clientKey, caFile, "controller.internal")
	require.NoError(t, err)
	conn, client := dialIngestClientWithCreds(t, addr, clientCreds)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-mtls-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-a",
			Hostname:    "node-a",
		},
	}))

	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-mtls-1", ack.GetBatchId())

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.BatchesTotal)
	require.Equal(t, "collector-a", stats.LastPeerSubject)
}

func TestPushMTLSRejectsCollectorIdentityMismatch(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())

	serverCert, serverKey, clientCert, clientKey, caFile := generateTLSFixture(t, "collector-a")
	serverTLS, err := loadServerTLSConfigForTest(serverCert, serverKey, caFile, true)
	require.NoError(t, err)
	addr, cleanup := startIngestGRPCServerWithOptions(t, server, grpc.Creds(credentials.NewTLS(serverTLS)))
	defer cleanup()

	clientCreds, err := loadClientTLSConfigForTest(clientCert, clientKey, caFile, "controller.internal")
	require.NoError(t, err)
	conn, client := dialIngestClientWithCreds(t, addr, clientCreds)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-mtls-2",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-b",
			Hostname:    "node-b",
		},
	}))

	_, err = stream.Recv()
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.RejectedTotal)
	require.Equal(t, uint64(1), stats.IdentityMismatchTotal)
	require.Equal(t, "collector-a", stats.LastPeerSubject)
}

func TestPushRejectsCollectorOutsideAllowlist(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())
	server.SetAccessPolicy(AccessPolicy{AllowedCollectorIDs: []string{"collector-a"}})

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-allowlist-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-b",
			Hostname:    "node-b",
		},
	}))

	_, err = stream.Recv()
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.AllowlistRejectedTotal)
	require.Contains(t, stats.LastError, "allowlist")
}

func loadServerTLSConfigForTest(certFile, keyFile, caFile string, requireClientCert bool) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if requireClientCert {
		caBytes, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, errors.New("append client ca")
		}
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = pool
	}
	return cfg, nil
}

func loadClientTLSConfigForTest(certFile, keyFile, caFile, serverName string) (credentials.TransportCredentials, error) {
	caBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("append ca")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      pool,
		ServerName:   serverName,
		Certificates: []tls.Certificate{cert},
	}), nil
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func TestPushRecoversFromProcessorPanic(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop(), &panicProcessor{})

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	batch := &telemetryv1.TelemetryBatch{
		BatchId: "batch-panic-safe",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-panic-safe",
			Hostname:    "node-panic-safe",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 10},
		},
	}

	require.NoError(t, stream.Send(batch))
	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-panic-safe", ack.BatchId)
	require.NoError(t, stream.CloseSend())

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.BatchesTotal)
	require.Equal(t, uint64(0), stats.RejectedTotal)
}

func TestPushCarriesForwardSuppressedAuxPayloadsAndClearsOnExplicitRefresh(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-aux-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-aux",
			Hostname:    "node-aux",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 42},
		},
		Processes: []*telemetryv1.ProcessSample{
			{Pid: 321, Name: "trainer", CpuPercent: 70},
		},
		Logs: []*telemetryv1.LogFingerprint{
			{Fingerprint: "oom", Example: "OOM killer invoked", Count: 1},
		},
	}))
	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-aux-1", ack.BatchId)

	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-aux-2",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-aux",
			Hostname:    "node-aux",
		},
		Metrics: []*telemetryv1.Metric{
			{
				Name:  "collector_aux_payload_suppressed",
				Value: 1,
				Labels: []*telemetryv1.Label{
					{Key: "component", Value: "process_fallback"},
				},
			},
			{
				Name:  "collector_aux_payload_suppressed",
				Value: 1,
				Labels: []*telemetryv1.Label{
					{Key: "component", Value: "logs"},
				},
			},
		},
	}))
	ack, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-aux-2", ack.BatchId)

	node := store.Node("collector-aux")
	require.NotNil(t, node)
	require.Len(t, node.Processes, 1)
	require.Len(t, node.Logs, 1)

	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-aux-3",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-aux",
			Hostname:    "node-aux",
		},
		Metrics: []*telemetryv1.Metric{
			{
				Name:  "collector_aux_payload_refreshed",
				Value: 1,
				Labels: []*telemetryv1.Label{
					{Key: "component", Value: "process_fallback"},
				},
			},
			{
				Name:  "collector_aux_payload_refreshed",
				Value: 1,
				Labels: []*telemetryv1.Label{
					{Key: "component", Value: "logs"},
				},
			},
		},
	}))
	ack, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-aux-3", ack.BatchId)
	require.NoError(t, stream.CloseSend())

	node = store.Node("collector-aux")
	require.NotNil(t, node)
	require.Len(t, node.Processes, 0)
	require.Len(t, node.Logs, 0)
}

func TestPushCarriesForwardSuppressedCompatibilityHardwareMetrics(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-compat-hw-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-compat-hw",
			Hostname:    "node-compat-hw",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 41},
			{Name: "node_thermal_zone_temp_celsius", Value: 87.5},
			{Name: "node_network_interface_speed_mbps", Value: 25000, Labels: []*telemetryv1.Label{
				{Key: "device", Value: "eth0"},
			}},
		},
	}))
	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-compat-hw-1", ack.BatchId)

	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-compat-hw-2",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-compat-hw",
			Hostname:    "node-compat-hw",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 44},
			{
				Name:  "collector_compat_payload_suppressed",
				Value: 1,
				Labels: []*telemetryv1.Label{
					{Key: "component", Value: "hardware"},
				},
			},
		},
	}))
	ack, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-compat-hw-2", ack.BatchId)

	node := store.Node("collector-compat-hw")
	require.NotNil(t, node)
	require.Equal(t, 44.0, node.Metrics["node_cpu_usage_percent"])
	require.Equal(t, 87.5, node.Metrics["node_thermal_zone_temp_celsius"])
	require.Equal(t, 25000.0, node.Metrics["node_network_interface_speed_mbps"])

	require.NoError(t, stream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-compat-hw-3",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-compat-hw",
			Hostname:    "node-compat-hw",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 38},
			{
				Name:  "collector_compat_payload_refreshed",
				Value: 1,
				Labels: []*telemetryv1.Label{
					{Key: "component", Value: "hardware"},
				},
			},
		},
	}))
	ack, err = stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-compat-hw-3", ack.BatchId)
	require.NoError(t, stream.CloseSend())

	node = store.Node("collector-compat-hw")
	require.NotNil(t, node)
	require.Equal(t, 38.0, node.Metrics["node_cpu_usage_percent"])
	_, ok := node.Metrics["node_thermal_zone_temp_celsius"]
	require.False(t, ok)
	_, ok = node.Metrics["node_network_interface_speed_mbps"]
	require.False(t, ok)
}

func TestPushMixedStreamUpdatesAcceptedAndRejectedStats(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)

	valid := &telemetryv1.TelemetryBatch{
		BatchId: "batch-valid-1",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-mixed",
			Hostname:    "node-mixed",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 40},
		},
	}
	require.NoError(t, stream.Send(valid))

	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-valid-1", ack.BatchId)

	invalid := &telemetryv1.TelemetryBatch{
		BatchId: "",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-mixed",
			Hostname:    "node-mixed",
		},
	}
	require.NoError(t, stream.Send(invalid))

	_, err = stream.Recv()
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.NoError(t, stream.CloseSend())

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.BatchesTotal)
	require.Equal(t, uint64(1), stats.RejectedTotal)
	require.Equal(t, "collector-mixed", stats.LastCollector)
	require.Equal(t, "batch-valid-1", stats.LastBatchID)
	require.Contains(t, stats.LastError, "batch_id is required")
}

func TestPushRejectsNilMetricLabelThenAcceptsNextStream(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())

	addr, cleanup := startIngestGRPCServer(t, server)
	defer cleanup()

	conn, client := dialIngestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	invalidStream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, invalidStream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-invalid-label",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-invalid-label",
			Hostname:    "node-invalid-label",
		},
		Metrics: []*telemetryv1.Metric{
			{
				Name:   "node_cpu_usage_percent",
				Value:  88,
				Labels: []*telemetryv1.Label{nil},
			},
		},
	}))
	_, err = invalidStream.Recv()
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, err.Error(), "label key is required")
	require.NoError(t, invalidStream.CloseSend())

	validStream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, validStream.Send(&telemetryv1.TelemetryBatch{
		BatchId: "batch-valid-after-invalid-label",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-recovered-label",
			Hostname:    "node-recovered-label",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: 41},
		},
	}))
	ack, err := validStream.Recv()
	require.NoError(t, err)
	require.Equal(t, "batch-valid-after-invalid-label", ack.BatchId)
	require.NoError(t, validStream.CloseSend())

	stats := server.Stats()
	require.Equal(t, uint64(1), stats.BatchesTotal)
	require.Equal(t, uint64(1), stats.RejectedTotal)
	require.Equal(t, "collector-recovered-label", stats.LastCollector)
	require.Equal(t, "batch-valid-after-invalid-label", stats.LastBatchID)
	require.Contains(t, stats.LastError, "label key is required")
}
