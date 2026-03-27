package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// ... existing notifiers ...

// AlertmanagerNotifier sends alerts to Prometheus Alertmanager
type AlertmanagerNotifier struct {
	url    string
	client *http.Client
	logger *zap.Logger
}

// AlertmanagerConfig configures an Alertmanager notifier
type AlertmanagerConfig struct {
	URL     string        `json:"url"` // e.g. http://localhost:9093
	Timeout time.Duration `json:"timeout"`
}

// NewAlertmanagerNotifier creates a new Alertmanager notifier
func NewAlertmanagerNotifier(config AlertmanagerConfig, logger *zap.Logger) *AlertmanagerNotifier {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &AlertmanagerNotifier{
		url:    config.URL,
		client: &http.Client{Timeout: timeout},
		logger: logger.With(zap.String("component", "alertmanager_notifier")),
	}
}

// Send sends the alert to Alertmanager
func (n *AlertmanagerNotifier) Send(ctx context.Context, alert *Alert) error {
	// Format as Prometheus Alert (PostableAlert)
	// [
	//   {
	//     "labels": { "alertname": "<name>", ... },
	//     "annotations": { ... },
	//     "startsAt": "<rfc3339>",
	//     "endsAt": "<rfc3339>"
	//   }
	// ]

	promAlert := map[string]interface{}{
		"labels": func() map[string]string {
			l := make(map[string]string)
			for k, v := range alert.Labels {
				l[k] = v
			}
			l["alertname"] = alertName(alert)
			l["severity"] = alertSeverity(alert)
			l["source"] = "sre-agent"
			return l
		}(),
		"annotations": alert.Annotations,
		"startsAt":    alertStartTime(alert).Format(time.RFC3339),
	}

	if !alert.EndsAt.IsZero() {
		promAlert["endsAt"] = alert.EndsAt.Format(time.RFC3339)
	}

	payload := []interface{}{promAlert}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/v2/alerts", n.url)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("alertmanager returned status %d", resp.StatusCode)
	}

	n.logger.Debug("alert pushed to alertmanager", zap.String("id", alert.ID))
	return nil
}

func (n *AlertmanagerNotifier) Name() string {
	return "alertmanager"
}
