package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/services/aggregator"
	"go.uber.org/zap"
)

func main() {
	var (
		listenAddr    = flag.String("listen", ":8080", "Address to listen on")
		federationURL = flag.String("federation-url", "", "URL of global aggregator (for federation)")
		region        = flag.String("region", "unknown", "Region name for federation")
	)
	flag.Parse()

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// Create aggregator service
	svc := aggregator.New(aggregator.Config{
		ListenAddr:    *listenAddr,
		FederationURL: *federationURL,
		Region:        *region,
	}, logger)

	// Start service
	go func() {
		if err := svc.Start(); err != nil {
			logger.Fatal("failed to start aggregator", zap.Error(err))
		}
	}()

	// Wait for interrupt
	timeout := 5 * time.Second
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	svc.Stop(ctx)
}
