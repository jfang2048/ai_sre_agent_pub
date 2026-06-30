package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// HAConfig configures controller high-availability coordination.
// The default backend keeps single-node semantics. When the etcd backend is
// enabled, one controller becomes leader for write-sensitive tasks and the rest
// stay read-only.
type HAConfig struct {
	Enabled           bool          `yaml:"enabled"`
	Backend           string        `yaml:"backend"`
	Mode              string        `yaml:"mode"`
	NodeID            string        `yaml:"node_id"`
	AdvertiseHTTP     string        `yaml:"advertise_http"`
	AdvertiseGRPC     string        `yaml:"advertise_grpc"`
	EtcdEndpoints     []string      `yaml:"etcd_endpoints"`
	ElectionKey       string        `yaml:"election_key"`
	LeaseTTL          time.Duration `yaml:"lease_ttl"`
	ObserveInterval   time.Duration `yaml:"observe_interval"`
	CampaignTimeout   time.Duration `yaml:"campaign_timeout"`
	AllowFollowerRead bool          `yaml:"allow_follower_read"`
}

func DefaultHAConfig() HAConfig {
	return HAConfig{
		Enabled:           false,
		Backend:           "static",
		Mode:              "active",
		ElectionKey:       "/ai-sre-agent/controller/leader",
		LeaseTTL:          15 * time.Second,
		ObserveInterval:   3 * time.Second,
		CampaignTimeout:   5 * time.Second,
		AllowFollowerRead: true,
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

func normalizeHABackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "etcd":
		return "etcd"
	default:
		return "static"
	}
}

func (c *Controller) haState() HAState {
	if c == nil || c.haCoordinator == nil {
		mode := normalizeHAMode(c.config.HA.Mode)
		role := HARoleLeader
		active := true
		readOnly := false
		if c == nil || !c.config.HA.Enabled {
			role = HARoleStandalone
		} else if mode == "standby" {
			role = HARoleStandby
			active = false
			readOnly = true
		}
		return HAState{
			Enabled:           c != nil && c.config.HA.Enabled,
			Backend:           normalizeHABackend(c.config.HA.Backend),
			Mode:              mode,
			Role:              role,
			Active:            active,
			ReadOnly:          readOnly,
			NodeID:            strings.TrimSpace(c.config.HA.NodeID),
			AllowFollowerRead: c != nil && c.config.HA.AllowFollowerRead,
		}
	}
	return c.haCoordinator.State()
}

func (c *Controller) haMode() string {
	return c.haState().Mode
}

func (c *Controller) isStandby() bool {
	return c.haState().ReadOnly
}

func (c *Controller) isActiveController() bool {
	return c.haState().Active
}

func (c *Controller) activeControllerWriteError(surface string) error {
	state := c.haState()
	if state.Active {
		return nil
	}
	surface = strings.TrimSpace(surface)
	msg := "controller is not the active leader for write-sensitive operations"
	if surface != "" {
		msg = "controller is not the active leader for " + surface
	}
	switch {
	case strings.Contains(strings.ToLower(surface), "grpc") && strings.TrimSpace(state.LeaderGRPC) != "":
		msg += "; retry against active controller gRPC endpoint " + strings.TrimSpace(state.LeaderGRPC)
	case strings.Contains(strings.ToLower(surface), "http") && strings.TrimSpace(state.LeaderHTTP) != "":
		msg += "; retry against active controller HTTP endpoint " + strings.TrimSpace(state.LeaderHTTP)
	}
	return errors.New(msg)
}

func (c *Controller) requireActiveController(w http.ResponseWriter) bool {
	if err := c.activeControllerWriteError("write-sensitive operations"); err == nil {
		return true
	} else {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return false
	}
}

func (c *Controller) handleHAStatus(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	state := c.haState()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled":                          state.Enabled,
		"backend":                          state.Backend,
		"mode":                             state.Mode,
		"role":                             state.Role,
		"active":                           state.Active,
		"read_only":                        state.ReadOnly,
		"node_id":                          state.NodeID,
		"leader_id":                        state.LeaderID,
		"leader_http":                      state.LeaderHTTP,
		"leader_grpc":                      state.LeaderGRPC,
		"last_transition_at":               state.LastTransitionAt,
		"transition_count":                 state.TransitionCount,
		"lease_expires_at":                 state.LeaseExpiresAt,
		"last_error":                       state.LastError,
		"allow_follower_read":              state.AllowFollowerRead,
		"write_sensitive_requests_guarded": state.Enabled,
		"write_sensitive_requests_blocked": state.Enabled && !state.Active,
		"grpc_ingest_writes_guarded":       c.grpcIngestWritesGuarded(),
		"grpc_ingest_writes_blocked":       c.grpcIngestWritesBlocked(),
		"timestamp":                        time.Now().UTC(),
		"controller":                       c.ListenAddr(),
	})
}
