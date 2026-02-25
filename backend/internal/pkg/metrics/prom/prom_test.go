package prom

import "testing"

func BenchmarkSanitizeMetricName(b *testing.B) {
	in := "system.cpu.usage-percent"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeMetricName(in)
	}
}

func BenchmarkSanitizeLabelKey(b *testing.B) {
	in := "gpu-id"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeLabelKey(in)
	}
}
