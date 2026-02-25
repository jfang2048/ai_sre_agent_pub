package transport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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
		Endpoints: []string{"first:9090", "second:9090"},
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
		Endpoints: []string{"a:9090", "b:9090"},
		Mirror:    true,
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
	client, err := New(Config{Endpoints: []string{"a:9090"}}, zap.NewNop())
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

	client, err := New(Config{Endpoints: []string{"a:9090"}}, zap.NewNop())
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
