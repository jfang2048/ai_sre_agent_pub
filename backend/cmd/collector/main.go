package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector"
	"github.com/jfang2048/ai_sre_agent_pub/internal/observability"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	// Overridden at build time via -ldflags.
	version   = "dev"
	commit    = "dev"
	buildDate = "unknown"
)

type cliFlags struct {
	logLevel      string
	logFormat     string
	showVersion   bool
	configPath    string
	level         int
	interval      time.Duration
	endpoints     []string
	metricsListen string
}

func main() {
	flags := &cliFlags{}
	if err := newRootCmd(flags).Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd(flags *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "sre-collector",
		Short:        "SRE Collector - Push-first telemetry collector",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCollector(cmd, flags)
		},
	}

	cmd.Flags().StringVar(&flags.logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	cmd.Flags().StringVar(&flags.logFormat, "log-format", "json", "Log format (json, text)")
	cmd.Flags().BoolVar(&flags.showVersion, "version", false, "Show version")
	cmd.Flags().StringVar(&flags.configPath, "config", "", "Config file path")
	cmd.Flags().IntVar(&flags.level, "level", 0, "Collection level override (1-5)")
	cmd.Flags().DurationVar(&flags.interval, "interval", 0, "Collection interval override (e.g. 5s)")
	cmd.Flags().StringSliceVar(&flags.endpoints, "endpoint", nil, "Controller endpoint override (repeatable host:port)")
	cmd.Flags().StringVar(&flags.metricsListen, "metrics-listen", "", "Prometheus listen address (e.g. :9464)")
	return cmd
}

func runCollector(cmd *cobra.Command, flags *cliFlags) error {
	if flags.showVersion {
		fmt.Printf("SRE Collector %s (commit: %s, built: %s)\n", version, commit, buildDate)
		return nil
	}

	logger, cfg, runtimeCollector, metricsServer, cleanup, err := buildRuntime(cmd, flags)
	if err != nil {
		return err
	}
	defer cleanup()
	if metricsServer != nil {
		defer shutdownHTTPServer(metricsServer, logger)
	}

	return runWithSignals(cmd, flags, logger, runtimeCollector, cfg)
}

func buildRuntime(cmd *cobra.Command, flags *cliFlags) (*zap.Logger, collector.Config, *collector.Collector, *http.Server, func(), error) {
	logger, err := observability.NewLogger(flags.logLevel, flags.logFormat)
	if err != nil {
		return nil, collector.Config{}, nil, nil, nil, fmt.Errorf("create logger: %w", err)
	}
	logger = logger.With(zap.String("version", version), zap.String("commit", commit))

	cfg, err := loadRuntimeConfig(cmd, flags.configPath, flags)
	if err != nil {
		return nil, collector.Config{}, nil, nil, nil, err
	}
	cfg.Version = version

	cleanup := func() {
		_ = logger.Sync()
	}
	if cfg.TracingJaegerEndpoint != "" {
		if err := observability.InitTracing("sre-collector", cfg.TracingJaegerEndpoint, logger); err != nil {
			return nil, collector.Config{}, nil, nil, nil, fmt.Errorf("init tracing: %w", err)
		}
		cleanup = func() {
			_ = logger.Sync()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = observability.ShutdownTracing(shutdownCtx)
		}
	}

	collectorRuntime, err := collector.New(cfg, logger)
	if err != nil {
		return nil, collector.Config{}, nil, nil, nil, fmt.Errorf("create collector: %w", err)
	}

	metricsServer, err := startMetricsServer(cfg.MetricsListenAddress, logger)
	if err != nil {
		return nil, collector.Config{}, nil, nil, nil, err
	}

	return logger, cfg, collectorRuntime, metricsServer, cleanup, nil
}

func runWithSignals(cmd *cobra.Command, flags *cliFlags, logger *zap.Logger, runtimeCollector *collector.Collector, baseCfg collector.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- runtimeCollector.Run(ctx)
	}()

	stopSignals := make(chan os.Signal, 1)
	reloadSignals := make(chan os.Signal, 1)
	signal.Notify(stopSignals, os.Interrupt, syscall.SIGTERM)
	signal.Notify(reloadSignals, syscall.SIGHUP)
	defer signal.Stop(stopSignals)
	defer signal.Stop(reloadSignals)

	for {
		select {
		case sig := <-stopSignals:
			logger.Info("shutdown signal received", zap.String("signal", sig.String()))
			cancel()
		case <-reloadSignals:
			nextCfg, reloadErr := loadRuntimeConfig(cmd, flags.configPath, flags)
			if reloadErr != nil {
				logger.Warn("config reload failed", zap.Error(reloadErr))
				continue
			}
			nextCfg.Version = baseCfg.Version
			if reloadErr := runtimeCollector.ReloadConfig(nextCfg); reloadErr != nil {
				logger.Warn("collector reload rejected", zap.Error(reloadErr))
				continue
			}
		case runErr := <-runErrCh:
			if errors.Is(runErr, context.Canceled) {
				return nil
			}
			return fmt.Errorf("collector stopped: %w", runErr)
		case <-ctx.Done():
			return nil
		}
	}
}

func loadRuntimeConfig(cmd *cobra.Command, configPath string, flags *cliFlags) (collector.Config, error) {
	cfg, err := collector.LoadConfig(configPath)
	if err != nil {
		return cfg, fmt.Errorf("load collector config: %w", err)
	}
	applyCLIOverrides(cmd, flags, &cfg)
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("validate collector config: %w", err)
	}
	return cfg, nil
}

func applyCLIOverrides(cmd *cobra.Command, flags *cliFlags, cfg *collector.Config) {
	if cmd.Flags().Changed("level") {
		cfg.Level = flags.level
	}
	if cmd.Flags().Changed("interval") {
		cfg.CollectionInterval = flags.interval
	}
	if cmd.Flags().Changed("endpoint") {
		cfg.ControllerEndpoints = append([]string(nil), flags.endpoints...)
	}
	if cmd.Flags().Changed("metrics-listen") {
		cfg.MetricsListenAddress = flags.metricsListen
	}
}

func startMetricsServer(addr string, logger *zap.Logger) (*http.Server, error) {
	if addr == "" {
		return nil, nil
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("metrics server started", zap.String("addr", addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server stopped with error", zap.Error(err))
		}
	}()
	return server, nil
}

func shutdownHTTPServer(server *http.Server, logger *zap.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Warn("metrics server shutdown failed", zap.Error(err))
	}
}
