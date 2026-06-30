package prom

import (
	"strconv"
	"sync"
)

var (
	metricNameCache sync.Map // map[string]string
	labelKeyCache   sync.Map // map[string]string
)

// QuoteLabelValue returns a Prometheus-compatible quoted label value, including quotes.
// It uses Go's quoting rules, which match Prometheus text exposition escaping semantics.
func QuoteLabelValue(v string) string {
	return strconv.Quote(v)
}

// SanitizeLabelKey converts an arbitrary string into a Prometheus label name.
// Prom label name: [a-zA-Z_][a-zA-Z0-9_]*
func SanitizeLabelKey(in string) string {
	if in == "" {
		return ""
	}
	for i := 0; i < len(in); i++ {
		c := in[i]
		if i == 0 {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
				continue
			}
			return sanitizeLabelKeyCached(in)
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return sanitizeLabelKeyCached(in)
	}
	return in
}

func sanitizeLabelKeyCached(in string) string {
	if v, ok := labelKeyCache.Load(in); ok {
		return v.(string)
	}
	out := sanitizeLabelKeySlow(in)
	labelKeyCache.Store(in, out)
	return out
}

func sanitizeLabelKeySlow(in string) string {
	b := make([]byte, 0, len(in)+1)
	for i := 0; i < len(in); i++ {
		c := in[i]
		if i == 0 {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
				b = append(b, c)
				continue
			}
			b = append(b, '_')
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			b = append(b, c)
			continue
		}
		b = append(b, '_')
	}
	if len(b) == 0 {
		return ""
	}
	if b[0] >= '0' && b[0] <= '9' {
		b = append([]byte{'_'}, b...)
	}
	return string(b)
}

// SanitizeMetricName converts an arbitrary string into a Prometheus metric name.
// Prom metric name: [a-zA-Z_:][a-zA-Z0-9_:]*
func SanitizeMetricName(in string) string {
	if in == "" {
		return ""
	}
	for i := 0; i < len(in); i++ {
		c := in[i]
		if i == 0 {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == ':' {
				continue
			}
			return sanitizeMetricNameCached(in)
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == ':' {
			continue
		}
		return sanitizeMetricNameCached(in)
	}
	return in
}

func sanitizeMetricNameCached(in string) string {
	if v, ok := metricNameCache.Load(in); ok {
		return v.(string)
	}
	out := sanitizeMetricNameSlow(in)
	metricNameCache.Store(in, out)
	return out
}

func sanitizeMetricNameSlow(in string) string {
	b := make([]byte, 0, len(in)+1)
	for i := 0; i < len(in); i++ {
		c := in[i]
		if i == 0 {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == ':' {
				b = append(b, c)
				continue
			}
			b = append(b, '_')
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == ':' {
			b = append(b, c)
			continue
		}
		b = append(b, '_')
	}
	if len(b) == 0 {
		return ""
	}
	if b[0] >= '0' && b[0] <= '9' {
		b = append([]byte{'_'}, b...)
	}
	return string(b)
}
