package ring

import "testing"

func BenchmarkRingPush(b *testing.B) {
	r := New[int](1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Push(i)
	}
}
