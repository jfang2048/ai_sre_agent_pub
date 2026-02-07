package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector"
	"github.com/jfang2048/ai_sre_agent_pub/internal/observability"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	// Overridden at build time via -ldflags.
	version   = "dev"
	commit    = "dev"
	buildDate = "unknown"
)

var (
	logLevel    string
	logFormat   string
	showVersion bool
	configPath  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "sre-collector",
		Short: "SRE Collector - Push-first telemetry collector",
		Run:   runCollector,
	}

	rootCmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.Flags().StringVar(&logFormat, "log-format", "json", "Log format (json, text)")
	rootCmd.Flags().BoolVar(&showVersion, "version", false, "Show version")
	rootCmd.Flags().StringVar(&configPath, "config", "", "Config file path")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runCollector(cmd *cobra.Command, args []string) {
	if showVersion {
		fmt.Printf("SRE Collector %s (commit: %s, built: %s)\n", version, commit, buildDate)
		os.Exit(0)
	}

	logger, err := observability.NewLogger(logLevel, logFormat)
	if err != nil {
		panic(fmt.Sprintf("failed to create logger: %v", err))
	}
	logger = logger.With(zap.String("version", version), zap.String("commit", commit))

	cfg := collector.LoadConfig(configPath)
	cfg.Version = version

	c, err := collector.New(cfg, logger)
	if err != nil {
		logger.Fatal("failed to create collector", zap.Error(err))
	}

	ctx, cancel := signalContext()
	defer cancel()

	if err := c.Run(ctx); err != nil {
		logger.Warn("collector stopped", zap.Error(err))
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}
