// Package main implements `collect`, a small system-metrics collector.
//
// It prints a point-in-time sample in either JSON or a simple text format.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/monitoring"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/monitoring/sources"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/utils"
	"go.uber.org/zap"
)

var (
	// Overridden at build time via -ldflags.
	version = "dev"
)

func main() {
	var (
		outputFormat   = flag.String("format", "json", "Output format: json|text")
		once           = flag.Bool("once", false, "Collect once and exit")
		scrapeInterval = flag.Duration("interval", 10*time.Second, "Scrape interval")
		showVersion    = flag.Bool("version", false, "Show version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "collect - system metrics collector\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  collect [flags]\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("collect %s\n", version)
		return
	}

	logger := utils.GetLogger()

	monitoringConfig := &monitoring.Config{
		ScrapeInterval: *scrapeInterval,
		Proc:           sources.ProcConfig{Enabled: true},
		Process: sources.ProcessConfig{
			Enabled:          true,
			ScanInterval:     15 * time.Second,
			EnablePerProcess: true,
			EnableOpenFiles:  true,
			EnableIO:         true,
			TopNProcesses:    50,
		},
		EBPF: sources.EBPFConfig{Enabled: false},
	}

	c, err := monitoring.NewCollector(monitoringConfig, logger)
	if err != nil {
		logger.Error("failed to create collector", zap.Error(err))
		os.Exit(1)
	}

	// Register sources.
	c.RegisterSource(sources.NewProcSource(monitoringConfig.Proc))
	if processSource, err := sources.NewProcessSource(monitoringConfig.Process, logger); err == nil {
		c.RegisterSource(processSource)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx); err != nil {
		logger.Error("failed to start collector", zap.Error(err))
		os.Exit(1)
	}
	defer c.Stop()

	if *once {
		metrics, err := c.CollectOnce(ctx)
		if err != nil {
			logger.Error("collection failed", zap.Error(err))
			os.Exit(1)
		}
		outputMetrics(metrics, *outputFormat)
		return
	}

	ticker := time.NewTicker(*scrapeInterval)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	logger.Info("collector started", zap.Duration("interval", *scrapeInterval))

	for {
		select {
		case <-ticker.C:
			metrics, err := c.CollectOnce(ctx)
			if err != nil {
				logger.Error("collection failed", zap.Error(err))
				continue
			}
			outputMetrics(metrics, *outputFormat)
		case sig := <-sigCh:
			logger.Info("received signal, shutting down", zap.String("signal", sig.String()))
			return
		}
	}
}

func outputMetrics(metrics []sources.Metric, format string) {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(metrics)
	case "text":
		for _, m := range metrics {
			fmt.Printf("%s = %f\n", m.Name, m.Value)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown format: %q\n", format)
		os.Exit(2)
	}
}
