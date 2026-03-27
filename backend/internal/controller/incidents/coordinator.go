package incidents

import (
	"context"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"go.uber.org/zap"
)

// Coordinator watches for alerts and builds incident contexts end-to-end.
type Coordinator struct {
	cfg          Config
	orchestrator *Orchestrator
	analysis     *analysis.Engine
	sink         func(AggregatedContext)
	logger       *zap.Logger

	mu       sync.Mutex
	seen     map[string]struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	running  bool
	shutdown chan struct{}
}

// NewCoordinator builds a coordinator. analysis/agent may be nil; the
// coordinator will gracefully degrade.
func NewCoordinator(cfg Config, orchestrator *Orchestrator, analysisEngine *analysis.Engine, sink func(AggregatedContext), logger *zap.Logger) *Coordinator {
	if logger == nil {
		logger, _ = zap.NewDevelopment()
	}
	return &Coordinator{
		cfg:          cfg,
		orchestrator: orchestrator,
		analysis:     analysisEngine,
		sink:         sink,
		logger:       logger.With(zap.String("component", "incident_coordinator")),
		seen:         make(map[string]struct{}),
		shutdown:     make(chan struct{}),
	}
}

// Start begins polling for alerts.
func (c *Coordinator) Start(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.mu.Unlock()

	go c.loop()
}

// Stop terminates polling.
func (c *Coordinator) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	<-c.shutdown
}

// HandleExternalAlert processes a pushed alert payload immediately.
func (c *Coordinator) HandleExternalAlert(ctx context.Context, alert InputAlert) (*AggregatedContext, error) {
	if alert.ID == "" {
		alert.ID = "ext-" + time.Now().Format("20060102T150405.000")
	}
	return c.processAlert(ctx, alert, alert.ID)
}

// loop polls the analysis engine for newly firing alerts.
func (c *Coordinator) loop() {
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer func() {
		ticker.Stop()
		close(c.shutdown)
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.poll()
		}
	}
}

func (c *Coordinator) poll() {
	if c.analysis == nil || c.orchestrator == nil || !c.cfg.Enabled {
		return
	}
	alerts := c.analysis.GetAlerts()
	for _, al := range alerts {
		if al.ResolvedAt != nil {
			continue
		}
		if c.alreadySeen(al.ID) {
			continue
		}

		input := InputAlert{
			ID:       al.ID,
			Title:    al.Title,
			Service:  al.Labels["service"],
			Severity: string(al.Severity),
			StartsAt: al.CreatedAt,
			EndsAt:   time.Time{},
			Labels:   al.Labels,
		}
		if input.Labels == nil {
			input.Labels = map[string]string{"node": al.NodeName}
		}
		if input.Service == "" {
			input.Service = al.NodeName
		}
		if ctxBundle, err := c.processAlert(c.ctx, input, al.ID); err != nil {
			c.logger.Warn("failed to build incident context", zap.Error(err))
		} else {
			c.logger.Info("incident context built",
				zap.String("alert", al.ID),
				zap.String("service", input.Service),
				zap.Int("metrics", len(ctxBundle.Metrics)),
				zap.Int("logs", len(ctxBundle.Logs)))
		}
	}
}

func (c *Coordinator) processAlert(ctx context.Context, alert InputAlert, incidentID string) (*AggregatedContext, error) {
	if c.orchestrator == nil {
		return nil, nil
	}

	ctxBundle, err := c.orchestrator.BuildContext(ctx, alert, incidentID)
	if err != nil {
		return nil, err
	}

	if c.sink != nil {
		c.sink(*ctxBundle)
	}
	c.markSeen(alert.ID)
	return ctxBundle, nil
}

func (c *Coordinator) alreadySeen(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.seen[id]
	return ok
}

func (c *Coordinator) markSeen(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen[id] = struct{}{}
}
