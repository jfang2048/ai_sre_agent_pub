package ai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/classifier"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ai/queue"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/proto/ai" // Generated proto package
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GRPCMLClient implements classifier.MLClient using gRPC
type GRPCMLClient struct {
	conn   *grpc.ClientConn
	client ai.AIServiceClient
	logger *zap.Logger
}

// ClientConfig holds configuration for the gRPC client
type ClientConfig struct {
	Address  string
	UseTLS   bool
	CertFile string
	KeyFile  string
	CAFile   string
}

// NewGRPCMLClient creates a new gRPC ML client
func NewGRPCMLClient(cfg ClientConfig, logger *zap.Logger) (*GRPCMLClient, error) {
	var opts []grpc.DialOption

	if cfg.UseTLS {
		// Load client cert
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client cert: %w", err)
		}

		// Load CA cert
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA cert: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA cert")
		}

		// Create TLS config
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      caCertPool,
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	opts = append(opts, grpc.WithBlock()) // Wait for connection
	opts = append(opts, grpc.WithTimeout(5*time.Second))

	conn, err := grpc.Dial(cfg.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ML service: %w", err)
	}

	return &GRPCMLClient{
		conn:   conn,
		client: ai.NewAIServiceClient(conn),
		logger: logger.With(zap.String("component", "ml_client")),
	}, nil
}

// Close closes the connection
func (c *GRPCMLClient) Close() error {
	return c.conn.Close()
}

// IsAvailable checks if the service is responsive
func (c *GRPCMLClient) IsAvailable(ctx context.Context) bool {
	_ = ctx
	// Use transport state as a low-overhead readiness signal.
	return c.conn.GetState().String() == "READY"
}

// Classify classifies a data point via gRPC
func (c *GRPCMLClient) Classify(ctx context.Context, data *queue.DataPoint) ([]classifier.Classification, error) {
	// Convert queue.DataPoint to proto.ClassifyRequest
	req := &ai.ClassifyRequest{
		NodeName: data.NodeName,
		Metrics:  make([]*ai.MetricData, len(data.Metrics)),
		Logs:     make([]*ai.LogEntry, len(data.Logs)),
	}

	for i, m := range data.Metrics {
		req.Metrics[i] = &ai.MetricData{
			Name:      m.Name,
			Value:     m.Value,
			Timestamp: toTimestamp(m.Timestamp),
			Labels:    m.Labels,
		}
	}

	for i, l := range data.Logs {
		req.Logs[i] = &ai.LogEntry{
			Message:   l.Message,
			Level:     l.Level,
			Timestamp: toTimestamp(l.Timestamp),
			Labels:    l.Labels,
		}
	}

	resp, err := c.client.ClassifyIssue(ctx, req)
	if err != nil {
		return nil, err
	}

	// Convert proto response to classifier.Classification
	var results []classifier.Classification
	for _, cls := range resp.Classifications {
		results = append(results, classifier.Classification{
			Category:       classifier.IssueCategory(cls.Category),
			Severity:       classifier.Severity(cls.Severity),
			Confidence:     cls.Confidence,
			Description:    cls.Description,
			Factors:        cls.Factors,
			RelatedMetrics: cls.RelatedMetrics,
			Method:         "ml",
			Timestamp:      time.Now(),
		})
	}

	return results, nil
}

func toTimestamp(ts time.Time) *timestamppb.Timestamp {
	if ts.IsZero() {
		return nil
	}
	return timestamppb.New(ts)
}
