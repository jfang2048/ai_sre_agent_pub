package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newAlertmanagerNotifierForTest(rt http.RoundTripper) *AlertmanagerNotifier {
	n := NewAlertmanagerNotifier(AlertmanagerConfig{URL: "http://alertmanager.test"}, zap.NewNop())
	n.client.Transport = rt
	return n
}

func newHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// ── alertName tests ────────────────────────────────────────────────────

func TestAlertNameFromLabel(t *testing.T) {
	a := &Alert{ID: "id-1", Labels: map[string]string{"alertname": "HighCPU"}}
	assert.Equal(t, "HighCPU", alertName(a))
}

func TestAlertNameFallbackToSummary(t *testing.T) {
	a := &Alert{
		ID:          "id-2",
		Labels:      map[string]string{},
		Annotations: map[string]string{"summary": "Disk full"},
	}
	assert.Equal(t, "Disk full", alertName(a))
}

func TestAlertNameFallbackToID(t *testing.T) {
	a := &Alert{ID: "id-3", Labels: map[string]string{}}
	assert.Equal(t, "id-3", alertName(a))
}

// ── alertSeverity tests ────────────────────────────────────────────────

func TestAlertSeverityFromField(t *testing.T) {
	a := &Alert{Severity: "critical"}
	assert.Equal(t, "critical", alertSeverity(a))
}

func TestAlertSeverityFromLabel(t *testing.T) {
	a := &Alert{Severity: "", Labels: map[string]string{"severity": "warning"}}
	assert.Equal(t, "warning", alertSeverity(a))
}

func TestAlertSeverityDefault(t *testing.T) {
	a := &Alert{Labels: map[string]string{}}
	assert.Equal(t, "info", alertSeverity(a))
}

// ── alertStartTime tests ──────────────────────────────────────────────

func TestAlertStartTimeFromField(t *testing.T) {
	now := time.Now()
	a := &Alert{StartsAt: now}
	assert.Equal(t, now, alertStartTime(a))
}

func TestAlertStartTimeDefaultsToNow(t *testing.T) {
	before := time.Now()
	a := &Alert{}
	result := alertStartTime(a)
	assert.False(t, result.Before(before), "Should be at or after call time")
}

// ── AlertmanagerNotifier tests ──────────────────────────────────────

func TestNewAlertmanagerNotifierDefaultTimeout(t *testing.T) {
	n := NewAlertmanagerNotifier(AlertmanagerConfig{URL: "http://localhost:9093"}, zap.NewNop())
	assert.Equal(t, 10*time.Second, n.client.Timeout)
}

func TestNewAlertmanagerNotifierCustomTimeout(t *testing.T) {
	n := NewAlertmanagerNotifier(AlertmanagerConfig{
		URL:     "http://localhost:9093",
		Timeout: 5 * time.Second,
	}, zap.NewNop())
	assert.Equal(t, 5*time.Second, n.client.Timeout)
}

func TestAlertmanagerNotifierName(t *testing.T) {
	n := NewAlertmanagerNotifier(AlertmanagerConfig{URL: "http://localhost"}, zap.NewNop())
	assert.Equal(t, "alertmanager", n.Name())
}

func TestAlertmanagerNotifierSendSuccess(t *testing.T) {
	var receivedBody []byte
	n := newAlertmanagerNotifierForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "/api/v2/alerts", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBody = body

		return newHTTPResponse(http.StatusOK, ""), nil
	}))

	alert := &Alert{
		ID:     "alert-1",
		Status: "firing",
		Labels: map[string]string{
			"alertname": "HighCPU",
			"service":   "api",
		},
		Annotations: map[string]string{"summary": "CPU is high"},
		StartsAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	err := n.Send(context.Background(), alert)
	require.NoError(t, err)

	// Parse the sent payload
	var payload []map[string]interface{}
	require.NoError(t, json.Unmarshal(receivedBody, &payload))
	require.Len(t, payload, 1)

	labels := payload[0]["labels"].(map[string]interface{})
	assert.Equal(t, "HighCPU", labels["alertname"])
	assert.Equal(t, "sre-agent", labels["source"])
}

func TestAlertmanagerNotifierSendWithEndsAt(t *testing.T) {
	n := newAlertmanagerNotifierForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		var payload []map[string]interface{}
		json.Unmarshal(body, &payload)

		// Resolved alerts should have endsAt
		_, hasEndsAt := payload[0]["endsAt"]
		assert.True(t, hasEndsAt, "Resolved alert should include endsAt")
		return newHTTPResponse(http.StatusOK, ""), nil
	}))

	alert := &Alert{
		ID:       "alert-2",
		Status:   "resolved",
		Labels:   map[string]string{"alertname": "HighCPU"},
		StartsAt: time.Now().Add(-10 * time.Minute),
		EndsAt:   time.Now(),
	}

	err := n.Send(context.Background(), alert)
	require.NoError(t, err)
}

func TestAlertmanagerNotifierSendServerError(t *testing.T) {
	n := newAlertmanagerNotifierForTest(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return newHTTPResponse(http.StatusInternalServerError, ""), nil
	}))

	err := n.Send(context.Background(), &Alert{
		ID:     "alert-3",
		Labels: map[string]string{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestAlertmanagerNotifierSendConnectionRefused(t *testing.T) {
	n := newAlertmanagerNotifierForTest(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp 127.0.0.1:9093: connect: connection refused")
	}))

	err := n.Send(context.Background(), &Alert{
		ID:     "alert-4",
		Labels: map[string]string{},
	})
	require.Error(t, err)
}
