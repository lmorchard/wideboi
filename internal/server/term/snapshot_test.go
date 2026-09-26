package term

import (
	"testing"
	"time"
)

func TestGridSnapshotRoundTrip(t *testing.T) {
	g1 := NewVT(80, 24)
	defer g1.Close()

	// Write title, cwd, and enable mouse tracking (DEC 1002 and 1006)
	_, _ = g1.Write([]byte("\033]0;My Title\007\033]7;file:///tmp\007\033[?1002h\033[?1006h"))

	// Write some text, colors, and wide glyphs
	_, _ = g1.Write([]byte("Hello \033[31;1m世界\033[0m!\r\n"))
	// Fill more than 24 lines to trigger scrollback
	for i := 1; i <= 30; i++ {
		_, _ = g1.Write([]byte("Scroll line with emoji 🚀\r\n"))
	}
	_, _ = g1.Write([]byte("Bottom prompt> 世界 "))

	// Check that we have scrollback
	if g1.ScrollbackLen() == 0 {
		t.Fatalf("expected scrollback lines, got 0")
	}

	snap := g1.(Snapshotter).ExportSnapshot()
	if snap == nil {
		t.Fatalf("ExportSnapshot returned nil")
	}

	if len(snap.Scrollback) != g1.ScrollbackLen() {
		t.Fatalf("snapshot scrollback len %d != %d", len(snap.Scrollback), g1.ScrollbackLen())
	}
	if len(snap.Screen) != 24 {
		t.Fatalf("snapshot screen len %d != 24", len(snap.Screen))
	}

	// Now restore into a fresh grid
	g2 := NewVT(80, 24)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)

	if g2.ScrollbackLen() != g1.ScrollbackLen() {
		t.Errorf("restored scrollback len %d != %d", g2.ScrollbackLen(), g1.ScrollbackLen())
	}

	if g2.Title() != "My Title" {
		t.Errorf("restored title %q != %q", g2.Title(), "My Title")
	}
	if g2.CWD() != "/tmp" {
		t.Errorf("restored cwd %q != %q", g2.CWD(), "/tmp")
	}
	if !g2.MouseTracking() {
		t.Errorf("expected mouse tracking enabled on restored grid")
	}

	pos1 := g1.CursorPosition()
	// Allow a tiny window for emulator to settle
	time.Sleep(10 * time.Millisecond)
	pos2 := g2.CursorPosition()
	if pos1 != pos2 {
		t.Errorf("cursor position mismatch: g1=%v, g2=%v", pos1, pos2)
	}

	// Verify cell contents and widths (especially wide glyphs)
	for y := 0; y < 24; y++ {
		for x := 0; x < 80; x++ {
			c1 := g1.CellAt(x, y)
			c2 := g2.CellAt(x, y)
			if c1 == nil && c2 == nil {
				continue
			}
			if (c1 == nil) != (c2 == nil) {
				t.Fatalf("cell nil mismatch at (%d, %d)", x, y)
			}
			if c1.Content != c2.Content {
				t.Fatalf("cell content mismatch at (%d, %d): %q vs %q", x, y, c1.Content, c2.Content)
			}
			if c1.Width != c2.Width {
				t.Fatalf("cell width mismatch at (%d, %d): %d vs %d", x, y, c1.Width, c2.Width)
			}
		}
	}
}
