package compose_test

import (
	"image"
	"strings"
	"testing"

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
	// their own dest rectangle. Verified against uv.Buffer.Draw directly:
	// see task-7-report.md for the trace.
	want := []string{"UUOOOU  "}
	assertLines(t, got, want)
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
