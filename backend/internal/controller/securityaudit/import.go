package securityaudit

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxAuditReportBytes    = 4 << 20
	MaxAuditReportRecords  = 10000
	maxAuditFieldBytes     = 256
	kubernetesAuditVersion = "audit.k8s.io/v1"
)

var (
	errAuditReportSize        = errors.New("audit report must contain 1 byte to 4 MiB")
	errAuditReportCount       = errors.New("audit report exceeds 10000 records")
	errInvalidSARIF           = errors.New("invalid SARIF 2.1.0 audit report")
	errInvalidKubernetesAudit = errors.New("invalid Kubernetes audit.k8s.io/v1 report")
)

// ParseAuditReport normalizes a complete report before any records are persisted.
func ParseAuditReport(format, asset, report string, observedAt time.Time) ([]AuditRecord, error) {
	if len(report) == 0 || len(report) > MaxAuditReportBytes || strings.TrimSpace(report) == "" {
		return nil, errAuditReportSize
	}
	asset = strings.TrimSpace(asset)
	if !ValidAuditAsset(asset) {
		return nil, errors.New("invalid audit asset")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "sarif":
		return parseSARIFAudit(asset, report, observedAt.UTC())
	case "kubernetes", "kubernetes-audit", "k8s-audit":
		return parseKubernetesAudit(asset, report)
	default:
		return nil, errors.New("unsupported audit report format")
	}
}

type sarifRule struct {
	ID                   string `json:"id"`
	DefaultConfiguration struct {
		Level string `json:"level"`
	} `json:"defaultConfiguration"`
}

type sarifArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId"`
	Index     *int   `json:"index"`
}

type sarifResult struct {
	RuleID    string `json:"ruleId"`
	RuleIndex *int   `json:"ruleIndex"`
	Rule      struct {
		ID            string `json:"id"`
		Index         *int   `json:"index"`
		ToolComponent struct {
			Name  string `json:"name"`
			Index *int   `json:"index"`
		} `json:"toolComponent"`
	} `json:"rule"`
	Level               string            `json:"level"`
	Kind                string            `json:"kind"`
	Fingerprints        map[string]string `json:"fingerprints"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Message             struct {
		ID        string   `json:"id"`
		Text      string   `json:"text"`
		Markdown  string   `json:"markdown"`
		Arguments []string `json:"arguments"`
	} `json:"message"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
			Region           struct {
				StartLine   int `json:"startLine"`
				StartColumn int `json:"startColumn"`
			} `json:"region"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

type sarifComponent struct {
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

type sarifRun struct {
	Tool struct {
		Driver     sarifComponent   `json:"driver"`
		Extensions []sarifComponent `json:"extensions"`
	} `json:"tool"`
	Artifacts []struct {
		Location sarifArtifactLocation `json:"location"`
	} `json:"artifacts"`
	Results []*sarifResult `json:"results"`
}

func parseSARIFAudit(asset, report string, observedAt time.Time) ([]AuditRecord, error) {
	var document struct {
		Version string       `json:"version"`
		Runs    *[]*sarifRun `json:"runs"`
	}
	if json.Unmarshal([]byte(report), &document) != nil || document.Version != "2.1.0" || document.Runs == nil || observedAt.IsZero() {
		return nil, errInvalidSARIF
	}
	records := make([]AuditRecord, 0)
	count := 0
	for _, run := range *document.Runs {
		if run == nil || !validAuditField(run.Tool.Driver.Name) {
			return nil, errInvalidSARIF
		}
		count += len(run.Results)
		if count > MaxAuditReportRecords {
			return nil, errAuditReportCount
		}
		components := make(map[string]*sarifComponent, len(run.Tool.Extensions))
		for i := range run.Tool.Extensions {
			component := &run.Tool.Extensions[i]
			components[component.Name] = component
		}
		levels := make(map[*sarifComponent]map[string]string)
		for _, result := range run.Results {
			if result == nil {
				return nil, errInvalidSARIF
			}
			switch result.Kind {
			case "pass", "notApplicable", "informational":
				continue
			case "", "fail", "review", "open":
			default:
				return nil, errInvalidSARIF
			}
			record, err := normalizeSARIFAudit(asset, observedAt, run, result, components, levels)
			if err != nil {
				return nil, err
			}
			records = append(records, record)
		}
	}
	return records, nil
}

func normalizeSARIFAudit(asset string, observedAt time.Time, run *sarifRun, result *sarifResult, components map[string]*sarifComponent, levels map[*sarifComponent]map[string]string) (AuditRecord, error) {
	component := &run.Tool.Driver
	reference := result.Rule.ToolComponent
	switch {
	case reference.Index != nil && *reference.Index >= 0:
		if *reference.Index >= len(run.Tool.Extensions) {
			return AuditRecord{}, errInvalidSARIF
		}
		component = &run.Tool.Extensions[*reference.Index]
	case reference.Name != "" && reference.Name != component.Name:
		component = components[reference.Name]
	}
	if component == nil || !validAuditField(component.Name) {
		return AuditRecord{}, errInvalidSARIF
	}

	ruleID := result.RuleID
	if ruleID == "" {
		ruleID = result.Rule.ID
	}
	ruleIndex := result.RuleIndex
	if result.Rule.Index != nil {
		ruleIndex = result.Rule.Index
	}
	if ruleID == "" && ruleIndex != nil && *ruleIndex >= 0 {
		if *ruleIndex >= len(component.Rules) {
			return AuditRecord{}, errInvalidSARIF
		}
		ruleID = component.Rules[*ruleIndex].ID
	}
	if ruleID != "" && !validAuditField(ruleID) {
		return AuditRecord{}, errInvalidSARIF
	}
	level := result.Level
	if level == "" && (result.Kind == "review" || result.Kind == "open") {
		level = "none"
	}
	if level == "" && ruleID != "" {
		byRule, ok := levels[component]
		if !ok {
			byRule = make(map[string]string, len(component.Rules))
			for _, rule := range component.Rules {
				byRule[rule.ID] = rule.DefaultConfiguration.Level
			}
			levels[component] = byRule
		}
		level = byRule[ruleID]
	}
	var severity string
	switch level {
	case "error":
		severity = "high"
	case "warning", "":
		severity = "medium"
	case "note", "none":
		severity = "low"
	default:
		return AuditRecord{}, errInvalidSARIF
	}

	identity := []string{"sarif", asset, run.Tool.Driver.Name, component.Name, ruleID}
	if ruleID == "" {
		ruleID = "unspecified"
	}
	fingerprints := sarifFingerprint(result.Fingerprints, result.PartialFingerprints)
	locations := make([]string, 0, len(result.Locations))
	var resource string
	for _, location := range result.Locations {
		physical := location.PhysicalLocation
		artifact := physical.ArtifactLocation
		if artifact.URI == "" && artifact.Index != nil && *artifact.Index >= 0 {
			if *artifact.Index >= len(run.Artifacts) {
				return AuditRecord{}, errInvalidSARIF
			}
			artifact = run.Artifacts[*artifact.Index].Location
		}
		name, locationID := sarifResource(artifact)
		if name != "" && (resource == "" || name < resource) {
			resource = name
		}
		if len(fingerprints) == 0 {
			locationID = auditID(locationID, strconv.Itoa(physical.Region.StartLine), strconv.Itoa(physical.Region.StartColumn))
		}
		locations = append(locations, locationID)
	}
	sort.Strings(locations)
	identity = append(identity, auditID(locations...), auditID(fingerprints...))
	if len(fingerprints) == 0 {
		// The SARIF message helps distinguish findings with an identical fallback
		// location. Its content is discarded after computing the identity.
		identity = append(identity, auditID(result.Message.ID, result.Message.Text, result.Message.Markdown, auditID(result.Message.Arguments...)))
	}
	return AuditRecord{
		ID: auditID(identity...), Asset: asset, Source: "sarif", Kind: "finding",
		Rule: ruleID, Severity: severity, ObservedAt: observedAt, Tool: run.Tool.Driver.Name, Resource: resource,
	}, nil
}

func sarifFingerprint(full, partial map[string]string) []string {
	for _, fields := range []map[string]string{full, partial} {
		fingerprints := make([]string, 0, len(fields))
		for key, value := range fields {
			if value != "" {
				fingerprints = append(fingerprints, auditID(key, value))
			}
		}
		if len(fingerprints) > 0 {
			sort.Strings(fingerprints)
			return fingerprints
		}
	}
	return nil
}

// Only relative repository paths are exposed. Other artifact identifiers can
// still scope an identity without retaining the URL or absolute checkout path.
func sarifResource(location sarifArtifactLocation) (name, id string) {
	parsed, err := url.Parse(location.URI)
	if err != nil {
		return "", auditID(location.URIBaseID, location.URI)
	}
	// Queries, credentials and fragments can contain sensitive material and
	// don't identify a source artifact.
	id = auditID(location.URIBaseID, parsed.Scheme, parsed.Host, parsed.EscapedPath(), parsed.Opaque)
	if !parsed.IsAbs() && parsed.Host == "" && parsed.User == nil && parsed.Path != "" &&
		!strings.HasPrefix(parsed.Path, "/") && !strings.Contains(parsed.Path, "\\") {
		cleaned := path.Clean(parsed.Path)
		if cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../") && validAuditField(cleaned) {
			return cleaned, auditID(location.URIBaseID, cleaned)
		}
	}
	return "", id
}

type kubernetesAuditEvent struct {
	APIVersion  string    `json:"apiVersion"`
	Kind        string    `json:"kind"`
	AuditID     string    `json:"auditID"`
	Stage       string    `json:"stage"`
	StageTime   time.Time `json:"stageTimestamp"`
	RequestTime time.Time `json:"requestReceivedTimestamp"`
	Verb        string    `json:"verb"`
	User        struct {
		Username string `json:"username"`
	} `json:"user"`
	ResponseStatus struct {
		Code int `json:"code"`
	} `json:"responseStatus"`
	ObjectRef struct {
		APIGroup    string `json:"apiGroup"`
		Namespace   string `json:"namespace"`
		Resource    string `json:"resource"`
		Name        string `json:"name"`
		Subresource string `json:"subresource"`
	} `json:"objectRef"`
}

func parseKubernetesAudit(asset, report string) ([]AuditRecord, error) {
	decoder := json.NewDecoder(strings.NewReader(report))
	records := make([]AuditRecord, 0)
	count := 0
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if err == io.EOF {
			return records, nil
		}
		if err != nil {
			return nil, errInvalidKubernetesAudit
		}
		var envelope struct {
			APIVersion string            `json:"apiVersion"`
			Kind       string            `json:"kind"`
			Items      []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.APIVersion != kubernetesAuditVersion {
			return nil, errInvalidKubernetesAudit
		}
		var events []json.RawMessage
		switch envelope.Kind {
		case "Event":
			events = []json.RawMessage{raw}
		case "EventList":
			if envelope.Items == nil {
				return nil, errInvalidKubernetesAudit
			}
			events = envelope.Items
		default:
			return nil, errInvalidKubernetesAudit
		}
		count += len(events)
		if count > MaxAuditReportRecords {
			return nil, errAuditReportCount
		}
		for _, event := range events {
			record, include, err := normalizeKubernetesAudit(asset, event)
			if err != nil {
				return nil, err
			}
			if include {
				records = append(records, record)
			}
		}
	}
}

func normalizeKubernetesAudit(asset string, raw json.RawMessage) (AuditRecord, bool, error) {
	var event kubernetesAuditEvent
	if json.Unmarshal(raw, &event) != nil ||
		(event.APIVersion != "" && event.APIVersion != kubernetesAuditVersion) ||
		(event.Kind != "" && event.Kind != "Event") {
		return AuditRecord{}, false, errInvalidKubernetesAudit
	}
	switch event.Stage {
	case "RequestReceived", "ResponseStarted":
		return AuditRecord{}, false, nil
	case "ResponseComplete", "Panic":
	default:
		return AuditRecord{}, false, errInvalidKubernetesAudit
	}
	occurred := event.StageTime
	if occurred.IsZero() {
		occurred = event.RequestTime
	}
	status := event.ResponseStatus.Code
	if event.Verb != "" && !validAuditField(event.Verb) {
		return AuditRecord{}, false, errInvalidKubernetesAudit
	}
	if !validAuditField(event.AuditID) || occurred.IsZero() || (status != 0 && (status < 100 || status > 599)) {
		return AuditRecord{}, false, errInvalidKubernetesAudit
	}

	ref := event.ObjectRef
	var parts []string
	for _, part := range []string{ref.APIGroup, ref.Namespace, ref.Resource, ref.Name, ref.Subresource} {
		if part == "" {
			continue
		}
		if !validAuditField(part) || strings.ContainsAny(part, "/?#\\") {
			return AuditRecord{}, false, errInvalidKubernetesAudit
		}
		parts = append(parts, part)
	}
	resource := strings.Join(parts, "/")
	if resource != "" && !validAuditField(resource) {
		return AuditRecord{}, false, errInvalidKubernetesAudit
	}
	actor := event.User.Username
	if actor != "" && !validAuditField(actor) {
		return AuditRecord{}, false, errInvalidKubernetesAudit
	}

	rule, severity := "api_request", "low"
	successful := event.Stage == "ResponseComplete" && status >= 200 && status < 300
	switch {
	case status == 401 || status == 403:
		rule, severity = "authorization_denied", "medium"
	case successful && ref.APIGroup == "rbac.authorization.k8s.io" && isAuditWriteVerb(event.Verb):
		rule, severity = "rbac_change", "high"
	case successful && ref.APIGroup == "" && ref.Resource == "secrets" && isAuditReadVerb(event.Verb):
		rule, severity = "secret_access", "medium"
	}
	return AuditRecord{
		ID:    auditID("kubernetes", asset, event.AuditID, event.Stage),
		Asset: asset, Source: "kubernetes", Kind: "event", Rule: rule, Severity: severity,
		ObservedAt: occurred.UTC(), Verb: event.Verb, Actor: actor, Resource: resource, Status: status,
	}, true, nil
}

func isAuditWriteVerb(verb string) bool {
	switch verb {
	case "create", "update", "patch", "delete", "deletecollection":
		return true
	default:
		return false
	}
}

func isAuditReadVerb(verb string) bool {
	return verb == "get" || verb == "list" || verb == "watch"
}

func validAuditField(value string) bool {
	return value != "" && len(value) <= maxAuditFieldBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, func(r rune) bool { return !unicode.IsPrint(r) }) == -1
}
