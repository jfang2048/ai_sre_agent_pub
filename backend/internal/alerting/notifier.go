package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
)

func alertName(alert *Alert) string {
	if name := alert.Labels["alertname"]; name != "" {
		return name
	}
	if name := alert.Annotations["summary"]; name != "" {
		return name
	}
	return alert.ID
}

func alertSeverity(alert *Alert) string {
	if alert.Severity != "" {
		return alert.Severity
	}
	if severity := alert.Labels["severity"]; severity != "" {
		return severity
	}
	return "info"
}

func alertState(alert *Alert) string {
	if alert.Status != "" {
		return alert.Status
	}
	return "firing"
}

func alertStartTime(alert *Alert) time.Time {
	if !alert.StartsAt.IsZero() {
		return alert.StartsAt
	}
	return time.Now()
}

func alertDedupKey(alert *Alert) string {
	if alert.Fingerprint != "" {
		return alert.Fingerprint
	}
	return alert.ID
}

// LogNotifier logs alerts
type LogNotifier struct {
	logger *zap.Logger
}

// NewLogNotifier creates a new log notifier
func NewLogNotifier(logger *zap.Logger) *LogNotifier {
	return &LogNotifier{
		logger: logger.With(zap.String("component", "log_notifier")),
	}
}

// Send logs the alert
func (n *LogNotifier) Send(ctx context.Context, alert *Alert) error {
	n.logger.Error("alert fired",
		zap.String("id", alert.ID),
		zap.String("name", alertName(alert)),
		zap.String("severity", alertSeverity(alert)),
		zap.String("status", alertState(alert)),
		zap.Any("labels", alert.Labels),
		zap.Any("annotations", alert.Annotations),
	)
	return nil
}

// Name returns the notifier name
func (n *LogNotifier) Name() string {
	return "log"
}

// WebhookNotifier sends alerts via HTTP webhook
type WebhookNotifier struct {
	url     string
	client  *http.Client
	logger  *zap.Logger
	headers map[string]string
}

// WebhookConfig configures a webhook notifier
type WebhookConfig struct {
	URLEnv     string            `json:"url_env"`
	Headers    map[string]string `json:"headers"`
	HeaderEnv  map[string]string `json:"header_env"`
	Timeout    time.Duration     `json:"timeout"`
	AuthBearer string            `json:"auth_bearer_env"`
}

// NewWebhookNotifier creates a new webhook notifier
func NewWebhookNotifier(config WebhookConfig, logger *zap.Logger) *WebhookNotifier {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	url := ""
	if config.URLEnv != "" {
		url = os.Getenv(config.URLEnv)
	}
	if url == "" {
		logger.Warn("webhook url env var is empty", zap.String("env_var", config.URLEnv))
	}

	headers := make(map[string]string, len(config.Headers)+len(config.HeaderEnv)+1)
	for k, v := range config.Headers {
		headers[k] = v
	}
	for k, envVar := range config.HeaderEnv {
		if envVar == "" {
			continue
		}
		if value := os.Getenv(envVar); value != "" {
			headers[k] = value
		}
	}
	if config.AuthBearer != "" {
		if token := os.Getenv(config.AuthBearer); token != "" {
			headers["Authorization"] = "Bearer " + token
		}
	}

	return &WebhookNotifier{
		url:     url,
		client:  &http.Client{Timeout: timeout},
		logger:  logger.With(zap.String("component", "webhook_notifier")),
		headers: headers,
	}
}

// Send sends the alert via webhook
func (n *WebhookNotifier) Send(ctx context.Context, alert *Alert) error {
	payload := map[string]interface{}{
		"alert_id":    alert.ID,
		"name":        alertName(alert),
		"severity":    alertSeverity(alert),
		"status":      alertState(alert),
		"starts_at":   alert.StartsAt,
		"ends_at":     alert.EndsAt,
		"labels":      alert.Labels,
		"annotations": alert.Annotations,
		"fingerprint": alert.Fingerprint,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range n.headers {
		req.Header.Set(k, v)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	n.logger.Debug("webhook sent successfully",
		zap.String("alert", alert.ID),
		zap.String("url", n.url))
	return nil
}

// Name returns the notifier name
func (n *WebhookNotifier) Name() string {
	return "webhook"
}

// SlackNotifier sends alerts to Slack
type SlackNotifier struct {
	webhookURL string
	channel    string
	username   string
	client     *http.Client
	logger     *zap.Logger
}

// SlackConfig configures a Slack notifier
type SlackConfig struct {
	WebhookEnv string `json:"webhook_env"`
	Channel    string `json:"channel"`
	Username   string `json:"username"`
}

// NewSlackNotifier creates a new Slack notifier
func NewSlackNotifier(config SlackConfig, logger *zap.Logger) *SlackNotifier {
	webhookURL := ""
	if config.WebhookEnv != "" {
		webhookURL = os.Getenv(config.WebhookEnv)
	}
	if webhookURL == "" {
		logger.Warn("slack webhook env var is empty", zap.String("env_var", config.WebhookEnv))
	}

	return &SlackNotifier{
		webhookURL: webhookURL,
		channel:    config.Channel,
		username:   config.Username,
		client:     &http.Client{Timeout: 30 * time.Second},
		logger:     logger.With(zap.String("component", "slack_notifier")),
	}
}

// Send sends the alert to Slack
func (n *SlackNotifier) Send(ctx context.Context, alert *Alert) error {
	color := n.colorForSeverity(alertSeverity(alert))
	startTime := alertStartTime(alert)

	attachment := map[string]interface{}{
		"color":     color,
		"title":     fmt.Sprintf("%s: %s", alertSeverity(alert), alertName(alert)),
		"text":      alert.Annotations["description"],
		"fields":    n.fieldsFromAlert(alert),
		"timestamp": startTime.Unix(),
	}

	payload := map[string]interface{}{
		"channel":     n.channel,
		"username":    n.username,
		"attachments": []interface{}{attachment},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send Slack message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Slack returned status %d", resp.StatusCode)
	}

	n.logger.Debug("Slack message sent successfully",
		zap.String("alert", alert.ID))
	return nil
}

// Name returns the notifier name
func (n *SlackNotifier) Name() string {
	return "slack"
}

// colorForSeverity returns a color for the severity level
func (n *SlackNotifier) colorForSeverity(severity string) string {
	switch severity {
	case "critical":
		return "#FF0000"
	case "error", "warning":
		return "#FFCC00"
	case "info":
		return "#36A64F"
	default:
		return "#808080"
	}
}

// fieldsFromAlert creates Slack fields from an alert
func (n *SlackNotifier) fieldsFromAlert(alert *Alert) []map[string]interface{} {
	fields := []map[string]interface{}{
		{
			"title": "Severity",
			"value": alertSeverity(alert),
			"short": true,
		},
		{
			"title": "State",
			"value": alertState(alert),
			"short": true,
		},
	}

	for k, v := range alert.Labels {
		fields = append(fields, map[string]interface{}{
			"title": k,
			"value": v,
			"short": true,
		})
	}

	return fields
}

// PagerDutyNotifier sends alerts to PagerDuty
type PagerDutyNotifier struct {
	apiKey    string
	serviceID string
	client    *http.Client
	logger    *zap.Logger
}

// PagerDutyConfig configures a PagerDuty notifier
type PagerDutyConfig struct {
	APIKeyEnv    string `json:"api_key_env"`
	ServiceIDEnv string `json:"service_id_env"`
}

// NewPagerDutyNotifier creates a new PagerDuty notifier
func NewPagerDutyNotifier(config PagerDutyConfig, logger *zap.Logger) *PagerDutyNotifier {
	apiKey := ""
	if config.APIKeyEnv != "" {
		apiKey = os.Getenv(config.APIKeyEnv)
	}
	if apiKey == "" {
		logger.Warn("pagerduty api key env var is empty", zap.String("env_var", config.APIKeyEnv))
	}

	serviceID := ""
	if config.ServiceIDEnv != "" {
		serviceID = os.Getenv(config.ServiceIDEnv)
	}

	return &PagerDutyNotifier{
		apiKey:    apiKey,
		serviceID: serviceID,
		client:    &http.Client{Timeout: 30 * time.Second},
		logger:    logger.With(zap.String("component", "pagerduty_notifier")),
	}
}

// Send sends the alert to PagerDuty
func (n *PagerDutyNotifier) Send(ctx context.Context, alert *Alert) error {
	startTime := alertStartTime(alert)
	payload := map[string]interface{}{
		"routing_key":  n.apiKey,
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":   fmt.Sprintf("%s: %s", alertSeverity(alert), alertName(alert)),
			"severity":  n.severityForPagerDuty(alertSeverity(alert)),
			"source":    "sre-agent",
			"timestamp": startTime.Format(time.RFC3339),
			"custom_details": map[string]interface{}{
				"annotations": alert.Annotations,
				"labels":      alert.Labels,
				"status":      alertState(alert),
				"fingerprint": alert.Fingerprint,
			},
		},
		"dedup_key": alertDedupKey(alert),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://events.pagerduty.com/v2/enqueue",
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send PagerDuty event: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("PagerDuty returned status %d", resp.StatusCode)
	}

	n.logger.Debug("PagerDuty event sent successfully",
		zap.String("alert", alert.ID))
	return nil
}

// Name returns the notifier name
func (n *PagerDutyNotifier) Name() string {
	return "pagerduty"
}

// severityForPagerDuty maps alert severity to PagerDuty severity
func (n *PagerDutyNotifier) severityForPagerDuty(severity string) string {
	switch severity {
	case "critical":
		return "critical"
	case "error", "warning":
		return "warning"
	case "info":
		return "info"
	default:
		return "info"
	}
}
