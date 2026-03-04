package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// HAConfig configures lightweight controller high-availability mode.
// v0.5 supports static active/standby deployment with read-only standby behavior.
type HAConfig struct {
	Enabled bool `yaml:"enabled"`
	// Mode accepts "active" (default) or "standby".
	Mode string `yaml:"mode"`
}

func DefaultHAConfig() HAConfig {
	return HAConfig{
		Enabled: false,
		Mode:    "active",
	}
}

func normalizeHAMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "standby":
		return "standby"
	default:
		return "active"
	}
}

func (c *Controller) haMode() string {
	if c == nil || !c.config.HA.Enabled {
		return "active"
	}
	return normalizeHAMode(c.config.HA.Mode)
}

func (c *Controller) isStandby() bool {
	return c.config.HA.Enabled && c.haMode() == "standby"
}

func (c *Controller) requireActiveController(w http.ResponseWriter) bool {
	if !c.isStandby() {
		return true
	}
	http.Error(w, "controller is running in standby mode (read-only)", http.StatusServiceUnavailable)
	return false
}

func (c *Controller) handleHAStatus(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	mode := c.haMode()
	standby := mode == "standby"
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":    c.config.HA.Enabled,
		"mode":       mode,
		"active":     !standby,
		"read_only":  standby,
		"timestamp":  time.Now().UTC(),
		"controller": c.ListenAddr(),
	})
}
