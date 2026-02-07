// Package main provides the entry point for the SRE Controller.
//
// The controller aggregates metrics from probes and provides a unified API.
//
// Usage:
//
//	sre-controller [flags]
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller"
	"github.com/jfang2048/ai_sre_agent_pub/internal/observability"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var (
	// Overridden at build time via -ldflags.
	version   = "dev"
	commit    = "dev"
	buildDate = "unknown"
)

var (
	configPath  string
	listenAddr  string
	grpcAddr    string
	portFile    string
	nodes       string
	logLevel    string
	logFormat   string
	webPath     string
	showVersion bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "sre-controller",
		Short: "SRE Controller - Central metrics aggregator",
		Long: `SRE Controller aggregates metrics from SRE Probes.
Provides unified API, web UI, and alerting.`,
		Run: runController,
	}

	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Config file path")
	rootCmd.Flags().StringVarP(&listenAddr, "listen", "l", ":8080", "HTTP listen address")
	rootCmd.Flags().StringVar(&grpcAddr, "grpc-listen", ":9090", "gRPC listen address")
	rootCmd.Flags().StringVar(&portFile, "port-file", "", "Write resolved listen addresses to this JSON file (for dev scripts)")
	rootCmd.Flags().StringVarP(&nodes, "nodes", "n", "", "Comma-separated probe addresses (host:port)")
	rootCmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.Flags().StringVar(&logFormat, "log-format", "json", "Log format (json, text)")
	rootCmd.Flags().StringVar(&webPath, "web-path", "./web", "Web UI files path")
	rootCmd.Flags().BoolVarP(&showVersion, "version", "v", false, "Show version")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runController(cmd *cobra.Command, args []string) {
	if showVersion {
		fmt.Printf("SRE Controller %s (commit: %s, built: %s)\n", version, commit, buildDate)
		os.Exit(0)
	}

	if configPath == "" {
		if env := os.Getenv("SRE_CONTROLLER_CONFIG"); env != "" {
			configPath = env
		} else if fileExists("./configs/controller.yaml") {
			configPath = "./configs/controller.yaml"
		} else if fileExists("/etc/sre-controller/config.yaml") {
			configPath = "/etc/sre-controller/config.yaml"
		}
	}

	// Initialize logger using shared utility
	logger, err := observability.NewLogger(logLevel, logFormat)
	if err != nil {
		panic(fmt.Sprintf("failed to create logger: %v", err))
	}
	logger = logger.With(zap.String("version", version), zap.String("commit", commit))

	logger.Info("starting SRE controller")

	// Build configuration
	cfg := buildConfig(logger)

	// Rebuild logger if config overrides log level
	if cfg.LogLevel != "" && cfg.LogLevel != logLevel {
		updatedLogger, err := observability.NewLogger(cfg.LogLevel, logFormat)
		if err != nil {
			logger.Warn("failed to apply config log level, using flag value", zap.Error(err))
		} else {
			logger = updatedLogger
		}
	}

	// Create and start controller
	ctrl, err := controller.New(cfg, logger)
	if err != nil {
		logger.Fatal("failed to create controller", zap.Error(err))
	}

	ctx, cancel := signalContext()
	defer cancel()

	if err := ctrl.Start(ctx); err != nil {
		logger.Fatal("failed to start controller", zap.Error(err))
	}

	if portFile != "" {
		if err := controller.WritePortFile(portFile, ctrl.ListenAddr(), ctrl.GRPCAddr()); err != nil {
			logger.Warn("failed to write port file", zap.String("path", portFile), zap.Error(err))
		} else {
			logger.Info("wrote port file", zap.String("path", portFile))
		}
	}

	logger.Info("controller running",
		zap.String("listen", ctrl.ListenAddr()),
		zap.Int("nodes", len(cfg.Nodes)),
		zap.String("api", fmt.Sprintf("http://%s/api/v1/status", controller.DialAddr(ctrl.ListenAddr()))))

	<-ctx.Done()
	logger.Info("shutting down")

	if err := ctrl.Stop(); err != nil {
		logger.Error("shutdown error", zap.Error(err))
		os.Exit(1)
	}
}

// buildConfig builds controller configuration from flags and file
func buildConfig(logger *zap.Logger) controller.Config {
	cfg := controller.DefaultConfig()

	// Load from config file if provided
	if configPath != "" {
		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err != nil {
			logger.Warn("failed to read config file", zap.Error(err))
		} else {
			overrideFromViper(&cfg)
		}
	}

	// Override with CLI flags
	if listenAddr != "" && listenAddr != ":8080" {
		cfg.ListenAddr = listenAddr
	}
	if grpcAddr != "" && grpcAddr != ":9090" {
		cfg.GRPCListenAddr = grpcAddr
	}
	if webPath != "" && webPath != "./web" {
		cfg.WebPath = webPath
	}

	// Parse nodes from CLI
	if nodes != "" {
		cfg.Nodes = parseNodes(nodes)
	}

	if viper.IsSet("auth") {
		if err := viper.UnmarshalKey("auth", &cfg.Auth); err != nil {
			logger.Warn("failed to parse auth config", zap.Error(err))
		}
	}

	if viper.IsSet("analysis") {
		if err := viper.UnmarshalKey("analysis", &cfg.Analysis); err != nil {
			logger.Warn("failed to parse analysis config", zap.Error(err))
		}
	}

	if viper.IsSet("checks") {
		if err := viper.UnmarshalKey("checks", &cfg.Checks); err != nil {
			logger.Warn("failed to parse checks config", zap.Error(err))
		}
	}

	if viper.IsSet("gpu") {
		if err := viper.UnmarshalKey("gpu", &cfg.GPU); err != nil {
			logger.Warn("failed to parse gpu config", zap.Error(err))
		}
	}

	if viper.IsSet("agent") {
		if err := viper.UnmarshalKey("agent", &cfg.Agent); err != nil {
			logger.Warn("failed to parse agent config", zap.Error(err))
		}
	}

	if viper.IsSet("incidents") {
		if err := viper.UnmarshalKey("incidents", &cfg.Incidents); err != nil {
			logger.Warn("failed to parse incidents config", zap.Error(err))
		}
	}

	// Lightweight env overrides (keep CLI minimal)
	if env := os.Getenv("SRE_CONTROLLER_HTTP_LISTEN"); env != "" {
		cfg.ListenAddr = env
	}
	if env := os.Getenv("SRE_CONTROLLER_GRPC_LISTEN"); env != "" {
		cfg.GRPCListenAddr = env
	}
	if env := os.Getenv("SRE_CONTROLLER_WEB_PATH"); env != "" {
		cfg.WebPath = env
	}
	if env := os.Getenv("SRE_AGENT_RAG_ENABLED"); env != "" {
		cfg.Agent.RAGEnabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_AGENT_RAG_PATHS"); env != "" {
		cfg.Agent.RAGPaths = parseCSV(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_MAX_CHARS"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Agent.RAGMaxChars = v
		}
	}
	if env := os.Getenv("SRE_AGENT_LLM_ENABLED"); env != "" {
		cfg.Agent.LLMEnabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
		cfg.Analysis.LLMEnabled = cfg.Agent.LLMEnabled
	}
	if env := os.Getenv("SRE_AGENT_LLM_MODEL"); env != "" {
		cfg.Analysis.LLMModel = env
	}
	if env := os.Getenv("SRE_AGENT_LLM_PROVIDER"); env != "" {
		cfg.Analysis.LLMProvider = env
	}

	if cfg.Auth.APIKeyEnv != "" && os.Getenv(cfg.Auth.APIKeyEnv) != "" {
		cfg.Auth.Enabled = true
	}

	if len(cfg.Nodes) == 0 {
		logger.Info("no scrape nodes configured, running in push-first mode")
	}

	return cfg
}

// overrideFromViper overrides config from viper
func overrideFromViper(cfg *controller.Config) {
	if v := viper.GetString("listen"); v != "" {
		cfg.ListenAddr = v
	}
	if v := viper.GetString("grpc_listen"); v != "" {
		cfg.GRPCListenAddr = v
	}
	if v := viper.GetString("log_level"); v != "" {
		cfg.LogLevel = v
	}
	if v := viper.GetDuration("scrape.interval"); v > 0 {
		cfg.ScrapeInterval = v
	}
	if v := viper.GetDuration("scrape.timeout"); v > 0 {
		cfg.ScrapeTimeout = v
	}
	if v := viper.GetString("web.path"); v != "" {
		cfg.WebPath = v
	}
	var configNodes []controller.NodeConfig
	if err := viper.UnmarshalKey("nodes", &configNodes); err == nil {
		cfg.Nodes = configNodes
	}
}

// parseNodes parses comma-separated node addresses
func parseNodes(s string) []controller.NodeConfig {
	var nodes []controller.NodeConfig
	parts := strings.Split(s, ",")
	for i, part := range parts {
		addr := strings.TrimSpace(part)
		if addr == "" {
			continue
		}
		nodes = append(nodes, controller.NodeConfig{
			Name:    fmt.Sprintf("node-%d", i+1),
			Address: addr,
		})
	}
	return nodes
}

// signalContext returns a context that cancels on SIGINT/SIGTERM
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

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

func parseCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
