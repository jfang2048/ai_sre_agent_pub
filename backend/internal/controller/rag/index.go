package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type posting struct {
	ChunkID string
	TF      float64
}

type scoredChunk struct {
	Chunk Chunk
	Score float64
}

type indexData struct {
	BuiltAt    time.Time
	UpdatedAt  time.Time
	Documents  map[string]SourceDocument
	Chunks     map[string]Chunk
	Sources    map[string]sourceRecord
	Quarantine []QuarantineRecord

	chunkOrder      []string
	chunkLengths    map[string]int
	tokenIndex      map[string][]posting
	avgChunkLength  float64
	sourceTypeCount map[string]int
}

type persistedIndex struct {
	Schema     string             `json:"schema"`
	BuiltAt    time.Time          `json:"built_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Documents  []SourceDocument   `json:"documents"`
	Chunks     []Chunk            `json:"chunks"`
	Sources    []sourceRecord     `json:"sources"`
	Quarantine []QuarantineRecord `json:"quarantine,omitempty"`
}

func loadIndex(path string) (*indexData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload persistedIndex
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode rag index: %w", err)
	}
	if payload.Schema != schemaVersion {
		return nil, fmt.Errorf("unsupported rag index schema %q", payload.Schema)
	}
	index := &indexData{
		BuiltAt:    payload.BuiltAt,
		UpdatedAt:  payload.UpdatedAt,
		Documents:  make(map[string]SourceDocument, len(payload.Documents)),
		Chunks:     make(map[string]Chunk, len(payload.Chunks)),
		Sources:    make(map[string]sourceRecord, len(payload.Sources)),
		Quarantine: append([]QuarantineRecord(nil), payload.Quarantine...),
	}
	for _, doc := range payload.Documents {
		index.Documents[doc.DocID] = doc
	}
	for _, chunk := range payload.Chunks {
		index.Chunks[chunk.ChunkID] = chunk
	}
	for _, source := range payload.Sources {
		index.Sources[source.SourceKey] = source
	}
	index.rebuildSearchStructures()
	return index, nil
}

func saveIndex(path string, index *indexData) error {
	if index == nil {
		return fmt.Errorf("index is nil")
	}
	docs := make([]SourceDocument, 0, len(index.Documents))
	for _, doc := range index.Documents {
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].SourcePath == docs[j].SourcePath {
			return docs[i].DocID < docs[j].DocID
		}
		return docs[i].SourcePath < docs[j].SourcePath
	})
	chunks := make([]Chunk, 0, len(index.Chunks))
	for _, chunk := range index.Chunks {
		chunks = append(chunks, chunk)
	}
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].SourcePath == chunks[j].SourcePath {
			return chunks[i].ChunkIndex < chunks[j].ChunkIndex
		}
		return chunks[i].SourcePath < chunks[j].SourcePath
	})
	sources := make([]sourceRecord, 0, len(index.Sources))
	for _, source := range index.Sources {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].SourceKey < sources[j].SourceKey
	})
	payload := persistedIndex{
		Schema:     schemaVersion,
		BuiltAt:    index.BuiltAt,
		UpdatedAt:  index.UpdatedAt,
		Documents:  docs,
		Chunks:     chunks,
		Sources:    sources,
		Quarantine: append([]QuarantineRecord(nil), index.Quarantine...),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode rag index: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create rag index directory: %w", err)
	}
	return os.WriteFile(path, raw, 0o644)
}

func newIndexData(builtAt time.Time, docs []SourceDocument, chunks []Chunk, sources []sourceRecord, quarantine []QuarantineRecord) *indexData {
	index := &indexData{
		BuiltAt:    builtAt.UTC(),
		UpdatedAt:  time.Now().UTC(),
		Documents:  make(map[string]SourceDocument, len(docs)),
		Chunks:     make(map[string]Chunk, len(chunks)),
		Sources:    make(map[string]sourceRecord, len(sources)),
		Quarantine: append([]QuarantineRecord(nil), quarantine...),
	}
	for _, doc := range docs {
		index.Documents[doc.DocID] = doc
	}
	for _, chunk := range chunks {
		index.Chunks[chunk.ChunkID] = chunk
	}
	for _, source := range sources {
		index.Sources[source.SourceKey] = source
	}
	index.rebuildSearchStructures()
	return index
}

func (i *indexData) rebuildSearchStructures() {
	i.tokenIndex = make(map[string][]posting, len(i.Chunks)*8)
	i.chunkLengths = make(map[string]int, len(i.Chunks))
	i.chunkOrder = make([]string, 0, len(i.Chunks))
	i.sourceTypeCount = make(map[string]int, 8)
	totalTokens := 0.0

	chunks := make([]Chunk, 0, len(i.Chunks))
	for _, chunk := range i.Chunks {
		chunks = append(chunks, chunk)
	}
	sort.Slice(chunks, func(left, right int) bool {
		if chunks[left].SourcePath == chunks[right].SourcePath {
			return chunks[left].ChunkIndex < chunks[right].ChunkIndex
		}
		return chunks[left].SourcePath < chunks[right].SourcePath
	})

	for _, chunk := range chunks {
		i.chunkOrder = append(i.chunkOrder, chunk.ChunkID)
		i.sourceTypeCount[chunk.SourceType]++
		tokens := tokenize(chunk.Title)
		tokens = append(tokens, tokenize(chunk.Title)...)
		tokens = append(tokens, tokenize(chunk.Content)...)
		i.chunkLengths[chunk.ChunkID] = len(tokens)
		totalTokens += float64(len(tokens))
		frequencies := make(map[string]float64, len(tokens))
		for _, token := range tokens {
			if token == "" {
				continue
			}
			frequencies[token]++
		}
		for token, frequency := range frequencies {
			i.tokenIndex[token] = append(i.tokenIndex[token], posting{ChunkID: chunk.ChunkID, TF: frequency})
		}
	}
	if len(i.Chunks) > 0 {
		i.avgChunkLength = totalTokens / float64(len(i.Chunks))
	}
}

func (i *indexData) query(ctx context.Context, cfg Config, embedder embedder, req QueryRequest) (QueryResult, error) {
	_ = ctx
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return QueryResult{Query: "", RetrievalMode: cfg.RetrievalMode}, nil
	}
	if i == nil || len(i.Chunks) == 0 {
		return QueryResult{Query: query, RetrievalMode: cfg.RetrievalMode}, nil
	}
	topK := req.TopK
	if topK <= 0 {
		topK = cfg.TopK
	}
	queryTokens := tokenize(query)
	lexicalScores := i.lexicalScores(queryTokens)
	vectorScores := i.vectorScores(query, cfg, embedder)
	return i.buildQueryResult(query, cfg, topK, combineScores(cfg.RetrievalMode, lexicalScores, vectorScores)), nil
}

func (i *indexData) buildQueryResult(query string, cfg Config, topK int, combined map[string]float64) QueryResult {
	ranked := make([]scoredChunk, 0, len(combined))
	for chunkID, score := range combined {
		if score <= 0 {
			continue
		}
		chunk, ok := i.Chunks[chunkID]
		if !ok {
			continue
		}
		ranked = append(ranked, scoredChunk{Chunk: chunk, Score: score})
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].Score == ranked[right].Score {
			if ranked[left].Chunk.SourcePath == ranked[right].Chunk.SourcePath {
				return ranked[left].Chunk.ChunkIndex < ranked[right].Chunk.ChunkIndex
			}
			return ranked[left].Chunk.SourcePath < ranked[right].Chunk.SourcePath
		}
		return ranked[left].Score > ranked[right].Score
	})
	if topK > 0 && len(ranked) > topK {
		ranked = ranked[:topK]
	}

	hits := make([]SearchHit, 0, len(ranked))
	retrievalEvidenceIDs := make([]string, 0, len(ranked))
	uniqueDocs := make(map[string]struct{}, len(ranked))
	for _, item := range ranked {
		uniqueDocs[item.Chunk.DocID] = struct{}{}
		retrievalEvidenceIDs = append(retrievalEvidenceIDs, item.Chunk.ChunkID)
		hits = append(hits, SearchHit{
			DocID:      item.Chunk.DocID,
			ChunkID:    item.Chunk.ChunkID,
			Score:      item.Score,
			SourcePath: item.Chunk.SourcePath,
			SourceType: item.Chunk.SourceType,
			Title:      item.Chunk.Title,
			Snippet:    makeSnippet(item.Chunk.Content, query, cfg.MaxSnippetChars),
			Tags:       append([]string(nil), item.Chunk.Tags...),
			Metadata:   cloneMap(item.Chunk.Metadata),
			Timestamp:  cloneTime(item.Chunk.Timestamp),
			Content:    item.Chunk.Content,
		})
	}
	confidence := retrievalConfidence(ranked)
	summary := "no knowledge hits matched the query"
	if len(hits) > 0 {
		summary = fmt.Sprintf("retrieved %d knowledge hits across %d documents", len(hits), len(uniqueDocs))
	}
	return QueryResult{
		Query:                query,
		Hits:                 hits,
		RetrievalMode:        cfg.RetrievalMode,
		Summary:              summary,
		Confidence:           confidence,
		RetrievalEvidenceIDs: append([]string(nil), retrievalEvidenceIDs...),
	}
}

func (i *indexData) lexicalScores(queryTokens []string) map[string]float64 {
	scores := make(map[string]float64)
	if len(queryTokens) == 0 {
		return scores
	}
	seen := make(map[string]struct{}, len(queryTokens))
	docCount := float64(len(i.Chunks))
	for _, token := range queryTokens {
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		postings := i.tokenIndex[token]
		if len(postings) == 0 {
			continue
		}
		df := float64(len(postings))
		idf := math.Log(1 + (docCount-df+0.5)/(df+0.5))
		for _, posting := range postings {
			length := float64(i.chunkLengths[posting.ChunkID])
			if length == 0 {
				length = 1
			}
			denom := posting.TF + 1.2*(1-0.75+0.75*(length/maxFloat(i.avgChunkLength, 1)))
			scores[posting.ChunkID] += idf * (posting.TF * 2.2) / maxFloat(denom, 1)
		}
	}
	return scores
}

func (i *indexData) vectorScores(query string, cfg Config, embedder embedder) map[string]float64 {
	scores := make(map[string]float64)
	if strings.EqualFold(cfg.RetrievalMode, "lexical") || embedder == nil {
		return scores
	}
	queryVector, err := embedder.EmbedQuery(context.Background(), query)
	if err != nil || len(queryVector) == 0 {
		return scores
	}
	for _, chunkID := range i.chunkOrder {
		chunk, ok := i.Chunks[chunkID]
		if !ok || len(chunk.Embedding) == 0 {
			continue
		}
		score := dotProduct(queryVector, chunk.Embedding)
		if score > 0 {
			scores[chunkID] = score
		}
	}
	return scores
}

func combineScores(mode string, lexicalScores, vectorScores map[string]float64) map[string]float64 {
	combined := make(map[string]float64)
	switch mode {
	case "lexical":
		for chunkID, score := range lexicalScores {
			combined[chunkID] = score
		}
		return combined
	case "vector":
		for chunkID, score := range vectorScores {
			combined[chunkID] = score
		}
		return combined
	default:
		maxLexical := maxScore(lexicalScores)
		maxVector := maxScore(vectorScores)
		for chunkID, score := range lexicalScores {
			combined[chunkID] += normalizeScore(score, maxLexical) * 0.6
		}
		for chunkID, score := range vectorScores {
			combined[chunkID] += normalizeScore(score, maxVector) * 0.4
		}
		return combined
	}
}

func retrievalConfidence(hits []scoredChunk) float64 {
	if len(hits) == 0 {
		return 0
	}
	top := hits[0].Score
	if len(hits) == 1 {
		return clamp01(top)
	}
	gap := top - hits[1].Score
	return clamp01(top*0.7 + gap*0.3)
}

func maxScore(values map[string]float64) float64 {
	best := 0.0
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func normalizeScore(value, maxValue float64) float64 {
	if maxValue <= 0 {
		return 0
	}
	return value / maxValue
}

func makeSnippet(content, query string, maxChars int) string {
	content = strings.TrimSpace(normalizeWhitespace(content))
	if maxChars <= 0 {
		maxChars = 280
	}
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	lowerContent := strings.ToLower(content)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	start := 0
	if lowerQuery != "" {
		if index := strings.Index(lowerContent, lowerQuery); index >= 0 {
			start = maxInt(0, len([]rune(content[:index]))-maxChars/4)
		}
	}
	end := minInt(len(runes), start+maxChars)
	snippet := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet += "..."
	}
	return snippet
}

func tokenize(text string) []string {
	text = strings.ToLower(stripUTF8BOM(text))
	runes := []rune(text)
	tokens := make([]string, 0, len(runes))
	buffer := make([]rune, 0, 24)
	flush := func() {
		if len(buffer) == 0 {
			return
		}
		segment := string(buffer)
		if containsHan(buffer) {
			for index := range buffer {
				tokens = append(tokens, string(buffer[index]))
				if index+1 < len(buffer) {
					tokens = append(tokens, string(buffer[index:index+2]))
				}
				if index+2 < len(buffer) {
					tokens = append(tokens, string(buffer[index:index+3]))
				}
			}
			if len(buffer) <= 8 {
				tokens = append(tokens, segment)
			}
		} else if len(segment) > 1 {
			tokens = append(tokens, segment)
		}
		buffer = buffer[:0]
	}
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || isHan(r) {
			buffer = append(buffer, r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func containsHan(runes []rune) bool {
	for _, r := range runes {
		if isHan(r) {
			return true
		}
	}
	return false
}

func isHan(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

func (i *indexData) document(requestedID string) (DocumentRecord, bool) {
	requestedID = strings.TrimSpace(requestedID)
	if requestedID == "" {
		return DocumentRecord{}, false
	}
	if doc, ok := i.Documents[requestedID]; ok {
		chunks := i.chunksForDoc(doc.DocID)
		return DocumentRecord{RequestedID: requestedID, Document: doc, Chunks: chunks}, true
	}
	if chunk, ok := i.Chunks[requestedID]; ok {
		doc, ok := i.Documents[chunk.DocID]
		if !ok {
			return DocumentRecord{}, false
		}
		chunks := i.chunksForDoc(doc.DocID)
		return DocumentRecord{RequestedID: requestedID, Document: doc, Chunks: chunks}, true
	}
	return DocumentRecord{}, false
}

func (i *indexData) chunksForDoc(docID string) []Chunk {
	chunks := make([]Chunk, 0, 4)
	for _, chunk := range i.Chunks {
		if chunk.DocID == docID {
			chunks = append(chunks, chunk)
		}
	}
	sort.Slice(chunks, func(left, right int) bool {
		return chunks[left].ChunkIndex < chunks[right].ChunkIndex
	})
	return chunks
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
