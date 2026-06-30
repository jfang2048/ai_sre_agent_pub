package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	artifactstore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/artifacts"
	"go.uber.org/zap"
)

type AgentMessageType string

const (
	AgentMessageTypeAnalysisHandoff      AgentMessageType = "analysis_handoff"
	AgentMessageTypeValidationRequest    AgentMessageType = "validation_request"
	AgentMessageTypeValidationResult     AgentMessageType = "validation_result"
	AgentMessageTypeActionDecision       AgentMessageType = "action_decision"
	AgentMessageTypePostActionValidation AgentMessageType = "post_action_validation"
	AgentMessageTypeCompensationResult   AgentMessageType = "compensation_result"
)

type AgentMessageHeader struct {
	SchemaVersion     string           `json:"schema_version"`
	MessageID         string           `json:"message_id"`
	RunID             string           `json:"run_id"`
	WorkflowType      string           `json:"workflow_type,omitempty"`
	FromAgent         string           `json:"from_agent"`
	ToAgent           string           `json:"to_agent"`
	MessageType       AgentMessageType `json:"message_type"`
	CreatedAt         time.Time        `json:"created_at"`
	ParentMessageID   string           `json:"parent_message_id,omitempty"`
	PreviousMessageID string           `json:"previous_message_id,omitempty"`
	Sequence          int              `json:"sequence"`
}

type AgentMessageBody struct {
	PayloadSummary string          `json:"payload_summary,omitempty"`
	ContentHash    string          `json:"content_hash,omitempty"`
	Payload        json.RawMessage `json:"payload"`
}

type AgentMessageEnvelope struct {
	Header AgentMessageHeader `json:"header"`
	Body   AgentMessageBody   `json:"body"`
}

type AgentMessageRef struct {
	MessageID         string           `json:"message_id"`
	RunID             string           `json:"run_id"`
	WorkflowType      string           `json:"workflow_type,omitempty"`
	FromAgent         string           `json:"from_agent,omitempty"`
	ToAgent           string           `json:"to_agent,omitempty"`
	MessageType       AgentMessageType `json:"message_type"`
	Sequence          int              `json:"sequence"`
	CreatedAt         time.Time        `json:"created_at"`
	ParentMessageID   string           `json:"parent_message_id,omitempty"`
	PreviousMessageID string           `json:"previous_message_id,omitempty"`
	Path              string           `json:"path"`
	ArtifactID        string           `json:"artifact_id,omitempty"`
	StorageBackend    string           `json:"storage_backend,omitempty"`
	StorageKey        string           `json:"storage_key,omitempty"`
	LocalCachePath    string           `json:"local_cache_path,omitempty"`
	PayloadSummary    string           `json:"payload_summary,omitempty"`
	ContentHash       string           `json:"content_hash,omitempty"`
}

type AgentMessageHistory struct {
	SchemaVersion string            `json:"schema_version"`
	RunID         string            `json:"run_id"`
	WorkflowType  string            `json:"workflow_type,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Messages      []AgentMessageRef `json:"messages,omitempty"`
}

type AnalysisHandoffMessage struct {
	Handoff AnalysisHandoff `json:"handoff"`
}

type ValidationRequestMessage struct {
	AnalysisMessage AgentMessageRef `json:"analysis_message"`
	TargetLimit     int             `json:"target_limit,omitempty"`
	ReadOnlyOnly    bool            `json:"read_only_only,omitempty"`
	RequestedAt     time.Time       `json:"requested_at"`
}

type ValidationResultMessage struct {
	Report ValidationActionReport `json:"report"`
}

type ActionDecisionMessage struct {
	SelectedAction         *ValidationActionCandidate `json:"selected_action,omitempty"`
	SelectedActionContract *ValidationActionContract  `json:"selected_action_contract,omitempty"`
	Governance             *ValidationGovernanceTrace `json:"governance,omitempty"`
	StepID                 string                     `json:"step_id,omitempty"`
	StepStatus             string                     `json:"step_status,omitempty"`
	Summary                string                     `json:"summary,omitempty"`
}

type PostActionValidationMessage struct {
	Result PostActionValidationSummary `json:"result"`
}

type CompensationResultMessage struct {
	StepID      string                     `json:"step_id,omitempty"`
	Governance  *ValidationGovernanceTrace `json:"governance,omitempty"`
	Status      string                     `json:"status,omitempty"`
	Summary     string                     `json:"summary,omitempty"`
	Error       string                     `json:"error,omitempty"`
	CompletedAt time.Time                  `json:"completed_at,omitempty"`
}

type AgentMessageStore struct {
	rootPath      string
	schemaVersion string
	prettyJSON    bool
	artifacts     *artifactstore.Manager
	logger        *zap.Logger
	mu            sync.Mutex
}

func NewAgentMessageStore(cfg WorkflowConfig, manager *artifactstore.Manager, logger *zap.Logger) *AgentMessageStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	root := strings.TrimSpace(cfg.AgentMessageDir)
	if root == "" {
		root = filepath.Join(strings.TrimSpace(cfg.WorkflowDataPath), "messages")
	}
	return &AgentMessageStore{
		rootPath:      root,
		schemaVersion: strings.TrimSpace(cfg.AgentMessageSchemaVersion),
		prettyJSON:    cfg.AgentMessagePrettyJSON,
		artifacts:     manager,
		logger:        logger.With(zap.String("component", "agent_message_store")),
	}
}

func (s *AgentMessageStore) Append(runID, workflowType, fromAgent, toAgent string, messageType AgentMessageType, parent *AgentMessageRef, payload any, summary string) (AgentMessageRef, *DurableArtifactRef, error) {
	if s == nil {
		return AgentMessageRef{}, nil, fmt.Errorf("agent message store is nil")
	}
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return AgentMessageRef{}, nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	runID = sanitizeID(strings.TrimSpace(runID))
	if runID == "" {
		return AgentMessageRef{}, nil, fmt.Errorf("run id is required")
	}
	workflowType = strings.TrimSpace(workflowType)
	history, manifestRef, err := s.loadHistoryLocked(runID)
	if err != nil {
		return AgentMessageRef{}, nil, err
	}
	seq := len(history.Messages) + 1
	now := time.Now().UTC()
	prevID := ""
	if len(history.Messages) > 0 {
		prevID = history.Messages[len(history.Messages)-1].MessageID
	}
	envelope := AgentMessageEnvelope{
		Header: AgentMessageHeader{
			SchemaVersion:     firstNonEmpty(s.schemaVersion, "agent-message/v1"),
			MessageID:         fmt.Sprintf("msg-%s-%04d", runID, seq),
			RunID:             runID,
			WorkflowType:      workflowType,
			FromAgent:         strings.TrimSpace(fromAgent),
			ToAgent:           strings.TrimSpace(toAgent),
			MessageType:       messageType,
			CreatedAt:         now,
			PreviousMessageID: prevID,
			Sequence:          seq,
		},
		Body: AgentMessageBody{
			PayloadSummary: truncateString(strings.TrimSpace(summary), 220),
			ContentHash:    agentMessageContentHash(payloadRaw),
			Payload:        payloadRaw,
		},
	}
	if parent != nil {
		envelope.Header.ParentMessageID = strings.TrimSpace(parent.MessageID)
	}
	raw, err := marshalAgentMessageEnvelope(envelope, s.prettyJSON)
	if err != nil {
		return AgentMessageRef{}, nil, err
	}
	ref, err := s.writeMessageArtifact(runID, workflowType, messageType, envelope, raw)
	if err != nil {
		return AgentMessageRef{}, nil, err
	}
	history.SchemaVersion = envelope.Header.SchemaVersion
	history.RunID = runID
	history.WorkflowType = firstNonEmpty(workflowType, history.WorkflowType)
	history.UpdatedAt = now
	history.Messages = append(history.Messages, ref)
	manifestRef, err = s.writeHistoryManifest(runID, workflowType, history)
	if err != nil {
		return AgentMessageRef{}, nil, err
	}
	return ref, manifestRef, nil
}

func (s *AgentMessageStore) Latest(runID string, messageType AgentMessageType) (*AgentMessageRef, error) {
	if s == nil {
		return nil, fmt.Errorf("agent message store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	history, _, err := s.loadHistoryLocked(runID)
	if err != nil {
		return nil, err
	}
	for idx := len(history.Messages) - 1; idx >= 0; idx-- {
		if history.Messages[idx].MessageType != messageType {
			continue
		}
		ref := history.Messages[idx]
		return &ref, nil
	}
	return nil, nil
}

func (s *AgentMessageStore) LoadHistory(runID string) (AgentMessageHistory, *DurableArtifactRef, error) {
	if s == nil {
		return AgentMessageHistory{}, nil, fmt.Errorf("agent message store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadHistoryLocked(runID)
}

func (s *AgentMessageStore) LoadEnvelope(ref AgentMessageRef) (AgentMessageEnvelope, error) {
	if s != nil && s.artifacts != nil && strings.TrimSpace(ref.ArtifactID) != "" {
		payload, _, err := s.artifacts.Read(context.Background(), ref.ArtifactID)
		if err == nil {
			var envelope AgentMessageEnvelope
			if unmarshalErr := json.Unmarshal(payload, &envelope); unmarshalErr != nil {
				return AgentMessageEnvelope{}, unmarshalErr
			}
			if expected := strings.TrimSpace(envelope.Body.ContentHash); expected != "" {
				actual := agentMessageContentHash(envelope.Body.Payload)
				if actual != expected {
					return AgentMessageEnvelope{}, fmt.Errorf("agent message content hash mismatch for artifact %s", ref.ArtifactID)
				}
			}
			return envelope, nil
		}
	}
	if strings.TrimSpace(ref.Path) == "" {
		return AgentMessageEnvelope{}, fmt.Errorf("agent message path is required")
	}
	return loadAgentMessageEnvelope(ref.Path)
}

func loadAgentMessageEnvelope(path string) (AgentMessageEnvelope, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return AgentMessageEnvelope{}, err
	}
	var envelope AgentMessageEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return AgentMessageEnvelope{}, err
	}
	if expected := strings.TrimSpace(envelope.Body.ContentHash); expected != "" {
		actual := agentMessageContentHash(envelope.Body.Payload)
		if actual != expected {
			return AgentMessageEnvelope{}, fmt.Errorf("agent message content hash mismatch for %s", path)
		}
	}
	return envelope, nil
}

func loadAgentMessageHistoryFromPath(path string) (AgentMessageHistory, error) {
	if strings.TrimSpace(path) == "" {
		return AgentMessageHistory{}, fmt.Errorf("agent message history path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return AgentMessageHistory{}, err
	}
	var history AgentMessageHistory
	if err := json.Unmarshal(raw, &history); err != nil {
		return AgentMessageHistory{}, err
	}
	return history, nil
}

func decodeAgentMessagePayload(envelope AgentMessageEnvelope, expectedType AgentMessageType, expectedSchema string, target any) error {
	if envelope.Header.MessageType != expectedType {
		return fmt.Errorf("unexpected agent message type: want %s got %s", expectedType, envelope.Header.MessageType)
	}
	if expectedSchema != "" && envelope.Header.SchemaVersion != expectedSchema {
		return fmt.Errorf("unexpected agent message schema: want %s got %s", expectedSchema, envelope.Header.SchemaVersion)
	}
	if len(envelope.Body.Payload) == 0 {
		return fmt.Errorf("agent message payload is empty")
	}
	if target == nil {
		return nil
	}
	return json.Unmarshal(envelope.Body.Payload, target)
}

func cloneAgentMessageRef(ref *AgentMessageRef) *AgentMessageRef {
	if ref == nil {
		return nil
	}
	copy := *ref
	return &copy
}

func cloneAgentMessageHistoryRefs(in []AgentMessageRef) []AgentMessageRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]AgentMessageRef, len(in))
	copy(out, in)
	return out
}

func trimAgentMessageHistory(in []AgentMessageRef, limit int) []AgentMessageRef {
	if limit <= 0 || len(in) <= limit {
		return cloneAgentMessageHistoryRefs(in)
	}
	return cloneAgentMessageHistoryRefs(in[len(in)-limit:])
}

func appendAgentMessageRef(history []AgentMessageRef, ref AgentMessageRef, limit int) []AgentMessageRef {
	for idx := range history {
		if history[idx].MessageID != ref.MessageID {
			continue
		}
		history[idx] = ref
		return trimAgentMessageHistory(history, limit)
	}
	history = append(history, ref)
	return trimAgentMessageHistory(history, limit)
}

func marshalAgentMessageEnvelope(envelope AgentMessageEnvelope, pretty bool) ([]byte, error) {
	if pretty {
		return json.MarshalIndent(envelope, "", "  ")
	}
	return json.Marshal(envelope)
}

func (s *AgentMessageStore) loadHistoryLocked(runID string) (AgentMessageHistory, *DurableArtifactRef, error) {
	runID = sanitizeID(strings.TrimSpace(runID))
	if runID == "" {
		return AgentMessageHistory{}, nil, fmt.Errorf("run id is required")
	}
	if s.artifacts != nil {
		record, err := s.artifacts.Get(context.Background(), agentMessageHistoryArtifactID(runID))
		if err == nil && record != nil {
			payload, readErr := s.artifacts.ReadRecord(context.Background(), record)
			if readErr != nil {
				return AgentMessageHistory{}, nil, readErr
			}
			var history AgentMessageHistory
			if err := json.Unmarshal(payload, &history); err != nil {
				return AgentMessageHistory{}, nil, err
			}
			if history.RunID == "" {
				history.RunID = runID
			}
			if history.SchemaVersion == "" {
				history.SchemaVersion = firstNonEmpty(s.schemaVersion, "agent-message/v1")
			}
			ref := durableArtifactRefFromRecord(record)
			return history, &ref, nil
		}
	}
	runDir := filepath.Join(strings.TrimSpace(s.rootPath), runID)
	manifestPath := filepath.Join(runDir, "history.json")
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return AgentMessageHistory{
				SchemaVersion: firstNonEmpty(s.schemaVersion, "agent-message/v1"),
				RunID:         runID,
			}, nil, nil
		}
		return AgentMessageHistory{}, nil, err
	}
	history, err := loadAgentMessageHistoryFromPath(manifestPath)
	if err != nil {
		return AgentMessageHistory{}, nil, err
	}
	if history.RunID == "" {
		history.RunID = runID
	}
	if history.SchemaVersion == "" {
		history.SchemaVersion = firstNonEmpty(s.schemaVersion, "agent-message/v1")
	}
	ref := &DurableArtifactRef{
		ArtifactID:     agentMessageHistoryArtifactID(runID),
		ArtifactType:   string(artifactstore.ArtifactTypeWorkflowMessageIndex),
		OwnerType:      string(artifactstore.OwnerTypeWorkflowRun),
		OwnerID:        runID,
		RunID:          runID,
		StorageBackend: "filesystem",
		StorageKey:     filepath.ToSlash(filepath.Join("messages", runID, "history.json")),
		LocalCachePath: manifestPath,
		Path:           manifestPath,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	return history, ref, nil
}

func agentMessageRefFromEnvelope(envelope AgentMessageEnvelope, path string) AgentMessageRef {
	return AgentMessageRef{
		MessageID:         envelope.Header.MessageID,
		RunID:             envelope.Header.RunID,
		WorkflowType:      envelope.Header.WorkflowType,
		FromAgent:         envelope.Header.FromAgent,
		ToAgent:           envelope.Header.ToAgent,
		MessageType:       envelope.Header.MessageType,
		Sequence:          envelope.Header.Sequence,
		CreatedAt:         envelope.Header.CreatedAt,
		ParentMessageID:   envelope.Header.ParentMessageID,
		PreviousMessageID: envelope.Header.PreviousMessageID,
		Path:              path,
		PayloadSummary:    envelope.Body.PayloadSummary,
		ContentHash:       envelope.Body.ContentHash,
	}
}

func agentMessageRefFromArtifact(envelope AgentMessageEnvelope, record *artifactstore.Record) AgentMessageRef {
	ref := agentMessageRefFromEnvelope(envelope, "")
	if record == nil {
		return ref
	}
	ref.ArtifactID = record.ArtifactID
	ref.StorageBackend = record.StorageBackend
	ref.StorageKey = record.StorageKey
	ref.LocalCachePath = record.LocalCachePath
	ref.Path = record.LocalCachePath
	return ref
}

func (s *AgentMessageStore) writeMessageArtifact(runID, workflowType string, messageType AgentMessageType, envelope AgentMessageEnvelope, payload []byte) (AgentMessageRef, error) {
	filename := fmt.Sprintf("%04d-%s.json", envelope.Header.Sequence, agentMessageFileStem(messageType))
	if s.artifacts != nil {
		record, err := s.artifacts.Write(context.Background(), artifactstore.WriteRequest{
			ArtifactID:    envelope.Header.MessageID,
			ArtifactType:  artifactstore.ArtifactTypeWorkflowMessage,
			OwnerType:     artifactstore.OwnerTypeWorkflowRun,
			OwnerID:       runID,
			RunID:         runID,
			FileExtension: ".json",
			ContentType:   "application/json",
			StorageKey:    filepath.ToSlash(filepath.Join("messages", runID, filename)),
			Metadata: map[string]string{
				"workflow_type": workflowType,
				"message_type":  string(messageType),
			},
			Payload: payload,
		})
		if err != nil {
			return AgentMessageRef{}, err
		}
		return agentMessageRefFromArtifact(envelope, record), nil
	}
	runDir := filepath.Join(strings.TrimSpace(s.rootPath), runID)
	targetPath := filepath.Join(runDir, filename)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return AgentMessageRef{}, err
	}
	if err := os.WriteFile(targetPath, payload, 0o644); err != nil {
		return AgentMessageRef{}, err
	}
	return agentMessageRefFromEnvelope(envelope, targetPath), nil
}

func (s *AgentMessageStore) writeHistoryManifest(runID, workflowType string, history AgentMessageHistory) (*DurableArtifactRef, error) {
	raw, err := json.Marshal(history)
	if err != nil {
		return nil, err
	}
	if s.prettyJSON {
		raw, err = json.MarshalIndent(history, "", "  ")
		if err != nil {
			return nil, err
		}
	}
	if s.artifacts != nil {
		record, err := s.artifacts.Write(context.Background(), artifactstore.WriteRequest{
			ArtifactID:    agentMessageHistoryArtifactID(runID),
			ArtifactType:  artifactstore.ArtifactTypeWorkflowMessageIndex,
			OwnerType:     artifactstore.OwnerTypeWorkflowRun,
			OwnerID:       runID,
			RunID:         runID,
			FileExtension: ".json",
			ContentType:   "application/json",
			StorageKey:    filepath.ToSlash(filepath.Join("messages", runID, "history.json")),
			Metadata: map[string]string{
				"workflow_type": workflowType,
				"artifact_role": "message_manifest",
			},
			Payload: raw,
		})
		if err != nil {
			return nil, err
		}
		ref := durableArtifactRefFromRecord(record)
		return &ref, nil
	}
	runDir := filepath.Join(strings.TrimSpace(s.rootPath), runID)
	manifestPath := filepath.Join(runDir, "history.json")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		return nil, err
	}
	return &DurableArtifactRef{
		ArtifactID:     agentMessageHistoryArtifactID(runID),
		ArtifactType:   string(artifactstore.ArtifactTypeWorkflowMessageIndex),
		OwnerType:      string(artifactstore.OwnerTypeWorkflowRun),
		OwnerID:        runID,
		RunID:          runID,
		StorageBackend: "filesystem",
		StorageKey:     filepath.ToSlash(filepath.Join("messages", runID, "history.json")),
		LocalCachePath: manifestPath,
		Path:           manifestPath,
		CreatedAt:      history.UpdatedAt,
		UpdatedAt:      history.UpdatedAt,
	}, nil
}

func agentMessageHistoryArtifactID(runID string) string {
	return fmt.Sprintf("msg-history-%s", sanitizeID(runID))
}

func agentMessageContentHash(payload []byte) string {
	normalized := payload
	if len(payload) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, payload); err == nil && compact.Len() > 0 {
			normalized = compact.Bytes()
		}
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

func agentMessageFileStem(messageType AgentMessageType) string {
	return strings.ReplaceAll(sanitizeID(string(messageType)), "_", "-")
}
