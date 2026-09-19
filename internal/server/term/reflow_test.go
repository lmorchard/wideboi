package term_test

import (
	"io"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/server/term"
)

// A line written at width 20, narrowed to 10, then widened back to 20
// must be recovered. Grid.Resize reflows the visible screen using term.Reflow
// so column-width cycling and window resizes are non-destructive.
func TestReflowRecoversTextAfterNarrowAndWiden(t *testing.T) {
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

// Narrowing must wrap the tail onto the next row rather than dropping it.
func TestNarrowWrapsRatherThanTruncates(t *testing.T) {
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

// Screen.Resize clears Touched, so a plain resize renders blank until new
// output arrives. Writing cells back re-touches them.
func TestResizeLeavesLinesDrawable(t *testing.T) {
	g := term.NewVT(20, 4)
	defer g.Close()
	io.WriteString(g, "hello")
	g.Resize(10, 4)

	s := compose.NewSurface(10, 4)
	g.Draw(s, s.Bounds())
	if got := compose.Text(s, s.Bounds())[0]; !strings.Contains(got, "hello") {
		t.Errorf("screen blank after resize: got %q", got)
	}
}

// Narrowing several populated rows at once must not smear the first row
// over the ones below it.
//
// Every other test in this package writes exactly ONE line of content,
// which is precisely the shape that hides Resize's capture aliasing the
// emulator's own backing array: uv.Line.At returns &l[x], and
// uv.Buffer.Resize narrows by reslicing in place, so a capture of bare
// *uv.Cell pointers still points at live memory after the resize. With
// one line the destination rows are blank, so writing back through stale
// pointers is harmless. With three, reflow pushes content down -- output
// row y is written from a source row <= y -- and the write-back clobbers
// rows that later rows still need to read. The first row wins every
// comparison and the screen reads "AAAAA" four times.
//
// The first row is deliberately exactly full width so narrowing makes it
// wrap into the row below, which is where the clobbering starts.
func TestNarrowPreservesRowsBelowTheFirst(t *testing.T) {
	g := term.NewVT(10, 4)
	defer g.Close()
	io.WriteString(g, "AAAAAAAAAA\r\nBBBBB\r\nCCCCC")

	g.Resize(5, 4)

	s := compose.NewSurface(5, 4)
	g.Draw(s, s.Bounds())
	got := compose.Text(s, s.Bounds())
	want := []string{"AAAAA", "AAAAA", "BBBBB", "CCCCC"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d after narrow 10->5:\n  got  %q\n  want %q\n  (full screen %q)", i, got[i], want[i], got)
		}
	}
}
