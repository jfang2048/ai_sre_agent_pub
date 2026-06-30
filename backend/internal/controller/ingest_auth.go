package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/identity"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func (c *Controller) authenticateIngestStream(ctx context.Context) (*identity.Identity, error) {
	if c == nil || !c.auth.Enabled || !c.auth.IngestAuthEnabled {
		return nil, nil
	}
	token := bearerTokenFromIncomingContext(ctx)
	if token == "" {
		return nil, fmt.Errorf("ingest bearer token is required")
	}
	return c.verifyBearerActor(token, c.auth.IngestTokenAudience, identity.AuthnMethodIngestAuth, peerAddressFromContext(ctx))
}

func bearerTokenFromIncomingContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(strings.ToLower(value), "bearer ") {
			return strings.TrimSpace(value[7:])
		}
	}
	return ""
}

func peerAddressFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return strings.TrimSpace(p.Addr.String())
	}
	return ""
}
