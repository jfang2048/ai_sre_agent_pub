package ring

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRingNilReceiver(t *testing.T) {
	var r *Ring[int]

	require.Equal(t, 0, r.Cap())
	require.Equal(t, 0, r.Len())
	require.Nil(t, r.SliceOldest())
	require.Nil(t, r.SliceLastN(5))

	r.Push(1)
	r.ForEachOldest(func(v int) {})
}

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

func TestRingPush(t *testing.T) {
	r := New[int](3)
	require.NotNil(t, r)

	r.Push(1)
	require.Equal(t, 1, r.Len())

	r.Push(2)
	require.Equal(t, 2, r.Len())

	r.Push(3)
	require.Equal(t, 3, r.Len())
	require.Equal(t, 3, r.Cap())

	r.Push(4)
	require.Equal(t, 3, r.Len())

	slice := r.SliceOldest()
	require.Equal(t, []int{2, 3, 4}, slice)
}

func TestRingOverwrite(t *testing.T) {
	r := New[int](3)

	r.Push(1)
	r.Push(2)
	r.Push(3)

	r.Push(4)
	r.Push(5)
	r.Push(6)

	slice := r.SliceOldest()
	require.Equal(t, []int{4, 5, 6}, slice)
}

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

func TestRingForEachOldestNilFunc(t *testing.T) {
	r := New[int](3)
	r.Push(1)
	r.Push(2)

	r.ForEachOldest(nil)
}

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

func TestRingSliceLastNAfterOverwrite(t *testing.T) {
	r := New[int](3)

	r.Push(1)
	r.Push(2)
	r.Push(3)
	r.Push(4)
	r.Push(5)

	// Ring now contains [3, 4, 5]
	result := r.SliceLastN(2)
	require.Equal(t, []int{4, 5}, result)
}

func TestRingSliceOldestAfterWrap(t *testing.T) {
	r := New[int](3)

	r.Push(1)
	r.Push(2)
	r.Push(3)

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

func TestRingStringTypes(t *testing.T) {
	r := New[string](3)

	r.Push("first")
	r.Push("second")
	r.Push("third")

	require.Equal(t, []string{"first", "second", "third"}, r.SliceOldest())

	r.Push("fourth")
	require.Equal(t, []string{"second", "third", "fourth"}, r.SliceOldest())
}

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

	require.Equal(t, 100, r.Len())
	require.Equal(t, 100, r.Cap())
}

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

	r.Push(6)
	require.Equal(t, 5, r.Len())
}

func TestRingCapAfterOperations(t *testing.T) {
	r := New[int](5)

	initialCap := r.Cap()
	require.Equal(t, 5, initialCap)

	for i := 0; i < 100; i++ {
		r.Push(i)
		require.Equal(t, initialCap, r.Cap())
	}
}
