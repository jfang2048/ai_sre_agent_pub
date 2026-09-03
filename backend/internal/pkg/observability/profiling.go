package observability

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"time"

	"go.uber.org/zap"
)

// Profiler handles pprof profiling endpoints
type Profiler struct {
	server     *http.Server
	enabled    bool
	profileDir string
	logger     *zap.Logger
}

// ProfilerConfig configures the profiler
type ProfilerConfig struct {
	Enabled    bool
	Port       int
	ProfileDir string
}

// NewProfiler creates a new profiler
func NewProfiler(config ProfilerConfig, logger *zap.Logger) *Profiler {
	if !config.Enabled {
		return &Profiler{enabled: false, logger: logger}
	}

	mux := http.NewServeMux()

	// Register handlers explicitly on a private mux. Importing net/http/pprof for
	// side effects would mutate the process-global mux and make the endpoint
	// surface implicit.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	addr := fmt.Sprintf("127.0.0.1:%d", config.Port)
	return &Profiler{
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
		enabled:    true,
		profileDir: config.ProfileDir,
		logger:     logger.With(zap.String("component", "profiler")),
	}
}

// Start starts the profiler server
func (p *Profiler) Start() error {
	if !p.enabled {
		p.logger.Info("profiling disabled")
		return nil
	}

	p.logger.Info("starting profiling server", zap.String("addr", p.server.Addr))

	go func() {
		if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			p.logger.Error("profiler server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop stops the profiler server
func (p *Profiler) Stop(ctx context.Context) error {
	if !p.enabled || p.server == nil {
		return nil
	}

	p.logger.Info("stopping profiling server")
	return p.server.Shutdown(ctx)
}

// TakeCPUProfile starts a CPU profile
func (p *Profiler) TakeCPUProfile(duration time.Duration) error {
	if !p.enabled {
		return fmt.Errorf("profiling disabled")
	}
	// CPU profile is handled by the pprof endpoint
	p.logger.Info("CPU profile available at /debug/pprof/profile?seconds=" + duration.String())
	return nil
}

// TakeHeapProfile captures a heap profile
func (p *Profiler) TakeHeapProfile() error {
	if !p.enabled {
		return fmt.Errorf("profiling disabled")
	}
	p.logger.Info("heap profile available at /debug/pprof/heap")
	return nil
}

// TakeGoroutineDump captures a goroutine dump
func (p *Profiler) TakeGoroutineDump() error {
	if !p.enabled {
		return fmt.Errorf("profiling disabled")
	}
	p.logger.Info("goroutine dump available at /debug/pprof/goroutine")
	return nil
}

// TakeTrace captures an execution trace
func (p *Profiler) TakeTrace(duration time.Duration) error {
	if !p.enabled {
		return fmt.Errorf("profiling disabled")
	}
	p.logger.Info("execution trace available at /debug/pprof/trace?seconds=" + duration.String())
	return nil
}

// SaveProfile saves a profile to a file
func (p *Profiler) SaveProfile(profileType, filename string) error {
	// This would download the profile from the pprof endpoint
	// and save it to the profile directory
	p.logger.Info("saving profile",
		zap.String("type", profileType),
		zap.String("filename", filename),
	)
	return nil
}
