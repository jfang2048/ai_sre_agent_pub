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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inventory"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/observability"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var (
	// Overridden at build time via -ldflags.
	version   = "v0.7"
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

	listenAddrFlagSet bool
	grpcAddrFlagSet   bool
	webPathFlagSet    bool
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
		} else if fileExists("/etc/ai-sre-agent/controller.yaml") {
			configPath = "/etc/ai-sre-agent/controller.yaml"
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

	listenAddrFlagSet = cmd.Flags().Changed("listen")
	grpcAddrFlagSet = cmd.Flags().Changed("grpc-listen")
	webPathFlagSet = cmd.Flags().Changed("web-path")

	// Build configuration
	cfg, err := buildConfig(logger)
	if err != nil {
		logger.Fatal("failed to build controller config", zap.Error(err))
	}

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
	registerRuntimeConfigReload(ctx, ctrl, logger)

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
func buildConfig(logger *zap.Logger) (controller.Config, error) {
	cfg := controller.DefaultConfig()
	cfg.Version = version

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
	if listenAddrFlagSet {
		cfg.ListenAddr = listenAddr
	}
	if grpcAddrFlagSet {
		cfg.GRPCListenAddr = grpcAddr
	}
	if webPathFlagSet {
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
	if viper.IsSet("api") {
		if err := viper.UnmarshalKey("api", &cfg.API); err != nil {
			logger.Warn("failed to parse api config", zap.Error(err))
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
	if viper.IsSet("ingest") {
		if err := viper.UnmarshalKey("ingest", &cfg.Ingest); err != nil {
			logger.Warn("failed to parse ingest config", zap.Error(err))
		}
	}
	if viper.IsSet("tsdb") {
		if err := viper.UnmarshalKey("tsdb", &cfg.TSDB); err != nil {
			logger.Warn("failed to parse tsdb config", zap.Error(err))
		}
	}
	if viper.IsSet("orchestration") {
		if err := viper.UnmarshalKey("orchestration", &cfg.Orchestration); err != nil {
			logger.Warn("failed to parse orchestration config", zap.Error(err))
		}
	}
	if viper.IsSet("kubernetes") {
		if err := viper.UnmarshalKey("kubernetes", &cfg.Kubernetes); err != nil {
			logger.Warn("failed to parse kubernetes config", zap.Error(err))
		}
	}
	if viper.IsSet("inventory") {
		if err := viper.UnmarshalKey("inventory", &cfg.Inventory); err != nil {
			logger.Warn("failed to parse inventory config", zap.Error(err))
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
	if viper.IsSet("deployment") {
		if err := viper.UnmarshalKey("deployment", &cfg.Deployment); err != nil {
			logger.Warn("failed to parse deployment config", zap.Error(err))
		}
	}

	if viper.IsSet("incidents") {
		if err := viper.UnmarshalKey("incidents", &cfg.Incidents); err != nil {
			logger.Warn("failed to parse incidents config", zap.Error(err))
		}
	}
	if viper.IsSet("ha") {
		if err := viper.UnmarshalKey("ha", &cfg.HA); err != nil {
			logger.Warn("failed to parse ha config", zap.Error(err))
		}
	}

	// Lightweight env overrides (keep CLI minimal)
	if env, ok := os.LookupEnv("SRE_CONTROLLER_HTTP_LISTEN"); ok {
		cfg.ListenAddr = env
	}
	if env, ok := os.LookupEnv("SRE_CONTROLLER_GRPC_LISTEN"); ok {
		cfg.GRPCListenAddr = env
	}
	if env, ok := os.LookupEnv("SRE_CONTROLLER_WEB_PATH"); ok && env != "" {
		cfg.WebPath = env
	}
	if env := strings.TrimSpace(os.Getenv("SRE_CONTROLLER_DEPLOYMENT_MODE")); env != "" {
		cfg.Deployment.Mode = env
	} else if env := strings.TrimSpace(os.Getenv("SRE_DEPLOYMENT_MODE")); env != "" {
		cfg.Deployment.Mode = env
	}
	if env := strings.TrimSpace(os.Getenv("SRE_CONTROLLER_CLUSTER_NAME")); env != "" {
		cfg.Deployment.ClusterName = env
	} else if env := strings.TrimSpace(os.Getenv("SRE_CLUSTER_NAME")); env != "" {
		cfg.Deployment.ClusterName = env
	}
	if env := strings.TrimSpace(os.Getenv("SRE_CONTROLLER_DATA_ROOT")); env != "" {
		cfg.Deployment.DataRoot = env
	}
	if env := strings.TrimSpace(os.Getenv("SRE_CONTROLLER_EXTERNAL_URL")); env != "" {
		cfg.Deployment.ExternalURL = env
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_ENABLED"); env != "" {
		cfg.HA.Enabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_BACKEND"); env != "" {
		cfg.HA.Backend = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_MODE"); env != "" {
		cfg.HA.Mode = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_NODE_ID"); env != "" {
		cfg.HA.NodeID = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_ADVERTISE_HTTP"); env != "" {
		cfg.HA.AdvertiseHTTP = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_ADVERTISE_GRPC"); env != "" {
		cfg.HA.AdvertiseGRPC = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_ELECTION_KEY"); env != "" {
		cfg.HA.ElectionKey = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_LEASE_TTL"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.HA.LeaseTTL = v
		}
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_OBSERVE_INTERVAL"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.HA.ObserveInterval = v
		}
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_CAMPAIGN_TIMEOUT"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.HA.CampaignTimeout = v
		}
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_ALLOW_FOLLOWER_READ"); env != "" {
		cfg.HA.AllowFollowerRead = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_CONTROLLER_HA_ETCD_ENDPOINTS"); env != "" {
		cfg.HA.EtcdEndpoints = parseCSV(env)
	}
	if env := os.Getenv("SRE_CONTROLLER_API_RATE_LIMIT_ENABLED"); env != "" {
		cfg.API.RateLimitEnabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_CONTROLLER_API_RATE_LIMIT_RPS"); env != "" {
		if v, err := strconv.ParseFloat(strings.TrimSpace(env), 64); err == nil {
			cfg.API.RateLimitRPS = v
		}
	}
	if env := os.Getenv("SRE_CONTROLLER_API_RATE_LIMIT_BURST"); env != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(env)); err == nil {
			cfg.API.RateLimitBurst = v
		}
	}
	if env := os.Getenv("SRE_CONTROLLER_API_AUDIT_MUTATIONS"); env != "" {
		cfg.API.AuditMutations = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_INGEST_NODE_RETENTION"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Ingest.NodeRetention = v
		}
	}
	if env := os.Getenv("SRE_INGEST_HISTORY_SAMPLES_PER_NODE"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Ingest.HistorySamplesPerNode = v
		}
	}
	if env := os.Getenv("SRE_INGEST_MAX_NODES"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Ingest.MaxNodes = v
		}
	}
	if env := os.Getenv("SRE_INGEST_PERSIST_ENABLED"); env != "" {
		cfg.Ingest.Persistence.Enabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_INGEST_PERSIST_PATH"); env != "" {
		cfg.Ingest.Persistence.Path = env
	}
	if env := os.Getenv("SRE_INGEST_PERSIST_SYNC_INTERVAL"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Ingest.Persistence.SyncInterval = v
		}
	}
	if env := os.Getenv("SRE_INGEST_PERSIST_COMPACTION_INTERVAL"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Ingest.Persistence.CompactionInterval = v
		}
	}
	if env := os.Getenv("SRE_INGEST_PERSIST_MAX_DB_BYTES"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			cfg.Ingest.Persistence.MaxDBSizeBytes = v
		}
	}
	if env := os.Getenv("SRE_TSDB_ENABLED"); env != "" {
		cfg.TSDB.Enabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_TSDB_PROVIDER"); env != "" {
		cfg.TSDB.Provider = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_TSDB_URL"); env != "" {
		cfg.TSDB.URL = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_TSDB_ORG"); env != "" {
		cfg.TSDB.Org = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_TSDB_BUCKET"); env != "" {
		cfg.TSDB.Bucket = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_TSDB_TOKEN"); env != "" {
		cfg.TSDB.Token = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_TSDB_MEASUREMENT"); env != "" {
		cfg.TSDB.Measurement = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_TSDB_RETENTION"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.TSDB.Retention = v
		}
	}
	if env := os.Getenv("SRE_TSDB_WRITE_BATCH_SIZE"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.TSDB.WriteBatchSize = v
		}
	}
	if env := os.Getenv("SRE_TSDB_WRITE_QUEUE_SIZE"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.TSDB.WriteQueueSize = v
		}
	}
	if env := os.Getenv("SRE_TSDB_FLUSH_INTERVAL"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.TSDB.FlushInterval = v
		}
	}
	if env := os.Getenv("SRE_TSDB_QUERY_TIMEOUT"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.TSDB.QueryTimeout = v
		}
	}
	if env := os.Getenv("SRE_TSDB_FALLBACK_TO_MEMORY"); env != "" {
		cfg.TSDB.FallbackToMemory = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_TSDB_MANAGE_BUCKET"); env != "" {
		cfg.TSDB.ManageBucket = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_AGENT_RAG_ENABLED"); env != "" {
		cfg.Agent.RAGEnabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_AGENT_RAG_DATASET_PATH"); env != "" {
		cfg.Agent.RAGDatasetPath = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_INDEX_PATH"); env != "" {
		cfg.Agent.RAGIndexPath = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_SOURCE_PATHS"); env != "" {
		cfg.Agent.RAGSourcePaths = parseCSV(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_PATHS"); env != "" {
		cfg.Agent.RAGPaths = parseCSV(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_DOC_PATHS"); env != "" {
		cfg.Agent.RAGSourcePaths = parseCSV(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_TOP_K"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Agent.RAGTopK = v
		}
	}
	if env := os.Getenv("SRE_AGENT_RAG_MAX_SNIPPET_CHARS"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Agent.RAGMaxChars = v
		}
	}
	if env := os.Getenv("SRE_AGENT_RAG_MAX_CHARS"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Agent.RAGMaxChars = v
		}
	}
	if env := os.Getenv("SRE_AGENT_RAG_CHUNK_SIZE"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Agent.RAGChunkSize = v
		}
	}
	if env := os.Getenv("SRE_AGENT_RAG_CHUNK_OVERLAP"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Agent.RAGChunkOverlap = v
		}
	}
	if env := os.Getenv("SRE_AGENT_RAG_CHUNK_STRATEGY"); env != "" {
		cfg.Agent.RAGChunkStrategy = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_RETRIEVAL_MODE"); env != "" {
		cfg.Agent.RAGRetrievalMode = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_EMBEDDING_PROVIDER"); env != "" {
		cfg.Agent.RAGEmbeddingProvider = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_EMBEDDING_MODEL"); env != "" {
		cfg.Agent.RAGEmbeddingModel = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_EMBEDDING_BASE_URL"); env != "" {
		cfg.Agent.RAGEmbeddingBaseURL = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_EMBEDDING_API_KEY"); env != "" {
		cfg.Agent.RAGEmbeddingAPIKey = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_REBUILD_POLICY"); env != "" {
		cfg.Agent.RAGRebuildPolicy = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_VECTOR_BACKEND"); env != "" {
		cfg.Agent.RAGVectorBackend = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_VECTOR_ENDPOINT"); env != "" {
		cfg.Agent.RAGVectorEndpoint = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_VECTOR_COLLECTION"); env != "" {
		cfg.Agent.RAGVectorCollection = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_VECTOR_DATABASE"); env != "" {
		cfg.Agent.RAGVectorDatabase = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_VECTOR_TOKEN"); env != "" {
		cfg.Agent.RAGVectorToken = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_AGENT_RAG_VECTOR_TIMEOUT"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Agent.RAGVectorTimeout = v
		}
	}
	if env := os.Getenv("SRE_AGENT_LLM_ENABLED"); env != "" {
		cfg.Agent.LLMEnabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
		cfg.Analysis.LLMEnabled = cfg.Agent.LLMEnabled
	}
	if env := os.Getenv("SRE_ANALYSIS_ML_ANOMALY_ENABLED"); env != "" {
		cfg.Analysis.MLAnomalyDetection = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_ANALYSIS_ML_METHOD"); env != "" {
		cfg.Analysis.MLMethod = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_ANALYSIS_ML_SEASONAL_PERIOD"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Analysis.MLSeasonalPeriod = v
		}
	}
	if env := os.Getenv("SRE_ANALYSIS_ML_SCORE_THRESHOLD"); env != "" {
		if v, err := strconv.ParseFloat(env, 64); err == nil {
			cfg.Analysis.MLScoreThreshold = v
		}
	}
	if env := os.Getenv("SRE_ANALYSIS_CROSS_NODE_CORRELATION"); env != "" {
		cfg.Analysis.CrossNodeCorrelation = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_AGENT_LLM_MODEL"); env != "" {
		cfg.Analysis.LLMModel = env
	}
	if env := os.Getenv("SRE_AGENT_LLM_PROVIDER"); env != "" {
		cfg.Analysis.LLMProvider = env
	}
	if env := os.Getenv("SRE_ORCHESTRATION_ENABLED"); env != "" {
		cfg.Orchestration.Enabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_ORCHESTRATION_RECONCILE_INTERVAL"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Orchestration.ReconcileInterval = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_STALE_AFTER"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Orchestration.TelemetryStaleAfter = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_PEAK_PRESSURE"); env != "" {
		if v, err := strconv.ParseFloat(env, 64); err == nil {
			cfg.Orchestration.PeakPressureThreshold = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_QUEUE_MAX"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Orchestration.MaxQueueSize = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_SLO_BREACH_RATIO"); env != "" {
		if v, err := strconv.ParseFloat(env, 64); err == nil {
			cfg.Orchestration.SLOBreachRatio = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_SLO_BREACH_CONSECUTIVE"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Orchestration.SLOBreachConsecutive = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_AUTO_REMEDIATION_ENABLED"); env != "" {
		cfg.Orchestration.AutoRemediationEnabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_ORCHESTRATION_REMEDIATION_COOLDOWN"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Orchestration.RemediationCooldown = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_REMEDIATIONS_PER_RECONCILE"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Orchestration.MaxRemediationsPerReconcile = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_REMEDIATIONS_PER_WORKLOAD"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Orchestration.MaxRemediationsPerWorkload = v
		}
	}
	if env := os.Getenv("SRE_ORCHESTRATION_REMEDIATION_MIN_IMPROVEMENT"); env != "" {
		if v, err := strconv.ParseFloat(env, 64); err == nil {
			cfg.Orchestration.RemediationMinImprovement = v
		}
	}
	if env := os.Getenv("SRE_K8S_ENABLED"); env != "" {
		cfg.Kubernetes.Enabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_K8S_REFRESH_INTERVAL"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Kubernetes.RefreshInterval = v
		}
	}
	if env := os.Getenv("SRE_K8S_REQUEST_TIMEOUT"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Kubernetes.RequestTimeout = v
		}
	}
	if env := os.Getenv("SRE_INVENTORY_ENABLED"); env != "" {
		cfg.Inventory.Enabled = strings.EqualFold(env, "1") || strings.EqualFold(env, "true")
	}
	if env := os.Getenv("SRE_INVENTORY_HEARTBEAT_TTL"); env != "" {
		if v, err := time.ParseDuration(env); err == nil {
			cfg.Inventory.HeartbeatTTL = v
		}
	}
	if env := os.Getenv("SRE_INVENTORY_TARGETS_FILE"); env != "" {
		cfg.Inventory.TargetsFile = strings.TrimSpace(env)
	}
	if env := os.Getenv("SRE_CONTROLLER_TARGETS_FILE"); env != "" {
		cfg.Inventory.TargetsFile = strings.TrimSpace(env)
	}

	if cfg.Auth.APIKeyEnv != "" && os.Getenv(cfg.Auth.APIKeyEnv) != "" {
		cfg.Auth.Enabled = true
	}

	cfg.API = controller.NormalizeAPIConfigForRuntime(cfg.API)
	cfg = controller.ApplyDeploymentDefaults(cfg)

	if len(cfg.Nodes) == 0 {
		logger.Info("no scrape nodes configured, running in push-first mode")
	}

	if strings.TrimSpace(cfg.Inventory.TargetsFile) != "" {
		targets, err := inventory.LoadTargetsFile(cfg.Inventory.TargetsFile)
		if err != nil {
			return cfg, err
		}
		cfg.Inventory.StaticTargets = append([]inventory.StaticProbe(nil), targets...)
		if len(targets) > 0 {
			logger.Info("loaded controller target inventory",
				zap.String("path", cfg.Inventory.TargetsFile),
				zap.Int("targets", len(targets)))
		}
	}

	return cfg, nil
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

func registerRuntimeConfigReload(ctx context.Context, ctrl *controller.Controller, logger *zap.Logger) {
	if ctrl == nil {
		return
	}
	reload := func(source string) (controller.RuntimeConfigReloadReport, error) {
		cfg, err := buildConfig(logger)
		if err != nil {
			return controller.RuntimeConfigReloadReport{Source: source, Timestamp: time.Now().UTC()}, err
		}
		return ctrl.ApplyRuntimeConfigWithSource(cfg, source)
	}
	ctrl.SetRuntimeConfigReloader(func(_ context.Context, source string) (controller.RuntimeConfigReloadReport, error) {
		return reload(source)
	})
	startRuntimeConfigSignalLoop(ctx, logger, reload)
	startRuntimeConfigWatcher(ctx, configPath, logger, reload)
}

func startRuntimeConfigSignalLoop(ctx context.Context, logger *zap.Logger, reload func(string) (controller.RuntimeConfigReloadReport, error)) {
	if reload == nil {
		return
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	go func() {
		defer signal.Stop(sigCh)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				report, err := reload("signal:sighup")
				if err != nil {
					logger.Warn("controller runtime config reload failed", zap.Error(err))
					continue
				}
				logger.Info("controller runtime config reloaded",
					zap.Strings("applied", report.Applied),
					zap.Strings("restart_required", report.RestartRequired))
			}
		}
	}()
}

func startRuntimeConfigWatcher(ctx context.Context, path string, logger *zap.Logger, reload func(string) (controller.RuntimeConfigReloadReport, error)) {
	path = strings.TrimSpace(path)
	if path == "" || reload == nil {
		return
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		logger.Warn("failed to resolve config path for watcher", zap.String("path", path), zap.Error(err))
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warn("failed to create config watcher", zap.Error(err))
		return
	}
	dir := filepath.Dir(absPath)
	base := filepath.Base(absPath)
	if err := watcher.Add(dir); err != nil {
		logger.Warn("failed to watch controller config directory", zap.String("dir", dir), zap.Error(err))
		_ = watcher.Close()
		return
	}
	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) != base {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				report, err := reload("watch:" + base)
				if err != nil {
					logger.Warn("controller runtime config reload failed", zap.String("path", absPath), zap.Error(err))
					continue
				}
				logger.Info("controller runtime config reloaded from file",
					zap.String("path", absPath),
					zap.Strings("applied", report.Applied),
					zap.Strings("restart_required", report.RestartRequired))
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Warn("controller config watcher error", zap.String("path", absPath), zap.Error(err))
			}
		}
	}()
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
