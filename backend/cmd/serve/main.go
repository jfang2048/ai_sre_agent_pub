// Package main implements `serve`, a minimal HTTP server exposing collected metrics.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
		addr           = flag.String("addr", ":8080", "HTTP listen address")
		scrapeInterval = flag.Duration("interval", 10*time.Second, "Scrape interval")
		showVersion    = flag.Bool("version", false, "Show version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "serve - metrics HTTP server\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  serve [flags]\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("serve %s\n", version)
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

	var (
		mu            sync.RWMutex
		latestMetrics []sources.Metric
	)

	go func() {
		ticker := time.NewTicker(*scrapeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				metrics, err := c.CollectOnce(ctx)
				if err != nil {
					continue
				}
				mu.Lock()
				latestMetrics = metrics
				mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.RLock()
		defer mu.RUnlock()
		_ = json.NewEncoder(w).Encode(latestMetrics)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"version": version})
	})

	server := &http.Server{
		Addr:    *addr,
		Handler: mux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("server started", zap.String("addr", *addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", zap.Error(err))
		}
	}()

	<-sigCh
	logger.Info("shutting down server")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}
