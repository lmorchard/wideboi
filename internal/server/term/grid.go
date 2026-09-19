// Package term wraps the VT emulator behind an interface wideboi owns.
//
// charmbracelet/x/vt has no tagged release. This interface is the seam
// that keeps an upstream break confined to one file, and the reason a
// different emulator could be substituted without touching callers.
package term

import (
	"image"
	"io"
	"strings"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Grid is one pane's terminal state.
//
// Write takes PTY output in. Read gives out encoded keystrokes and
// terminal replies, destined for the PTY. Routing input through the
// emulator keeps it the single authority on pane state, and is the only
// way to turn a decoded uv.KeyEvent back into the bytes a child expects.
//
// SendKey used to silently drop every shifted key. The pinned x/vt's own
// SendKey only special-cases Ctrl (control chars) and Alt (an ESC
// prefix) before falling back to a default case that requires
// k.Key().Mod == 0 — Shift never clears that, so
// KeyPressEvent{Code:'m', Text:"M", Mod:ModShift} (and every other
// capital letter or shifted punctuation key) matches nothing and emits
// zero bytes. x/vt's decoder already bakes Shift into Text (that is what
// makes it "M" instead of "m"), so vtGrid.SendKey instead routes any key
// event whose Text is set and whose modifiers are all ones already
// reflected in Text — only Shift, currently — through SendText, and
// everything else through x/vt's SendKey. Ctrl/Alt/Meta/Super/Hyper stay
// on the SendKey path because they change the encoding rather than the
// text: alt+l must become "\x1bl", not the bare "l" its Text carries.
// See the encodingMods comment in grid.go for the exact boundary.
//
// SendText is exported here, not merely used internally by SendKey,
// because it is the only correct route for printable input and a future
// caller (e.g. paste) should be able to reach it directly rather than
// spoofing a KeyEvent to get there.
//
// Close releases the emulator's own resources. Grid.Read blocks on an
// internal pipe with no other way to unblock it, so a caller that owns a
// Grid must call Close to let a pending Read return.
type Grid interface {
	io.Writer
	io.Reader
	SendKey(k uv.KeyEvent)
	SendText(text string)

	// CursorPosition reports the emulator's cursor in cell coordinates
	// relative to this Grid's own origin — (0,0) is the pane's top-left
	// corner, not the host screen's. A caller compositing multiple panes
	// must translate it by the pane's destination rect origin before
	// handing it to the host screen.
	CursorPosition() image.Point

	// CursorVisible reports whether the emulator's cursor is currently
	// shown. x/vt has no CursorVisible query of its own; this is tracked
	// from vt.Callbacks.CursorVisibility, which fires on cursor
	// visibility mode (DECTCEM) changes, and defaults to true because a
	// freshly started shell has not hidden it yet.
	CursorVisible() bool

	// Status reports the current agent/command status derived from OSC 133
	// sequences or output heuristics.
	Status() PaneStatus

	// Resize changes the emulator's dimensions, reflowing the visible
	// screen so narrowing does not destroy text.
	Resize(cols, rows int)
	Draw(dst uv.Screen, area image.Rectangle)
	Size() (cols, rows int)
	Close() error
}

// encodingMods are the modifiers that change how a key encodes as bytes
// rather than merely which text it carries. Ctrl and Alt are handled
// explicitly by x/vt's own SendKey (a control byte, and an ESC prefix,
// respectively); Meta, Super, and Hyper are not handled by it at all,
// which — same as Ctrl/Alt — means the raw Text must not be sent in
// their place, because that would silently discard the modifier. Shift
// is deliberately excluded: x/vt's decoder already bakes it into Text
// (shift+m arrives as Text:"M"), so a shifted printable key is exactly
// the case SendText exists to handle. ModCapsLock, ModNumLock, and
// ModScrollLock are lock states rather than encoding modifiers — a
// caps-locked letter likewise already has the right case in Text — so
// they are left out too.
//
// Checked against the pinned ultraviolet (key.go): ModShift, ModAlt,
// ModCtrl, ModMeta, ModHyper, ModSuper, ModCapsLock, ModNumLock, and
// ModScrollLock are the full set of KeyMod constants that exist. Nothing
// here is guessed.
const encodingMods = uv.ModCtrl | uv.ModAlt | uv.ModMeta | uv.ModSuper | uv.ModHyper

type PaneStatus int

const (
	StatusIdle PaneStatus = iota
	StatusWorking
	StatusNeedsInput
	StatusDone
	StatusFailed
)

func (s PaneStatus) Glyph() string {
	switch s {
	case StatusWorking:
		return "»"
	case StatusNeedsInput:
		return "!"
	case StatusDone:
		return "✓"
	case StatusFailed:
		return "✗"
	default:
		return " "
	}
}

// vtGrid adapts x/vt's SafeEmulator to Grid. SafeEmulator rather than
// Emulator because a pane's PTY reader goroutine writes to it while the
// compositor reads from it.
type vtGrid struct {
	em            *vt.SafeEmulator
	cursorVisible atomic.Bool
	status        atomic.Int32
	lastWriteTime atomic.Pointer[time.Time]
	sawOSC133     atomic.Bool
}

// NewVT returns a Grid backed by charmbracelet/x/vt.
func NewVT(cols, rows int) Grid {
	g := &vtGrid{em: vt.NewSafeEmulator(cols, rows)}
	g.cursorVisible.Store(true)
	g.status.Store(int32(StatusIdle))

	g.em.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { g.cursorVisible.Store(visible) },
	})

	g.em.RegisterOscHandler(133, func(data []byte) bool {
		g.sawOSC133.Store(true)
		s := string(data)
		switch {
		case strings.HasPrefix(s, "A"):
			g.status.Store(int32(StatusNeedsInput))
		case strings.HasPrefix(s, "B") || strings.HasPrefix(s, "C"):
			g.status.Store(int32(StatusWorking))
		case strings.HasPrefix(s, "D"):
			if strings.Contains(s, ";") && !strings.HasSuffix(s, ";0") {
				g.status.Store(int32(StatusFailed))
			} else {
				g.status.Store(int32(StatusDone))
			}
		}
		return true
	})

	return g
}

func (g *vtGrid) Write(p []byte) (int, error) {
	now := time.Now()
	g.lastWriteTime.Store(&now)
	if !g.sawOSC133.Load() {
		g.status.Store(int32(StatusWorking))
	}
	return g.em.Write(p)
}

func (g *vtGrid) Status() PaneStatus {
	st := PaneStatus(g.status.Load())
	if !g.sawOSC133.Load() && st == StatusWorking {
		if t := g.lastWriteTime.Load(); t != nil && time.Since(*t) > 3*time.Second {
			return StatusIdle
		}
	}
	return st
}
func (g *vtGrid) Read(p []byte) (int, error)  { return g.em.Read(p) }

// SendKey routes a shifted printable key through SendText instead of
// x/vt's own SendKey, which drops it. See the Grid.SendKey doc comment
// and encodingMods above for why.
func (g *vtGrid) SendKey(k uv.KeyEvent) {
	key := k.Key()
	if key.Text != "" && key.Mod&encodingMods == 0 {
		g.em.SendText(key.Text)
		return
	}
	g.em.SendKey(k)
}

func (g *vtGrid) SendText(text string) { g.em.SendText(text) }

// Resize changes the emulator's dimensions, reflowing the visible screen
// so narrowing does not destroy text.
//
// The pinned x/vt truncates on narrow (uv.Buffer.Resize does
// Lines[i][:width]) and its Draw paints only Touched lines while
// Screen.Resize clears Touched, so a plain resize both loses text and
// renders blank. Reading the cells out, reflowing, and writing them back
// with SetCell fixes both: SetCell re-touches every line it writes.
//
// Deliberately NOT handled:
//
//   - Scrollback. Measured: Resize leaves it untouched at its original
//     width, so there is nothing to repair. It will need reflowing for
//     DISPLAY when scroll-back navigation lands, which is a presentation
//     concern and non-destructive to defer.
//   - The alternate screen. A full-screen app repaints itself from its
//     own model on SIGWINCH, so reflowing it would be wasted work on
//     data the app is about to overwrite.
//   - The cursor. x/vt exposes no public setter, so it is left where the
//     resize put it; SIGWINCH makes most programs reposition themselves.
func (g *vtGrid) Resize(cols, rows int) {
	oldCols, oldRows := g.em.Width(), g.em.Height()
	if cols == oldCols && rows == oldRows {
		return
	}
	if g.em.IsAltScreen() {
		g.em.Resize(cols, rows)
		return
	}

	before := make([]Row, oldRows)
	for y := 0; y < oldRows; y++ {
		r := make(Row, oldCols)
		for x := 0; x < oldCols; x++ {
			r[x] = g.em.CellAt(x, y)
		}
		before[y] = r
	}

	g.em.Resize(cols, rows)

	after := Reflow(before, oldCols, cols)
	for y := 0; y < rows && y < len(after); y++ {
		x := 0
		for x < cols {
			c := after[y][x]
			if c == nil {
				g.em.SetCell(x, y, nil)
				x++
				continue
			}
			g.em.SetCell(x, y, c)
			// Never write a wide glyph's placeholder cell: it trips
			// uv.Line.Set's partial-overwrite protection and blanks the
			// whole glyph.
			if c.Width > 1 {
				x += c.Width
			} else {
				x++
			}
		}
	}
}
func (g *vtGrid) Size() (int, int) { return g.em.Width(), g.em.Height() }

// CursorPosition reports the emulator's cursor, relative to this Grid's
// own origin. See the Grid.CursorPosition doc comment for the
// translation a multi-pane caller still owes it.
func (g *vtGrid) CursorPosition() image.Point {
	pos := g.em.CursorPosition()
	return image.Pt(pos.X, pos.Y)
}

func (g *vtGrid) CursorVisible() bool { return g.cursorVisible.Load() }

func (g *vtGrid) Draw(dst uv.Screen, area image.Rectangle) {
	g.em.Draw(dst, area)
}

// Close closes the underlying emulator, which unblocks any goroutine
// parked in Read: SafeEmulator embeds *vt.Emulator, whose Close calls
// CloseWithError(io.EOF) on the pipe writer Read's pipe reader drains
// from, so the pending Read returns (0, io.EOF) rather than blocking
// forever.
//
// Known upstream data race, not ours to fix: SafeEmulator does not
// override Close, so this call reaches the promoted (*Emulator).Close
// directly, which writes e.closed with no se.mu held — while
// SafeEmulator.Write reads e.closed under that same lock. Reproduced
// under -race at x/vt's emulator.go:264 (Close's write) vs. :270
// (Write's read). Practically inert here: e.closed is a single bool,
// Close is its only writer, and a Write losing the race just sees
// io.ErrClosedPipe a moment later than it "should" — but -race will
// flag it for anyone who runs this path with the race detector.
func (g *vtGrid) Close() error { return g.em.Close() }
