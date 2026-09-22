package main

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// present writes the current frame to the host terminal, leaving the
// cursor where the frame asked for it.
//
// One Render and Flush is not enough. Flush positions the cursor with
// the renderer's MoveTo, but MoveTo only reaches the renderer's own
// buffer, which is not written out until the next Render (see
// docs/LESSONS.md). A single pass therefore shows the cursor on the last
// cell it drew -- with background cards busy, somewhere in one of those
// on every frame. The second pass draws nothing new; it only writes out
// the stranded cursor move.
//
// The first pass still ends by showing the cursor on that last cell, so
// both are bracketed in one synchronized update (mode 2026): a terminal
// that supports it paints the frame only once the corrected cursor has
// arrived, rather than possibly repainting in between. One that does not
// ignores the bracket. This is deliberately not SetSynchronizedUpdates,
// which would bracket each Flush on its own and leave the gap open.
func present(scr *uv.TerminalScreen) {
	_, _ = scr.WriteString(ansi.SetModeSynchronizedOutput)
	scr.Render()
	_ = scr.Flush()
	scr.Render()
	_, _ = scr.WriteString(ansi.ResetModeSynchronizedOutput)
	_ = scr.Flush()
}
