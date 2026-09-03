package transport

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestNewRejectsMissingEndpoints(t *testing.T) {
	_, err := New(Config{}, zap.NewNop())
	require.Error(t, err)

	var typed *Error
	require.ErrorAs(t, err, &typed)
	require.Equal(t, ErrorKindConfig, typed.Kind)
}

func TestSendFailoverToSecondEndpoint(t *testing.T) {
	client, err := New(Config{
		Endpoints:      []string{"first:9090", "second:9090"},
		AllowPlaintext: true,
	}, zap.NewNop())
	require.NoError(t, err)

	client.sendToEndpointFn = func(ctx context.Context, attempt int, endpoint string, payload []byte) (*telemetryv1.Ack, error) {
		if endpoint == "first:9090" {
			return nil, &Error{Kind: ErrorKindSend, Endpoint: endpoint, Attempt: attempt, Err: errors.New("boom")}
		}
		return &telemetryv1.Ack{BatchId: "ok"}, nil
	}

	ack, err := client.Send(context.Background(), []byte("payload"))
	require.NoError(t, err)
	require.Equal(t, "ok", ack.BatchId)
	require.Equal(t, uint64(1), client.LastErrs())
	require.Equal(t, uint64(1), client.LastRetries())
}

func TestSendMirrorAllFailReturnsRetryExhausted(t *testing.T) {
	client, err := New(Config{
		Endpoints:      []string{"a:9090", "b:9090"},
		Mirror:         true,
		AllowPlaintext: true,
	}, zap.NewNop())
	require.NoError(t, err)

	client.sendToEndpointFn = func(ctx context.Context, attempt int, endpoint string, payload []byte) (*telemetryv1.Ack, error) {
		return nil, &Error{Kind: ErrorKindDial, Endpoint: endpoint, Attempt: attempt, Err: errors.New("down")}
	}

	_, err = client.Send(context.Background(), []byte("payload"))
	require.Error(t, err)

	var typed *Error
	require.ErrorAs(t, err, &typed)
	require.Equal(t, ErrorKindRetryExhaust, typed.Kind)
}

func TestApplyConfigRejectsEmptyEndpoints(t *testing.T) {
	client, err := New(Config{Endpoints: []string{"a:9090"}, AllowPlaintext: true}, zap.NewNop())
	require.NoError(t, err)

	err = client.ApplyConfig(Config{})
	require.Error(t, err)
}

func TestNormalizeEndpointsDeduplicatesAndTrims(t *testing.T) {
	got := normalizeEndpoints([]string{" a:1 ", "a:1", "", "b:2"})
	require.Equal(t, []string{"a:1", "b:2"}, got)
}

func TestCurrentTransportCredentialsReloads(t *testing.T) {
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")
	require.NoError(t, os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----\n"), 0o644))

	client, err := New(Config{Endpoints: []string{"a:9090"}, AllowPlaintext: true}, zap.NewNop())
	require.NoError(t, err)

	_, err = client.currentTransportCredentials(TLSConfig{
		Enabled:            true,
		CAFile:             caPath,
		InsecureSkipVerify: true,
		ReloadInterval:     10 * time.Millisecond,
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid PEM")
}

func TestDecodeBatchPayloadRejectsMissingBatchID(t *testing.T) {
	payload, err := proto.Marshal(&telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{CollectorId: "node-a"},
	})
	require.NoError(t, err)

	_, err = decodeBatchPayload(payload)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidBatch)
	require.ErrorContains(t, err, "batch_id")
}

func TestDecodeBatchPayloadAcceptsValidPayload(t *testing.T) {
	payload, err := proto.Marshal(&telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{CollectorId: "node-a"},
		BatchId:   "node-a-1",
	})
	require.NoError(t, err)

	batch, err := decodeBatchPayload(payload)
	require.NoError(t, err)
	require.Equal(t, "node-a-1", batch.BatchId)
	require.Equal(t, "node-a", batch.Collector.CollectorId)
}

func TestValidateAckRejectsMissingBatchID(t *testing.T) {
	err := validateAck("batch-a", &telemetryv1.Ack{})
	require.ErrorIs(t, err, ErrEmptyAckBatchID)
}

func TestValidateAckRejectsMismatchedBatchID(t *testing.T) {
	err := validateAck("batch-a", &telemetryv1.Ack{BatchId: "batch-b"})
	require.ErrorIs(t, err, ErrUnexpectedAckBatchID)
	require.ErrorContains(t, err, "expected \"batch-a\" got \"batch-b\"")
}

func TestValidateAckAcceptsMatchingBatchID(t *testing.T) {
	err := validateAck("batch-a", &telemetryv1.Ack{BatchId: "batch-a"})
	require.NoError(t, err)
}

func TestIsPermanentPayloadError(t *testing.T) {
	require.True(t, IsPermanentPayloadError(&Error{Kind: ErrorKindDecode, Err: errors.New("bad payload")}))
	require.True(t, IsPermanentPayloadError(&Error{
		Kind: ErrorKindReceive,
		Err:  status.Error(codes.InvalidArgument, "label value too long"),
	}))
	require.True(t, IsPermanentPayloadError(&Error{
		Kind: ErrorKindRetryExhaust,
		Err: errors.Join(
			&Error{Kind: ErrorKindReceive, Err: status.Error(codes.InvalidArgument, "label value too long")},
		),
	}))
	require.False(t, IsPermanentPayloadError(&Error{
		Kind: ErrorKindReceive,
		Err:  status.Error(codes.Unavailable, "controller down"),
	}))
}

func TestDrainDropsPermanentInvalidPayloadAndContinues(t *testing.T) {
	client, err := New(Config{Endpoints: []string{"controller:9090"}, AllowPlaintext: true}, zap.NewNop())
	require.NoError(t, err)

	sp := newTestSpoolWithBatches(t,
		&telemetryv1.TelemetryBatch{
			BatchId:   "bad-batch",
			Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-a"},
		},
		&telemetryv1.TelemetryBatch{
			BatchId:   "good-batch",
			Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-a"},
		},
	)

	attempted := make([]string, 0, 2)
	err = client.Drain(context.Background(), sp, func(payload []byte) (string, error) {
		var batch telemetryv1.TelemetryBatch
		require.NoError(t, proto.Unmarshal(payload, &batch))
		attempted = append(attempted, batch.GetBatchId())
		if batch.GetBatchId() == "bad-batch" {
			return "", &Error{
				Kind: ErrorKindReceive,
				Err:  status.Error(codes.InvalidArgument, "metric[834]: label value too long"),
			}
		}
		return batch.GetBatchId(), nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"bad-batch", "good-batch"}, attempted)

	payload, _, err := sp.Next()
	require.NoError(t, err)
	require.Nil(t, payload)
}

func newTestSpoolWithBatches(t *testing.T, batches ...*telemetryv1.TelemetryBatch) *spool.Spool {
	t.Helper()

	sp, err := spool.New(t.TempDir(), 1<<20)
	require.NoError(t, err)
	for _, batch := range batches {
		payload, err := proto.Marshal(batch)
		require.NoError(t, err)
		require.NoError(t, sp.Enqueue(payload))
	}
	return sp
}

type authCheckingIngestServer struct {
	telemetryv1.UnimplementedTelemetryIngestServer
	t          *testing.T
	wantBearer string
}

func (s *authCheckingIngestServer) Push(stream telemetryv1.TelemetryIngest_PushServer) error {
	s.t.Helper()
	md, ok := metadata.FromIncomingContext(stream.Context())
	require.True(s.t, ok)
	require.Equal(s.t, "Bearer "+s.wantBearer, firstMetadataValue(md.Get("authorization")))

	batch, err := stream.Recv()
	require.NoError(s.t, err)
	require.Equal(s.t, "batch-auth-header", batch.GetBatchId())
	require.NoError(s.t, stream.Send(&telemetryv1.Ack{BatchId: batch.GetBatchId()}))
	return nil
}

func TestSendToEndpointAttachesBearerTokenMetadata(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	server := grpc.NewServer()
	telemetryv1.RegisterTelemetryIngestServer(server, &authCheckingIngestServer{
		t:          t,
		wantBearer: "collector-token",
	})
	defer server.Stop()
	go server.Serve(listener)

	client, err := New(Config{
		Endpoints:      []string{listener.Addr().String()},
		AllowPlaintext: true,
		Auth: AuthConfig{
			Enabled:     true,
			BearerToken: "collector-token",
		},
	}, zap.NewNop())
	require.NoError(t, err)
	defer client.Close()

	payload, err := proto.Marshal(&telemetryv1.TelemetryBatch{
		BatchId:   "batch-auth-header",
		Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-a"},
	})
	require.NoError(t, err)

	ack, err := client.Send(context.Background(), payload)
	require.NoError(t, err)
	require.Equal(t, "batch-auth-header", ack.GetBatchId())
}

func firstMetadataValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
