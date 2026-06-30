package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

type HARole string

const (
	HARoleStandalone HARole = "standalone"
	HARoleLeader     HARole = "leader"
	HARoleFollower   HARole = "follower"
	HARoleStandby    HARole = "standby"
)

// HAState reports the controller role and current leader coordination state.
type HAState struct {
	Enabled           bool      `json:"enabled"`
	Backend           string    `json:"backend"`
	Mode              string    `json:"mode"`
	Role              HARole    `json:"role"`
	Active            bool      `json:"active"`
	ReadOnly          bool      `json:"read_only"`
	NodeID            string    `json:"node_id,omitempty"`
	LeaderID          string    `json:"leader_id,omitempty"`
	LeaderHTTP        string    `json:"leader_http,omitempty"`
	LeaderGRPC        string    `json:"leader_grpc,omitempty"`
	LeaseExpiresAt    time.Time `json:"lease_expires_at,omitempty"`
	LastTransitionAt  time.Time `json:"last_transition_at,omitempty"`
	TransitionCount   uint64    `json:"transition_count"`
	LastError         string    `json:"last_error,omitempty"`
	AllowFollowerRead bool      `json:"allow_follower_read"`
}

type haLeaderRecord struct {
	NodeID        string    `json:"node_id"`
	AdvertiseHTTP string    `json:"advertise_http,omitempty"`
	AdvertiseGRPC string    `json:"advertise_grpc,omitempty"`
	ElectedAt     time.Time `json:"elected_at"`
}

type haCoordinator interface {
	Start(context.Context, func(HAState)) error
	Stop(context.Context) error
	State() HAState
	Active() bool
}

func newHACoordinator(cfg HAConfig, logger *zap.Logger) (haCoordinator, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if !cfg.Enabled || normalizeHABackend(cfg.Backend) == "static" {
		return newStaticHACoordinator(cfg), nil
	}
	switch normalizeHABackend(cfg.Backend) {
	case "etcd":
		return newEtcdHACoordinator(cfg, logger)
	default:
		return newStaticHACoordinator(cfg), nil
	}
}

type staticHACoordinator struct {
	state HAState
}

func newStaticHACoordinator(cfg HAConfig) *staticHACoordinator {
	mode := normalizeHAMode(cfg.Mode)
	state := HAState{
		Enabled:           cfg.Enabled,
		Backend:           "static",
		Mode:              mode,
		NodeID:            strings.TrimSpace(cfg.NodeID),
		AllowFollowerRead: cfg.AllowFollowerRead,
		LastTransitionAt:  time.Now().UTC(),
	}
	if !cfg.Enabled {
		state.Role = HARoleStandalone
		state.Active = true
		return &staticHACoordinator{state: state}
	}
	if mode == "standby" {
		state.Role = HARoleStandby
		state.ReadOnly = true
	} else {
		state.Role = HARoleLeader
		state.Active = true
		state.LeaderID = state.NodeID
	}
	return &staticHACoordinator{state: state}
}

func (c *staticHACoordinator) Start(_ context.Context, onChange func(HAState)) error {
	if onChange != nil {
		onChange(c.state)
	}
	return nil
}

func (c *staticHACoordinator) Stop(context.Context) error { return nil }
func (c *staticHACoordinator) State() HAState             { return c.state }
func (c *staticHACoordinator) Active() bool               { return c.state.Active }

type etcdHACoordinator struct {
	cfg    HAConfig
	logger *zap.Logger

	mu       sync.RWMutex
	state    HAState
	client   *clientv3.Client
	cancel   context.CancelFunc
	onChange func(HAState)
}

func newEtcdHACoordinator(cfg HAConfig, logger *zap.Logger) (*etcdHACoordinator, error) {
	endpoints := cleanHAEndpoints(cfg.EtcdEndpoints)
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("ha backend etcd requires etcd_endpoints")
	}
	if strings.TrimSpace(cfg.ElectionKey) == "" {
		cfg.ElectionKey = DefaultHAConfig().ElectionKey
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = DefaultHAConfig().LeaseTTL
	}
	if cfg.ObserveInterval <= 0 {
		cfg.ObserveInterval = DefaultHAConfig().ObserveInterval
	}
	if cfg.CampaignTimeout <= 0 {
		cfg.CampaignTimeout = DefaultHAConfig().CampaignTimeout
	}
	if strings.TrimSpace(cfg.NodeID) == "" {
		cfg.NodeID = fmt.Sprintf("controller-%d", time.Now().UTC().UnixNano())
	}
	state := HAState{
		Enabled:           true,
		Backend:           "etcd",
		Mode:              normalizeHAMode(cfg.Mode),
		Role:              HARoleFollower,
		ReadOnly:          true,
		NodeID:            cfg.NodeID,
		AllowFollowerRead: cfg.AllowFollowerRead,
		LastTransitionAt:  time.Now().UTC(),
	}
	return &etcdHACoordinator{
		cfg:    cfg,
		logger: logger.With(zap.String("component", "controller_ha")),
		state:  state,
	}, nil
}

func (c *etcdHACoordinator) Start(ctx context.Context, onChange func(HAState)) error {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:        cleanHAEndpoints(c.cfg.EtcdEndpoints),
		DialTimeout:      c.cfg.CampaignTimeout,
		AutoSyncInterval: maxHADuration(c.cfg.ObserveInterval, 2*time.Second),
	})
	if err != nil {
		return fmt.Errorf("create etcd ha client: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.client = cli
	c.cancel = cancel
	c.onChange = onChange
	c.mu.Unlock()
	c.publishState(c.state)
	go c.loop(runCtx)
	return nil
}

func (c *etcdHACoordinator) Stop(ctx context.Context) error {
	c.mu.Lock()
	cancel := c.cancel
	cli := c.client
	c.cancel = nil
	c.client = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cli != nil {
		return cli.Close()
	}
	return nil
}

func (c *etcdHACoordinator) Active() bool {
	return c.State().Active
}

func (c *etcdHACoordinator) State() HAState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *etcdHACoordinator) loop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := c.campaignOnce(ctx); err != nil && ctx.Err() == nil {
			c.failoverState(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(maxHADuration(c.cfg.ObserveInterval, time.Second)):
			}
		}
	}
}

func (c *etcdHACoordinator) campaignOnce(ctx context.Context) error {
	cli := c.clientRef()
	if cli == nil {
		return fmt.Errorf("etcd client unavailable")
	}

	grantCtx, cancelGrant := context.WithTimeout(ctx, c.cfg.CampaignTimeout)
	lease, err := cli.Grant(grantCtx, int64(maxHADuration(c.cfg.LeaseTTL, 5*time.Second).Seconds()))
	cancelGrant()
	if err != nil {
		return fmt.Errorf("grant etcd lease: %w", err)
	}

	record := haLeaderRecord{
		NodeID:        c.cfg.NodeID,
		AdvertiseHTTP: strings.TrimSpace(c.cfg.AdvertiseHTTP),
		AdvertiseGRPC: strings.TrimSpace(c.cfg.AdvertiseGRPC),
		ElectedAt:     time.Now().UTC(),
	}
	payload, _ := json.Marshal(record)

	txnCtx, cancelTxn := context.WithTimeout(ctx, c.cfg.CampaignTimeout)
	resp, err := cli.Txn(txnCtx).
		If(clientv3.Compare(clientv3.CreateRevision(c.cfg.ElectionKey), "=", 0)).
		Then(clientv3.OpPut(c.cfg.ElectionKey, string(payload), clientv3.WithLease(lease.ID))).
		Else(clientv3.OpGet(c.cfg.ElectionKey)).
		Commit()
	cancelTxn()
	if err != nil {
		_, _ = cli.Revoke(context.Background(), lease.ID)
		return fmt.Errorf("campaign leadership: %w", err)
	}
	if resp.Succeeded {
		return c.holdLeadership(ctx, cli, lease.ID, record)
	}

	leader := parseHARecordFromTxn(resp)
	c.publishState(HAState{
		Enabled:           true,
		Backend:           "etcd",
		Mode:              normalizeHAMode(c.cfg.Mode),
		Role:              HARoleFollower,
		ReadOnly:          true,
		NodeID:            c.cfg.NodeID,
		LeaderID:          leader.NodeID,
		LeaderHTTP:        leader.AdvertiseHTTP,
		LeaderGRPC:        leader.AdvertiseGRPC,
		LastError:         "",
		AllowFollowerRead: c.cfg.AllowFollowerRead,
	})
	_, _ = cli.Revoke(context.Background(), lease.ID)
	return c.waitForLeaderChange(ctx, cli)
}

func (c *etcdHACoordinator) holdLeadership(ctx context.Context, cli *clientv3.Client, leaseID clientv3.LeaseID, record haLeaderRecord) error {
	keepAliveCtx, cancelKeepAlive := context.WithCancel(ctx)
	defer cancelKeepAlive()
	keepAliveCh, err := cli.KeepAlive(keepAliveCtx, leaseID)
	if err != nil {
		_, _ = cli.Revoke(context.Background(), leaseID)
		return fmt.Errorf("keepalive leadership lease: %w", err)
	}

	c.publishState(HAState{
		Enabled:           true,
		Backend:           "etcd",
		Mode:              normalizeHAMode(c.cfg.Mode),
		Role:              HARoleLeader,
		Active:            true,
		NodeID:            c.cfg.NodeID,
		LeaderID:          record.NodeID,
		LeaderHTTP:        record.AdvertiseHTTP,
		LeaderGRPC:        record.AdvertiseGRPC,
		LeaseExpiresAt:    time.Now().UTC().Add(c.cfg.LeaseTTL),
		AllowFollowerRead: c.cfg.AllowFollowerRead,
	})

	for {
		select {
		case <-ctx.Done():
			_, _ = cli.Revoke(context.Background(), leaseID)
			return ctx.Err()
		case ka, ok := <-keepAliveCh:
			if !ok || ka == nil {
				return fmt.Errorf("leadership keepalive channel closed")
			}
			state := c.State()
			state.Active = true
			state.Role = HARoleLeader
			state.ReadOnly = false
			state.LeaderID = record.NodeID
			state.LeaderHTTP = record.AdvertiseHTTP
			state.LeaderGRPC = record.AdvertiseGRPC
			state.LeaseExpiresAt = time.Now().UTC().Add(time.Duration(ka.TTL) * time.Second)
			state.LastError = ""
			c.publishState(state)
		}
	}
}

func (c *etcdHACoordinator) waitForLeaderChange(ctx context.Context, cli *clientv3.Client) error {
	watchCh := cli.Watch(ctx, c.cfg.ElectionKey)
	timer := time.NewTimer(maxHADuration(c.cfg.ObserveInterval, time.Second))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case watchResp, ok := <-watchCh:
			if !ok {
				return nil
			}
			if watchResp.Err() != nil {
				return watchResp.Err()
			}
			for _, ev := range watchResp.Events {
				if ev.Type == clientv3.EventTypeDelete || ev.Type == clientv3.EventTypePut {
					return nil
				}
			}
		}
	}
}

func (c *etcdHACoordinator) failoverState(err error) {
	state := c.State()
	state.Active = false
	state.ReadOnly = true
	state.Role = HARoleFollower
	state.LastError = strings.TrimSpace(err.Error())
	state.LeaseExpiresAt = time.Time{}
	c.publishState(state)
}

func (c *etcdHACoordinator) publishState(next HAState) {
	c.mu.Lock()
	current := c.state
	changedRole := current.Role != next.Role || current.Active != next.Active || current.LeaderID != next.LeaderID
	if next.LastTransitionAt.IsZero() {
		next.LastTransitionAt = current.LastTransitionAt
	}
	if next.LastTransitionAt.IsZero() {
		next.LastTransitionAt = time.Now().UTC()
	}
	if changedRole {
		next.TransitionCount = current.TransitionCount + 1
		next.LastTransitionAt = time.Now().UTC()
	} else if next.TransitionCount == 0 {
		next.TransitionCount = current.TransitionCount
	}
	c.state = next
	onChange := c.onChange
	c.mu.Unlock()
	if onChange != nil {
		onChange(next)
	}
}

func (c *etcdHACoordinator) clientRef() *clientv3.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

func parseHARecordFromTxn(resp *clientv3.TxnResponse) haLeaderRecord {
	if resp == nil {
		return haLeaderRecord{}
	}
	for _, item := range resp.Responses {
		getResp := item.GetResponseRange()
		if getResp == nil || len(getResp.Kvs) == 0 {
			continue
		}
		var record haLeaderRecord
		if err := json.Unmarshal(getResp.Kvs[0].Value, &record); err == nil {
			return record
		}
		record.NodeID = strings.TrimSpace(string(getResp.Kvs[0].Value))
		return record
	}
	return haLeaderRecord{}
}

func cleanHAEndpoints(values []string) []string {
	out := make([]string, 0, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		out = append(out, raw)
	}
	return out
}

func maxHADuration(values ...time.Duration) time.Duration {
	best := time.Duration(0)
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}
