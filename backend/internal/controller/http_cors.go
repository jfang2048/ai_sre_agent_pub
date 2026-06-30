package controller

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
)

var localDevAllowedOrigins = []string{
	"http://127.0.0.1:3000",
	"http://localhost:3000",
	"http://127.0.0.1:4173",
	"http://localhost:4173",
	"http://127.0.0.1:5173",
	"http://localhost:5173",
	"http://127.0.0.1:8080",
	"http://localhost:8080",
}

func normalizeAllowedOrigins(origins []string) []string {
	if len(origins) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(origins))
	out := make([]string, 0, len(origins))
	for _, raw := range origins {
		if origin := canonicalOrigin(raw); origin != "" {
			if _, ok := seen[origin]; ok {
				continue
			}
			seen[origin] = struct{}{}
			out = append(out, origin)
		}
	}
	sort.Strings(out)
	return out
}

func canonicalOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	if (scheme != "http" && scheme != "https") || host == "" {
		return ""
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return ""
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	return scheme + "://" + host
}

func requestOrigin(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); proto != "" {
		scheme = strings.ToLower(proto)
	} else if r.TLS != nil {
		scheme = "https"
	}
	return canonicalOrigin(scheme + "://" + host)
}

func (c *Controller) corsConfiguredOrigins() []string {
	if c == nil {
		return nil
	}
	return normalizeAllowedOrigins(c.config.API.AllowedOrigins)
}

func (c *Controller) corsUsesLocalDevDefaults() bool {
	return len(c.corsConfiguredOrigins()) == 0 && normalizeControllerDeploymentMode(c.config.Deployment.Mode) == defaultDeploymentMode
}

func (c *Controller) corsSameOriginOnly() bool {
	return len(c.corsConfiguredOrigins()) == 0 && !c.corsUsesLocalDevDefaults()
}

func (c *Controller) corsMode() string {
	switch {
	case len(c.corsConfiguredOrigins()) > 0:
		return "configured_origins"
	case c.corsUsesLocalDevDefaults():
		return "local_dev_defaults"
	default:
		return "same_origin_only"
	}
}

func (c *Controller) allowedCORSOriginsForStatus() []string {
	switch {
	case len(c.corsConfiguredOrigins()) > 0:
		return append([]string(nil), c.corsConfiguredOrigins()...)
	case c.corsUsesLocalDevDefaults():
		return append([]string(nil), localDevAllowedOrigins...)
	default:
		return []string{}
	}
}

func (c *Controller) isAllowedCORSOrigin(origin string, r *http.Request) bool {
	origin = canonicalOrigin(origin)
	if origin == "" {
		return false
	}
	if origin == requestOrigin(r) {
		return true
	}
	if externalOrigin := canonicalOrigin(c.config.Deployment.ExternalURL); externalOrigin != "" && origin == externalOrigin {
		return true
	}
	for _, allowed := range c.corsConfiguredOrigins() {
		if origin == allowed {
			return true
		}
	}
	if c.corsUsesLocalDevDefaults() {
		for _, allowed := range localDevAllowedOrigins {
			if origin == allowed {
				return true
			}
		}
	}
	return false
}
