package ingest

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func TestBoltInboxCommitRejectsOverflowedPayloadAccounting(t *testing.T) {
	ctx := context.Background()
	inbox := testInbox(t, InboxBackendBbolt, InboxConfig{MaxBytes: 1 << 20})
	boltStore := inbox.(*boltInbox)
	require.NoError(t, boltStore.db.Update(func(tx *bolt.Tx) error {
		return putInboxBytes(tx.Bucket(bucketInboxMeta), math.MaxUint64)
	}))

	_, created, err := inbox.Commit(ctx, testReceipt(t, 1))

	require.ErrorIs(t, err, ErrInboxFull)
	require.False(t, created)
}

func TestBoltInboxPruneRejectsPayloadAccountingUnderflow(t *testing.T) {
	ctx := context.Background()
	inbox := testInbox(t, InboxBackendBbolt, InboxConfig{})
	boltStore := inbox.(*boltInbox)
	receipt := testReceipt(t, 1)
	_, _, err := inbox.Commit(ctx, receipt)
	require.NoError(t, err)
	now := time.Now().UTC()
	_, claimed, err := inbox.Claim(ctx, receipt.Identity, "worker", now, time.Second)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, inbox.MarkApplied(ctx, receipt.Identity, "worker", now))
	require.NoError(t, boltStore.db.Update(func(tx *bolt.Tx) error {
		return putInboxBytes(tx.Bucket(bucketInboxMeta), 0)
	}))

	removed, err := inbox.Prune(ctx, now.Add(time.Second), 1)

	require.ErrorContains(t, err, "payload byte accounting underflow")
	require.Zero(t, removed)
	_, err = inbox.Get(ctx, receipt.Identity)
	require.NoError(t, err, "the failed prune transaction must retain the receipt")
}
