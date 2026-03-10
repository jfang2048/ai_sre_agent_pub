package rag

import (
	"fmt"
	"strings"
)

func chunkDocuments(doc SourceDocument, cfg Config) []Chunk {
	content := strings.TrimSpace(normalizeWhitespace(doc.Content))
	if content == "" {
		return nil
	}
	strategy := chooseChunkStrategy(doc, cfg)
	sections := splitSections(content, strategy)
	if len(sections) == 0 {
		sections = []string{content}
	}

	chunks := make([]Chunk, 0, maxInt(1, len(content)/maxInt(cfg.ChunkSize, 1)+1))
	var builder strings.Builder
	sectionStart := 0
	currentStart := 0
	currentEnd := 0

	flush := func() {
		text := strings.TrimSpace(builder.String())
		if text == "" {
			builder.Reset()
			return
		}
		chunkIndex := len(chunks) + 1
		chunkMetadata := cloneMap(doc.Metadata)
		if chunkMetadata == nil {
			chunkMetadata = make(map[string]string, 3)
		}
		chunkMetadata["chunk_strategy"] = strategy
		chunkMetadata["chunk_index"] = fmt.Sprintf("%d", chunkIndex)
		chunkMetadata["source_key"] = doc.SourceKey
		chunks = append(chunks, Chunk{
			ChunkID:     fmt.Sprintf("%s#%03d", doc.DocID, chunkIndex),
			DocID:       doc.DocID,
			SourceKey:   doc.SourceKey,
			SourcePath:  doc.SourcePath,
			SourceType:  doc.SourceType,
			Title:       doc.Title,
			Content:     text,
			Tags:        append([]string(nil), doc.Tags...),
			Timestamp:   cloneTime(doc.Timestamp),
			Metadata:    chunkMetadata,
			UpdatedAt:   doc.UpdatedAt,
			ChunkIndex:  chunkIndex,
			OffsetStart: currentStart,
			OffsetEnd:   currentEnd,
			Strategy:    strategy,
		})
		builder.Reset()
	}

	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			sectionStart += len(section)
			continue
		}
		if builder.Len() == 0 {
			currentStart = sectionStart
		}
		if builder.Len() > 0 && builder.Len()+len(section)+2 > cfg.ChunkSize {
			flush()
			if len(chunks) > 0 && cfg.ChunkOverlap > 0 {
				overlap := tailRunes(chunks[len(chunks)-1].Content, cfg.ChunkOverlap)
				if overlap != "" {
					builder.WriteString(overlap)
					builder.WriteString("\n")
					currentStart = maxInt(0, sectionStart-len([]rune(overlap)))
				}
			}
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(section)
		currentEnd = sectionStart + len([]rune(section))
		sectionStart += len([]rune(section)) + 2
	}
	flush()
	return chunks
}

func chooseChunkStrategy(doc SourceDocument, cfg Config) string {
	if strings.EqualFold(cfg.ChunkStrategy, "auto") {
		switch {
		case strings.Contains(doc.SourceType, "markdown"):
			return "markdown"
		case strings.Contains(doc.SourceType, "json"), strings.Contains(doc.SourceType, "csv"), strings.Contains(doc.SourceType, "tsv"), strings.Contains(doc.SourceType, "jsonl"):
			return "record"
		case strings.Contains(doc.SourceType, "html"), strings.Contains(doc.SourceType, "xml"):
			return "paragraph"
		default:
			return "paragraph"
		}
	}
	return cfg.ChunkStrategy
}

func splitSections(content, strategy string) []string {
	switch strategy {
	case "markdown":
		return splitMarkdownSections(content)
	case "line":
		return splitLineSections(content)
	case "record":
		return splitRecordSections(content)
	default:
		return splitParagraphSections(content)
	}
}

func splitMarkdownSections(content string) []string {
	lines := strings.Split(content, "\n")
	sections := make([]string, 0, len(lines)/4+1)
	var builder strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") && builder.Len() > 0 {
			sections = append(sections, strings.TrimSpace(builder.String()))
			builder.Reset()
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(line)
	}
	if builder.Len() > 0 {
		sections = append(sections, strings.TrimSpace(builder.String()))
	}
	return sections
}

func splitParagraphSections(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	raw := strings.Split(content, "\n\n")
	sections := make([]string, 0, len(raw))
	for _, section := range raw {
		section = strings.TrimSpace(section)
		if section != "" {
			sections = append(sections, section)
		}
	}
	return sections
}

func splitLineSections(content string) []string {
	lines := strings.Split(content, "\n")
	sections := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			sections = append(sections, line)
		}
	}
	return sections
}

func splitRecordSections(content string) []string {
	lines := strings.Split(content, "\n")
	sections := make([]string, 0, len(lines)/2+1)
	var builder strings.Builder
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if builder.Len() > 0 {
				sections = append(sections, strings.TrimSpace(builder.String()))
				builder.Reset()
			}
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(line)
	}
	if builder.Len() > 0 {
		sections = append(sections, strings.TrimSpace(builder.String()))
	}
	if len(sections) == 0 {
		return splitParagraphSections(content)
	}
	return sections
}

func tailRunes(content string, count int) string {
	if count <= 0 {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= count {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(string(runes[len(runes)-count:]))
}

func normalizeWhitespace(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}
