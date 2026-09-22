package term_test

import (
	"io"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/server/term"
)

// A child asks for mouse input with one of four DEC modes, and more
// than one can be set at once -- clearing one must not report the pane
// as untracked while another is still on.
func TestGridTracksChildMouseModes(t *testing.T) {
	g := term.NewVT(20, 5)
	step := func(seq string, want bool) {
		t.Helper()
		if _, err := io.WriteString(g, seq); err != nil {
			t.Fatalf("write %q: %v", seq, err)
		}
		if got := g.MouseTracking(); got != want {
			t.Errorf("after %q: MouseTracking = %v, want %v", seq, got, want)
		}
	}

	if g.MouseTracking() {
		t.Fatal("a fresh grid reports mouse tracking")
	}
	step("\x1b[?1006h", false) // an encoding, not a tracking mode
	step("\x1b[?1002h", true)
	step("\x1b[?1000h", true)
	step("\x1b[?1002l", true) // 1000 is still on
	step("\x1b[?1000l", false)
	step("\x1b[?9h", true)
	step("\x1b[?9l", false)
	step("\x1b[?1003h", true)
	step("\x1b[?1003l", false)
}

// vt encodes the event in whatever mode the child asked for. SGR here;
// coordinates go 0-based in, 1-based out.
func TestGridForwardsMouseInChildEncoding(t *testing.T) {
	g := term.NewVT(20, 5)
	_, _ = io.WriteString(g, "\x1b[?1002h\x1b[?1006h")

	got := drainOne(t, g, func() {
		g.SendMouse(uv.MouseClickEvent{X: 3, Y: 2, Button: uv.MouseLeft})
	})
	if want := "\x1b[<0;4;3M"; got != want {
		t.Errorf("forwarded %q, want %q", got, want)
	}
}
