package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrTokenMalformed     = errors.New("token is malformed")
	ErrTokenSignature     = errors.New("token signature is invalid")
	ErrTokenExpired       = errors.New("token is expired")
	ErrTokenIssuer        = errors.New("token issuer mismatch")
	ErrTokenAudience      = errors.New("token audience mismatch")
	ErrTokenMissingSub    = errors.New("token subject is required")
	ErrTokenMissingRoles  = errors.New("token roles are required")
	ErrTokenMissingSecret = errors.New("token secret is required")
)

type TokenClaims struct {
	Subject     string   `json:"sub"`
	ActorType   string   `json:"actor_type"`
	Roles       []string `json:"roles"`
	Issuer      string   `json:"iss,omitempty"`
	Audience    string   `json:"aud,omitempty"`
	IssuedAt    int64    `json:"iat,omitempty"`
	ExpiresAt   int64    `json:"exp,omitempty"`
	CollectorID string   `json:"collector_id,omitempty"`
}

type VerifyOptions struct {
	Issuer   string
	Audience string
	Now      time.Time
	Method   AuthnMethod
	SourceIP string
}

var tokenEncoding = base64.RawURLEncoding

func SignToken(secret []byte, claims TokenClaims) (string, error) {
	if len(secret) == 0 {
		return "", ErrTokenMissingSecret
	}
	claims.Subject = strings.TrimSpace(claims.Subject)
	if claims.Subject == "" {
		return "", ErrTokenMissingSub
	}
	claims.Roles = normalizeClaimRoles(claims.Roles)
	if len(claims.Roles) == 0 {
		return "", ErrTokenMissingRoles
	}

	headerJSON, err := json.Marshal(map[string]string{
		"alg": "HS256",
		"typ": "SRE",
	})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := tokenEncoding.EncodeToString(headerJSON) + "." + tokenEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signingInput))
	signature := tokenEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature, nil
}

func VerifyToken(secret []byte, token string, opts VerifyOptions) (*Identity, error) {
	if len(secret) == 0 {
		return nil, ErrTokenMissingSecret
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, ErrTokenMalformed
	}

	signingInput := parts[0] + "." + parts[1]
	expectedMAC := hmac.New(sha256.New, secret)
	_, _ = expectedMAC.Write([]byte(signingInput))
	expectedSig := expectedMAC.Sum(nil)

	providedSig, err := tokenEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrTokenMalformed
	}
	if !hmac.Equal(providedSig, expectedSig) {
		return nil, ErrTokenSignature
	}

	payloadJSON, err := tokenEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrTokenMalformed
	}
	var claims TokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, ErrTokenMalformed
	}
	claims.Subject = strings.TrimSpace(claims.Subject)
	if claims.Subject == "" {
		return nil, ErrTokenMissingSub
	}
	roles := NormalizeRoles(claims.Roles)
	if len(roles) == 0 {
		return nil, ErrTokenMissingRoles
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if claims.ExpiresAt > 0 && now.After(time.Unix(claims.ExpiresAt, 0).UTC()) {
		return nil, ErrTokenExpired
	}
	if issuer := strings.TrimSpace(opts.Issuer); issuer != "" && strings.TrimSpace(claims.Issuer) != issuer {
		return nil, ErrTokenIssuer
	}
	if audience := strings.TrimSpace(opts.Audience); audience != "" && strings.TrimSpace(claims.Audience) != audience {
		return nil, ErrTokenAudience
	}

	id := &Identity{
		Subject:     claims.Subject,
		ActorType:   NormalizeActorType(claims.ActorType),
		Roles:       roles,
		AuthnMethod: opts.Method,
		SourceIP:    strings.TrimSpace(opts.SourceIP),
		Audience:    strings.TrimSpace(claims.Audience),
		CollectorID: strings.TrimSpace(claims.CollectorID),
	}
	if claims.IssuedAt > 0 {
		id.IssuedAt = time.Unix(claims.IssuedAt, 0).UTC()
	}
	if claims.ExpiresAt > 0 {
		id.ExpiresAt = time.Unix(claims.ExpiresAt, 0).UTC()
	}
	if id.ActorType == ActorTypeUnknown {
		id.ActorType = ActorTypeService
	}
	return id, nil
}

func normalizeClaimRoles(raw []string) []string {
	roles := NormalizeRoles(raw)
	if len(roles) == 0 {
		return nil
	}
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		out = append(out, string(role))
	}
	return out
}

func MintToken(secret []byte, subject string, actorType ActorType, roles []Role, issuer, audience, collectorID string, ttl time.Duration, now time.Time) (string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	roleStrings := make([]string, 0, len(roles))
	for _, role := range roles {
		if role == "" {
			continue
		}
		roleStrings = append(roleStrings, string(role))
	}
	claims := TokenClaims{
		Subject:     strings.TrimSpace(subject),
		ActorType:   string(actorType),
		Roles:       roleStrings,
		Issuer:      strings.TrimSpace(issuer),
		Audience:    strings.TrimSpace(audience),
		IssuedAt:    now.Unix(),
		CollectorID: strings.TrimSpace(collectorID),
	}
	if ttl > 0 {
		claims.ExpiresAt = now.Add(ttl).Unix()
	}
	token, err := SignToken(secret, claims)
	if err != nil {
		return "", fmt.Errorf("mint token: %w", err)
	}
	return token, nil
}
