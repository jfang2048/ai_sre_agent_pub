package rag

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

type sourceUnit struct {
	SourceKey  string
	SourcePath string
	ActualPath string
	SourceType string
	TitleHint  string
	Tags       []string
	Metadata   map[string]string
	Timestamp  *time.Time
	UpdatedAt  time.Time
	Signature  string
}

type sourceRecord struct {
	SourceKey  string   `json:"source_key"`
	SourcePath string   `json:"source_path"`
	SourceType string   `json:"source_type"`
	Signature  string   `json:"signature"`
	DocIDs     []string `json:"doc_ids,omitempty"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`
}

func discoverSourceUnits(cfg Config) ([]sourceUnit, []QuarantineRecord, error) {
	datasetPath := filepath.Clean(cfg.DatasetPath)
	info, err := os.Stat(datasetPath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat dataset path: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("dataset path is not a directory: %s", datasetPath)
	}
	if err := os.MkdirAll(extractedPath(cfg.IndexPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create rag extract cache: %w", err)
	}

	units := make([]sourceUnit, 0, 512)
	quarantine := make([]QuarantineRecord, 0, 64)
	roots := append([]string{datasetPath}, cfg.SourcePaths...)
	seen := make(map[string]struct{}, len(roots))

	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		rootInfo, err := os.Stat(root)
		if err != nil {
			quarantine = append(quarantine, QuarantineRecord{
				Path:       root,
				SourceType: "missing",
				Reason:     err.Error(),
			})
			continue
		}
		if !rootInfo.IsDir() {
			discovered, skipped, err := inspectFile(root, cfg, datasetPath)
			if err != nil {
				return nil, nil, err
			}
			units = append(units, discovered...)
			quarantine = append(quarantine, skipped...)
			continue
		}
		err = filepath.WalkDir(root, func(current string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				quarantine = append(quarantine, QuarantineRecord{
					Path:       relativeDatasetPath(datasetPath, current),
					SourceType: "walk_error",
					Reason:     walkErr.Error(),
				})
				return nil
			}
			if d.IsDir() {
				return nil
			}
			discovered, skipped, err := inspectFile(current, cfg, datasetPath)
			if err != nil {
				return err
			}
			units = append(units, discovered...)
			quarantine = append(quarantine, skipped...)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	sort.Slice(units, func(i, j int) bool {
		if units[i].SourcePath == units[j].SourcePath {
			return units[i].SourceKey < units[j].SourceKey
		}
		return units[i].SourcePath < units[j].SourcePath
	})
	sort.Slice(quarantine, func(i, j int) bool {
		if quarantine[i].Path == quarantine[j].Path {
			return quarantine[i].Reason < quarantine[j].Reason
		}
		return quarantine[i].Path < quarantine[j].Path
	})
	return units, dedupeQuarantine(quarantine), nil
}

func inspectFile(path string, cfg Config, datasetRoot string) ([]sourceUnit, []QuarantineRecord, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat source %s: %w", path, err)
	}
	relative := relativeDatasetPath(datasetRoot, path)
	kind := detectFileKind(path)
	if reason, ok := corpusExclusionReason(relative, kind, datasetRoot); ok {
		return nil, []QuarantineRecord{{
			Path:       relative,
			SourceType: kind,
			Reason:     reason,
			SizeBytes:  info.Size(),
		}}, nil
	}
	switch kind {
	case "archive_zip", "archive_zedx":
		return extractArchiveUnits(path, relative, info, cfg)
	case "unsupported":
		return nil, []QuarantineRecord{{
			Path:       relative,
			SourceType: "unsupported",
			Reason:     "file type unsupported for rag ingestion",
			SizeBytes:  info.Size(),
		}}, nil
	default:
		unit := sourceUnit{
			SourceKey:  relative,
			SourcePath: relative,
			ActualPath: path,
			SourceType: kind,
			Tags:       tagsFromPath(relative),
			Metadata: map[string]string{
				"dataset_path": relative,
			},
			Timestamp: cloneTimePtr(info.ModTime().UTC()),
			UpdatedAt: info.ModTime().UTC(),
			Signature: buildPlainSignature(info),
		}
		return []sourceUnit{unit}, nil, nil
	}
}

func extractArchiveUnits(archivePath, relativeArchive string, info os.FileInfo, cfg Config) ([]sourceUnit, []QuarantineRecord, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open archive %s: %w", archivePath, err)
	}
	defer reader.Close()

	extractRoot := filepath.Join(extractedPath(cfg.IndexPath), sanitizePathForFS(relativeArchive))
	if err := os.RemoveAll(extractRoot); err != nil {
		return nil, nil, fmt.Errorf("reset archive cache %s: %w", extractRoot, err)
	}
	if err := os.MkdirAll(extractRoot, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create archive cache %s: %w", extractRoot, err)
	}

	units := make([]sourceUnit, 0, len(reader.File))
	quarantine := make([]QuarantineRecord, 0, 16)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		entryName := sanitizeArchiveEntry(file.Name)
		if entryName == "" {
			quarantine = append(quarantine, QuarantineRecord{
				Path:       relativeArchive + "::" + file.Name,
				SourceType: "archive_entry",
				Reason:     "unsafe archive entry path",
				SizeBytes:  int64(file.UncompressedSize64),
			})
			continue
		}
		entryKind := detectFileKind(entryName)
		if entryKind == "unsupported" || strings.HasPrefix(entryKind, "archive_") {
			quarantine = append(quarantine, QuarantineRecord{
				Path:       relativeArchive + "::" + entryName,
				SourceType: "archive_entry",
				Reason:     "archive entry type unsupported for rag ingestion",
				SizeBytes:  int64(file.UncompressedSize64),
			})
			continue
		}
		destination := filepath.Join(extractRoot, filepath.FromSlash(entryName))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return nil, nil, fmt.Errorf("create archive extract path %s: %w", destination, err)
		}
		rc, err := file.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("open archive entry %s: %w", file.Name, err)
		}
		if err := writeArchiveEntry(destination, rc); err != nil {
			_ = rc.Close()
			return nil, nil, fmt.Errorf("extract archive entry %s: %w", file.Name, err)
		}
		_ = rc.Close()
		modTime := file.Modified.UTC()
		if modTime.IsZero() {
			modTime = info.ModTime().UTC()
		}
		sourcePath := relativeArchive + "::" + entryName
		units = append(units, sourceUnit{
			SourceKey:  sourcePath,
			SourcePath: sourcePath,
			ActualPath: destination,
			SourceType: entryKind,
			Tags:       append(tagsFromPath(relativeArchive), tagsFromPath(entryName)...),
			Metadata: map[string]string{
				"archive_path":  relativeArchive,
				"archive_entry": entryName,
				"dataset_path":  relativeArchive,
			},
			Timestamp: cloneTimePtr(modTime),
			UpdatedAt: modTime,
			Signature: buildArchiveSignature(info, file),
		})
	}
	return units, quarantine, nil
}

func writeArchiveEntry(path string, reader io.Reader) error {
	output, err := os.Create(path)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, reader)
	return err
}

func parseSourceUnit(unit sourceUnit) ([]SourceDocument, error) {
	switch unit.SourceType {
	case "json":
		return parseJSONDocuments(unit)
	case "jsonl":
		return parseJSONLDocuments(unit)
	case "csv":
		return parseDelimitedDocuments(unit, ',')
	case "tsv":
		return parseDelimitedDocuments(unit, '\t')
	default:
		return parseTextDocument(unit)
	}
}

func parseTextDocument(unit sourceUnit) ([]SourceDocument, error) {
	raw, err := os.ReadFile(unit.ActualPath)
	if err != nil {
		return nil, fmt.Errorf("read source %s: %w", unit.SourcePath, err)
	}
	content := strings.TrimSpace(stripUTF8BOM(string(raw)))
	if content == "" {
		return nil, nil
	}
	title := strings.TrimSpace(unit.TitleHint)
	switch unit.SourceType {
	case "html", "xml":
		var htmlTitle string
		htmlTitle, content = htmlToText(content)
		if title == "" {
			title = htmlTitle
		}
	}
	if title == "" {
		title = chooseTitle(content, unit.SourcePath)
	}
	metadata := cloneMap(unit.Metadata)
	metadata["parser"] = unit.SourceType
	doc := SourceDocument{
		DocID:      stableDocID(unit.SourceKey, "0"),
		SourceKey:  unit.SourceKey,
		SourcePath: unit.SourcePath,
		SourceType: unit.SourceType,
		Title:      title,
		Content:    strings.TrimSpace(content),
		Tags:       append([]string(nil), dedupeStrings(unit.Tags)...),
		Timestamp:  cloneTime(unit.Timestamp),
		Metadata:   metadata,
		UpdatedAt:  unit.UpdatedAt,
	}
	return []SourceDocument{finalizeDocument(doc)}, nil
}

func parseJSONDocuments(unit sourceUnit) ([]SourceDocument, error) {
	raw, err := os.ReadFile(unit.ActualPath)
	if err != nil {
		return nil, fmt.Errorf("read json source %s: %w", unit.SourcePath, err)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode json source %s: %w", unit.SourcePath, err)
	}
	switch typed := value.(type) {
	case []any:
		docs := make([]SourceDocument, 0, len(typed))
		for index, item := range typed {
			content := structuredContent(item)
			if strings.TrimSpace(content) == "" {
				continue
			}
			metadata := cloneMap(unit.Metadata)
			metadata["parser"] = "json"
			metadata["record_index"] = strconv.Itoa(index)
			mergeStructuredMetadata(metadata, item)
			docs = append(docs, finalizeDocument(SourceDocument{
				DocID:      stableDocID(unit.SourceKey, strconv.Itoa(index)),
				SourceKey:  unit.SourceKey,
				SourcePath: unit.SourcePath,
				SourceType: unit.SourceType,
				Title:      chooseStructuredTitle(item, unit.SourcePath, index),
				Content:    content,
				Tags:       append([]string(nil), dedupeStrings(tagsWithStructuredFields(unit.Tags, item))...),
				Timestamp:  cloneTime(maybeTimestamp(unit.Timestamp, item)),
				Metadata:   metadata,
				UpdatedAt:  unit.UpdatedAt,
			}))
		}
		return docs, nil
	default:
		metadata := cloneMap(unit.Metadata)
		metadata["parser"] = "json"
		mergeStructuredMetadata(metadata, typed)
		return []SourceDocument{finalizeDocument(SourceDocument{
			DocID:      stableDocID(unit.SourceKey, "0"),
			SourceKey:  unit.SourceKey,
			SourcePath: unit.SourcePath,
			SourceType: unit.SourceType,
			Title:      chooseStructuredTitle(typed, unit.SourcePath, 0),
			Content:    structuredContent(typed),
			Tags:       append([]string(nil), dedupeStrings(tagsWithStructuredFields(unit.Tags, typed))...),
			Timestamp:  cloneTime(maybeTimestamp(unit.Timestamp, typed)),
			Metadata:   metadata,
			UpdatedAt:  unit.UpdatedAt,
		})}, nil
	}
}

func parseJSONLDocuments(unit sourceUnit) ([]SourceDocument, error) {
	file, err := os.Open(unit.ActualPath)
	if err != nil {
		return nil, fmt.Errorf("open jsonl source %s: %w", unit.SourcePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 8*1024*1024)
	docs := make([]SourceDocument, 0, 64)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(stripUTF8BOM(scanner.Text()))
		if line == "" {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			metadata := cloneMap(unit.Metadata)
			metadata["parser"] = "jsonl"
			metadata["line"] = strconv.Itoa(lineNumber)
			docs = append(docs, finalizeDocument(SourceDocument{
				DocID:      stableDocID(unit.SourceKey, strconv.Itoa(lineNumber)),
				SourceKey:  unit.SourceKey,
				SourcePath: unit.SourcePath,
				SourceType: unit.SourceType,
				Title:      fmt.Sprintf("%s line %d", filepath.Base(unit.SourcePath), lineNumber),
				Content:    line,
				Tags:       append([]string(nil), unit.Tags...),
				Timestamp:  cloneTime(unit.Timestamp),
				Metadata:   metadata,
				UpdatedAt:  unit.UpdatedAt,
			}))
			continue
		}
		metadata := cloneMap(unit.Metadata)
		metadata["parser"] = "jsonl"
		metadata["line"] = strconv.Itoa(lineNumber)
		mergeStructuredMetadata(metadata, payload)
		docs = append(docs, finalizeDocument(SourceDocument{
			DocID:      stableDocID(unit.SourceKey, strconv.Itoa(lineNumber)),
			SourceKey:  unit.SourceKey,
			SourcePath: unit.SourcePath,
			SourceType: unit.SourceType,
			Title:      chooseStructuredTitle(payload, unit.SourcePath, lineNumber),
			Content:    structuredContent(payload),
			Tags:       append([]string(nil), dedupeStrings(tagsWithStructuredFields(unit.Tags, payload))...),
			Timestamp:  cloneTime(maybeTimestamp(unit.Timestamp, payload)),
			Metadata:   metadata,
			UpdatedAt:  unit.UpdatedAt,
		}))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan jsonl source %s: %w", unit.SourcePath, err)
	}
	return docs, nil
}

func parseDelimitedDocuments(unit sourceUnit, delimiter rune) ([]SourceDocument, error) {
	file, err := os.Open(unit.ActualPath)
	if err != nil {
		return nil, fmt.Errorf("open delimited source %s: %w", unit.SourcePath, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.Comma = delimiter
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read delimited source %s: %w", unit.SourcePath, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	headers := rows[0]
	for index, header := range headers {
		headers[index] = strings.TrimSpace(stripUTF8BOM(header))
	}
	docs := make([]SourceDocument, 0, maxInt(0, len(rows)-1))
	for rowIndex, row := range rows[1:] {
		record := make(map[string]any)
		for columnIndex, value := range row {
			key := fmt.Sprintf("column_%d", columnIndex+1)
			if columnIndex < len(headers) && strings.TrimSpace(headers[columnIndex]) != "" {
				key = headers[columnIndex]
			}
			record[key] = strings.TrimSpace(stripUTF8BOM(value))
		}
		metadata := cloneMap(unit.Metadata)
		metadata["parser"] = "csv"
		metadata["row"] = strconv.Itoa(rowIndex + 1)
		mergeStructuredMetadata(metadata, record)
		docs = append(docs, finalizeDocument(SourceDocument{
			DocID:      stableDocID(unit.SourceKey, strconv.Itoa(rowIndex+1)),
			SourceKey:  unit.SourceKey,
			SourcePath: unit.SourcePath,
			SourceType: unit.SourceType,
			Title:      chooseStructuredTitle(record, unit.SourcePath, rowIndex+1),
			Content:    structuredContent(record),
			Tags:       append([]string(nil), dedupeStrings(tagsWithStructuredFields(unit.Tags, record))...),
			Timestamp:  cloneTime(maybeTimestamp(unit.Timestamp, record)),
			Metadata:   metadata,
			UpdatedAt:  unit.UpdatedAt,
		}))
	}
	return docs, nil
}

func detectFileKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return "markdown"
	case ".txt", ".log":
		return "text"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".jsonl":
		return "jsonl"
	case ".csv":
		return "csv"
	case ".tsv":
		return "tsv"
	case ".html", ".htm":
		return "html"
	case ".xml":
		return "xml"
	case ".zip":
		return "archive_zip"
	case ".zedx":
		return "archive_zedx"
	default:
		return "unsupported"
	}
}

func chooseTitle(content, sourcePath string) string {
	lines := strings.Split(normalizeWhitespace(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(line, "#*- "))
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > 120 {
			line = string([]rune(line)[:120])
		}
		return line
	}
	return filepath.Base(sourcePath)
}

func chooseStructuredTitle(value any, sourcePath string, index int) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"title", "query", "question", "name", "summary", "id"} {
			if raw, ok := typed[key]; ok {
				text := strings.TrimSpace(fmt.Sprint(raw))
				if text != "" {
					return text
				}
			}
		}
	case string:
		text := strings.TrimSpace(typed)
		if text != "" {
			return text
		}
	}
	return fmt.Sprintf("%s #%d", filepath.Base(sourcePath), index+1)
}

func structuredContent(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("%s: %s", key, formatStructuredValue(typed[key])))
		}
		return strings.Join(lines, "\n")
	case []any:
		lines := make([]string, 0, len(typed))
		for index, item := range typed {
			lines = append(lines, fmt.Sprintf("[%d] %s", index, formatStructuredValue(item)))
		}
		return strings.Join(lines, "\n")
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func formatStructuredValue(value any) string {
	switch typed := value.(type) {
	case map[string]any, []any:
		raw, _ := json.Marshal(typed)
		return string(raw)
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func tagsWithStructuredFields(base []string, value any) []string {
	tags := append([]string(nil), base...)
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"document", "category", "service", "topic", "language"} {
			if raw, ok := typed[key]; ok {
				text := strings.TrimSpace(fmt.Sprint(raw))
				if text != "" {
					tags = append(tags, text)
				}
			}
		}
	}
	return dedupeStrings(tags)
}

func maybeTimestamp(base *time.Time, value any) *time.Time {
	if timestamp := extractStructuredTimestamp(value); timestamp != nil {
		return timestamp
	}
	return cloneTime(base)
}

func extractStructuredTimestamp(value any) *time.Time {
	typed, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"timestamp", "created_at", "updated_at", "time"} {
		raw, ok := typed[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(raw))
		if text == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				utc := parsed.UTC()
				return &utc
			}
		}
	}
	return nil
}

func htmlToText(raw string) (string, string) {
	root, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return "", strings.TrimSpace(raw)
	}
	title := ""
	lines := make([]string, 0, 64)
	var walk func(*xhtml.Node, bool)
	walk = func(node *xhtml.Node, skip bool) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode {
			switch strings.ToLower(node.Data) {
			case "script", "style", "noscript", "nav", "header", "footer", "aside", "svg", "button", "iframe":
				skip = true
			case "title":
				if title == "" && node.FirstChild != nil {
					title = strings.TrimSpace(stdhtml.UnescapeString(node.FirstChild.Data))
				}
			case "h1", "h2":
				if title == "" {
					title = strings.TrimSpace(nodeText(node))
				}
			}
		}
		if skip {
			return
		}
		if node.Type == xhtml.TextNode {
			text := strings.TrimSpace(stdhtml.UnescapeString(node.Data))
			if text != "" {
				lines = append(lines, text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip)
		}
		if node.Type == xhtml.ElementNode {
			switch strings.ToLower(node.Data) {
			case "p", "div", "section", "article", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "br":
				lines = append(lines, "")
			}
		}
	}
	walk(root, false)
	return title, cleanHTMLText(lines)
}

func nodeText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current == nil {
			return
		}
		if current.Type == xhtml.TextNode {
			text := strings.TrimSpace(stdhtml.UnescapeString(current.Data))
			if text != "" {
				if builder.Len() > 0 {
					builder.WriteString(" ")
				}
				builder.WriteString(text)
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func cleanHTMLText(lines []string) string {
	parts := make([]string, 0, len(lines))
	lastBlank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !lastBlank {
				parts = append(parts, "")
			}
			lastBlank = true
			continue
		}
		parts = append(parts, line)
		lastBlank = false
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func tagsFromPath(path string) []string {
	cleaned := filepath.ToSlash(path)
	parts := strings.Split(cleaned, "/")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimSuffix(part, filepath.Ext(part))
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		if lower == "dataset" || lower == "data" || lower == "documents" || lower == "publish" || lower == "files" {
			continue
		}
		if isMostlyDigits(lower) {
			continue
		}
		tags = append(tags, lower)
	}
	return dedupeStrings(tags)
}

func corpusExclusionReason(relativePath, sourceType, datasetRoot string) (string, bool) {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(relativePath)))
	base := strings.TrimSuffix(filepath.Base(lower), filepath.Ext(lower))

	if counterpart := processedGPUCounterpart(lower, datasetRoot); counterpart != "" {
		return "processed markdown counterpart preferred: " + counterpart, true
	}
	if strings.Contains(lower, "/.github/") ||
		strings.HasSuffix(lower, "/.gitignore") ||
		strings.HasSuffix(lower, "/.gitmodules") ||
		strings.HasSuffix(lower, "/codeowners") {
		return "excluded repository metadata from retrieval corpus", true
	}
	if strings.Contains(lower, "/sources/git/scoutflo-sre-playbooks/docs/") {
		return "excluded repository documentation in favor of operational playbooks", true
	}
	if strings.Contains(lower, "/raw/archives/") {
		return "excluded archive datasets from retrieval corpus", true
	}
	if strings.Contains(lower, "/raw/structured/") {
		return "excluded raw structured datasets from retrieval corpus", true
	}
	if strings.Contains(lower, "/themes/") {
		return "excluded Hugo/site theme assets from retrieval corpus", true
	}
	if strings.Contains(lower, "/sources/git/") && (strings.Contains(lower, "/layouts/") || strings.Contains(lower, "/archetypes/")) {
		return "excluded Hugo site scaffolding from retrieval corpus", true
	}
	if strings.Contains(lower, "/sources/git/") && strings.Contains(lower, "/scripts/") {
		return "excluded repository build/validation scripts from retrieval corpus", true
	}
	if strings.HasPrefix(lower, "dataset/tools/") || strings.Contains(lower, "/dataset/tools/") {
		return "excluded dataset tooling scripts from retrieval corpus", true
	}
	switch base {
	case "_index", "readme", "changelog", "code_of_conduct", "contributing", "contributors",
		"roadmap", "license", "security", "faq", "examples", "maintainers",
		"quick_reference", "troubleshooting_flowchart", "funding", "getting_started",
		"github_setup_guide", "support", "pull_request_template":
		return "excluded low-value repository metadata from retrieval corpus", true
	}
	if strings.HasSuffix(lower, "/config.yaml") && strings.Contains(lower, "/prometheus-operator-runbooks/") {
		return "excluded repository configuration metadata from retrieval corpus", true
	}
	if sourceType == "yaml" && strings.Contains(lower, "/.github/workflows/") {
		return "excluded repository workflow metadata from retrieval corpus", true
	}
	return "", false
}

func processedGPUCounterpart(relativePath, datasetRoot string) string {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(relativePath)))
	if !strings.Contains(lower, "/sources/web/") || !strings.HasSuffix(lower, ".html") {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(lower), ".html") + ".md"
	counterpart := filepath.Join(datasetRoot, "processed", "gpu-docs", base)
	info, err := os.Stat(counterpart)
	if err != nil || info.IsDir() {
		return ""
	}
	return relativeDatasetPath(datasetRoot, counterpart)
}

func isMostlyDigits(text string) bool {
	if text == "" {
		return false
	}
	digits := 0
	for _, r := range text {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	return digits == len(text)
}

func relativeDatasetPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	relative = filepath.ToSlash(relative)
	if relative == "." {
		return filepath.Base(path)
	}
	return filepath.ToSlash(filepath.Join(filepath.Base(root), relative))
}

func sanitizeArchiveEntry(entry string) string {
	entry = filepath.ToSlash(strings.TrimSpace(entry))
	entry = strings.TrimPrefix(entry, "/")
	entry = pathClean(entry)
	if entry == "" || strings.HasPrefix(entry, "../") || entry == ".." {
		return ""
	}
	return entry
}

func pathClean(path string) string {
	parts := strings.Split(path, "/")
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return ""
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}
	return strings.Join(stack, "/")
}

func sanitizePathForFS(path string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(path)
}

func buildPlainSignature(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UTC().UnixNano())
}

func buildArchiveSignature(info os.FileInfo, file *zip.File) string {
	return fmt.Sprintf("%d:%d:%d:%d:%d", info.Size(), info.ModTime().UTC().UnixNano(), file.UncompressedSize64, file.CRC32, file.Modified.UTC().UnixNano())
}

func stableDocID(sourceKey, suffix string) string {
	return "doc-" + shortHash(sourceKey+"::"+suffix)
}
