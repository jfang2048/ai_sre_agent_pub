package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type RAGIndex struct {
	chunks []string
}

func BuildRAGIndex(paths []string, maxChars int) (*RAGIndex, error) {
	if maxChars <= 0 {
		maxChars = 1200
	}
	chunks := make([]string, 0)
	for _, p := range paths {
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if !isRAGFile(path) {
					return nil
				}
				fileChunks := loadChunks(path, maxChars)
				chunks = append(chunks, fileChunks...)
				return nil
			})
			continue
		}
		if isRAGFile(p) {
			chunks = append(chunks, loadChunks(p, maxChars)...)
		}
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no rag documents loaded")
	}
	return &RAGIndex{chunks: chunks}, nil
}

func (r *RAGIndex) Search(queries []string, limit int) []string {
	if r == nil || len(r.chunks) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 4
	}
	query := strings.ToLower(strings.Join(queries, " "))
	queryTokens := tokenize(query)
	type scored struct {
		score int
		text  string
	}
	scoredChunks := make([]scored, 0, len(r.chunks))
	for _, chunk := range r.chunks {
		text := strings.ToLower(chunk)
		score := 0
		for _, tok := range queryTokens {
			if tok == "" {
				continue
			}
			score += strings.Count(text, tok)
		}
		if score > 0 {
			scoredChunks = append(scoredChunks, scored{score: score, text: chunk})
		}
	}
	sort.Slice(scoredChunks, func(i, j int) bool {
		return scoredChunks[i].score > scoredChunks[j].score
	})
	out := make([]string, 0, limit)
	for i := 0; i < len(scoredChunks) && i < limit; i++ {
		out = append(out, scoredChunks[i].text)
	}
	return out
}

func joinSnippets(snippets []string) string {
	if len(snippets) == 0 {
		return ""
	}
	return strings.Join(snippets, "\n---\n")
}

func isRAGFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".txt")
}

func loadChunks(path string, maxChars int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var buf strings.Builder
	chunks := make([]string, 0)
	for scanner.Scan() {
		line := scanner.Text()
		if buf.Len()+len(line)+1 > maxChars {
			chunks = append(chunks, buf.String())
			buf.Reset()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(line)
	}
	if buf.Len() > 0 {
		chunks = append(chunks, buf.String())
	}
	return chunks
}

func tokenize(text string) []string {
	replacer := strings.NewReplacer(",", " ", ".", " ", ":", " ", ";", " ", "(", " ", ")", " ", "[", " ", "]", " ")
	clean := replacer.Replace(text)
	parts := strings.Fields(clean)
	return parts
}
