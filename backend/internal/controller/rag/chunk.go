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
	if strategy == "case" {
		if chunks := chunkStructuredKnowledge(doc, cfg); len(chunks) > 0 {
			return chunks
		}
		strategy = "paragraph"
	}
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
		chunkMetadata["knowledge_type"] = doc.KnowledgeType
		chunkMetadata["case_type"] = doc.CaseType
		chunks = append(chunks, Chunk{
			ChunkID:          fmt.Sprintf("%s#%03d", doc.DocID, chunkIndex),
			DocID:            doc.DocID,
			SourceKey:        doc.SourceKey,
			SourcePath:       doc.SourcePath,
			SourceType:       doc.SourceType,
			KnowledgeType:    doc.KnowledgeType,
			CaseType:         doc.CaseType,
			Title:            doc.Title,
			Summary:          doc.Summary,
			Content:          text,
			RetrievalText:    compactKnowledgeText(doc.Title, doc.Summary, strings.Join(doc.Signals, " "), text),
			EmbeddingText:    compactKnowledgeText(doc.Title, doc.Summary, strings.Join(doc.LikelyCauses, "\n"), strings.Join(doc.RemediationSteps, "\n"), text),
			Symptoms:         append([]string(nil), doc.Symptoms...),
			Evidence:         append([]string(nil), doc.Evidence...),
			LikelyCauses:     append([]string(nil), doc.LikelyCauses...),
			RemediationSteps: append([]string(nil), doc.RemediationSteps...),
			Commands:         append([]string(nil), doc.Commands...),
			Environment:      append([]string(nil), doc.Environment...),
			Signals:          append([]string(nil), doc.Signals...),
			RetrievalWeight:  doc.RetrievalWeight,
			Tags:             append([]string(nil), doc.Tags...),
			Timestamp:        cloneTime(doc.Timestamp),
			Metadata:         chunkMetadata,
			UpdatedAt:        doc.UpdatedAt,
			ChunkIndex:       chunkIndex,
			OffsetStart:      currentStart,
			OffsetEnd:        currentEnd,
			Strategy:         strategy,
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
		isHTML := strings.Contains(doc.SourceType, "html") || strings.Contains(doc.SourceType, "xml")
		if hasStructuredKnowledge(doc) && !isHTML {
			return "case"
		}
		switch {
		case strings.Contains(doc.SourceType, "markdown"):
			return "markdown"
		case strings.Contains(doc.SourceType, "json"), strings.Contains(doc.SourceType, "csv"), strings.Contains(doc.SourceType, "tsv"), strings.Contains(doc.SourceType, "jsonl"):
			return "record"
		case isHTML:
			return "paragraph"
		default:
			return "paragraph"
		}
	}
	return cfg.ChunkStrategy
}

func chunkStructuredKnowledge(doc SourceDocument, cfg Config) []Chunk {
	sections := []struct {
		sectionType string
		title       string
		content     string
	}{
		{
			sectionType: "summary",
			title:       doc.Title,
			content: compactKnowledgeText(
				doc.Summary,
				joinLabeledList("Symptoms", doc.Symptoms),
				joinLabeledList("Signals", doc.Signals),
				joinLabeledList("Environment", doc.Environment),
			),
		},
		{
			sectionType: "evidence",
			title:       doc.Title,
			content: compactKnowledgeText(
				joinLabeledList("Evidence", doc.Evidence),
				joinLabeledList("Likely causes", doc.LikelyCauses),
			),
		},
		{
			sectionType: "remediation",
			title:       doc.Title,
			content: compactKnowledgeText(
				joinLabeledList("Remediation steps", doc.RemediationSteps),
				joinLabeledList("Commands", doc.Commands),
			),
		},
		{
			sectionType: "body",
			title:       doc.Title,
			content:     doc.Content,
		},
	}

	chunks := make([]Chunk, 0, len(sections))
	seen := map[string]struct{}{}
	for _, section := range sections {
		text := strings.TrimSpace(section.content)
		if text == "" {
			continue
		}
		fingerprint := shortHash(section.sectionType + "\n" + text)
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		seen[fingerprint] = struct{}{}
		parts := splitSections(text, "paragraph")
		if len(parts) == 0 {
			parts = []string{text}
		}
		var builder strings.Builder
		flush := func() {
			payload := strings.TrimSpace(builder.String())
			if payload == "" {
				builder.Reset()
				return
			}
			chunkIndex := len(chunks) + 1
			metadata := cloneMap(doc.Metadata)
			if metadata == nil {
				metadata = make(map[string]string, 4)
			}
			metadata["chunk_strategy"] = "case"
			metadata["chunk_index"] = fmt.Sprintf("%d", chunkIndex)
			metadata["source_key"] = doc.SourceKey
			metadata["knowledge_type"] = doc.KnowledgeType
			metadata["case_type"] = doc.CaseType
			metadata["section_type"] = section.sectionType
			chunks = append(chunks, Chunk{
				ChunkID:          fmt.Sprintf("%s#%03d", doc.DocID, chunkIndex),
				DocID:            doc.DocID,
				SourceKey:        doc.SourceKey,
				SourcePath:       doc.SourcePath,
				SourceType:       doc.SourceType,
				KnowledgeType:    doc.KnowledgeType,
				CaseType:         doc.CaseType,
				Title:            doc.Title,
				Summary:          doc.Summary,
				Content:          payload,
				RetrievalText:    compactKnowledgeText(doc.Title, doc.Summary, payload, strings.Join(doc.Tags, " ")),
				EmbeddingText:    compactKnowledgeText(doc.Title, doc.Summary, strings.Join(doc.LikelyCauses, "\n"), strings.Join(doc.RemediationSteps, "\n"), payload),
				Symptoms:         append([]string(nil), doc.Symptoms...),
				Evidence:         append([]string(nil), doc.Evidence...),
				LikelyCauses:     append([]string(nil), doc.LikelyCauses...),
				RemediationSteps: append([]string(nil), doc.RemediationSteps...),
				Commands:         append([]string(nil), doc.Commands...),
				Environment:      append([]string(nil), doc.Environment...),
				Signals:          append([]string(nil), doc.Signals...),
				SectionType:      section.sectionType,
				RetrievalWeight:  doc.RetrievalWeight,
				Tags:             append([]string(nil), doc.Tags...),
				Timestamp:        cloneTime(doc.Timestamp),
				Metadata:         metadata,
				UpdatedAt:        doc.UpdatedAt,
				ChunkIndex:       chunkIndex,
				OffsetStart:      0,
				OffsetEnd:        len([]rune(payload)),
				Strategy:         "case",
			})
			builder.Reset()
		}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if builder.Len() > 0 && builder.Len()+len(part)+2 > cfg.ChunkSize {
				flush()
			}
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(part)
		}
		flush()
	}
	return chunks
}

func hasStructuredKnowledge(doc SourceDocument) bool {
	return strings.TrimSpace(doc.Summary) != "" ||
		len(doc.Symptoms) > 0 ||
		len(doc.LikelyCauses) > 0 ||
		len(doc.RemediationSteps) > 0 ||
		len(doc.Commands) > 0 ||
		len(doc.Signals) > 0
}

func joinLabeledList(label string, values []string) string {
	values = dedupeStrings(values)
	if len(values) == 0 {
		return ""
	}
	return fmt.Sprintf("%s:\n- %s", label, strings.Join(values, "\n- "))
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
