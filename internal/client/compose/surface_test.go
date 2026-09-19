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
