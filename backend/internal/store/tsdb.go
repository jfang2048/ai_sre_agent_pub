package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
)

// TSDB defines the interface for time-series storage
type TSDB interface {
	// Write writes a batch of samples
	Write(ctx context.Context, samples []TimeSeries) error

	// Query executes a PromQL query
	Query(ctx context.Context, query string, time time.Time) (*VectorResult, error)

	// QueryRange executes a PromQL query over a range
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*MatrixResult, error)
}

// TimeSeries represents a single metric series
type TimeSeries struct {
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	Value     float64           `json:"value"`
	Timestamp int64             `json:"timestamp"` // Unix millisecond
}

// VectorResult is the result of an instant query
type VectorResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"` // [timestamp, "value"]
		} `json:"result"`
	} `json:"data"`
}

// MatrixResult is the result of a range query
type MatrixResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"` // [[ts, "val"], ...]
		} `json:"result"`
	} `json:"data"`
}

// VictoriaMetricsClient implements TSDB for VictoriaMetrics
type VictoriaMetricsClient struct {
	writeURL string
	queryURL string
	client   *http.Client
	logger   *zap.Logger

	// Query Optimization
	cacheMu    sync.RWMutex
	queryCache map[string]*cachedResult
}

type cachedResult struct {
	Result    *VectorResult
	ExpiresAt time.Time
}

// NewVictoriaMetricsClient creates a new client
// addr: e.g., "http://localhost:8428"
func NewVictoriaMetricsClient(addr string, logger *zap.Logger) *VictoriaMetricsClient {
	return &VictoriaMetricsClient{
		writeURL:   fmt.Sprintf("%s/api/v1/import/prometheus", addr), // Supports Prometheus text format or similar
		queryURL:   fmt.Sprintf("%s/api/v1", addr),
		client:     &http.Client{Timeout: 5 * time.Second},
		logger:     logger.With(zap.String("component", "tsdb")),
		queryCache: make(map[string]*cachedResult),
	}
}

// StartCacheCleanup starts a background routine to clean expired cache entries
func (c *VictoriaMetricsClient) StartCacheCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				c.cleanupCache()
			}
		}
	}()
}

func (c *VictoriaMetricsClient) cleanupCache() {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	now := time.Now()
	for k, v := range c.queryCache {
		if now.After(v.ExpiresAt) {
			delete(c.queryCache, k)
		}
	}
}

// Write sends metrics using Influx Line Protocol (supported by VM at /write)
// OR simpler: Prometheus text format to /api/v1/import/prometheus
// We'll use Influx Line Protocol to /write for performance if VM supports it standard,
// but /api/v1/import/prometheus is specific to VM for accepting Prometheus metrics.
// Let's use the standard "Influx" endpoint which VM supports at port 8428/write usually?
// No, VM unification is powerful.
// Use: /api/v1/import providing JSON lines is also good.
// Let's use the simplest efficient method: Influx Line Protocol to /write.
func (c *VictoriaMetricsClient) Write(ctx context.Context, samples []TimeSeries) error {
	// Implementation note: We'll actually use the /api/v1/import endpoint from VM
	// which accepts JSON lines, or Influx.
	// Let's stick to Influx Line Protocol for generic TSDB compatibility.
	// URL: /write (Influx compatible)

	// Fallback/Simpler: Use the /api/v1/import endpoint with JSON for robustness here
	// Format: {"metric":{"__name__":"foo", "label":"val"}, "values":[1.0], "timestamps":[1234567890]}

	var buf bytes.Buffer
	for _, s := range samples {
		// Prepare labels
		labels := make(map[string]string, len(s.Labels)+1)
		for k, v := range s.Labels {
			labels[k] = v
		}
		labels["__name__"] = s.Name

		line := struct {
			Metric     map[string]string `json:"metric"`
			Values     []float64         `json:"values"`
			Timestamps []int64           `json:"timestamps"`
		}{
			Metric:     labels,
			Values:     []float64{s.Value},
			Timestamps: []int64{s.Timestamp},
		}

		b, _ := json.Marshal(line)
		buf.Write(b)
		buf.WriteByte('\n')
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.writeURL, &buf)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tsdb write failed: %s %s", resp.Status, string(body))
	}

	return nil
}

// Query executes a PromQL query with Caching
func (c *VictoriaMetricsClient) Query(ctx context.Context, query string, t time.Time) (*VectorResult, error) {
	// 1. Check Cache
	cacheKey := fmt.Sprintf("%s|%d", query, t.Unix()/60) // Cache key resolution: 1 minute
	c.cacheMu.RLock()
	cached, ok := c.queryCache[cacheKey]
	c.cacheMu.RUnlock()

	if ok && time.Now().Before(cached.ExpiresAt) {
		return cached.Result, nil
	}

	// 2. Execute
	u, _ := url.Parse(c.queryURL + "/query")
	q := u.Query()
	q.Set("query", query)
	if !t.IsZero() {
		q.Set("time", fmt.Sprintf("%d", t.Unix()))
	}
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query failed: %s", resp.Status)
	}

	var result VectorResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// 3. Cache Result (TTL 1 min)
	c.cacheMu.Lock()
	c.queryCache[cacheKey] = &cachedResult{
		Result:    &result,
		ExpiresAt: time.Now().Add(1 * time.Minute),
	}
	c.cacheMu.Unlock()

	return &result, nil
}

// QueryRange executes a range query (no cache for now due to variability)
func (c *VictoriaMetricsClient) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*MatrixResult, error) {
	u, _ := url.Parse(c.queryURL + "/query_range")
	q := u.Query()
	q.Set("query", query)
	q.Set("start", fmt.Sprintf("%d", start.Unix()))
	q.Set("end", fmt.Sprintf("%d", end.Unix()))
	q.Set("step", fmt.Sprintf("%f", step.Seconds()))
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("query_range failed: %s", resp.Status)
	}

	var result MatrixResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
