package rag

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Config controls local RAG indexing and retrieval behavior.
type Config struct {
	Enabled         bool
	Paths           []string
	IndexPath       string
	TopK            int
	MaxSnippetChars int
}

// DefaultConfig returns safe local defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:         false,
		Paths:           []string{"README.md", "docs", "configs"},
		IndexPath:       "var/rag/index.json",
		TopK:            4,
		MaxSnippetChars: 1000,
	}
}

// Document is a searchable snippet entry.
type Document struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Retriever is the pluggable retrieval contract.
type Retriever interface {
	Search(context.Context, string, int) ([]Document, error)
	Stats() Stats
}

// Stats reports retriever index metadata.
type Stats struct {
	Enabled       bool      `json:"enabled"`
	DocCount      int       `json:"doc_count"`
	IndexPath     string    `json:"index_path"`
	LastBuiltAt   time.Time `json:"last_built_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	MaxSnippetLen int       `json:"max_snippet_len"`
}

type persistedIndex struct {
	BuiltAt    time.Time  `json:"built_at"`
	Documents  []Document `json:"documents"`
	IndexPath  string     `json:"index_path"`
	Schema     string     `json:"schema"`
	Repository string     `json:"repository"`
}

// LocalRetriever is an embedded lexical retriever for local docs/runbooks/incidents.
type LocalRetriever struct {
	cfg   Config
	log   *zap.Logger
	docs  []Document
	stats Stats
}

// NewLocalRetriever initializes a local retriever and persists the built index.
func NewLocalRetriever(cfg Config, logger *zap.Logger) (*LocalRetriever, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = normalizeConfig(cfg)
	r := &LocalRetriever{
		cfg: cfg,
		log: logger.With(zap.String("component", "rag_local_retriever")),
		stats: Stats{
			Enabled:       cfg.Enabled,
			IndexPath:     cfg.IndexPath,
			MaxSnippetLen: cfg.MaxSnippetChars,
		},
	}
	if !cfg.Enabled {
		return r, nil
	}

	if loaded, err := r.loadIndex(); err == nil && len(loaded) > 0 {
		r.docs = loaded
		r.stats.DocCount = len(loaded)
	}

	built, err := buildDocuments(cfg.Paths, cfg.MaxSnippetChars)
	if err != nil {
		r.stats.LastError = err.Error()
		if len(r.docs) == 0 {
			return r, err
		}
		return r, nil
	}
	if len(built) > 0 {
		r.docs = built
		r.stats.DocCount = len(built)
		r.stats.LastBuiltAt = time.Now().UTC()
		if err := r.saveIndex(); err != nil {
			r.stats.LastError = err.Error()
		}
	}
	return r, nil
}

func normalizeConfig(cfg Config) Config {
	def := DefaultConfig()
	if cfg.TopK <= 0 {
		cfg.TopK = def.TopK
	}
	if cfg.MaxSnippetChars <= 0 {
		cfg.MaxSnippetChars = def.MaxSnippetChars
	}
	if strings.TrimSpace(cfg.IndexPath) == "" {
		cfg.IndexPath = def.IndexPath
	}
	if len(cfg.Paths) == 0 {
		cfg.Paths = append([]string(nil), def.Paths...)
	}
	return cfg
}

func buildDocuments(paths []string, maxChars int) ([]Document, error) {
	docs := make([]Document, 0, 256)
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			err = filepath.WalkDir(path, func(current string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if !isRAGFile(current) {
					return nil
				}
				items, readErr := readDocumentChunks(current, maxChars)
				if readErr != nil {
					return nil
				}
				docs = append(docs, items...)
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if !isRAGFile(path) {
			continue
		}
		items, err := readDocumentChunks(path, maxChars)
		if err != nil {
			continue
		}
		docs = append(docs, items...)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no RAG documents were indexed")
	}
	return docs, nil
}

func isRAGFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".md") ||
		strings.HasSuffix(lower, ".txt") ||
		strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") ||
		strings.HasSuffix(lower, ".json")
}

func readDocumentChunks(path string, maxChars int) ([]Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, _ := f.Stat()
	title := filepath.Base(path)
	scanner := bufio.NewScanner(f)
	buf := strings.Builder{}
	items := make([]Document, 0, 4)
	chunkIdx := 0

	flush := func() {
		content := strings.TrimSpace(buf.String())
		if content == "" {
			buf.Reset()
			return
		}
		id := fmt.Sprintf("%s#%d", path, chunkIdx)
		items = append(items, Document{
			ID:        id,
			Path:      path,
			Title:     title,
			Content:   content,
			UpdatedAt: fileModTime(info),
		})
		chunkIdx++
		buf.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()
		if buf.Len()+len(line)+1 > maxChars {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
	}
	flush()
	return items, nil
}

func fileModTime(info os.FileInfo) time.Time {
	if info == nil {
		return time.Now().UTC()
	}
	return info.ModTime().UTC()
}

// Search performs deterministic lexical retrieval.
func (r *LocalRetriever) Search(_ context.Context, query string, topK int) ([]Document, error) {
	if r == nil || !r.cfg.Enabled {
		return nil, nil
	}
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil, nil
	}
	if topK <= 0 {
		topK = r.cfg.TopK
	}
	if topK <= 0 {
		topK = 4
	}

	tokens := tokenize(q)
	type scored struct {
		score float64
		doc   Document
	}
	rows := make([]scored, 0, len(r.docs))
	for _, doc := range r.docs {
		text := strings.ToLower(doc.Content)
		score := 0.0
		for _, token := range tokens {
			if token == "" {
				continue
			}
			score += float64(strings.Count(text, token))
		}
		if strings.Contains(strings.ToLower(doc.Title), tokens[0]) {
			score += 0.7
		}
		if score <= 0 {
			continue
		}
		ageHours := time.Since(doc.UpdatedAt).Hours()
		if ageHours > 0 && ageHours < 24*30 {
			score += 0.2
		}
		rows = append(rows, scored{score: score, doc: doc})
	}
	if len(rows) == 0 {
		return nil, nil
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].doc.UpdatedAt.After(rows[j].doc.UpdatedAt)
		}
		return rows[i].score > rows[j].score
	})

	out := make([]Document, 0, topK)
	for i := 0; i < len(rows) && i < topK; i++ {
		out = append(out, rows[i].doc)
	}
	return out, nil
}

// Stats returns retriever metadata.
func (r *LocalRetriever) Stats() Stats {
	if r == nil {
		return Stats{}
	}
	return r.stats
}

func (r *LocalRetriever) saveIndex() error {
	if r == nil || strings.TrimSpace(r.cfg.IndexPath) == "" {
		return nil
	}
	dir := filepath.Dir(r.cfg.IndexPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	payload := persistedIndex{
		BuiltAt:    time.Now().UTC(),
		Documents:  r.docs,
		IndexPath:  r.cfg.IndexPath,
		Schema:     "rag-v0.5-local",
		Repository: "ai_sre_agent",
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.cfg.IndexPath, raw, 0o644)
}

func (r *LocalRetriever) loadIndex() ([]Document, error) {
	if r == nil || strings.TrimSpace(r.cfg.IndexPath) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(r.cfg.IndexPath)
	if err != nil {
		return nil, err
	}
	payload := persistedIndex{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload.Documents, nil
}

func tokenize(text string) []string {
	replacer := strings.NewReplacer(",", " ", ".", " ", ":", " ", ";", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ",
		"/", " ", "\\", " ", "|", " ", "=", " ", "\"", " ", "'", " ",
	)
	parts := strings.Fields(replacer.Replace(strings.ToLower(text)))
	if len(parts) == 0 {
		return []string{text}
	}
	return parts
}
