package ring

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRingNilReceiver validates nil receiver behavior
func TestRingNilReceiver(t *testing.T) {
	var r *Ring[int]

	require.Equal(t, 0, r.Cap())
	require.Equal(t, 0, r.Len())
	require.Nil(t, r.SliceOldest())
	require.Nil(t, r.SliceLastN(5))

	// Should not panic
	r.Push(1)
	r.ForEachOldest(func(v int) {})
}

// TestRingNewCapacity validates New with various capacities
func TestRingNewCapacity(t *testing.T) {
	testCases := []struct {
		name     string
		capacity int
		expected int
	}{
		{"zero capacity", 0, 0},
		{"negative capacity", -5, 0},
		{"capacity 1", 1, 1},
		{"capacity 10", 10, 10},
		{"capacity 100", 100, 100},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := New[int](tc.capacity)
			if tc.expected == 0 {
				require.Nil(t, r)
			} else {
				require.NotNil(t, r)
				require.Equal(t, tc.expected, r.Cap())
				require.Equal(t, 0, r.Len())
			}
		})
	}
}

// TestRingPush validates Push operations
func TestRingPush(t *testing.T) {
	r := New[int](3)
	require.NotNil(t, r)

	// Push first element
	r.Push(1)
	require.Equal(t, 1, r.Len())

	// Push second element
	r.Push(2)
	require.Equal(t, 2, r.Len())

	// Push third element (fill buffer)
	r.Push(3)
	require.Equal(t, 3, r.Len())
	require.Equal(t, 3, r.Cap())

	// Push fourth element (overwrite oldest)
	r.Push(4)
	require.Equal(t, 3, r.Len()) // Size should stay at capacity

	// Verify order
	slice := r.SliceOldest()
	require.Equal(t, []int{2, 3, 4}, slice)
}

// TestRingOverwrite validates overwrite behavior when full
func TestRingOverwrite(t *testing.T) {
	r := New[int](3)

	// Fill the ring
	r.Push(1)
	r.Push(2)
	r.Push(3)

	// Overwrite multiple times
	r.Push(4)
	r.Push(5)
	r.Push(6)

	slice := r.SliceOldest()
	require.Equal(t, []int{4, 5, 6}, slice)
}

// TestRingForEachOldest validates ForEachOldest iteration
func TestRingForEachOldest(t *testing.T) {
	r := New[int](5)

	r.Push(10)
	r.Push(20)
	r.Push(30)
	r.Push(40)

	var result []int
	r.ForEachOldest(func(v int) {
		result = append(result, v)
	})

	require.Equal(t, []int{10, 20, 30, 40}, result)
}

// TestRingForEachOldestNilFunc validates ForEachOldest with nil function
func TestRingForEachOldestNilFunc(t *testing.T) {
	r := New[int](3)
	r.Push(1)
	r.Push(2)

	// Should not panic
	r.ForEachOldest(nil)
}

// TestRingSliceLastN validates SliceLastN behavior
func TestRingSliceLastN(t *testing.T) {
	r := New[int](10)

	for i := 1; i <= 10; i++ {
		r.Push(i)
	}

	testCases := []struct {
		name     string
		n        int
		expected []int
	}{
		{"last 0", 0, nil},
		{"last 1", 1, []int{10}},
		{"last 3", 3, []int{8, 9, 10}},
		{"last 5", 5, []int{6, 7, 8, 9, 10}},
		{"last all", 10, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"more than available", 15, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"negative", -1, nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := r.SliceLastN(tc.n)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestRingSliceLastNAfterOverwrite validates SliceLastN after wraparound
func TestRingSliceLastNAfterOverwrite(t *testing.T) {
	r := New[int](3)

	r.Push(1)
	r.Push(2)
	r.Push(3)
	r.Push(4) // Overwrites 1
	r.Push(5) // Overwrites 2

	// Ring now contains [3, 4, 5]
	result := r.SliceLastN(2)
	require.Equal(t, []int{4, 5}, result)
}

// TestRingSliceOldestAfterWrap validates SliceOldest after wraparound
func TestRingSliceOldestAfterWrap(t *testing.T) {
	r := New[int](3)

	r.Push(1)
	r.Push(2)
	r.Push(3)

	// Normal state
	require.Equal(t, []int{1, 2, 3}, r.SliceOldest())

	r.Push(4)
	// After wrap: [2, 3, 4]
	require.Equal(t, []int{2, 3, 4}, r.SliceOldest())

	r.Push(5)
	// After wrap: [3, 4, 5]
	require.Equal(t, []int{3, 4, 5}, r.SliceOldest())

	r.Push(6)
	// After wrap: [4, 5, 6]
	require.Equal(t, []int{4, 5, 6}, r.SliceOldest())
}

// TestRingEmptyRing validates empty ring behavior
func TestRingEmptyRing(t *testing.T) {
	r := New[int](5)

	require.Equal(t, 0, r.Len())
	require.Nil(t, r.SliceOldest())
	require.Nil(t, r.SliceLastN(1))

	var count int
	r.ForEachOldest(func(v int) {
		count++
	})
	require.Equal(t, 0, count)
}

// TestRingSingleElement validates single element ring
func TestRingSingleElement(t *testing.T) {
	r := New[int](1)

	r.Push(10)
	require.Equal(t, 1, r.Len())
	require.Equal(t, []int{10}, r.SliceOldest())
	require.Equal(t, []int{10}, r.SliceLastN(1))

	r.Push(20)
	require.Equal(t, 1, r.Len())
	require.Equal(t, []int{20}, r.SliceOldest())
}

// TestRingLargeCapacity validates large capacity ring
func TestRingLargeCapacity(t *testing.T) {
	r := New[int](1000)

	for i := 0; i < 1500; i++ {
		r.Push(i)
	}

	require.Equal(t, 1000, r.Len())

	slice := r.SliceOldest()
	require.Equal(t, 1000, len(slice))
	require.Equal(t, 500, slice[0])    // First element pushed that's still in ring
	require.Equal(t, 1499, slice[999]) // Last element pushed
}

// TestRingStringTypes validates ring with string types
func TestRingStringTypes(t *testing.T) {
	r := New[string](3)

	r.Push("first")
	r.Push("second")
	r.Push("third")

	require.Equal(t, []string{"first", "second", "third"}, r.SliceOldest())

	r.Push("fourth")
	require.Equal(t, []string{"second", "third", "fourth"}, r.SliceOldest())
}

// TestRingConcurrentPush validates concurrent Push operations
func TestRingConcurrentPush(t *testing.T) {
	r := New[int](100)

	const numGoroutines = 10
	const pushesPerGoroutine = 100

	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < pushesPerGoroutine; j++ {
				r.Push(id*pushesPerGoroutine + j)
			}
		}(i)
	}

	wg.Wait()

	require.Equal(t, 100, r.Len()) // At capacity
	require.Equal(t, 100, r.Cap())
}

// TestRingConcurrentRead validates concurrent reads
func TestRingConcurrentRead(t *testing.T) {
	r := New[int](100)

	for i := 0; i < 100; i++ {
		r.Push(i)
	}

	const numGoroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			slice := r.SliceOldest()
			require.Equal(t, 100, len(slice))
			lastN := r.SliceLastN(10)
			require.Equal(t, 10, len(lastN))
		}(i)
	}

	wg.Wait()
}

// TestRingMixedTypes validates ring with struct types
func TestRingMixedTypes(t *testing.T) {
	type Person struct {
		Name string
		Age  int
	}

	r := New[Person](3)

	r.Push(Person{Name: "Alice", Age: 30})
	r.Push(Person{Name: "Bob", Age: 25})
	r.Push(Person{Name: "Charlie", Age: 35})

	slice := r.SliceOldest()
	require.Equal(t, 3, len(slice))
	require.Equal(t, "Alice", slice[0].Name)
	require.Equal(t, "Charlie", slice[2].Name)
}

// TestRingPointerTypes validates ring with pointer types
func TestRingPointerTypes(t *testing.T) {
	r := New[*int](3)

	val1 := 100
	val2 := 200
	val3 := 300

	r.Push(&val1)
	r.Push(&val2)
	r.Push(&val3)

	slice := r.SliceOldest()
	require.Equal(t, 3, len(slice))
	require.Equal(t, 100, *slice[0])
	require.Equal(t, 300, *slice[2])
}

// TestRingLenAfterOperations validates Len after various operations
func TestRingLenAfterOperations(t *testing.T) {
	r := New[int](5)

	require.Equal(t, 0, r.Len())

	r.Push(1)
	require.Equal(t, 1, r.Len())

	r.Push(2)
	r.Push(3)
	r.Push(4)
	r.Push(5)
	require.Equal(t, 5, r.Len())

	r.Push(6)                    // Overwrites oldest
	require.Equal(t, 5, r.Len()) // Stays at capacity
}

// TestRingCapAfterOperations validates Cap remains constant
func TestRingCapAfterOperations(t *testing.T) {
	r := New[int](5)

	initialCap := r.Cap()
	require.Equal(t, 5, initialCap)

	for i := 0; i < 100; i++ {
		r.Push(i)
		require.Equal(t, initialCap, r.Cap())
	}
}
