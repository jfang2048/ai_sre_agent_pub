package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func testReceipt(t *testing.T, sequence uint64) Receipt {
	t.Helper()
	batch := &telemetryv1.TelemetryBatch{
		BatchId: fmt.Sprintf("batch-%d", sequence), ProducerEpoch: "test-epoch", ProducerSequence: sequence,
		Collector: &telemetryv1.CollectorInfo{CollectorId: "test-collector", Hostname: "test-node"},
		Metrics:   []*telemetryv1.Metric{{Name: "node_cpu_usage_percent", Value: float64(sequence % 100)}},
	}
	receipt, err := receiptFromBatch(batch, time.Unix(1_800_000_000, int64(sequence%100_000)*1000))
	require.NoError(t, err)
	return receipt
}

// PostgreSQL is genuinely exercised when configured. A missing database is a
// visible skip in ordinary CPU tests; the required integration target rejects it.
func testPostgresDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("not run: SRE_TEST_POSTGRES_DSN is required for real PostgreSQL tests")
	}
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	admin := stdlib.OpenDB(*config)
	schema := fmt.Sprintf("ingest_test_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(context.Background(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	require.NoError(t, err)
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = db.Close()
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		if err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
		_ = admin.Close()
	})
	return db
}

func testInbox(t *testing.T, backend string, cfg InboxConfig) Inbox {
	t.Helper()
	cfg.Backend = backend
	if cfg.Path == "" {
		cfg.Path = filepath.Join(t.TempDir(), "inbox.db")
	}
	var inbox Inbox
	var err error
	if backend == InboxBackendPostgres {
		inbox, err = NewPostgresInboxWithDB(context.Background(), cfg, testPostgresDB(t), zap.NewNop())
	} else {
		inbox, err = OpenInbox(context.Background(), cfg, zap.NewNop())
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = inbox.Close() })
	return inbox
}

func TestInboxContract(t *testing.T) {
	for _, backend := range []string{InboxBackendMemory, InboxBackendBbolt, InboxBackendPostgres} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			inbox := testInbox(t, backend, InboxConfig{MaxRecords: 3})
			receipt := testReceipt(t, math.MaxUint64)
			stored, created, err := inbox.Commit(ctx, receipt)
			require.NoError(t, err)
			require.True(t, created)
			stored.Payload[0] ^= 0xff
			stored, created, err = inbox.Commit(ctx, receipt)
			require.NoError(t, err)
			require.False(t, created)
			require.Equal(t, receipt.Payload, stored.Payload)
			changed := cloneReceipt(receipt)
			changed.Payload[0] ^= 1
			_, _, err = inbox.Commit(ctx, changed)
			require.ErrorIs(t, err, ErrIdentityConflict)
			now := time.Now().UTC()
			claimed, ok, err := inbox.Claim(ctx, receipt.Identity, "first", now, time.Second)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, uint64(2), claimed.Revision)
			_, ok, err = inbox.Claim(ctx, receipt.Identity, "first", now, time.Second)
			require.NoError(t, err)
			require.False(t, ok, "same owner cannot claim twice")
			_, ok, err = inbox.Claim(ctx, receipt.Identity, "second", now.Add(2*time.Second), time.Second)
			require.NoError(t, err)
			require.True(t, ok)
			require.ErrorIs(t, inbox.MarkApplied(ctx, receipt.Identity, "first", now), ErrLeaseLost)
			require.NoError(t, inbox.MarkApplied(ctx, receipt.Identity, "second", now))
			_, ok, err = inbox.Claim(ctx, receipt.Identity, "third", now.Add(time.Hour), time.Second)
			require.NoError(t, err)
			require.False(t, ok)
			retained, err := inbox.Retained(ctx, "", 1)
			require.NoError(t, err)
			require.Len(t, retained, 1)
			require.Equal(t, uint64(math.MaxUint64), retained[0].ProducerSequence)
			next, err := inbox.Retained(ctx, receiptOrder(retained[0]), 1)
			require.NoError(t, err)
			require.Empty(t, next)
			pending := testReceipt(t, 2)
			_, _, err = inbox.Commit(ctx, pending)
			require.NoError(t, err)
			removed, err := inbox.Prune(ctx, now.Add(time.Second), 1)
			require.NoError(t, err)
			require.Equal(t, 1, removed)
			_, err = inbox.Get(ctx, pending.Identity)
			require.NoError(t, err, "never prune unapplied data")
			stats := inbox.Stats(ctx)
			require.Equal(t, int64(1), stats.Records)
			require.Equal(t, int64(len(pending.Payload)), stats.PayloadBytes)
		})
	}
}

func TestValidateReceiptRejectsNonInitialAttemptCounter(t *testing.T) {
	receipt := testReceipt(t, 1)
	receipt.Attempts = 1
	require.Error(t, validateReceipt(receipt))
}

func TestInboxBoundsPreserveDedupeWindow(t *testing.T) {
	for _, backend := range []string{InboxBackendMemory, InboxBackendBbolt, InboxBackendPostgres} {
		for _, bound := range []string{"records", "bytes"} {
			t.Run(backend+"/"+bound, func(t *testing.T) {
				ctx := context.Background()
				first := testReceipt(t, 1)
				cfg := InboxConfig{MaxRecords: 1}
				if bound == "bytes" {
					cfg.MaxRecords = 100
					cfg.MaxBytes = int64(len(first.Payload))
				}
				inbox := testInbox(t, backend, cfg)
				_, _, err := inbox.Commit(ctx, first)
				require.NoError(t, err)
				_, _, err = inbox.Claim(ctx, first.Identity, "writer", time.Now(), time.Second)
				require.NoError(t, err)
				require.NoError(t, inbox.MarkApplied(ctx, first.Identity, "writer", time.Now()))
				_, _, err = inbox.Commit(ctx, testReceipt(t, 2))
				require.ErrorIs(t, err, ErrInboxFull)
				_, created, err := inbox.Commit(ctx, first)
				require.NoError(t, err)
				require.False(t, created)
				stats := inbox.Stats(ctx)
				require.Equal(t, int64(1), stats.Records)
				require.LessOrEqual(t, stats.PayloadBytes, stats.MaxBytes)
			})
		}
	}
}

type singleBatchStream struct {
	grpc.ServerStream
	ctx   context.Context
	batch *telemetryv1.TelemetryBatch
	ack   *telemetryv1.Ack
}

func (s *singleBatchStream) Context() context.Context { return s.ctx }
func (s *singleBatchStream) Recv() (*telemetryv1.TelemetryBatch, error) {
	if s.batch == nil {
		return nil, io.EOF
	}
	batch := s.batch
	s.batch = nil
	return batch, nil
}
func (s *singleBatchStream) Send(ack *telemetryv1.Ack) error { s.ack = ack; return nil }

func deliverReceipt(ctx context.Context, server *Server, receipt Receipt) (*telemetryv1.Ack, error) {
	batch, err := decodeReceiptBatch(receipt)
	if err != nil {
		return nil, err
	}
	stream := &singleBatchStream{ctx: ctx, batch: batch}
	err = server.Push(stream)
	return stream.ack, err
}

func TestInboxConcurrentControllersMaterializeOnce(t *testing.T) {
	for _, backend := range []string{InboxBackendMemory, InboxBackendBbolt, InboxBackendPostgres} {
		t.Run(backend, func(t *testing.T) {
			inbox := testInbox(t, backend, InboxConfig{})
			second := inbox
			if pg, ok := inbox.(*postgresInbox); ok {
				var err error
				second, err = NewPostgresInboxWithDB(context.Background(), pg.cfg, pg.db, zap.NewNop())
				require.NoError(t, err, "idempotent migration for second controller")
			}
			processor := &recordingProcessor{}
			servers := []*Server{NewServerWithInbox(NewMemoryStore(), inbox, zap.NewNop(), processor), NewServerWithInbox(NewMemoryStore(), second, zap.NewNop(), processor)}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			receipt := testReceipt(t, 1)
			var wait sync.WaitGroup
			errors := make(chan error, 32)
			for i := 0; i < 32; i++ {
				wait.Add(1)
				go func(i int) {
					defer wait.Done()
					ack, err := deliverReceipt(ctx, servers[i%2], receipt)
					if err == nil && (ack == nil || ack.BatchId != receipt.BatchID) {
						err = fmt.Errorf("missing/mismatched ACK")
					}
					errors <- err
				}(i)
			}
			wait.Wait()
			close(errors)
			for err := range errors {
				require.NoError(t, err)
			}
			calls, _, _ := processor.snapshot()
			require.Equal(t, 1, calls)
			require.Equal(t, int64(1), inbox.Stats(ctx).Applied)
		})
	}
}

func TestIngestCrashMatrix(t *testing.T) {
	for _, point := range []IngestFaultPoint{FaultBeforeInboxCommit, FaultAfterInboxCommit, FaultBeforeMaterialize, FaultAfterMaterialize, FaultAfterApplied, FaultBeforeACK, FaultAfterACK} {
		t.Run(string(point), func(t *testing.T) {
			cfg := InboxConfig{Backend: InboxBackendBbolt, Path: filepath.Join(t.TempDir(), "inbox.db"), Lease: time.Millisecond}
			ctx := context.Background()
			inbox, err := OpenInbox(ctx, cfg, zap.NewNop())
			require.NoError(t, err)
			server := NewServerWithInbox(NewMemoryStore(), inbox, zap.NewNop())
			server.SetInboxPolicy(cfg)
			server.SetFaultInjector(func(at IngestFaultPoint) error {
				if at == point {
					return errors.New("injected process termination")
				}
				return nil
			})
			receipt := testReceipt(t, 1)
			ack, err := deliverReceipt(ctx, server, receipt)
			require.Error(t, err)
			if ack != nil {
				stored, err := inbox.Get(ctx, receipt.Identity)
				require.NoError(t, err)
				require.Equal(t, ReceiptApplied, stored.State)
			}
			require.NoError(t, inbox.Close())
			inbox, err = OpenInbox(ctx, cfg, zap.NewNop())
			require.NoError(t, err)
			defer inbox.Close()
			hot := NewMemoryStore()
			server = NewServerWithInbox(hot, inbox, zap.NewNop())
			server.SetInboxPolicy(cfg)
			require.NoError(t, server.RestoreHotState(ctx))
			ack, err = deliverReceipt(ctx, server, receipt)
			require.NoError(t, err)
			require.Equal(t, receipt.BatchID, ack.BatchId)
			require.NoError(t, server.ReplayPending(ctx))
			stored, err := inbox.Get(ctx, receipt.Identity)
			require.NoError(t, err)
			require.Equal(t, ReceiptApplied, stored.State)
			require.Equal(t, 1, hot.Stats().HistorySamples)
		})
	}
}

func TestIngestReplayOlderPendingPreservesLatestSnapshot(t *testing.T) {
	ctx := context.Background()
	inbox := testInbox(t, InboxBackendBbolt, InboxConfig{})
	older := testReceipt(t, 1)
	newer := testReceipt(t, 2)
	_, _, err := inbox.Commit(ctx, older)
	require.NoError(t, err)
	_, _, err = inbox.Commit(ctx, newer)
	require.NoError(t, err)
	_, claimed, err := inbox.Claim(ctx, newer.Identity, "before-restart", time.Now(), time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, inbox.MarkApplied(ctx, newer.Identity, "before-restart", time.Now()))

	hot := NewMemoryStore()
	processor := &recordingProcessor{}
	server := NewServerWithInbox(hot, inbox, zap.NewNop(), processor)
	require.NoError(t, server.RestoreHotState(ctx))
	require.NoError(t, server.ReplayPending(ctx))
	node := hot.Node(newer.CollectorID)
	require.Equal(t, newer.BatchID, node.LastBatchID)
	require.Equal(t, newer.AcceptedAt, node.LastIngestAt)
	require.Equal(t, 2.0, node.Metrics["node_cpu_usage_percent"])
	history := hot.MetricHistory(newer.CollectorID, time.Time{}, 10)
	require.Len(t, history, 2, "older replay must still retain its history point")
	require.Equal(t, 1.0, history[0].Metrics["node_cpu_usage_percent"])
	stored, err := inbox.Get(ctx, older.Identity)
	require.NoError(t, err)
	require.Equal(t, ReceiptApplied, stored.State)
	calls, _, _ := processor.snapshot()
	require.Equal(t, 1, calls)
	require.NoError(t, hot.SetRetention(time.Hour, 1))
	batch, err := decodeReceiptBatch(older)
	require.NoError(t, err)
	hot.StoreHistoricalBatch(older.CollectorID, batch, older.AcceptedAt)
	history = hot.MetricHistory(newer.CollectorID, time.Time{}, 10)
	require.Len(t, history, 1)
	require.Equal(t, 2.0, history[0].Metrics["node_cpu_usage_percent"], "old replay must not evict newer history")
}

func TestIngestProcessTerminationAfterACK(t *testing.T) {
	const crashEnv = "SRE_TEST_INGEST_CRASH_PATH"
	if path := os.Getenv(crashEnv); path != "" {
		inbox, err := OpenInbox(context.Background(), InboxConfig{Path: path}, zap.NewNop())
		if err != nil {
			os.Exit(31)
		}
		server := NewServerWithInbox(NewMemoryStore(), inbox, zap.NewNop())
		ack, err := deliverReceipt(context.Background(), server, testReceipt(t, 1))
		if err != nil || ack == nil {
			os.Exit(32)
		}
		// No deferred Close or graceful shutdown is allowed to help the commit.
		os.Exit(23)
	}
	path := filepath.Join(t.TempDir(), "inbox.db")
	command := exec.Command(os.Args[0], "-test.run=^TestIngestProcessTerminationAfterACK$")
	command.Env = append(os.Environ(), crashEnv+"="+path)
	err := command.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 23, exitErr.ExitCode())
	inbox := testInbox(t, InboxBackendBbolt, InboxConfig{Path: path})
	stored, err := inbox.Get(context.Background(), testReceipt(t, 1).Identity)
	require.NoError(t, err)
	require.Equal(t, ReceiptApplied, stored.State)
	hot := NewMemoryStore()
	require.NoError(t, NewServerWithInbox(hot, inbox, zap.NewNop()).RestoreHotState(context.Background()))
	require.Equal(t, 1, hot.Stats().HistorySamples)
}

func TestIngestTenThousandBatchesAcrossRestarts(t *testing.T) {
	const total = 10_000
	ctx := context.Background()
	dir := t.TempDir()
	cfg := InboxConfig{Backend: InboxBackendBbolt, Path: filepath.Join(dir, "inbox.db"), MaxRecords: total, MaxBytes: 8 << 20}
	var observedACKs atomic.Int64
	processor := &recordingProcessor{}
	for cycle := 0; cycle < 20; cycle++ {
		inbox, err := OpenInbox(ctx, cfg, zap.NewNop())
		require.NoError(t, err)
		hot := NewMemoryStoreWithConfig(StoreConfig{HistorySamplesPerNode: total}, zap.NewNop())
		server := NewServerWithInbox(hot, inbox, zap.NewNop(), processor)
		server.SetInboxPolicy(cfg)
		require.NoError(t, server.RestoreHotState(ctx))
		queue, err := spool.New(filepath.Join(dir, "spool"), 1<<20)
		require.NoError(t, err)
		for i := cycle*500 + 1; i <= (cycle+1)*500; i++ {
			receipt := testReceipt(t, uint64(i))
			require.NoError(t, queue.Enqueue(receipt.Payload))
			payload, offset, err := queue.Next()
			require.NoError(t, err)
			var batch telemetryv1.TelemetryBatch
			require.NoError(t, proto.Unmarshal(payload, &batch))
			stream := &singleBatchStream{ctx: ctx, batch: &batch}
			require.NoError(t, server.Push(stream))
			require.Equal(t, receipt.BatchID, stream.ack.BatchId)
			observedACKs.Add(1)
			// Duplicate delivery is real ingest, not a mocked dedupe result.
			_, err = deliverReceipt(ctx, server, receipt)
			require.NoError(t, err)
			if i%500 == 0 {
				// Restart after observing ACK but before persisting the cursor.
				require.NoError(t, queue.Close())
				queue, err = spool.New(filepath.Join(dir, "spool"), 1<<20)
				require.NoError(t, err)
				_, offset, err = queue.Next()
				require.NoError(t, err)
				_, err = deliverReceipt(ctx, server, receipt)
				require.NoError(t, err)
			}
			require.NoError(t, queue.Commit(offset))
		}
		require.LessOrEqual(t, queue.Snapshot().FileSizeBytes, int64(1<<20))
		require.NoError(t, queue.Close())
		stats := inbox.Stats(ctx)
		require.Equal(t, observedACKs.Load(), stats.Applied)
		require.LessOrEqual(t, stats.PayloadBytes, cfg.MaxBytes)
		require.LessOrEqual(t, stats.Records, int64(cfg.MaxRecords))
		require.Equal(t, int(observedACKs.Load()), hot.Stats().HistorySamples)
		require.NoError(t, inbox.Close())
	}
	final := testInbox(t, InboxBackendBbolt, cfg)
	for i := 1; i <= total; i++ {
		receipt := testReceipt(t, uint64(i))
		stored, err := final.Get(ctx, receipt.Identity)
		require.NoError(t, err)
		require.Equal(t, ReceiptApplied, stored.State)
	}
	calls, _, _ := processor.snapshot()
	require.Equal(t, total, calls)
	stat, err := os.Stat(cfg.Path)
	require.NoError(t, err)
	// Includes bbolt pages, indexes and free-page reserve, not just payloads.
	// The bounded high-water mark is checked separately from logical retention.
	// Exact file size depends on page size and bbolt's geometric mmap growth.
	t.Logf("ACKs=%d materializations=%d inbox_bytes=%d", observedACKs.Load(), calls, stat.Size())
	require.Less(t, stat.Size(), int64(64<<20))
}
