// Package safeconv provides explicit, saturating integer conversions for
// telemetry counters that can cross machine-sized integer boundaries.
package safeconv

import "math"

// Uint64ToInt converts value to int and saturates at the platform maximum.
func Uint64ToInt(value uint64) int {
	if value > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value) // #nosec G115 -- value is bounded above by math.MaxInt.
}

// Uint64ToInt64 converts value to int64 and saturates at math.MaxInt64.
func Uint64ToInt64(value uint64) int64 {
	if value > uint64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(value) // #nosec G115 -- value is bounded above by math.MaxInt64.
}

// Int64ToInt converts value to int and saturates at the platform limits.
func Int64ToInt(value int64) int {
	if value > int64(math.MaxInt) {
		return math.MaxInt
	}
	if value < int64(math.MinInt) {
		return math.MinInt
	}
	return int(value) // #nosec G115 -- value is bounded by the platform int limits.
}

// NonNegativeInt64ToUint64 converts a signed counter to uint64, clamping
// negative sentinel values to zero.
func NonNegativeInt64ToUint64(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value) // #nosec G115 -- negative values are rejected above.
}

// NonNegativeIntToUint64 converts a machine-sized counter to uint64,
// clamping negative sentinel values to zero.
func NonNegativeIntToUint64(value int) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

// IntToInt32 converts value to int32 and saturates at the int32 limits.
func IntToInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value) // #nosec G115 -- value is bounded by the int32 limits.
}

// AddUint64 adds two counters and saturates at math.MaxUint64.
func AddUint64(left, right uint64) uint64 {
	if left > math.MaxUint64-right {
		return math.MaxUint64
	}
	return left + right
}

// AddUint64ToInt adds a counter to an int and saturates instead of wrapping.
func AddUint64ToInt(base int, value uint64) int {
	if value == 0 {
		return base
	}
	if base >= math.MaxInt {
		return math.MaxInt
	}
	if base < 0 {
		converted := Uint64ToInt(value)
		if converted > math.MaxInt+base {
			return math.MaxInt
		}
		return base + converted
	}
	if value > uint64(math.MaxInt-base) {
		return math.MaxInt
	}
	return base + int(value) // #nosec G115 -- value is bounded by the remaining int capacity.
}

// AddInts adds two machine-sized counters and saturates instead of wrapping.
func AddInts(left, right int) int {
	if right > 0 && left > math.MaxInt-right {
		return math.MaxInt
	}
	if right < 0 && left < math.MinInt-right {
		return math.MinInt
	}
	return left + right
}

// MultiplyUint64 multiplies counters and saturates at math.MaxUint64.
func MultiplyUint64(left, right uint64) uint64 {
	if left == 0 || right == 0 {
		return 0
	}
	if left > math.MaxUint64/right {
		return math.MaxUint64
	}
	return left * right
}
