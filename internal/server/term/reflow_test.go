package term_test

import (
	"io"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/term"
)

// A line written at width 20, narrowed to 10, then widened back to 20
// should be recoverable. If the emulator truncates instead of reflowing,
// the tail is gone forever and column-width cycling silently eats text.
//
// Observed: x/vt does not reflow. Emulator.Resize -> Screen.Resize ->
// uv.Buffer.Resize truncates each line's cell slice to the new width on
// narrow (verified by reading em.CellAt directly, bypassing Draw): after
// Resize(10,4) row 0 holds exactly "abcdefghij", the rest is gone. On
// widening back to 20, Buffer.Resize appends fresh blank cells rather
// than restoring anything, so row 0 becomes "abcdefghij" + 10 spaces —
// "klmnopqrst" is unrecoverable. This is the same defect that forced
// gwae's ADR-004 emulator swap.
//
// A second, compounding effect showed up going through this package's
// own Grid.Draw/compose.Text path (the interfaces production code
// actually uses): Emulator.Draw only paints lines returned by
// e.Touched(), and Screen.Resize sets buf.Touched = nil on every resize
// instead of marking all lines dirty. So immediately after ANY Resize,
// Draw onto a fresh surface renders entirely blank cells (not even the
// truncated prefix), until further writes touch a line. That is why
// compose.Text below reports an all-space screen rather than a
// half-populated one -- the underlying data loss (truncation) is real
// and independently confirmed via direct CellAt access; the Touched
// reset just also erases the *visible* prefix until the next write.
func TestReflowRecoversTextAfterNarrowAndWiden(t *testing.T) {
	t.Skip("x/vt does not reflow; see spec Open Questions")
	const line = "abcdefghijklmnopqrst" // exactly 20 columns

	g := term.NewVT(20, 4)
	io.WriteString(g, line)

	g.Resize(10, 4)
	g.Resize(20, 4)

	s := compose.NewSurface(20, 4)
	g.Draw(s, s.Bounds())
	got := strings.Join(compose.Text(s, s.Bounds()), "")

	if !strings.Contains(got, line) {
		t.Errorf("text not recovered after narrow+widen.\n  want to find: %q\n  got screen:   %q", line, got)
	}
}

// Narrowing should wrap the tail onto the next row rather than dropping it.
//
// Observed: it does not wrap. See the comment on
// TestReflowRecoversTextAfterNarrowAndWiden for the confirmed mechanism
// (Buffer.Resize truncates line slices to the new width) and the
// compounding Touched-reset effect that also blanks the visible prefix
// through this package's Draw path.
func TestNarrowWrapsRatherThanTruncates(t *testing.T) {
	t.Skip("x/vt does not reflow; see spec Open Questions")
	const line = "abcdefghijklmnopqrst"

	g := term.NewVT(20, 4)
	io.WriteString(g, line)
	g.Resize(10, 4)

	s := compose.NewSurface(10, 4)
	g.Draw(s, s.Bounds())
	got := strings.Join(compose.Text(s, s.Bounds()), "")

	if !strings.Contains(got, "klmnopqrst") {
		t.Errorf("tail lost on narrow.\n  want to find: %q\n  got screen:   %q", "klmnopqrst", got)
	}
}
