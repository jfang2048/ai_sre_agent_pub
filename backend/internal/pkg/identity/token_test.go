package identity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMintAndVerifyToken(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	token, err := MintToken([]byte("secret"), "alice", ActorTypeUser, []Role{RoleOperator, RoleApprover}, "issuer-a", "controller-api", "", time.Hour, now)
	require.NoError(t, err)

	id, err := VerifyToken([]byte("secret"), token, VerifyOptions{
		Issuer:   "issuer-a",
		Audience: "controller-api",
		Now:      now.Add(30 * time.Minute),
		Method:   AuthnMethodBearer,
		SourceIP: "127.0.0.1",
	})
	require.NoError(t, err)
	require.Equal(t, "alice", id.Subject)
	require.Equal(t, ActorTypeUser, id.ActorType)
	require.Equal(t, []Role{RoleOperator, RoleApprover}, id.Roles)
	require.Equal(t, AuthnMethodBearer, id.AuthnMethod)
	require.Equal(t, "127.0.0.1", id.SourceIP)
}

func TestVerifyTokenRejectsWrongAudience(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	token, err := MintToken([]byte("secret"), "collector-a", ActorTypeService, []Role{RoleCollector}, "issuer-a", "controller-ingest", "collector-a", time.Hour, now)
	require.NoError(t, err)

	_, err = VerifyToken([]byte("secret"), token, VerifyOptions{
		Issuer:   "issuer-a",
		Audience: "controller-api",
		Now:      now,
		Method:   AuthnMethodIngestAuth,
	})
	require.ErrorIs(t, err, ErrTokenAudience)
}

func TestVerifyTokenRejectsExpiredToken(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	token, err := MintToken([]byte("secret"), "bob", ActorTypeUser, []Role{RoleViewer}, "issuer-a", "controller-api", "", time.Minute, now)
	require.NoError(t, err)

	_, err = VerifyToken([]byte("secret"), token, VerifyOptions{
		Issuer:   "issuer-a",
		Audience: "controller-api",
		Now:      now.Add(2 * time.Minute),
		Method:   AuthnMethodBearer,
	})
	require.ErrorIs(t, err, ErrTokenExpired)
}
