package collector

import (
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	"github.com/stretchr/testify/require"
)

func TestSpoolEnqueueNextCommit(t *testing.T) {
	sp, err := spool.New(t.TempDir(), 1024)
	require.NoError(t, err)

	require.NoError(t, sp.Enqueue([]byte("first")))
	require.NoError(t, sp.Enqueue([]byte("second")))

	payload, nextOffset, err := sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("first"), payload)
	require.NoError(t, sp.Commit(nextOffset))

	payload, nextOffset, err = sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("second"), payload)
	require.NoError(t, sp.Commit(nextOffset))

	payload, _, err = sp.Next()
	require.NoError(t, err)
	require.Nil(t, payload)

	backlog, _ := sp.Stats()
	require.EqualValues(t, 0, backlog)
}

func TestSpoolRotatesWhenMaxSizeExceeded(t *testing.T) {
	sp, err := spool.New(t.TempDir(), 16)
	require.NoError(t, err)

	require.NoError(t, sp.Enqueue([]byte("01234567890123456789")))
	require.NoError(t, sp.Enqueue([]byte("tail")))

	payload, _, err := sp.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("tail"), payload)
}
