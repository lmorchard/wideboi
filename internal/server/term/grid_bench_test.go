package term

import (
	"testing"
)

// BenchmarkGridScrollbackFull measures emulator write throughput and memory allocation
// when writing lines continuously to a terminal with a full 10,000-line scrollback buffer.
// Prior to Issue #205, vt.Scrollback.Push was O(scrollback) per line via slices.Delete memmove,
// and allocated on every line via slices.Clone.
func BenchmarkGridScrollbackFull(b *testing.B) {
	g := NewVT(80, 24)
	defer g.Close()

	// Fill scrollback buffer to capacity (10,000 lines + 24 screen lines)
	line := []byte("01234567890123456789012345678901234567890123456789012345678901234567890123456789\r\n")
	for i := 0; i < 10024; i++ {
		_, _ = g.Write(line)
	}

	if g.ScrollbackLen() < 10000 {
		b.Fatalf("expected scrollback to be full (10000), got %d", g.ScrollbackLen())
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = g.Write(line)
	}
}
