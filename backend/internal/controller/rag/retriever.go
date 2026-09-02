package rag

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const schemaVersion = "rag-v0.95"

// Config controls local RAG ingestion, indexing, and retrieval behavior.
type Config struct {
	Enabled            bool               `json:"enabled" yaml:"enabled"`
	DatasetPath        string             `json:"dataset_path" yaml:"dataset_path"`
	SourcePaths        []string           `json:"source_paths,omitempty" yaml:"source_paths,omitempty"`
	IndexPath          string             `json:"index_path" yaml:"index_path"`
	TopK               int                `json:"top_k" yaml:"top_k"`
	MaxSnippetChars    int                `json:"max_snippet_chars" yaml:"max_snippet_chars"`
	ChunkSize          int                `json:"chunk_size" yaml:"chunk_size"`
	ChunkOverlap       int                `json:"chunk_overlap" yaml:"chunk_overlap"`
	ChunkStrategy      string             `json:"chunk_strategy" yaml:"chunk_strategy"`
	RetrievalMode      string             `json:"retrieval_mode" yaml:"retrieval_mode"`
	EmbeddingProvider  string             `json:"embedding_provider" yaml:"embedding_provider"`
	EmbeddingModel     string             `json:"embedding_model" yaml:"embedding_model"`
	EmbeddingBaseURL   string             `json:"embedding_base_url,omitempty" yaml:"embedding_base_url,omitempty"`
	EmbeddingAPIKey    string             `json:"embedding_api_key,omitempty" yaml:"embedding_api_key,omitempty"`
	VectorBackend      string             `json:"vector_backend,omitempty" yaml:"vector_backend,omitempty"`
	VectorEndpoint     string             `json:"vector_endpoint,omitempty" yaml:"vector_endpoint,omitempty"`
	VectorCollection   string             `json:"vector_collection,omitempty" yaml:"vector_collection,omitempty"`
	VectorDatabase     string             `json:"vector_database,omitempty" yaml:"vector_database,omitempty"`
	VectorToken        string             `json:"vector_token,omitempty" yaml:"vector_token,omitempty"`
	VectorTimeout      time.Duration      `json:"vector_timeout,omitempty" yaml:"vector_timeout,omitempty"`
	RebuildPolicy      string             `json:"rebuild_policy" yaml:"rebuild_policy"`
	StalenessThreshold time.Duration      `json:"staleness_threshold,omitempty" yaml:"staleness_threshold,omitempty"`
	SourceTypeWeights  map[string]float64 `json:"source_type_weights,omitempty" yaml:"source_type_weights,omitempty"`
}

// DefaultConfig returns conservative local-first defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:           false,
		DatasetPath:       "./dataset",
		IndexPath:         "./data/agent/rag/index.json",
		TopK:              5,
		MaxSnippetChars:   280,
		ChunkSize:         900,
		ChunkOverlap:      120,
		ChunkStrategy:     "auto",
		RetrievalMode:     "hybrid",
		EmbeddingProvider: "local",
		EmbeddingModel:    "local-hash-64",
		VectorBackend:     "local",
		VectorCollection:  "ai_sre_agent_knowledge",
		VectorTimeout:     5 * time.Second,
		RebuildPolicy:     "manual",
	}
}

func normalizeConfig(cfg Config) Config {
	def := DefaultConfig()
	if strings.TrimSpace(cfg.DatasetPath) == "" {
		cfg.DatasetPath = def.DatasetPath
	}
	if strings.TrimSpace(cfg.IndexPath) == "" {
		cfg.IndexPath = def.IndexPath
	}
	if cfg.TopK <= 0 {
		cfg.TopK = def.TopK
	}
	if cfg.MaxSnippetChars <= 0 {
		cfg.MaxSnippetChars = def.MaxSnippetChars
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = def.ChunkSize
	}
	if cfg.ChunkOverlap < 0 {
		cfg.ChunkOverlap = def.ChunkOverlap
	}
	if cfg.ChunkOverlap >= cfg.ChunkSize {
		cfg.ChunkOverlap = cfg.ChunkSize / 4
	}
	if strings.TrimSpace(cfg.ChunkStrategy) == "" {
		cfg.ChunkStrategy = def.ChunkStrategy
	}
	if strings.TrimSpace(cfg.RetrievalMode) == "" {
		cfg.RetrievalMode = def.RetrievalMode
	}
	if strings.TrimSpace(cfg.EmbeddingProvider) == "" {
		cfg.EmbeddingProvider = def.EmbeddingProvider
	}
	if strings.TrimSpace(cfg.EmbeddingModel) == "" {
		cfg.EmbeddingModel = def.EmbeddingModel
	}
	if strings.TrimSpace(cfg.VectorBackend) == "" {
		cfg.VectorBackend = def.VectorBackend
	}
	if strings.TrimSpace(cfg.VectorCollection) == "" {
		cfg.VectorCollection = def.VectorCollection
	}
	if cfg.VectorTimeout <= 0 {
		cfg.VectorTimeout = def.VectorTimeout
	}
	if strings.TrimSpace(cfg.RebuildPolicy) == "" {
		cfg.RebuildPolicy = def.RebuildPolicy
	}
	cfg.RetrievalMode = normalizeOption(cfg.RetrievalMode, def.RetrievalMode, "hybrid", "lexical", "vector")
	cfg.ChunkStrategy = normalizeOption(cfg.ChunkStrategy, def.ChunkStrategy, "auto", "paragraph", "markdown", "line", "record", "case")
	cfg.RebuildPolicy = normalizeOption(cfg.RebuildPolicy, def.RebuildPolicy, "manual", "if_missing", "startup")
	cfg.EmbeddingProvider = strings.ToLower(strings.TrimSpace(cfg.EmbeddingProvider))
	cfg.EmbeddingModel = strings.TrimSpace(cfg.EmbeddingModel)
	cfg.VectorBackend = normalizeOption(cfg.VectorBackend, def.VectorBackend, "local", "milvus")
	cfg.VectorEndpoint = strings.TrimSpace(cfg.VectorEndpoint)
	cfg.VectorCollection = strings.TrimSpace(cfg.VectorCollection)
	cfg.VectorDatabase = strings.TrimSpace(cfg.VectorDatabase)
	cfg.VectorToken = strings.TrimSpace(cfg.VectorToken)
	cfg.DatasetPath = filepath.Clean(cfg.DatasetPath)
	cfg.IndexPath = filepath.Clean(cfg.IndexPath)
	return cfg
}

// ConfigFromEnv applies environment overrides without requiring the controller config path.
func ConfigFromEnv(base Config) Config {
	cfg := normalizeConfig(base)
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_ENABLED")); raw != "" {
		cfg.Enabled = parseBool(raw, cfg.Enabled)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_DATASET_PATH")); raw != "" {
		cfg.DatasetPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_SOURCE_PATHS")); raw != "" {
		cfg.SourcePaths = splitCommaList(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_DOC_PATHS")); raw != "" {
		cfg.SourcePaths = splitCommaList(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_INDEX_PATH")); raw != "" {
		cfg.IndexPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_TOP_K")); raw != "" {
		cfg.TopK = parseInt(raw, cfg.TopK)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_MAX_SNIPPET_CHARS")); raw != "" {
		cfg.MaxSnippetChars = parseInt(raw, cfg.MaxSnippetChars)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_CHUNK_SIZE")); raw != "" {
		cfg.ChunkSize = parseInt(raw, cfg.ChunkSize)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_CHUNK_OVERLAP")); raw != "" {
		cfg.ChunkOverlap = parseInt(raw, cfg.ChunkOverlap)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_CHUNK_STRATEGY")); raw != "" {
		cfg.ChunkStrategy = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_RETRIEVAL_MODE")); raw != "" {
		cfg.RetrievalMode = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_EMBEDDING_PROVIDER")); raw != "" {
		cfg.EmbeddingProvider = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_EMBEDDING_MODEL")); raw != "" {
		cfg.EmbeddingModel = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_EMBEDDING_BASE_URL")); raw != "" {
		cfg.EmbeddingBaseURL = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_EMBEDDING_API_KEY")); raw != "" {
		cfg.EmbeddingAPIKey = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_BACKEND")); raw != "" {
		cfg.VectorBackend = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_ENDPOINT")); raw != "" {
		cfg.VectorEndpoint = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_COLLECTION")); raw != "" {
		cfg.VectorCollection = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_DATABASE")); raw != "" {
		cfg.VectorDatabase = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_TOKEN")); raw != "" {
		cfg.VectorToken = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_VECTOR_TIMEOUT")); raw != "" {
		cfg.VectorTimeout = parseDuration(raw, cfg.VectorTimeout)
	}
	if raw := strings.TrimSpace(os.Getenv("SRE_AGENT_RAG_REBUILD_POLICY")); raw != "" {
		cfg.RebuildPolicy = raw
	}
	return normalizeConfig(cfg)
}

// Document is the searchable chunk payload returned through the Retriever contract.
type Document struct {
	ID         string            `json:"id"`
	DocID      string            `json:"doc_id,omitempty"`
	ChunkID    string            `json:"chunk_id,omitempty"`
	Path       string            `json:"path"`
	SourcePath string            `json:"source_path"`
	SourceType string            `json:"source_type"`
	Title      string            `json:"title"`
	Content    string            `json:"content"`
	Tags       []string          `json:"tags,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Timestamp  *time.Time        `json:"timestamp_if_available,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// SourceDocument is the normalized pre-chunk document representation.
type SourceDocument struct {
	DocID            string            `json:"doc_id"`
	SourceKey        string            `json:"source_key"`
	SourcePath       string            `json:"source_path"`
	SourceType       string            `json:"source_type"`
	KnowledgeType    string            `json:"knowledge_type,omitempty"`
	CaseType         string            `json:"case_type,omitempty"`
	Title            string            `json:"title"`
	Summary          string            `json:"summary,omitempty"`
	Content          string            `json:"content"`
	RetrievalText    string            `json:"retrieval_text,omitempty"`
	EmbeddingText    string            `json:"embedding_text,omitempty"`
	Symptoms         []string          `json:"symptoms,omitempty"`
	Evidence         []string          `json:"evidence,omitempty"`
	LikelyCauses     []string          `json:"likely_causes,omitempty"`
	RemediationSteps []string          `json:"remediation_steps,omitempty"`
	Commands         []string          `json:"commands,omitempty"`
	Environment      []string          `json:"environment,omitempty"`
	Signals          []string          `json:"signals,omitempty"`
	RetrievalWeight  float64           `json:"retrieval_weight,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Timestamp        *time.Time        `json:"timestamp_if_available,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// Chunk is the persisted searchable unit derived from a normalized document.
type Chunk struct {
	ChunkID          string            `json:"chunk_id"`
	DocID            string            `json:"doc_id"`
	SourceKey        string            `json:"source_key"`
	SourcePath       string            `json:"source_path"`
	SourceType       string            `json:"source_type"`
	KnowledgeType    string            `json:"knowledge_type,omitempty"`
	CaseType         string            `json:"case_type,omitempty"`
	Title            string            `json:"title"`
	Summary          string            `json:"summary,omitempty"`
	Content          string            `json:"content"`
	RetrievalText    string            `json:"retrieval_text,omitempty"`
	EmbeddingText    string            `json:"embedding_text,omitempty"`
	Symptoms         []string          `json:"symptoms,omitempty"`
	Evidence         []string          `json:"evidence,omitempty"`
	LikelyCauses     []string          `json:"likely_causes,omitempty"`
	RemediationSteps []string          `json:"remediation_steps,omitempty"`
	Commands         []string          `json:"commands,omitempty"`
	Environment      []string          `json:"environment,omitempty"`
	Signals          []string          `json:"signals,omitempty"`
	SectionType      string            `json:"section_type,omitempty"`
	RetrievalWeight  float64           `json:"retrieval_weight,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Timestamp        *time.Time        `json:"timestamp_if_available,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at"`
	ChunkIndex       int               `json:"chunk_index"`
	OffsetStart      int               `json:"offset_start"`
	OffsetEnd        int               `json:"offset_end"`
	Strategy         string            `json:"strategy"`
	Embedding        []float32         `json:"embedding,omitempty"`
}

// SearchHit is the API-facing retrieval hit.
type SearchHit struct {
	EvidenceID       string            `json:"evidence_id,omitempty"`
	DocID            string            `json:"doc_id"`
	ChunkID          string            `json:"chunk_id"`
	Score            float64           `json:"score"`
	SourcePath       string            `json:"source_path"`
	SourceType       string            `json:"source_type"`
	KnowledgeType    string            `json:"knowledge_type,omitempty"`
	CaseType         string            `json:"case_type,omitempty"`
	Title            string            `json:"title"`
	Summary          string            `json:"summary,omitempty"`
	Snippet          string            `json:"snippet"`
	Symptoms         []string          `json:"symptoms,omitempty"`
	Evidence         []string          `json:"evidence,omitempty"`
	LikelyCauses     []string          `json:"likely_causes,omitempty"`
	RemediationSteps []string          `json:"remediation_steps,omitempty"`
	Commands         []string          `json:"commands,omitempty"`
	Signals          []string          `json:"signals,omitempty"`
	SectionType      string            `json:"section_type,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Timestamp        *time.Time        `json:"timestamp_if_available,omitempty"`
	Content          string            `json:"-"`
}

// QueryRequest is the structured RAG query input.
type QueryRequest struct {
	Query          string   `json:"query"`
	TopK           int      `json:"top_k,omitempty"`
	Intent         string   `json:"intent,omitempty"`
	KnowledgeTypes []string `json:"knowledge_types,omitempty"`
	CaseTypes      []string `json:"case_types,omitempty"`
	SourceTypes    []string `json:"source_types,omitempty"`
}

// QueryResult is the structured RAG query response.
type QueryResult struct {
	Query                string      `json:"query"`
	NormalizedQuery      string      `json:"normalized_query,omitempty"`
	Intent               string      `json:"intent,omitempty"`
	Hits                 []SearchHit `json:"hits"`
	RetrievalMode        string      `json:"retrieval_mode"`
	LatencyMS            int64       `json:"latency_ms"`
	Summary              string      `json:"summary,omitempty"`
	Confidence           float64     `json:"confidence,omitempty"`
	RetrievalEvidenceIDs []string    `json:"retrieval_evidence_ids,omitempty"`
}

// DocumentRecord is returned by the document lookup endpoint.
type DocumentRecord struct {
	RequestedID string         `json:"requested_id"`
	Document    SourceDocument `json:"document"`
	Chunks      []Chunk        `json:"chunks"`
}

// QuarantineRecord captures unsupported or binary sources encountered during ingestion.
type QuarantineRecord struct {
	Path       string `json:"path"`
	SourceType string `json:"source_type"`
	Reason     string `json:"reason"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
}

// Retriever is the pluggable retrieval contract used by the agent query service.
type Retriever interface {
	Search(context.Context, string, int) ([]Document, error)
	Stats() Stats
}

// KnowledgeBase extends Retriever with index lifecycle and document lookup operations.
type KnowledgeBase interface {
	Retriever
	Query(context.Context, QueryRequest) (QueryResult, error)
	Rebuild(context.Context) (Stats, error)
	Update(context.Context) (Stats, error)
	Document(string) (DocumentRecord, bool)
}

// Stats reports retriever index metadata and current health.
type Stats struct {
	Enabled           bool           `json:"enabled"`
	Ready             bool           `json:"ready"`
	DatasetPath       string         `json:"dataset_path"`
	IndexPath         string         `json:"index_path"`
	StoragePath       string         `json:"storage_path"`
	CachePath         string         `json:"cache_path"`
	DocCount          int            `json:"doc_count"`
	ChunkCount        int            `json:"chunk_count"`
	SourceCount       int            `json:"source_count"`
	QuarantineCount   int            `json:"quarantine_count"`
	RetrievalMode     string         `json:"retrieval_mode"`
	EmbeddingProvider string         `json:"embedding_provider"`
	EmbeddingModel    string         `json:"embedding_model,omitempty"`
	VectorBackend     string         `json:"vector_backend,omitempty"`
	VectorCollection  string         `json:"vector_collection,omitempty"`
	VectorHealthy     bool           `json:"vector_healthy"`
	VectorGeneration  string         `json:"vector_generation,omitempty"`
	VectorLastError   string         `json:"vector_last_error,omitempty"`
	ChunkSize         int            `json:"chunk_size"`
	ChunkOverlap      int            `json:"chunk_overlap"`
	MaxSnippetLen     int            `json:"max_snippet_len"`
	LastBuiltAt       time.Time      `json:"last_built_at,omitempty"`
	LastUpdatedAt     time.Time      `json:"last_updated_at,omitempty"`
	LastError         string         `json:"last_error,omitempty"`
	SourceTypes       map[string]int `json:"source_types,omitempty"`
	KnowledgeTypes    map[string]int `json:"knowledge_types,omitempty"`
	CaseTypes         map[string]int `json:"case_types,omitempty"`
}

func storagePath(indexPath string) string {
	return filepath.Dir(filepath.Clean(indexPath))
}

func cachePath(indexPath string) string {
	return filepath.Join(storagePath(indexPath), "cache")
}

func extractedPath(indexPath string) string {
	return filepath.Join(cachePath(indexPath), "extracted")
}

func quarantinePath(indexPath string) string {
	return filepath.Join(storagePath(indexPath), "quarantine.json")
}

func normalizeOption(raw, fallback string, allowed ...string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return fallback
	}
	for _, item := range allowed {
		if value == item {
			return item
		}
	}
	return fallback
}

func parseBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}
