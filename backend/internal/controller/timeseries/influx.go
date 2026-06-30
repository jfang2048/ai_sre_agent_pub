package timeseries

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type metricPoint struct {
	CollectorID string
	Hostname    string
	Metric      string
	Value       float64
	Timestamp   time.Time
}

type influxClient struct {
	baseURL string
	org     string
	bucket  string
	token   string
	client  *http.Client
}

func newInfluxClient(cfg Config) *influxClient {
	return &influxClient{
		baseURL: cfg.URL,
		org:     cfg.Org,
		bucket:  cfg.Bucket,
		token:   cfg.Token,
		client: &http.Client{
			Timeout: maxDuration(cfg.QueryTimeout, 5*time.Second),
		},
	}
}

func (c *influxClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("influx health check failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *influxClient) EnsureBucket(ctx context.Context, retention time.Duration) error {
	orgID, err := c.lookupOrgID(ctx)
	if err != nil {
		return err
	}
	exists, err := c.bucketExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	body := map[string]any{
		"name":  c.bucket,
		"orgID": orgID,
	}
	if retention > 0 {
		body["retentionRules"] = []map[string]any{{
			"type":         "expire",
			"everySeconds": int64(retention / time.Second),
		}}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/buckets", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyAuth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("create influx bucket failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *influxClient) Write(ctx context.Context, measurement string, points []metricPoint) error {
	if len(points) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, point := range points {
		if point.CollectorID == "" || point.Metric == "" || point.Timestamp.IsZero() || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
			continue
		}
		buf.WriteString(escapeMeasurement(measurement))
		buf.WriteByte(',')
		buf.WriteString("collector_id=")
		buf.WriteString(escapeTagValue(point.CollectorID))
		if point.Hostname != "" {
			buf.WriteByte(',')
			buf.WriteString("hostname=")
			buf.WriteString(escapeTagValue(point.Hostname))
		}
		buf.WriteByte(',')
		buf.WriteString("metric=")
		buf.WriteString(escapeTagValue(point.Metric))
		buf.WriteString(" value=")
		buf.WriteString(strconv.FormatFloat(point.Value, 'f', -1, 64))
		buf.WriteByte(' ')
		buf.WriteString(strconv.FormatInt(point.Timestamp.UTC().UnixNano(), 10))
		buf.WriteByte('\n')
	}
	if buf.Len() == 0 {
		return nil
	}

	writeURL := fmt.Sprintf("%s/api/v2/write?org=%s&bucket=%s&precision=ns", c.baseURL, url.QueryEscape(c.org), url.QueryEscape(c.bucket))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, writeURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	c.applyAuth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("influx write failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *influxClient) QueryMetricHistory(ctx context.Context, measurement, collectorID string, since time.Time) ([]metricPoint, error) {
	if collectorID == "" {
		return nil, nil
	}
	if since.IsZero() {
		since = time.Unix(0, 0).UTC()
	}
	flux := fmt.Sprintf(`from(bucket: %q)
  |> range(start: time(v: %q), stop: now())
  |> filter(fn: (r) => r._measurement == %q and r._field == "value" and r.collector_id == %q)
  |> keep(columns: ["_time", "_value", "collector_id", "hostname", "metric"])
  |> sort(columns: ["_time", "metric"])`,
		c.bucket,
		since.UTC().Format(time.RFC3339Nano),
		measurement,
		collectorID,
	)
	payload, err := json.Marshal(map[string]any{
		"type":  "flux",
		"query": flux,
	})
	if err != nil {
		return nil, err
	}

	queryURL := fmt.Sprintf("%s/api/v2/query?org=%s", c.baseURL, url.QueryEscape(c.org))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, queryURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/csv")
	c.applyAuth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("influx query failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseInfluxCSV(resp.Body)
}

func (c *influxClient) lookupOrgID(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v2/orgs?org=%s", c.baseURL, url.QueryEscape(c.org)), nil)
	if err != nil {
		return "", err
	}
	c.applyAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("lookup influx org failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Orgs []struct {
			ID string `json:"id"`
		} `json:"orgs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if len(payload.Orgs) == 0 || strings.TrimSpace(payload.Orgs[0].ID) == "" {
		return "", fmt.Errorf("influx org %q not found", c.org)
	}
	return strings.TrimSpace(payload.Orgs[0].ID), nil
}

func (c *influxClient) bucketExists(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v2/buckets?name=%s&org=%s", c.baseURL, url.QueryEscape(c.bucket), url.QueryEscape(c.org)), nil)
	if err != nil {
		return false, err
	}
	c.applyAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("lookup influx bucket failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Buckets []struct {
			ID string `json:"id"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, err
	}
	return len(payload.Buckets) > 0, nil
}

func (c *influxClient) applyAuth(req *http.Request) {
	if strings.TrimSpace(c.token) != "" {
		req.Header.Set("Authorization", "Token "+c.token)
	}
}

func parseInfluxCSV(r io.Reader) ([]metricPoint, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	var (
		header  []string
		indices map[string]int
		points  []metricPoint
	)
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(record) == 0 {
			continue
		}
		if strings.HasPrefix(record[0], "#") {
			continue
		}
		if header == nil {
			header = record
			indices = make(map[string]int, len(header))
			for idx, name := range header {
				indices[name] = idx
			}
			continue
		}
		point, ok, err := parseInfluxRecord(record, indices)
		if err != nil {
			return nil, err
		}
		if ok {
			points = append(points, point)
		}
	}
	sort.Slice(points, func(i, j int) bool {
		if !points[i].Timestamp.Equal(points[j].Timestamp) {
			return points[i].Timestamp.Before(points[j].Timestamp)
		}
		return points[i].Metric < points[j].Metric
	})
	return points, nil
}

func parseInfluxRecord(record []string, indices map[string]int) (metricPoint, bool, error) {
	field := readCSVField(record, indices, "_field")
	if field != "" && field != "value" {
		return metricPoint{}, false, nil
	}
	tsRaw := readCSVField(record, indices, "_time")
	valueRaw := readCSVField(record, indices, "_value")
	metric := readCSVField(record, indices, "metric")
	collectorID := readCSVField(record, indices, "collector_id")
	if tsRaw == "" || valueRaw == "" || metric == "" || collectorID == "" {
		return metricPoint{}, false, nil
	}
	timestamp, err := time.Parse(time.RFC3339Nano, tsRaw)
	if err != nil {
		return metricPoint{}, false, err
	}
	value, err := strconv.ParseFloat(valueRaw, 64)
	if err != nil {
		return metricPoint{}, false, err
	}
	return metricPoint{
		Timestamp:   timestamp.UTC(),
		Value:       value,
		Metric:      metric,
		CollectorID: collectorID,
		Hostname:    readCSVField(record, indices, "hostname"),
	}, true, nil
}

func readCSVField(record []string, indices map[string]int, name string) string {
	idx, ok := indices[name]
	if !ok || idx < 0 || idx >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[idx])
}

func escapeMeasurement(value string) string {
	replacer := strings.NewReplacer(",", `\,`, " ", `\ `)
	return replacer.Replace(value)
}

func escapeTagValue(value string) string {
	replacer := strings.NewReplacer(",", `\,`, " ", `\ `, "=", `\=`)
	return replacer.Replace(value)
}

func maxDuration(values ...time.Duration) time.Duration {
	var out time.Duration
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}
