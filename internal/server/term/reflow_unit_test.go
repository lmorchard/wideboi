package term

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// rowsFrom builds Rows from strings, padding each to width with nil.
func rowsFrom(width int, lines ...string) []Row {
	out := make([]Row, len(lines))
	for i, s := range lines {
		r := make(Row, width)
		for x, ch := range []rune(s) {
			if x >= width {
				break
			}
			if ch != ' ' {
				r[x] = &uv.Cell{Content: string(ch), Width: 1}
			}
		}
		out[i] = r
	}
	return out
}

func render(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		var b strings.Builder
		for _, c := range r {
			if c == nil || c.Content == "" {
				b.WriteString(" ")
			} else {
				b.WriteString(c.Content)
			}
		}
		out[i] = strings.TrimRight(b.String(), " ")
	}
	return out
}

func assertRows(t *testing.T, got []Row, want []string) {
	t.Helper()
	g := render(got)
	if len(g) != len(want) {
		t.Fatalf("got %d rows, want %d:\n  got  %q\n  want %q", len(g), len(want), g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Errorf("row %d:\n  got  %q\n  want %q", i, g[i], want[i])
		}
	}
}

func TestReflowNarrowWrapsRatherThanTruncating(t *testing.T) {
	in := rowsFrom(10, "abcdefghij", "klmno", "", "")
	got := Reflow(in, 10, 5)
	assertRows(t, got, []string{"abcde", "fghij", "klmno", ""})
}

func TestReflowWidenRejoins(t *testing.T) {
	in := rowsFrom(5, "abcde", "fghij", "klmno", "")
	got := Reflow(in, 5, 10)
	assertRows(t, got, []string{"abcdefghij", "klmno", "", ""})
}

func TestReflowRoundTripRecoversText(t *testing.T) {
	in := rowsFrom(20, "abcdefghijklmnopqrst", "", "", "")
	got := Reflow(Reflow(in, 20, 10), 10, 20)
	assertRows(t, got, []string{"abcdefghijklmnopqrst", "", "", ""})
}

// The regression for the spike's worst finding: a blank row between
// content must not be priced as a full-width logical line, and live
// content must not be pushed off the top.
func TestReflowDoesNotEvictLiveContentViaBlankRows(t *testing.T) {
	in := rowsFrom(20, "hello", "", "world", "")
	got := Reflow(in, 20, 10)
	assertRows(t, got, []string{"hello", "", "world", ""})
}

func TestReflowKeepsShortLinesIntact(t *testing.T) {
	in := rowsFrom(20, "one", "two", "three", "")
	got := Reflow(in, 20, 10)
	assertRows(t, got, []string{"one", "two", "three", ""})
}

// A hard break landing at exactly the old width is indistinguishable
// from a soft wrap without metadata the library does not keep. This
// test PINS the accepted wrong answer so a future change is a decision
// rather than an accident.
func TestReflowJoinsHardBreakAtExactWidth_AcceptedLimitation(t *testing.T) {
	in := rowsFrom(5, "abcde", "fgh", "", "")
	got := Reflow(in, 5, 10)
	assertRows(t, got, []string{"abcdefgh", "", "", ""})
}

// Content that no longer fits scrolls off the TOP, as a terminal does.
func TestReflowOverflowDropsFromTheTop(t *testing.T) {
	in := rowsFrom(10, "aaaaaaaaaa", "bbbbbbbbbb", "cc", "")
	got := Reflow(in, 10, 5)
	if len(got) != 4 {
		t.Fatalf("got %d rows, want 4", len(got))
	}
	last := render(got)[3]
	if last != "cc" {
		t.Errorf("bottom row = %q, want %q -- overflow must drop from the top", last, "cc")
	}
}

func TestReflowPreservesStyleAndWideGlyphs(t *testing.T) {
	in := make([]Row, 2)
	in[0] = make(Row, 6)
	in[0][0] = &uv.Cell{Content: "中", Width: 2}
	in[0][2] = &uv.Cell{Content: "x", Width: 1, Style: uv.Style{Attrs: uv.AttrBold}}
	in[1] = make(Row, 6)

	got := Reflow(in, 6, 8)
	if got[0][0] == nil || got[0][0].Content != "中" || got[0][0].Width != 2 {
		t.Errorf("wide glyph not preserved: %+v", got[0][0])
	}
	if got[0][2] == nil || got[0][2].Style.Attrs != uv.AttrBold {
		t.Errorf("style not preserved: %+v", got[0][2])
	}
}
