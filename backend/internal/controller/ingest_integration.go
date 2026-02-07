package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

func (c *Controller) initIngest() {
	if c.ingestStore != nil {
		return
	}
	c.ingestStore = ingest.NewMemoryStore()

	var processors []ingest.Processor
	if c.config.GPU.Enabled {
		c.gpuStore = gpuobs.New(c.config.GPU)
		processors = append(processors, c.gpuStore)
	}

	c.ingestServer = ingest.NewServer(c.ingestStore, c.logger, processors...)
}

func (c *Controller) startIngest() error {
	if c.ingestServer == nil {
		c.initIngest()
	}

	if c.config.GRPCListenAddr == "" {
		c.logger.Info("ingest server disabled (grpc_listen empty)")
		return nil
	}

	listener, resolvedAddr, err := listenWithFallback(c.config.GRPCListenAddr, c.logger, "grpc")
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer()
	telemetryv1.RegisterTelemetryIngestServer(grpcServer, c.ingestServer)

	c.grpcServer = grpcServer
	c.grpcListener = listener
	c.actualGRPCAddr = resolvedAddr

	c.logger.Info("ingest server started", zap.String("listen", c.GRPCAddr()))

	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			c.logger.Error("ingest server stopped", zap.Error(err))
		}
	}()

	return nil
}

func (c *Controller) stopIngest() {
	if c.grpcServer != nil {
		c.grpcServer.GracefulStop()
	}
	if c.grpcListener != nil {
		_ = c.grpcListener.Close()
	}
}

func (c *Controller) registerIngestHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/fleet", c.withCORS(c.handleFleet))
	mux.HandleFunc("/api/v1/fleet/", c.withCORS(c.handleFleetNode))
}

func (c *Controller) handleFleet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if c.ingestStore == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"nodes": []interface{}{}})
		return
	}

	nodes := c.ingestStore.Snapshot()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"nodes":     nodes,
		"count":     len(nodes),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleFleetNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/fleet/")
	if id == "" {
		http.Error(w, "collector id required", http.StatusBadRequest)
		return
	}

	if c.ingestStore == nil {
		http.Error(w, "ingest disabled", http.StatusNotFound)
		return
	}

	node := c.ingestStore.Node(id)
	if node == nil {
		http.Error(w, "collector not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}
