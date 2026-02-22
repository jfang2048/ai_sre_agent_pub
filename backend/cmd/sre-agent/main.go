// Package main provides the entry point for the SRE Agent.
//
// The agent orchestrates monitoring, analysis, and remediation.
//
// Usage:
//
//	sre-agent [flags]
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jfang2048/ai_sre_agent_pub/internal/core"
	"github.com/jfang2048/ai_sre_agent_pub/internal/observability"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/config"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	configPath  string
	logLevel    string
	logFormat   string
	verbose     bool
	showVersion bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "sre-agent",
		Short: "AI-powered SRE agent for infrastructure monitoring",
		Long:  `SRE Agent combines Linux kernel integration with AI reasoning for predictive incident prevention.`,
		Run:   runAgent,
	}

	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Config file path")
	rootCmd.Flags().StringVarP(&logLevel, "log-level", "l", "info", "Log level (debug, info, warn, error)")
	rootCmd.Flags().StringVar(&logFormat, "log-format", "json", "Log format (json, text)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output (debug level)")
	rootCmd.Flags().BoolVar(&showVersion, "version", false, "Show version")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runAgent(cmd *cobra.Command, args []string) {
	if showVersion {
		fmt.Printf("SRE Agent %s (commit: %s)\n", core.Version(), core.Commit())
		os.Exit(0)
	}

	// Verbose implies debug level
	if verbose {
		logLevel = "debug"
	}

	// Initialize logger using shared utility
	logger, err := observability.NewLogger(logLevel, logFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}

	logger.Info("starting SRE agent",
		zap.String("version", core.Version()),
		zap.String("commit", core.Commit()))

	// Load configuration
	cfgLoader := config.NewLoader(logger)
	cfg, err := cfgLoader.Load(configPath)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	// Override config with flags
	if logLevel != "" {
		cfg.Logging.Level = logLevel
	}
	if logFormat != "" {
		cfg.Logging.Format = logFormat
	}

	// Re-initialize logger with config settings
	logger, err = observability.NewLogger(cfg.Logging.Level, cfg.Logging.Format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}

	// Create and start agent
	a, err := core.NewAgent(cfg, logger)
	if err != nil {
		logger.Fatal("failed to create agent", zap.Error(err))
	}

	ctx, cancel := signalContext()
	defer cancel()

	if err := a.Start(ctx); err != nil {
		logger.Fatal("failed to start agent", zap.Error(err))
	}

	logger.Info("agent running, press Ctrl+C to stop")
	<-ctx.Done()

	logger.Info("shutting down")
	if err := a.Stop(); err != nil {
		logger.Fatal("shutdown error", zap.Error(err))
	}
}

// signalContext returns a context that cancels on interrupt
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
