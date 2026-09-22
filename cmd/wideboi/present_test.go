package main

import (
	"bytes"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Replays each presented frame through a real emulator and asks where
// its cursor was when the frame's synchronized update ended. The pty suites cannot make this assertion: a
// byte stream has no write boundaries, so "the end of a frame" is not
// observable there, and that is exactly the moment this is about.
func TestPresentLeavesCursorAtRequestedPosition(t *testing.T) {
	const cols, rows = 80, 24
	var out bytes.Buffer
	scr := uv.NewTerminalScreen(&out, []string{"TERM=xterm-256color"})
	scr.Resize(cols, rows)
	scr.EnterAltScreen()
	em := vt.NewEmulator(cols, rows)

	want := uv.Pos(2, 1)
	frame := func(name string, x, y int) {
		t.Helper()
		scr.SetCell(x, y, &uv.Cell{Content: "x", Width: 1})
		scr.SetCursorPosition(want.X, want.Y)
		scr.ShowCursor()
		present(scr)
		// Replayed only up to the end of the synchronized update: that
		// is when a terminal honouring mode 2026 paints, so the drawn
		// cell and the cursor move must both be inside the bracket.
		b := out.Bytes()
		begin := bytes.Index(b, []byte(ansi.SetModeSynchronizedOutput))
		end := bytes.LastIndex(b, []byte(ansi.ResetModeSynchronizedOutput))
		if begin < 0 || end < begin {
			t.Fatalf("%s: frame is not one synchronized update: %q", name, b)
		}
		if bytes.Contains(b[:begin], []byte("x")) {
			t.Errorf("%s: cells drawn before the synchronized update began: %q", name, b)
		}
		if _, err := em.Write(b[:end]); err != nil {
			t.Fatalf("%s: replay: %v", name, err)
		}
		if got := em.CursorPosition(); got != want {
			t.Errorf("%s: cursor at %v when the update ends, want %v", name, got, want)
		}
		if _, err := em.Write(b[end:]); err != nil {
			t.Fatalf("%s: replay: %v", name, err)
		}
		out.Reset()
	}

	// The focused pane draws near the cursor.
	frame("focused pane", 0, 1)
	// Then only background cards change, far from it, frame after
	// frame -- the case where the cursor used to be left behind on
	// whatever cell was written last.
	frame("background card", 60, 20)
	frame("another background card", 40, 10)
	frame("background card again", 70, 5)
}
