package ingest

import (
	"context"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/identity"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	maxMetricsPerBatch   = 20000
	maxProcessesPerBatch = 5000
	maxLogsPerBatch      = 5000
	maxNameLength        = 256
	maxLabelLength       = 256
	maxBatchIDLength     = 256
	maxCollectorIDLength = 128

	auxPayloadRefreshedMetric = "collector_aux_payload_refreshed"
)

// Server implements TelemetryIngest.
type Server struct {
	telemetryv1.UnimplementedTelemetryIngestServer
	store           Store
	logger          *zap.Logger
	processors      []Processor
	writeGuard      func() error
	authenticator   func(context.Context) (*identity.Identity, error)
	accessPolicy    AccessPolicy
	inbox           Inbox
	inboxLease      time.Duration
	inboxRetention  time.Duration
	inboxMaxRecords int
	inboxGCInterval time.Duration
	applyMu         sync.Mutex
	faultInjector   func(IngestFaultPoint) error
	lastInboxGC     time.Time
	mu              sync.RWMutex
	stats           Stats
}

// AccessPolicy constrains which collectors may submit telemetry to the controller.
type AccessPolicy struct {
	AllowedCollectorIDs []string
	AllowedClusterNames []string
	AllowedPeerSubjects []string
}

// Stats summarizes ingest behavior and quality.
type Stats struct {
	BatchesTotal              uint64     `json:"batches_total"`
	DuplicatesTotal           uint64     `json:"duplicates_total"`
	RejectedTotal             uint64     `json:"rejected_total"`
	WriteGuardRejectionsTotal uint64     `json:"write_guard_rejections_total"`
	AuthnRejectedTotal        uint64     `json:"authn_rejected_total"`
	AuthzRejectedTotal        uint64     `json:"authz_rejected_total"`
	AllowlistRejectedTotal    uint64     `json:"allowlist_rejected_total"`
	IdentityMismatchTotal     uint64     `json:"identity_mismatch_total"`
	MetricsTotal              uint64     `json:"metrics_total"`
	ProcessesTotal            uint64     `json:"processes_total"`
	LogsTotal                 uint64     `json:"logs_total"`
	LastBatchAt               time.Time  `json:"last_batch_at,omitempty"`
	LastRejectAt              time.Time  `json:"last_reject_at,omitempty"`
	LastCollector             string     `json:"last_collector,omitempty"`
	LastBatchID               string     `json:"last_batch_id,omitempty"`
	LastAuthSubject           string     `json:"last_auth_subject,omitempty"`
	LastPeerSubject           string     `json:"last_peer_subject,omitempty"`
	LastError                 string     `json:"last_error,omitempty"`
	Inbox                     InboxStats `json:"inbox"`
}

// IngestFaultPoint identifies receive-order crash boundaries for tests.
type IngestFaultPoint string

const (
	FaultBeforeInboxCommit IngestFaultPoint = "before_inbox_commit"
	FaultAfterInboxCommit  IngestFaultPoint = "after_inbox_commit"
	FaultBeforeMaterialize IngestFaultPoint = "before_materialize"
	FaultAfterMaterialize  IngestFaultPoint = "after_materialize"
	FaultAfterApplied      IngestFaultPoint = "after_applied_checkpoint"
	FaultBeforeACK         IngestFaultPoint = "before_ack"
	FaultAfterACK          IngestFaultPoint = "after_ack"
)

// Schema captures ingest validation contract exposed to operators and integration tests.
type Schema struct {
	Version              string `json:"version"`
	MaxMetricsPerBatch   int    `json:"max_metrics_per_batch"`
	MaxProcessesPerBatch int    `json:"max_processes_per_batch"`
	MaxLogsPerBatch      int    `json:"max_logs_per_batch"`
	MaxNameLength        int    `json:"max_name_length"`
	MaxLabelLength       int    `json:"max_label_length"`
	MaxBatchIDLength     int    `json:"max_batch_id_length"`
	MaxCollectorIDLength int    `json:"max_collector_id_length"`
}

// Processor can observe or derive additional data from incoming telemetry batches.
// Processors must be fast and non-blocking; they should do best-effort work and never return errors.
type Processor interface {
	ProcessBatch(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time)
}

// NewServer creates a new ingest server.
func NewServer(store Store, logger *zap.Logger, processors ...Processor) *Server {
	cfg := DefaultInboxConfig()
	cfg.Backend = InboxBackendMemory
	return NewServerWithInbox(store, newMemoryInbox(cfg), logger, processors...)
}

// NewServerWithInbox creates an ingest server backed by the supplied durable inbox.
func NewServerWithInbox(store Store, inbox Inbox, logger *zap.Logger, processors ...Processor) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg := DefaultInboxConfig()
	return &Server{
		store:           store,
		logger:          logger.With(zap.String("component", "ingest")),
		processors:      processors,
		inbox:           inbox,
		inboxLease:      cfg.Lease,
		inboxRetention:  cfg.Retention,
		inboxMaxRecords: cfg.MaxRecords,
		inboxGCInterval: cfg.GCInterval,
	}
}

// SetInboxPolicy configures lease and retention bounds used by replay/GC.
func (s *Server) SetInboxPolicy(cfg InboxConfig) {
	if s == nil {
		return
	}
	cfg = cfg.normalized()
	s.inboxLease = cfg.Lease
	s.inboxRetention = cfg.Retention
	s.inboxMaxRecords = cfg.MaxRecords
	s.inboxGCInterval = cfg.GCInterval
}

// SetFaultInjector installs deterministic receive-order fault injection for tests.
func (s *Server) SetFaultInjector(injector func(IngestFaultPoint) error) {
	if s != nil {
		s.faultInjector = injector
	}
}

// SetWriteGuard installs an optional write guard that runs before any hot-state mutation.
func (s *Server) SetWriteGuard(guard func() error) {
	if s == nil {
		return
	}
	s.writeGuard = guard
}

// SetAuthenticator installs an optional stream authenticator. When present, the
// stream must authenticate before any batch is accepted.
func (s *Server) SetAuthenticator(fn func(context.Context) (*identity.Identity, error)) {
	if s == nil {
		return
	}
	s.authenticator = fn
}

// SetAccessPolicy applies optional collector allowlists and peer-subject checks.
func (s *Server) SetAccessPolicy(policy AccessPolicy) {
	if s == nil {
		return
	}
	s.accessPolicy = AccessPolicy{
		AllowedCollectorIDs: normalizeStringList(policy.AllowedCollectorIDs),
		AllowedClusterNames: normalizeStringList(policy.AllowedClusterNames),
		AllowedPeerSubjects: normalizeStringList(policy.AllowedPeerSubjects),
	}
}

// WriteGuardEnabled reports whether follower-safe write gating is active.
func (s *Server) WriteGuardEnabled() bool {
	return s != nil && s.writeGuard != nil
}

// Push receives telemetry batches over a gRPC stream.
func (s *Server) Push(stream telemetryv1.TelemetryIngest_PushServer) error {
	if s == nil || s.inbox == nil {
		return status.Error(codes.Unavailable, "durable ingest inbox unavailable")
	}
	actor, err := s.authenticate(stream.Context())
	if err != nil {
		s.logger.Warn("rejecting telemetry stream on authentication failure",
			zap.String("peer", peerAddress(stream.Context())),
			zap.Error(err))
		s.recordAuthnRejected(err)
		return status.Error(codes.Unauthenticated, err.Error())
	}
	s.recordAuthenticatedSubject(actor)
	peerSubjects := peerIdentitySubjects(stream.Context())
	s.recordPeerSubject(peerSubjects)

	for {
		batch, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := validateBatch(batch); err != nil {
			s.logger.Warn("rejecting invalid telemetry batch", zap.Error(err))
			s.recordRejected(err)
			return status.Error(codes.InvalidArgument, err.Error())
		}
		if err := authorizeCollector(actor, batch.GetCollector(), peerSubjects, s.accessPolicy); err != nil {
			s.logger.Warn("rejecting telemetry batch on collector identity mismatch",
				zap.String("peer", peerAddress(stream.Context())),
				zap.String("batch_id", strings.TrimSpace(batch.GetBatchId())),
				zap.String("collector_id", strings.TrimSpace(batch.GetCollector().GetCollectorId())),
				zap.String("subject", authenticatedSubject(actor)),
				zap.Strings("peer_subjects", peerSubjects),
				zap.Error(err),
			)
			s.recordAuthzRejected(actor, peerSubjects, err)
			return status.Error(codes.PermissionDenied, err.Error())
		}
		if err := s.guardWrite(); err != nil {
			s.logger.Warn("rejecting telemetry batch on write guard",
				zap.String("batch_id", strings.TrimSpace(batch.GetBatchId())),
				zap.String("collector_id", strings.TrimSpace(batch.GetCollector().GetCollectorId())),
				zap.Error(err),
			)
			s.recordWriteGuardRejected(err)
			return status.Error(codes.Unavailable, err.Error())
		}

		receivedAt := time.Now().UTC()
		collectorID := strings.TrimSpace(batch.GetCollector().GetCollectorId())
		if err := s.injectFault(FaultBeforeInboxCommit); err != nil {
			return status.Error(codes.Unavailable, err.Error())
		}
		receipt, err := receiptFromBatch(batch, receivedAt)
		if err != nil {
			s.recordRejected(err)
			return status.Error(codes.Internal, err.Error())
		}
		stored, created, err := s.inbox.Commit(stream.Context(), receipt)
		if err != nil {
			s.recordRejected(err)
			if errors.Is(err, ErrIdentityConflict) {
				return status.Error(codes.AlreadyExists, "batch identity has different contents")
			}
			return status.Error(codes.Unavailable, "durable ingest receipt failed")
		}
		if err := s.injectFault(FaultAfterInboxCommit); err != nil {
			return status.Error(codes.Unavailable, err.Error())
		}
		if err := s.applyReceipt(stream.Context(), stored); err != nil {
			s.recordRejected(err)
			return status.Error(codes.Unavailable, err.Error())
		}
		if err := s.maybePruneInbox(stream.Context(), receivedAt); err != nil {
			s.logger.Warn("ingest inbox retention failed", zap.Error(err))
		}
		if err := s.injectFault(FaultBeforeACK); err != nil {
			return status.Error(codes.Unavailable, err.Error())
		}
		ack := &telemetryv1.Ack{BatchId: batch.BatchId}
		if err := stream.Send(ack); err != nil {
			return err
		}
		if err := s.injectFault(FaultAfterACK); err != nil {
			return status.Error(codes.Unavailable, err.Error())
		}
		if created {
			s.recordAccepted(batch, collectorID, receivedAt)
		} else {
			s.recordDuplicate(collectorID, batch.BatchId, receivedAt)
		}
	}
}

func (s *Server) recordAuthenticatedSubject(actor *identity.Identity) {
	if s == nil || actor == nil {
		return
	}
	subject := authenticatedSubject(actor)
	if subject == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.LastAuthSubject = subject
}

func (s *Server) recordPeerSubject(subjects []string) {
	if s == nil || len(subjects) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.LastPeerSubject = subjects[0]
}

func (s *Server) authenticate(ctx context.Context) (*identity.Identity, error) {
	if s == nil || s.authenticator == nil {
		return nil, nil
	}
	return s.authenticator(ctx)
}

func (s *Server) guardWrite() error {
	if s == nil || s.writeGuard == nil {
		return nil
	}
	return s.writeGuard()
}

func (s *Server) processBatchSafely(p Processor, collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ingest processor panic: %v", r)
			s.logger.Error("ingest processor panic recovered",
				zap.String("collector_id", collectorID),
				zap.String("batch_id", strings.TrimSpace(batch.GetBatchId())),
				zap.Any("panic", r))
		}
	}()

	p.ProcessBatch(collectorID, batch, receivedAt)
	return nil
}

func (s *Server) applyReceipt(ctx context.Context, receipt Receipt) error {
	if s == nil || s.inbox == nil {
		return fmt.Errorf("durable ingest inbox is unavailable")
	}
	// Keep deterministic processors ordered within one controller. The durable
	// claim below coordinates different controllers; each attempt has a unique
	// fencing token so a stale attempt cannot checkpoint a reclaimed receipt.
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	owner, err := newInboxOwner()
	if err != nil {
		return fmt.Errorf("generate ingest claim token: %w", err)
	}
	for {
		if err := s.guardWrite(); err != nil {
			return err
		}
		claimedReceipt, claimed, err := s.inbox.Claim(ctx, receipt.Identity, owner, time.Now().UTC(), s.inboxLease)
		if err != nil {
			return fmt.Errorf("claim durable ingest receipt: %w", err)
		}
		if claimed {
			finishLease := s.renewReceiptLease(ctx, receipt.Identity, owner)
			defer finishLease()
			if err := s.injectFault(FaultBeforeMaterialize); err != nil {
				return err
			}
			batch, err := decodeReceiptBatch(claimedReceipt)
			if err != nil {
				_ = s.inbox.MarkFailed(ctx, receipt.Identity, owner, err)
				return err
			}
			if err := s.materializeBatch(claimedReceipt.CollectorID, batch, claimedReceipt.AcceptedAt); err != nil {
				_ = s.inbox.MarkFailed(ctx, receipt.Identity, owner, err)
				return err
			}
			if err := s.injectFault(FaultAfterMaterialize); err != nil {
				return err
			}
			if err := finishLease(); err != nil {
				return fmt.Errorf("ingest lease renewal failed: %w", err)
			}
			if err := s.inbox.MarkApplied(ctx, receipt.Identity, owner, time.Now().UTC()); err != nil {
				return fmt.Errorf("checkpoint applied ingest receipt: %w", err)
			}
			if err := s.injectFault(FaultAfterApplied); err != nil {
				return err
			}
			return nil
		}
		if claimedReceipt.State == ReceiptApplied {
			return nil
		}

		wait := 5 * time.Millisecond
		if remaining := time.Until(claimedReceipt.LeaseExpiresAt); remaining > 0 && remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Server) renewReceiptLease(ctx context.Context, identity, owner string) func() error {
	leaseCtx, cancel := context.WithCancel(ctx)
	finished := make(chan struct{})
	var renewalError error
	go func() {
		defer close(finished)
		interval := s.inboxLease / 3
		if interval < time.Millisecond {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				if err := s.inbox.Renew(leaseCtx, identity, owner, time.Now().UTC(), s.inboxLease); err != nil {
					if leaseCtx.Err() == nil {
						renewalError = err
					}
					return
				}
			}
		}
	}()
	return func() error { cancel(); <-finished; return renewalError }
}

func (s *Server) materializeBatch(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) error {
	if err := s.materializeHotState(collectorID, batch, receivedAt); err != nil {
		return err
	}
	for _, processor := range s.processors {
		if processor == nil {
			continue
		}
		if err := s.processBatchSafely(processor, collectorID, batch, receivedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) materializeHotState(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) error {
	if s.store == nil {
		return fmt.Errorf("ingest hot store is unavailable")
	}
	// Lease recovery can apply an older receipt after a newer one. Keep the
	// latest view monotonic while retaining historical metrics where supported.
	if node := s.store.Node(collectorID); node != nil && node.LastIngestAt.After(receivedAt) {
		if history, ok := s.store.(interface {
			StoreHistoricalBatch(string, *telemetryv1.TelemetryBatch, time.Time)
		}); ok {
			history.StoreHistoricalBatch(collectorID, batch, receivedAt)
		}
		return nil
	}
	if batch.GetCollector() != nil {
		s.store.UpsertCollector(batch.GetCollector(), receivedAt)
	}
	s.store.StoreBatchMeta(collectorID, batch, receivedAt)
	if len(batch.GetMetrics()) > 0 {
		s.store.StoreMetrics(collectorID, batch.GetMetrics(), receivedAt)
	}
	if len(batch.GetProcesses()) > 0 || auxPayloadRefreshed(batch.GetMetrics(), "process_fallback") {
		s.store.StoreProcesses(collectorID, batch.GetProcesses(), receivedAt)
	}
	if len(batch.GetLogs()) > 0 || auxPayloadRefreshed(batch.GetMetrics(), "logs") {
		s.store.StoreLogs(collectorID, batch.GetLogs(), receivedAt)
	}
	return nil
}

// RestoreHotState rebuilds retained acknowledged telemetry after a process exit,
// without re-invoking observers for already applied receipts. Call before serving.
func (s *Server) RestoreHotState(ctx context.Context) error {
	if s == nil || s.inbox == nil {
		return fmt.Errorf("durable ingest inbox is unavailable")
	}
	after := ""
	for {
		receipts, err := s.inbox.Retained(ctx, after, 256)
		if err != nil {
			return err
		}
		if len(receipts) == 0 {
			return nil
		}
		for _, receipt := range receipts {
			if receipt.State == ReceiptApplied {
				batch, err := decodeReceiptBatch(receipt)
				if err != nil {
					return err
				}
				if err := s.materializeHotState(receipt.CollectorID, batch, receipt.AcceptedAt); err != nil {
					return err
				}
			}
			after = receiptOrder(receipt)
		}
	}
}

// ReplayPending applies receipts committed before an earlier controller exit.
func (s *Server) ReplayPending(ctx context.Context) error {
	if s == nil || s.inbox == nil {
		return fmt.Errorf("durable ingest inbox is unavailable")
	}
	for {
		receipts, err := s.inbox.Pending(ctx, time.Now().UTC(), 256)
		if err != nil {
			return fmt.Errorf("list pending ingest receipts: %w", err)
		}
		if len(receipts) == 0 {
			return nil
		}
		for _, receipt := range receipts {
			if err := s.applyReceipt(ctx, receipt); err != nil {
				return fmt.Errorf("replay ingest receipt %s: %w", receipt.Identity, err)
			}
		}
	}
}

func (s *Server) maybePruneInbox(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	if !s.lastInboxGC.IsZero() && now.Sub(s.lastInboxGC) < s.inboxGCInterval {
		s.mu.Unlock()
		return nil
	}
	s.lastInboxGC = now
	s.mu.Unlock()
	_, err := s.inbox.Prune(ctx, now.Add(-s.inboxRetention), s.inboxMaxRecords)
	return err
}

// PruneInbox runs interval-limited retention even when no new data arrives.
func (s *Server) PruneInbox(ctx context.Context) error {
	return s.maybePruneInbox(ctx, time.Now().UTC())
}

func (s *Server) injectFault(point IngestFaultPoint) error {
	if s == nil || s.faultInjector == nil {
		return nil
	}
	if err := s.faultInjector(point); err != nil {
		return fmt.Errorf("ingest fault at %s: %w", point, err)
	}
	return nil
}

func auxPayloadRefreshed(metrics []*telemetryv1.Metric, component string) bool {
	component = strings.TrimSpace(component)
	if component == "" {
		return false
	}
	for _, metric := range metrics {
		if metric == nil || metric.Name != auxPayloadRefreshedMetric || metric.Value < 0.5 {
			continue
		}
		for _, label := range metric.Labels {
			if label != nil && label.Key == "component" && strings.TrimSpace(label.Value) == component {
				return true
			}
		}
	}
	return false
}

// Register registers the server with a gRPC registrar.
func (s *Server) Register(registrar interface {
	RegisterService(*grpc.ServiceDesc, interface{})
}) {
	telemetryv1.RegisterTelemetryIngestServer(registrar, s)
}

// HealthCheck provides a simple health check for the ingest subsystem.
func (s *Server) HealthCheck(ctx context.Context) error {
	_, _ = ctx, s
	return nil
}

// Stats returns ingest counters and latest batch metadata.
func (s *Server) Stats() Stats {
	s.mu.RLock()
	stats := s.stats
	s.mu.RUnlock()
	if s.inbox != nil {
		stats.Inbox = s.inbox.Stats(context.Background())
	}
	return stats
}

// Schema returns ingest payload limits and validation contract.
func (s *Server) Schema() Schema {
	return Schema{
		Version:              "v2",
		MaxMetricsPerBatch:   maxMetricsPerBatch,
		MaxProcessesPerBatch: maxProcessesPerBatch,
		MaxLogsPerBatch:      maxLogsPerBatch,
		MaxNameLength:        maxNameLength,
		MaxLabelLength:       maxLabelLength,
		MaxBatchIDLength:     maxBatchIDLength,
		MaxCollectorIDLength: maxCollectorIDLength,
	}
}

func (s *Server) recordAccepted(batch *telemetryv1.TelemetryBatch, collectorID string, receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.BatchesTotal++
	s.stats.MetricsTotal += uint64(len(batch.Metrics))
	s.stats.ProcessesTotal += uint64(len(batch.Processes))
	s.stats.LogsTotal += uint64(len(batch.Logs))
	s.stats.LastBatchAt = receivedAt
	s.stats.LastCollector = strings.TrimSpace(collectorID)
	s.stats.LastBatchID = strings.TrimSpace(batch.BatchId)
}

func (s *Server) recordAuthnRejected(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.RejectedTotal++
	s.stats.AuthnRejectedTotal++
	s.stats.LastRejectAt = time.Now().UTC()
	s.stats.LastError = strings.TrimSpace(err.Error())
}

func (s *Server) recordAuthzRejected(actor *identity.Identity, peerSubjects []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.RejectedTotal++
	s.stats.AuthzRejectedTotal++
	s.stats.LastRejectAt = time.Now().UTC()
	s.stats.LastAuthSubject = authenticatedSubject(actor)
	if len(peerSubjects) > 0 {
		s.stats.LastPeerSubject = peerSubjects[0]
	}
	s.stats.LastError = strings.TrimSpace(err.Error())
	switch classifyAuthorizationError(err) {
	case authorizationErrorAllowlist:
		s.stats.AllowlistRejectedTotal++
	case authorizationErrorIdentityMismatch:
		s.stats.IdentityMismatchTotal++
	}
}

func (s *Server) recordDuplicate(collectorID, batchID string, receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.DuplicatesTotal++
	s.stats.LastBatchAt = receivedAt
	s.stats.LastCollector = strings.TrimSpace(collectorID)
	s.stats.LastBatchID = strings.TrimSpace(batchID)
}

func (s *Server) recordRejected(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.RejectedTotal++
	s.stats.LastRejectAt = time.Now()
	if err != nil {
		s.stats.LastError = err.Error()
	}
}

type authorizationErrorKind string

const (
	authorizationErrorAllowlist        authorizationErrorKind = "allowlist"
	authorizationErrorIdentityMismatch authorizationErrorKind = "identity_mismatch"
)

type authorizationError struct {
	kind authorizationErrorKind
	err  error
}

func (e *authorizationError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *authorizationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func authorizeCollector(actor *identity.Identity, collector *telemetryv1.CollectorInfo, peerSubjects []string, policy AccessPolicy) error {
	collectorID := ""
	if collector != nil {
		collectorID = strings.TrimSpace(collector.GetCollectorId())
	}
	if collectorAllowed := normalizeStringList(policy.AllowedCollectorIDs); len(collectorAllowed) > 0 && !containsFold(collectorAllowed, collectorID) {
		return &authorizationError{
			kind: authorizationErrorAllowlist,
			err:  fmt.Errorf("collector_id %q is not in the controller allowlist", collectorID),
		}
	}
	if clusterAllowed := normalizeStringList(policy.AllowedClusterNames); len(clusterAllowed) > 0 {
		cluster := collectorClusterName(collector)
		if !containsFold(clusterAllowed, cluster) {
			return &authorizationError{
				kind: authorizationErrorAllowlist,
				err:  fmt.Errorf("collector %q from cluster %q is not in the controller allowlist", collectorID, cluster),
			}
		}
	}
	if subjectAllowed := normalizeStringList(policy.AllowedPeerSubjects); len(subjectAllowed) > 0 && !containsAnyFold(subjectAllowed, peerSubjects) {
		return &authorizationError{
			kind: authorizationErrorAllowlist,
			err:  fmt.Errorf("peer subject %q is not in the controller allowlist", firstString(peerSubjects)),
		}
	}

	if actor != nil && actor.HasAnyRole(identity.RoleAdmin) {
		return nil
	}
	if actor != nil {
		if !actor.HasAnyRole(identity.RoleCollector) {
			return &authorizationError{
				kind: authorizationErrorIdentityMismatch,
				err:  fmt.Errorf("authenticated actor %q is not allowed to submit ingest batches", strings.TrimSpace(actor.Subject)),
			}
		}
		allowedCollectorID := strings.TrimSpace(actor.CollectorID)
		if allowedCollectorID == "" {
			allowedCollectorID = strings.TrimSpace(actor.Subject)
		}
		if allowedCollectorID != "*" && (allowedCollectorID == "" || !strings.EqualFold(allowedCollectorID, collectorID)) {
			return &authorizationError{
				kind: authorizationErrorIdentityMismatch,
				err:  fmt.Errorf("authenticated collector %q cannot submit telemetry for collector_id %q", allowedCollectorID, collectorID),
			}
		}
	}

	if len(peerSubjects) == 0 {
		return nil
	}
	if peerIdentityMatchesCollector(peerSubjects, collectorID) {
		return nil
	}
	return &authorizationError{
		kind: authorizationErrorIdentityMismatch,
		err:  fmt.Errorf("peer identity %q cannot submit telemetry for collector_id %q", firstString(peerSubjects), collectorID),
	}
}

func authenticatedSubject(actor *identity.Identity) string {
	if actor == nil {
		return ""
	}
	return strings.TrimSpace(actor.Subject)
}

func peerAddress(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return strings.TrimSpace(p.Addr.String())
	}
	return ""
}

func peerIdentitySubjects(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return nil
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
		return nil
	}
	return certificateIdentityAliases(tlsInfo.State.PeerCertificates[0].Subject, tlsInfo.State.PeerCertificates[0].DNSNames, tlsInfo.State.PeerCertificates[0].URIs)
}

func certificateIdentityAliases(subject pkix.Name, dnsNames []string, uris []*url.URL) []string {
	seen := map[string]struct{}{}
	add := func(values ...string) []string {
		out := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[strings.ToLower(value)]; ok {
				continue
			}
			seen[strings.ToLower(value)] = struct{}{}
			out = append(out, value)
		}
		return out
	}
	out := make([]string, 0, len(dnsNames)+len(uris)+4)
	out = append(out, add(strings.TrimSpace(subject.CommonName))...)
	for _, uri := range uris {
		if uri == nil {
			continue
		}
		out = append(out, add(uri.String(), uri.Host, path.Base(strings.TrimSpace(uri.Path)))...)
	}
	for _, dnsName := range dnsNames {
		out = append(out, add(dnsName)...)
	}
	return out
}

func peerIdentityMatchesCollector(subjects []string, collectorID string) bool {
	collectorID = strings.TrimSpace(collectorID)
	if collectorID == "" {
		return false
	}
	for _, subject := range subjects {
		if strings.EqualFold(strings.TrimSpace(subject), collectorID) {
			return true
		}
	}
	return false
}

func collectorClusterName(collector *telemetryv1.CollectorInfo) string {
	if collector == nil {
		return ""
	}
	for _, label := range collector.GetLabels() {
		if label == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(label.GetKey()))
		switch key {
		case "cluster", "cluster_name", "sre.cluster", "topology.kubernetes.io/cluster":
			return strings.TrimSpace(label.GetValue())
		}
	}
	return ""
}

func normalizeStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func containsFold(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}

func containsAnyFold(values, candidates []string) bool {
	for _, candidate := range candidates {
		if containsFold(values, candidate) {
			return true
		}
	}
	return false
}

func firstString(values []string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func classifyAuthorizationError(err error) authorizationErrorKind {
	var authzErr *authorizationError
	if err != nil && errors.As(err, &authzErr) {
		return authzErr.kind
	}
	return ""
}

func (s *Server) recordWriteGuardRejected(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.RejectedTotal++
	s.stats.WriteGuardRejectionsTotal++
	s.stats.LastRejectAt = time.Now()
	if err != nil {
		s.stats.LastError = err.Error()
	}
}

func validateBatch(batch *telemetryv1.TelemetryBatch) error {
	if batch != nil && proto.Size(batch) > maxInboxPayloadBytes {
		return fmt.Errorf("batch payload exceeds %d bytes", maxInboxPayloadBytes)
	}
	if batch == nil {
		return fmt.Errorf("batch cannot be nil")
	}
	if strings.TrimSpace(batch.BatchId) == "" {
		return fmt.Errorf("batch_id is required")
	}
	if len(batch.BatchId) > maxBatchIDLength {
		return fmt.Errorf("batch_id too long")
	}
	producerEpoch := strings.TrimSpace(batch.GetProducerEpoch())
	if len(producerEpoch) > 64 {
		return fmt.Errorf("producer_epoch too long")
	}
	if (producerEpoch == "") != (batch.GetProducerSequence() == 0) {
		return fmt.Errorf("producer_epoch and producer_sequence must be provided together")
	}
	if batch.Collector == nil {
		return fmt.Errorf("collector is required")
	}
	if err := validateCollector(batch.Collector); err != nil {
		return err
	}
	if len(batch.Metrics) > maxMetricsPerBatch {
		return fmt.Errorf("too many metrics: %d", len(batch.Metrics))
	}
	if len(batch.Processes) > maxProcessesPerBatch {
		return fmt.Errorf("too many process samples: %d", len(batch.Processes))
	}
	if len(batch.Logs) > maxLogsPerBatch {
		return fmt.Errorf("too many log fingerprints: %d", len(batch.Logs))
	}

	for i, metric := range batch.Metrics {
		if err := validateMetric(metric); err != nil {
			return fmt.Errorf("metric[%d]: %w", i, err)
		}
	}
	for i, process := range batch.Processes {
		if err := validateProcess(process); err != nil {
			return fmt.Errorf("process[%d]: %w", i, err)
		}
	}
	for i, log := range batch.Logs {
		if err := validateLog(log); err != nil {
			return fmt.Errorf("log[%d]: %w", i, err)
		}
	}
	return nil
}

func validateCollector(collector *telemetryv1.CollectorInfo) error {
	if strings.TrimSpace(collector.CollectorId) == "" {
		return fmt.Errorf("collector_id is required")
	}
	if len(collector.CollectorId) > maxCollectorIDLength {
		return fmt.Errorf("collector_id too long")
	}
	if strings.TrimSpace(collector.Hostname) == "" {
		return fmt.Errorf("collector hostname is required")
	}
	for _, label := range collector.Labels {
		if err := validateLabel(label); err != nil {
			return fmt.Errorf("collector label invalid: %w", err)
		}
	}
	return nil
}

func validateMetric(metric *telemetryv1.Metric) error {
	if metric == nil {
		return fmt.Errorf("metric cannot be nil")
	}
	if strings.TrimSpace(metric.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(metric.Name) > maxNameLength {
		return fmt.Errorf("name too long")
	}
	if !finite(metric.Value) {
		return fmt.Errorf("value must be finite")
	}
	for _, label := range metric.Labels {
		if err := validateLabel(label); err != nil {
			return err
		}
	}
	return nil
}

func validateProcess(process *telemetryv1.ProcessSample) error {
	if process == nil {
		return fmt.Errorf("process sample cannot be nil")
	}
	if process.Pid < 0 {
		return fmt.Errorf("pid must be non-negative")
	}
	if len(process.Name) > maxNameLength {
		return fmt.Errorf("process name too long")
	}
	if !finite(process.CpuPercent) || !finite(process.IoReadBps) || !finite(process.IoWriteBps) {
		return fmt.Errorf("process contains non-finite numeric values")
	}
	return nil
}

func validateLog(log *telemetryv1.LogFingerprint) error {
	if log == nil {
		return fmt.Errorf("log fingerprint cannot be nil")
	}
	if strings.TrimSpace(log.Fingerprint) == "" {
		return fmt.Errorf("fingerprint is required")
	}
	if len(log.Fingerprint) > maxNameLength {
		return fmt.Errorf("fingerprint too long")
	}
	if len(log.Example) > 4096 {
		return fmt.Errorf("example too long")
	}
	return nil
}

func validateLabel(label *telemetryv1.Label) error {
	if label == nil {
		return fmt.Errorf("label cannot be nil")
	}
	key := strings.TrimSpace(label.Key)
	if key == "" {
		return fmt.Errorf("label key is required")
	}
	if len(key) > maxLabelLength {
		return fmt.Errorf("label key too long")
	}
	if len(label.Value) > maxLabelLength {
		return fmt.Errorf("label value too long")
	}
	if strings.ContainsAny(key, "\n\r\t") || strings.ContainsAny(label.Value, "\n\r\t") {
		return fmt.Errorf("label contains control characters")
	}
	return nil
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
