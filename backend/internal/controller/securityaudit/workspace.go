package securityaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	AuditRetention   = 30 * 24 * time.Hour
	maxAuditRecords  = 20000
	maxAuditSnapshot = 64 << 20
)

var (
	ErrAuditCapacity  = errors.New("audit workspace capacity reached")
	ErrAuditTime      = errors.New("record time is outside the audit retention window")
	auditAssetPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,127}$`)
)

type AuditRecord struct {
	ID         string    `json:"id"`
	Asset      string    `json:"asset"`
	Source     string    `json:"source"`
	Kind       string    `json:"kind"`
	Rule       string    `json:"rule"`
	Severity   string    `json:"severity"`
	ObservedAt time.Time `json:"observed_at"`
	Tool       string    `json:"tool,omitempty"`
	Verb       string    `json:"verb,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	Resource   string    `json:"resource,omitempty"`
	Status     int       `json:"status,omitempty"`
}

func ValidAuditAsset(asset string) bool {
	return auditAssetPattern.MatchString(asset)
}

func auditID(parts ...string) string {
	// Encode boundaries before hashing so user-controlled fields cannot collide
	// by moving separators between fields.
	h := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(h, "%d:", len(part))
		io.WriteString(h, part)
	}
	return "audit-" + hex.EncodeToString(h.Sum(nil))
}

type AuditWorkspace struct {
	mu      sync.RWMutex
	path    string
	records map[string]AuditRecord
}

type AuditImportResult struct {
	Imported int `json:"imported"`
	Updated  int `json:"updated"`
}

type auditSnapshot struct {
	Version int                    `json:"version"`
	Records map[string]AuditRecord `json:"records"`
}

// OpenAuditWorkspace opens the controller's local normalized evidence snapshot.
func OpenAuditWorkspace(path string) (*AuditWorkspace, error) {
	w := &AuditWorkspace{path: path, records: make(map[string]AuditRecord)}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return w, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("audit snapshot must be a private regular file")
	}
	if info.Size() > maxAuditSnapshot {
		return nil, errors.New("audit snapshot exceeds size limit")
	}
	var snapshot auditSnapshot
	data, err := io.ReadAll(io.LimitReader(file, maxAuditSnapshot+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAuditSnapshot || json.Unmarshal(data, &snapshot) != nil ||
		snapshot.Version != 1 || snapshot.Records == nil || len(snapshot.Records) > maxAuditRecords {
		return nil, errors.New("invalid audit snapshot")
	}
	w.records = snapshot.Records
	return w, nil
}

// Import merges a validated batch and publishes it only after the snapshot
// replacement succeeds. A Kubernetes audit record is immutable once imported.
func (w *AuditWorkspace) Import(records []AuditRecord, now time.Time) (AuditImportResult, error) {
	now = now.UTC()
	cutoff := now.Add(-AuditRetention)
	batch := make(map[string]AuditRecord, len(records))
	for _, record := range records {
		if record.ObservedAt.Before(cutoff) || record.ObservedAt.After(now) {
			return AuditImportResult{}, ErrAuditTime
		}
		existing, found := batch[record.ID]
		if !found || record.ObservedAt.After(existing.ObservedAt) {
			batch[record.ID] = record
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	next := make(map[string]AuditRecord, len(w.records))
	for id, record := range w.records {
		if !record.ObservedAt.Before(cutoff) {
			next[id] = record
		}
	}
	result := AuditImportResult{}
	for id, record := range batch {
		existing, found := next[id]
		switch {
		case !found:
			next[id] = record
			result.Imported++
		case existing.Source == "sarif" && record.ObservedAt.After(existing.ObservedAt):
			next[id] = record
			result.Updated++
		}
	}
	if len(next) > maxAuditRecords {
		return AuditImportResult{}, ErrAuditCapacity
	}
	if result.Imported == 0 && result.Updated == 0 && len(next) == len(w.records) {
		return result, nil
	}
	if err := w.save(next); err != nil {
		return AuditImportResult{}, err
	}
	w.records = next
	return result, nil
}

func (w *AuditWorkspace) save(records map[string]AuditRecord) error {
	data, err := json.Marshal(auditSnapshot{Version: 1, Records: records})
	if err != nil {
		return err
	}
	if len(data) > maxAuditSnapshot {
		return ErrAuditCapacity
	}
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".security-audit-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), w.path)
}

func (w *AuditWorkspace) Report(asset string, window time.Duration, now time.Time) AuditReport {
	w.mu.RLock()
	records := make([]AuditRecord, 0, len(w.records))
	from := now.Add(-min(window, AuditRetention))
	for _, record := range w.records {
		if (asset == "" || record.Asset == asset) && !record.ObservedAt.Before(from) && !record.ObservedAt.After(now) {
			records = append(records, record)
		}
	}
	w.mu.RUnlock()
	slices.SortFunc(records, func(a, b AuditRecord) int {
		if order := a.ObservedAt.Compare(b.ObservedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	report := analyzeAudit(records, from, now)
	report.Count = len(records)
	slices.Reverse(records)
	// Keep all cited evidence navigable even if the latest rows fill the table.
	cited := make(map[string]bool)
	for _, match := range report.Correlations {
		for _, id := range match.EvidenceIDs {
			cited[id] = true
		}
	}
	for _, anomaly := range report.Anomalies {
		for _, id := range anomaly.EvidenceIDs {
			cited[id] = true
		}
	}
	report.Records = make([]AuditRecord, 0, min(len(records), 500+len(cited)))
	for i, record := range records {
		if i < 500 || cited[record.ID] {
			report.Records = append(report.Records, record)
		}
	}
	return report
}
