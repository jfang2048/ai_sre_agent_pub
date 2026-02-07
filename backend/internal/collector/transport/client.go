package transport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/protobuf/proto"
)

// Client manages telemetry delivery to controller endpoints.
type Client struct {
	endpoints []string
	mirror    bool
	logger    *zap.Logger
	index     int
	compress  bool
	stats     clientStats
}

type clientStats struct {
	mu             sync.Mutex
	lastSendMs     float64
	lastAckMs      float64
	lastErrs       uint64
	lastEndpoint   string
	lastCompressed bool
}

// New creates a transport client.
func New(endpoints []string, mirror bool, compress bool, logger *zap.Logger) *Client {
	return &Client{endpoints: endpoints, mirror: mirror, compress: compress, logger: logger.With(zap.String("component", "transport"))}
}

// Send sends a payload to a controller.
func (c *Client) Send(ctx context.Context, payload []byte) (*telemetryv1.Ack, error) {
	if len(c.endpoints) == 0 {
		return nil, fmt.Errorf("no controller endpoints configured")
	}

	if c.mirror {
		var ack *telemetryv1.Ack
		for _, endpoint := range c.endpoints {
			resp, err := c.sendToEndpoint(ctx, endpoint, payload)
			if err != nil {
				c.logger.Warn("mirror send failed", zap.String("endpoint", endpoint), zap.Error(err))
				c.stats.bumpErr()
				continue
			}
			ack = resp
		}
		if ack == nil {
			return nil, fmt.Errorf("mirror send failed")
		}
		return ack, nil
	}

	endpoint := c.endpoints[c.index%len(c.endpoints)]
	c.index++

	resp, err := c.sendToEndpoint(ctx, endpoint, payload)
	if err == nil {
		return resp, nil
	}
	c.stats.bumpErr()

	for _, candidate := range c.endpoints {
		if candidate == endpoint {
			continue
		}
		resp, retryErr := c.sendToEndpoint(ctx, candidate, payload)
		if retryErr == nil {
			return resp, nil
		}
		c.stats.bumpErr()
	}

	c.stats.bumpErr()
	return nil, err
}

// Drain attempts to flush the spool.
func (c *Client) Drain(ctx context.Context, sp *spool.Spool, send func([]byte) (string, error)) error {
	for {
		payload, nextOffset, err := sp.Next()
		if err != nil {
			return err
		}
		if payload == nil {
			return nil
		}

		if _, err := send(payload); err != nil {
			return err
		}
		if err := sp.Commit(nextOffset); err != nil {
			return err
		}
	}
}

func (c *Client) sendToEndpoint(ctx context.Context, endpoint string, payload []byte) (*telemetryv1.Ack, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if c.compress {
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)))
	}

	conn, err := grpc.DialContext(ctx, endpoint, opts...)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := telemetryv1.NewTelemetryIngestClient(conn)
	stream, err := client.Push(ctx)
	if err != nil {
		return nil, err
	}

	batch := &telemetryv1.TelemetryBatch{}
	if err := proto.Unmarshal(payload, batch); err != nil {
		return nil, err
	}

	sendStart := time.Now()
	if err := stream.Send(batch); err != nil {
		return nil, err
	}
	sendElapsed := time.Since(sendStart)

	ackStart := time.Now()
	ack, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	ackElapsed := time.Since(ackStart)

	_ = stream.CloseSend()
	c.stats.update(endpoint, sendElapsed, ackElapsed, c.compress)
	return ack, nil
}

func (c *Client) LastSendMs() float64 {
	return c.stats.lastSendMs
}

func (c *Client) LastAckMs() float64 {
	return c.stats.lastAckMs
}

func (c *Client) LastErrs() uint64 {
	return c.stats.lastErrs
}

func (c *Client) LastEndpoint() string {
	return c.stats.lastEndpoint
}

func (c *Client) LastCompressed() bool {
	return c.stats.lastCompressed
}

func (s *clientStats) update(endpoint string, send time.Duration, ack time.Duration, compressed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEndpoint = endpoint
	s.lastSendMs = float64(send.Milliseconds())
	s.lastAckMs = float64(ack.Milliseconds())
	s.lastCompressed = compressed
}

func (s *clientStats) bumpErr() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErrs++
}
