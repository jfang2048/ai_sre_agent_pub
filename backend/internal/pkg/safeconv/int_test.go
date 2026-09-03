package safeconv

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSaturatingIntegerConversions(t *testing.T) {
	require.Equal(t, 42, Uint64ToInt(42))
	require.Equal(t, math.MaxInt, Uint64ToInt(math.MaxUint64))
	require.Equal(t, int64(42), Uint64ToInt64(42))
	require.Equal(t, int64(math.MaxInt64), Uint64ToInt64(math.MaxUint64))
	require.Equal(t, 0, Int64ToInt(0))
	require.Equal(t, uint64(0), NonNegativeInt64ToUint64(-1))
	require.Equal(t, uint64(42), NonNegativeInt64ToUint64(42))
	require.Equal(t, uint64(0), NonNegativeIntToUint64(-1))
	require.Equal(t, uint64(42), NonNegativeIntToUint64(42))
	require.Equal(t, int32(math.MaxInt32), IntToInt32(math.MaxInt))
	require.Equal(t, int32(math.MinInt32), IntToInt32(math.MinInt))
}

func TestSaturatingCounterArithmetic(t *testing.T) {
	require.Equal(t, 42, AddUint64ToInt(40, 2))
	require.Equal(t, math.MaxInt, AddUint64ToInt(math.MaxInt-1, 2))
	require.Equal(t, 42, AddInts(40, 2))
	require.Equal(t, math.MaxInt, AddInts(math.MaxInt-1, 2))
	require.Equal(t, uint64(42), MultiplyUint64(6, 7))
	require.Equal(t, uint64(math.MaxUint64), MultiplyUint64(math.MaxUint64, 2))
	require.Equal(t, uint64(42), AddUint64(40, 2))
	require.Equal(t, uint64(math.MaxUint64), AddUint64(math.MaxUint64-1, 2))
}
