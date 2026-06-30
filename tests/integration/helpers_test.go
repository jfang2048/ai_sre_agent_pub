package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const (
	bufConnSize      = 1024 * 1024
	defaultTestDelay = 3 * time.Second
)

func startIngestBufConn(t *testing.T, store *ingest.MemoryStore) (telemetryv1.TelemetryIngestClient, func()) {
	t.Helper()

	server := ingest.NewServer(store, zap.NewNop())
	grpcServer := grpc.NewServer()
	server.Register(grpcServer)

	listener := bufconn.Listen(bufConnSize)
	go func() {
		_ = grpcServer.Serve(listener)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestDelay)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithBlock(),
	)
	require.NoError(t, err)

	cleanup := func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close grpc conn: %v", err)
		}
		grpcServer.Stop()
		if err := listener.Close(); err != nil {
			t.Errorf("close bufconn listener: %v", err)
		}
	}

	return telemetryv1.NewTelemetryIngestClient(conn), cleanup
}

func pushBatch(t *testing.T, client telemetryv1.TelemetryIngestClient, batch *telemetryv1.TelemetryBatch) (*telemetryv1.Ack, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestDelay)
	defer cancel()

	stream, err := client.Push(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(batch))

	ack, recvErr := stream.Recv()
	_ = stream.CloseSend()
	return ack, recvErr
}
