//go:build e2e

package e2e

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	controllerBaseURL = "http://127.0.0.1:8080"
	defaultTimeout    = 10 * time.Second
)

func newE2EClient() *http.Client {
	return &http.Client{Timeout: defaultTimeout}
}

func controllerURL(path string) string {
	return controllerBaseURL + path
}

func requireControllerReachable(t *testing.T, client *http.Client, probeURL string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, probeURL, nil)
	if err != nil {
		t.Fatalf("build controller preflight request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		if isSkippableE2EEnvError(err) {
			t.Skipf("skipping e2e: controller unavailable or sockets restricted (%v)", err)
		}
		t.Fatalf("controller preflight request failed: %v", err)
	}
	_ = resp.Body.Close()
}

func newJSONRequest(t *testing.T, method, url string, body string) *http.Request {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build %s request %s: %v", method, url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func requireHTTPStatus(t *testing.T, resp *http.Response, want int, context string) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s status = %d, want %d", context, resp.StatusCode, want)
	}
}

func isSkippableE2EEnvError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "network is unreachable") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout")
}
