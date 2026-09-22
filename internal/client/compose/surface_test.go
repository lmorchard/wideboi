package compose_test

import (
	"image"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
)

func TestBlitPlacesSurfaceAtOffset(t *testing.T) {
	src := compose.NewSurface(5, 1)
	compose.WriteString(src, 0, 0, "hello")

	dst := compose.NewSurface(12, 3)
	compose.Blit(dst, src, image.Rect(3, 1, 8, 2))

	got := compose.Text(dst, dst.Bounds())
	want := []string{
		"            ",
		"   hello    ",
		"            ",
	}
	assertLines(t, got, want)
}

func TestBlitClipsToDestination(t *testing.T) {
	src := compose.NewSurface(10, 1)
	compose.WriteString(src, 0, 0, "abcdefghij")

	dst := compose.NewSurface(10, 1)
	// Only four columns of room.
	compose.Blit(dst, src, image.Rect(0, 0, 4, 1))

	got := compose.Text(dst, dst.Bounds())
	want := []string{"abcd      "}
	assertLines(t, got, want)
}

func TestBlitLaterDrawsCoverEarlierOnes(t *testing.T) {
	under := compose.NewSurface(6, 1)
	compose.WriteString(under, 0, 0, "UUUUUU")
	over := compose.NewSurface(3, 1)
	compose.WriteString(over, 0, 0, "OOO")

	dst := compose.NewSurface(8, 1)
	compose.Blit(dst, under, image.Rect(0, 0, 6, 1))
	compose.Blit(dst, over, image.Rect(2, 0, 5, 1))

	got := compose.Text(dst, dst.Bounds())
	// Column 5 is outside blit 2's dest rect (cols 2-4), so it keeps the
	// "U" written by blit 1 — later draws only cover the cells within
	// their own dest rectangle. Verified against uv.Buffer.Draw directly.
	want := []string{"UUOOOU  "}
	assertLines(t, got, want)
}

func TestWriteStyledPutsTheStyleOnEveryCellItWrites(t *testing.T) {
	s := compose.NewSurface(6, 1)
	compose.WriteStyled(s, 1, 0, "hi", uv.Style{Attrs: uv.AttrReverse})

	for _, x := range []int{1, 2} {
		if got := s.CellAt(x, 0).Style.Attrs; got&uv.AttrReverse == 0 {
			t.Errorf("cell %d: attrs %d, want AttrReverse set", x, got)
		}
	}
	// Cells outside the written range must be left alone, or the status
	// bar's inversion would bleed across the whole row.
	for _, x := range []int{0, 3} {
		if got := s.CellAt(x, 0).Style.Attrs; got != 0 {
			t.Errorf("cell %d was not written but carries attrs %d", x, got)
		}
	}
}

func TestWriteStyledStylesBlanks(t *testing.T) {
	// The inverted status bar is mostly spaces. uv.NewCell special-cases
	// " " by returning EmptyCell.Clone(), so a blank takes a different
	// path through the constructor than any other glyph -- the one place
	// a style could plausibly be dropped.
	s := compose.NewSurface(3, 1)
	compose.WriteStyled(s, 0, 0, "   ", uv.Style{Attrs: uv.AttrReverse})

	for x := 0; x < 3; x++ {
		if s.CellAt(x, 0).Style.Attrs&uv.AttrReverse == 0 {
			t.Errorf("blank cell %d lost its style", x)
		}
	}
}

func TestWriteStringStaysUnstyled(t *testing.T) {
	// WriteString delegates to WriteStyled. If it ever passes anything
	// but a zero style, every pane's chrome picks up an attribute.
	s := compose.NewSurface(3, 1)
	compose.WriteString(s, 0, 0, "ab")

	for x := 0; x < 2; x++ {
		if !s.CellAt(x, 0).Style.IsZero() {
			t.Errorf("cell %d picked up a style", x)
		}
	}
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\ngot:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n  got  %q\n  want %q", i, got[i], want[i])
		}
	}
}

// Chrome that renders a child-supplied title cannot assume
// single-width input, and truncating by rune count overflows the
// budget: a double-width glyph occupies two cells, so N runes can be
// up to 2N cells and the text spills into whatever is drawn beside it.
func TestTruncateWidthCountsCellsNotRunes(t *testing.T) {
	s := compose.NewSurface(20, 1)

	// 6 runes, 9 cells: three double-width then three single.
	const text = "日本語abc"

	got := compose.TruncateWidth(s, text, 4)
	if got != "日本" {
		t.Errorf("TruncateWidth(%q, 4) = %q, want %q (4 cells, not 4 runes)", text, got, "日本")
	}
}

// A budget that falls mid-glyph must drop the glyph rather than emit
// half of it: there is no such thing as half a cell of content, and
// the continuation cell would land under the next thing drawn.
func TestTruncateWidthNeverSplitsAWideGlyph(t *testing.T) {
	s := compose.NewSurface(20, 1)
	if got := compose.TruncateWidth(s, "日本", 3); got != "日" {
		t.Errorf("TruncateWidth(%q, 3) = %q, want %q", "日本", got, "日")
	}
	if got := compose.TruncateWidth(s, "日", 1); got != "" {
		t.Errorf("TruncateWidth(%q, 1) = %q, want empty", "日", got)
	}
}

func TestTruncateWidthPassesThroughWhatFits(t *testing.T) {
	s := compose.NewSurface(20, 1)
	if got := compose.TruncateWidth(s, "abc", 10); got != "abc" {
		t.Errorf("TruncateWidth(%q, 10) = %q, want it unchanged", "abc", got)
	}
	// Claude Code's real titles: the spinner glyphs measure one cell
	// each under WcWidth, so these are not a truncation hazard -- but
	// the budget still has to be respected.
	if got := compose.TruncateWidth(s, "◐ Pong reply", 6); got != "◐ Pong" {
		t.Errorf("TruncateWidth = %q, want %q", got, "◐ Pong")
	}
}

func TestTruncateWidthEmptyAndZeroBudget(t *testing.T) {
	s := compose.NewSurface(20, 1)
	if got := compose.TruncateWidth(s, "", 5); got != "" {
		t.Errorf("empty text gave %q", got)
	}
	if got := compose.TruncateWidth(s, "abc", 0); got != "" {
		t.Errorf("zero budget gave %q", got)
	}
	if got := compose.TruncateWidth(s, "abc", -3); got != "" {
		t.Errorf("negative budget gave %q", got)
	}
}

// The counterpart guard: writing is already width-aware, so a
// truncated title lands where its measured width says it should. This
// pins behaviour the doc comments used to deny.
func TestWriteStyledAdvancesByMeasuredWidth(t *testing.T) {
	s := compose.NewSurface(20, 1)
	compose.WriteStyled(s, 0, 0, "日X", uv.Style{})

	if c := s.CellAt(0, 0); c == nil || c.Content != "日" {
		t.Fatalf("col 0 = %v, want the wide glyph", c)
	}
	if c := s.CellAt(2, 0); c == nil || c.Content != "X" {
		t.Errorf("col 2 = %v, want \"X\" -- a wide glyph must advance two columns", c)
	}
}

// TruncateWidth and WriteStyled have to agree about what a rune costs,
// or a string that passes the budget check still overflows when drawn.
// WriteStyled advances at least one column per rune; truncation must
// charge the same, even for a zero-width combining mark.
func TestTruncateWidthMatchesWriterAdvance(t *testing.T) {
	s := compose.NewSurface(40, 1)
	// "e" + combining acute, twice, then padding. The marks measure
	// zero but each still consumes a column when written.
	const text = "ééabcdefgh"

	const budget = 6
	got := compose.TruncateWidth(s, text, budget)

	dst := compose.NewSurface(40, 1)
	compose.WriteStyled(dst, 0, 0, got, uv.Style{})
	for x := budget; x < 40; x++ {
		if c := dst.CellAt(x, 0); c != nil && c.Content != "" && c.Content != " " {
			t.Fatalf("truncated to %q for a %d-cell budget, but it wrote into column %d (%q)",
				got, budget, x, c.Content)
		}
	}
}

func TestStringWidthMeasuresCells(t *testing.T) {
	s := compose.NewSurface(40, 1)
	if got := compose.StringWidth(s, "abc"); got != 3 {
		t.Errorf("StringWidth(\"abc\") = %d, want 3", got)
	}
	if got := compose.StringWidth(s, "日本"); got != 4 {
		t.Errorf("StringWidth(\"日本\") = %d, want 4", got)
	}
}
