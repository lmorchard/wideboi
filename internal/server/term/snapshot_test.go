package term

import (
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/lmorchard/wideboi/internal/protocol"
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

	if snap.ScrollbackCount() != g1.ScrollbackLen() {
		t.Fatalf("snapshot scrollback len %d != %d", snap.ScrollbackCount(), g1.ScrollbackLen())
	}
	if snap.ScreenCount() != 24 {
		t.Fatalf("snapshot screen len %d != 24", snap.ScreenCount())
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

	// Verify cell contents, widths, and styles
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
			if !c1.Style.Equal(&c2.Style) {
				t.Fatalf("cell style mismatch at (%d, %d): %+v vs %+v", x, y, c1.Style, c2.Style)
			}
		}
	}
}

func TestGridSnapshotModesRoundTrip(t *testing.T) {
	g1 := NewVT(80, 24)
	defer g1.Close()

	// Enter alt-screen, enable bracketed paste, enable cursor key mode
	_, _ = g1.Write([]byte("\033[?1049h\033[?2004h\033[?1h"))
	_, _ = g1.Write([]byte("\033[1;1HAltScreen Mode Text"))

	snap := g1.(Snapshotter).ExportSnapshot()
	if snap == nil {
		t.Fatal("ExportSnapshot returned nil")
	}
	if !snap.IsAltScreen {
		t.Error("expected snap.IsAltScreen to be true")
	}
	if !snap.BracketedPaste {
		t.Error("expected snap.BracketedPaste to be true")
	}
	if !snap.CursorKeys {
		t.Error("expected snap.CursorKeys to be true")
	}

	g2 := NewVT(80, 24)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)

	snap2 := g2.(Snapshotter).ExportSnapshot()
	if !snap2.IsAltScreen {
		t.Error("expected restored grid to be in alt-screen")
	}
	if !snap2.BracketedPaste {
		t.Error("expected restored grid to have bracketed paste enabled")
	}
	if !snap2.CursorKeys {
		t.Error("expected restored grid to have cursor keys enabled")
	}

	// Verify the alt screen cell content was preserved
	cell := g2.CellAt(0, 0)
	if cell == nil || cell.Content != "A" {
		t.Fatalf("cell at (0, 0) content = %v, want 'A'", cell)
	}
}

// sendKeyBytes returns what the grid encodes for k. SendKey writes to an
// io.Pipe and blocks until it is read, so the read runs alongside it.
func sendKeyBytes(t *testing.T, g Grid, k uv.KeyEvent) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := g.Read(buf)
		got <- string(buf[:n])
	}()
	g.SendKey(k)
	select {
	case s := <-got:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timed out reading encoded key")
		return ""
	}
}

func TestGridSnapshotKeypadRoundTrip(t *testing.T) {
	kpEnter := uv.KeyPressEvent{Code: uv.KeyKpEnter}

	g1 := NewVT(80, 24)
	defer g1.Close()
	_, _ = g1.Write([]byte("\033="))

	snap := g1.(Snapshotter).ExportSnapshot()
	if !snap.KeypadApp {
		t.Fatal("expected snap.KeypadApp after ESC =")
	}

	g2 := NewVT(80, 24)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)
	if !g2.(Snapshotter).ExportSnapshot().KeypadApp {
		t.Error("expected restored grid to keep keypad application mode")
	}
	if got := sendKeyBytes(t, g2, kpEnter); got != "\033OM" {
		t.Errorf("restored keypad Enter = %q, want application sequence %q", got, "\033OM")
	}

	// ESC > leaves application mode, and a snapshot then carries none.
	_, _ = g1.Write([]byte("\033>"))
	snapOff := g1.(Snapshotter).ExportSnapshot()
	if snapOff.KeypadApp {
		t.Error("expected snap.KeypadApp false after ESC >")
	}
	g3 := NewVT(80, 24)
	defer g3.Close()
	g3.(Snapshotter).RestoreSnapshot(snapOff)
	if got := sendKeyBytes(t, g3, kpEnter); got == "\033OM" {
		t.Errorf("keypad Enter = %q in numeric mode, want the non-application encoding", got)
	}
}

func cellContent(g Grid, x, y int) string {
	c := g.CellAt(x, y)
	if c == nil {
		return ""
	}
	return c.Content
}

func TestGridSnapshotScrollRegionRoundTrip(t *testing.T) {
	g1 := NewVT(80, 24)
	defer g1.Close()
	// Rows outside the region (0-based 0 and 22) and one inside it (4).
	_, _ = g1.Write([]byte("\033[1;1HTOP\033[5;1HIN\033[23;1HBOT"))
	_, _ = g1.Write([]byte("\033[5;20r\033[10;3H"))

	snap := g1.(Snapshotter).ExportSnapshot()
	if snap.ScrollTop != 4 || snap.ScrollBottom != 20 {
		t.Fatalf("snap region = %d-%d, want 4-20", snap.ScrollTop, snap.ScrollBottom)
	}

	g2 := NewVT(80, 24)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)

	snap2 := g2.(Snapshotter).ExportSnapshot()
	if snap2.ScrollTop != 4 || snap2.ScrollBottom != 20 {
		t.Errorf("restored region = %d-%d, want 4-20", snap2.ScrollTop, snap2.ScrollBottom)
	}
	// DECSTBM homes the cursor; restore must put it back afterwards.
	if snap2.CursorX != 2 || snap2.CursorY != 9 {
		t.Errorf("restored cursor = (%d,%d), want (2,9)", snap2.CursorX, snap2.CursorY)
	}

	// Scrolling at the region's bottom moves only the rows inside it.
	_, _ = g2.Write([]byte("\033[20;1H" + strings.Repeat("\n", 30)))
	if got := cellContent(g2, 0, 0); got != "T" {
		t.Errorf("row 0 above the region = %q, want %q (untouched)", got, "T")
	}
	if got := cellContent(g2, 0, 22); got != "B" {
		t.Errorf("row 22 below the region = %q, want %q (untouched)", got, "B")
	}
	if got := cellContent(g2, 0, 4); got == "I" {
		t.Error("row 4 inside the region did not scroll")
	}

	// A full-screen region is the default and is not recorded.
	g3 := NewVT(80, 24)
	defer g3.Close()
	if s := g3.(Snapshotter).ExportSnapshot(); s.ScrollTop != 0 || s.ScrollBottom != 0 {
		t.Errorf("default region recorded as %d-%d, want none", s.ScrollTop, s.ScrollBottom)
	}
}

func TestGridSnapshotPenRoundTrip(t *testing.T) {
	g1 := NewVT(80, 24)
	defer g1.Close()
	_, _ = g1.Write([]byte("\033[1;31m"))

	snap := g1.(Snapshotter).ExportSnapshot()
	if snap.Pen == nil {
		t.Fatal("snap.Pen is nil after CSI 1;31m")
	}

	g2 := NewVT(80, 24)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)
	_, _ = g2.Write([]byte("X"))
	c := g2.CellAt(0, 0)
	if c == nil || c.Content != "X" {
		t.Fatalf("cell (0,0) = %v, want X", c)
	}
	if got := protocol.EncodeStyle(c.Style); got != *snap.Pen {
		t.Errorf("text after restore has style %+v, want the pre-upgrade pen %+v", got, *snap.Pen)
	}

	// A default pen is not recorded, and text after restore is unstyled.
	g3 := NewVT(80, 24)
	defer g3.Close()
	plain := g3.(Snapshotter).ExportSnapshot()
	if plain.Pen != nil {
		t.Fatalf("default pen recorded as %+v, want nil", *plain.Pen)
	}
	g4 := NewVT(80, 24)
	defer g4.Close()
	g4.(Snapshotter).RestoreSnapshot(plain)
	_, _ = g4.Write([]byte("Y"))
	if c := g4.CellAt(0, 0); c == nil || !c.Style.IsZero() {
		t.Errorf("text after restoring a default pen = %+v, want unstyled", c)
	}
}

// TestGridSnapshotRegionPenWithAltScreen restores alt screen, region
// and pen together: the later mode sequences must not reset the others.
func TestGridSnapshotRegionPenWithAltScreen(t *testing.T) {
	g1 := NewVT(80, 24)
	defer g1.Close()
	_, _ = g1.Write([]byte("\033[?1049h\033[?2004h\033=\033[3;12r\033[4m\033[6;7H"))

	snap := g1.(Snapshotter).ExportSnapshot()
	g2 := NewVT(80, 24)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)

	snap2 := g2.(Snapshotter).ExportSnapshot()
	if !snap2.IsAltScreen || !snap2.BracketedPaste || !snap2.KeypadApp {
		t.Errorf("modes after restore: alt=%v paste=%v keypad=%v, want all set", snap2.IsAltScreen, snap2.BracketedPaste, snap2.KeypadApp)
	}
	if snap2.ScrollTop != 2 || snap2.ScrollBottom != 12 {
		t.Errorf("region after restore = %d-%d, want 2-12", snap2.ScrollTop, snap2.ScrollBottom)
	}
	if snap2.Pen == nil || snap2.Pen.Underline == 0 {
		t.Errorf("pen after restore = %+v, want underline", snap2.Pen)
	}
	if snap2.CursorX != 6 || snap2.CursorY != 5 {
		t.Errorf("cursor after restore = (%d,%d), want (6,5)", snap2.CursorX, snap2.CursorY)
	}
}

func TestGridSnapshotCompactScrollbackAndStyles(t *testing.T) {
	g1 := NewVT(80, 24)
	defer g1.Close()

	// Produce diverse scrollback lines
	lines := []string{
		"\x1b[31;1mRed Bold Text\x1b[0m\r\n",
		"\x1b[42;30mGreen Bg Black Fg\x1b[0m\r\n",
		"Wide emoji 🚀 and characters 世界 in text\r\n",
		"Line with repeated spaces      and more\r\n",
		"\x1b[4mUnderlined text\x1b[0m\r\n",
	}
	// Write enough lines to push all into scrollback
	for _, l := range lines {
		_, _ = g1.Write([]byte(l))
	}
	for i := 0; i < 30; i++ {
		_, _ = g1.Write([]byte("Filler line\r\n"))
	}

	snap := g1.(Snapshotter).ExportSnapshot()
	if snap.ScrollbackCount() < 5 {
		t.Fatalf("expected >= 5 scrollback lines, got %d", snap.ScrollbackCount())
	}
	if len(snap.Styles) < 2 {
		t.Fatalf("expected style palette with multiple styles, got %d", len(snap.Styles))
	}

	g2 := NewVT(80, 24)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)

	if g2.ScrollbackLen() != g1.ScrollbackLen() {
		t.Fatalf("scrollback len mismatch: %d vs %d", g2.ScrollbackLen(), g1.ScrollbackLen())
	}

	// Verify all cells in scrollback match between g1 and g2
	vg1, vg2 := g1.(*vtGrid), g2.(*vtGrid)
	for y := 0; y < g1.ScrollbackLen(); y++ {
		for x := 0; x < 80; x++ {
			c1 := vg1.em.ScrollbackCellAt(x, y)
			c2 := vg2.em.ScrollbackCellAt(x, y)
			if c1 == nil && c2 == nil {
				continue
			}
			if (c1 == nil) != (c2 == nil) {
				t.Fatalf("scrollback nil mismatch at (%d, %d)", x, y)
			}
			if c1.Content != c2.Content {
				t.Fatalf("scrollback content mismatch at (%d, %d): %q vs %q", x, y, c1.Content, c2.Content)
			}
			if c1.Width != c2.Width {
				t.Fatalf("scrollback width mismatch at (%d, %d): %d vs %d", x, y, c1.Width, c2.Width)
			}
			if !c1.Style.Equal(&c2.Style) {
				t.Fatalf("scrollback style mismatch at (%d, %d): %+v vs %+v", x, y, c1.Style, c2.Style)
			}
		}
	}
}

func TestGridSnapshotLegacyRestore(t *testing.T) {
	// Construct a snapshot that only has legacy Scrollback and Screen (no compact lines or styles)
	legacySnap := &GridSnapshot{
		Cols:          80,
		Rows:          24,
		CursorX:       5,
		CursorY:       2,
		CursorVisible: true,
		Scrollback: []protocol.LineData{
			{
				{Content: "L", Width: 1, Style: protocol.StyleData{Underline: 1}},
				{Content: "1", Width: 1},
			},
		},
		Screen: []protocol.LineData{
			{
				{Content: "H", Width: 1},
				{Content: "i", Width: 1},
			},
		},
	}

	g := NewVT(80, 24)
	defer g.Close()
	g.(Snapshotter).RestoreSnapshot(legacySnap)

	if g.ScrollbackLen() != 1 {
		t.Fatalf("restored legacy scrollback len = %d, want 1", g.ScrollbackLen())
	}
	vg := g.(*vtGrid)
	c0 := vg.em.ScrollbackCellAt(0, 0)
	if c0 == nil || c0.Content != "L" || c0.Style.Underline != 1 {
		t.Fatalf("legacy scrollback cell 0 = %+v, want 'L' underlined", c0)
	}

	sc0 := g.CellAt(0, 0)
	if sc0 == nil || sc0.Content != "H" {
		t.Fatalf("legacy screen cell 0 = %+v, want 'H'", sc0)
	}
}

func TestGridSnapshotMultiRuneCells(t *testing.T) {
	g1 := NewVT(80, 24)
	defer g1.Close()

	// "e\u0301" is a base character + combining acute accent (2 runes in 1 cell)
	// Write "Cafe\u0301" followed by a multi-rune emoji
	_, _ = g1.Write([]byte("Cafe\u0301 👨‍👩‍👧‍👦 done\r\n"))

	snap := g1.(Snapshotter).ExportSnapshot()

	g2 := NewVT(80, 24)
	defer g2.Close()
	g2.(Snapshotter).RestoreSnapshot(snap)

	vg1 := g1.(*vtGrid)
	vg2 := g2.(*vtGrid)

	// Check row 0 cells
	for x := 0; x < 80; x++ {
		c1 := vg1.em.CellAt(x, 0)
		c2 := vg2.em.CellAt(x, 0)
		if c1 == nil && c2 == nil {
			continue
		}
		if (c1 == nil) != (c2 == nil) {
			t.Fatalf("cell %d nil mismatch: c1=%v, c2=%v", x, c1, c2)
		}
		if c1.Content != c2.Content {
			t.Fatalf("cell %d Content mismatch: got %q, want %q", x, c2.Content, c1.Content)
		}
		if c1.Width != c2.Width {
			t.Fatalf("cell %d Width mismatch: got %d, want %d", x, c2.Width, c1.Width)
		}
	}
}

func TestGridSnapshotClearsStaleScreenCells(t *testing.T) {
	g1 := NewVT(80, 24)
	defer g1.Close()
	_, _ = g1.Write([]byte("Short line"))
	snap := g1.(Snapshotter).ExportSnapshot()

	// g2 has long text on row 0 that should be cleared by restoring the short snapshot
	g2 := NewVT(80, 24)
	defer g2.Close()
	_, _ = g2.Write([]byte("This is a much longer line with stale trailing text that must be cleared"))

	g2.(Snapshotter).RestoreSnapshot(snap)

	for x := len("Short line"); x < 80; x++ {
		c := g2.CellAt(x, 0)
		if c != nil && !c.Equal(&uv.EmptyCell) && !c.IsZero() {
			t.Fatalf("cell at %d was not cleared: %+v", x, c)
		}
	}
}
