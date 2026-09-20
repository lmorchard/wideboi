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
	"sync"
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

	ScrollbackLen() int
	ScrollOffset() int
	SetScrollOffset(offset int)

	// Resize changes the emulator's dimensions, reflowing the visible
	// screen so narrowing does not destroy text.
	Resize(cols, rows int)
	Draw(dst uv.Screen, area image.Rectangle)
	CellAt(x, y int) *uv.Cell
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
	scrollOffset  atomic.Int32

	// writeResizeMu serializes Write against Resize. SafeEmulator's
	// CellAt returns *uv.Cell aliasing its live buffer slot (uv.Line.At
	// is literally &l[x]), not a copy, and its own se.mu.RLock is
	// released the instant CellAt returns -- before Resize's caller ever
	// gets to read the pointer. Resize's read-out/reflow/write-back
	// walks a whole grid of these pointers across many such calls, so a
	// concurrent Write mutating a cell mid-walk (each individual SetCell
	// is itself locked, but the walk as a whole is not) is a real,
	// -race-confirmed data race, not just a theoretical one: reproduced
	// between vtGrid.Write and the Reflow pass reading a captured cell.
	// Write is what mutates the buffer; SendKey/SendText/Resize's own
	// dimension change go through se.mu for each call but never touch
	// cell content outside the walk this lock protects.
	//
	// Three methods hold it: Write, Resize, and Draw's scrollback
	// branch -- which holds it longest, for a whole frame's worth of
	// ScrollbackCellAt/CellAt pointers handed to dst.SetCell, plus the
	// scrollback-length and offset samples that decide where the
	// history/live boundary falls. Draw's fast path (offset 0) is
	// deliberately outside it.
	//
	// Read is deliberately excluded -- it can block indefinitely (the
	// same reason SafeEmulator's own Read is unlocked), and
	// Write/Resize/Draw all terminate on their own, so nothing here can
	// wedge against it.
	writeResizeMu sync.Mutex
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
		// x/vt hands OSC handlers the whole payload, command number
		// included -- "133;A", not "A". ansi.Parser.parseStringCmd
		// reads the leading digits into p.cmd without removing them
		// from p.data, which is why every one of vt's own OSC
		// handlers starts by splitting on ';' and reading parts[1]
		// (see handleTitle in x/vt's osc.go). Matching HasPrefix
		// against the raw payload is how this handler stayed dead
		// from the day it was written: no status glyph ever rendered
		// and VerbSmartJump never had a target.
		parts := strings.Split(string(data), ";")
		if len(parts) < 2 {
			return false
		}

		var st PaneStatus
		switch parts[1] {
		case "A", "B":
			// A is prompt-start, B is prompt-end, and a shell emits
			// both back to back on every prompt. Mapping B to Working
			// would clobber A microseconds later and leave an idle
			// shell reading as busy, so both mean "waiting on you".
			st = StatusNeedsInput
		case "C":
			st = StatusWorking
		case "D":
			// Bare "D" and "D;0" are success; any other exit-code
			// field is a failure. Split rather than match a ";0"
			// suffix: the payload may carry trailing key=value
			// fields, so "133;D;0;aid=1" is still a success.
			st = StatusDone
			if len(parts) > 2 && parts[2] != "" && parts[2] != "0" {
				st = StatusFailed
			}
		default:
			// Unrecognised. Let vt log it as unhandled, and leave
			// sawOSC133 clear -- the latch also disables the activity
			// fallback in Write and the idle timeout in Status, and
			// one malformed sequence should not switch those off
			// permanently.
			return false
		}

		g.status.Store(int32(st))
		g.sawOSC133.Store(true)
		return true
	})

	return g
}

// Write serializes against Resize via writeResizeMu; see that field's
// doc comment.
func (g *vtGrid) Write(p []byte) (int, error) {
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()
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
func (g *vtGrid) Read(p []byte) (int, error) { return g.em.Read(p) }

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
//
// Serializes against Write via writeResizeMu; see that field's doc
// comment. Not against Read: g.em.Resize can itself block writing an
// in-band resize notification into the emulator's own reply pipe, and
// only a concurrent, unlocked Read drains that pipe. Locking Read here
// too would make that drain wait on the very lock this call holds while
// blocked -- an unrecoverable deadlock, not a race.
func (g *vtGrid) Resize(cols, rows int) {
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()

	oldCols, oldRows := g.em.Width(), g.em.Height()
	if cols == oldCols && rows == oldRows {
		return
	}
	if g.em.IsAltScreen() {
		g.em.Resize(cols, rows)
		return
	}

	// Clone every captured cell. CellAt returns *uv.Cell aliasing the
	// emulator's live backing array (uv.Line.At is literally &l[x]), and
	// uv.Buffer.Resize narrows by reslicing in place
	// (Lines[i] = Lines[i][:width]), so those arrays outlive the resize
	// with the captured pointers still aliasing them. On a narrowing
	// reflow pushes content DOWN -- output row y is written from a
	// source row <= y -- so writing back through live pointers clobbers
	// rows that later output rows have yet to read, smearing the first
	// line over everything below it. Copying makes the capture a real
	// snapshot.
	before := make([]Row, oldRows)
	for y := 0; y < oldRows; y++ {
		r := make(Row, oldCols)
		for x := 0; x < oldCols; x++ {
			if c := g.em.CellAt(x, y); c != nil {
				cc := *c
				r[x] = &cc
			}
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

func (g *vtGrid) CellAt(x, y int) *uv.Cell { return g.em.CellAt(x, y) }

// CursorPosition reports the emulator's cursor, relative to this Grid's
// own origin. See the Grid.CursorPosition doc comment for the
// translation a multi-pane caller still owes it.
func (g *vtGrid) CursorPosition() image.Point {
	pos := g.em.CursorPosition()
	return image.Pt(pos.X, pos.Y)
}

func (g *vtGrid) CursorVisible() bool { return g.cursorVisible.Load() }

func (g *vtGrid) ScrollbackLen() int { return g.em.ScrollbackLen() }
func (g *vtGrid) ScrollOffset() int  { return int(g.scrollOffset.Load()) }

func (g *vtGrid) SetScrollOffset(offset int) {
	maxOffset := g.em.ScrollbackLen()
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	g.scrollOffset.Store(int32(offset))
}

// Draw's fast path (no scrollback in view) delegates to g.em.Draw, which
// runs entirely inside SafeEmulator's own se.mu.RLock and never leaks a
// cell pointer past it -- no extra locking needed. The scrollback branch
// below is different; see the comment where it takes writeResizeMu.
func (g *vtGrid) Draw(dst uv.Screen, area image.Rectangle) {
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()

	offset := int(g.scrollOffset.Load())
	sbLen := g.em.ScrollbackLen()
	if offset > sbLen {
		offset = sbLen
	}

	w, h := area.Dx(), area.Dy()
	for y := 0; y < h; y++ {
		sbY := (sbLen - offset) + y
		for x := 0; x < w; x++ {
			var cell *uv.Cell
			if sbY < sbLen && sbY >= 0 {
				cell = g.em.ScrollbackCellAt(x, sbY)
			} else {
				cell = g.em.CellAt(x, sbY-sbLen)
			}
			if cell != nil {
				dst.SetCell(area.Min.X+x, area.Min.Y+y, cell)
			}
		}
	}
}

// Close closes the underlying emulator, which unblocks any goroutine
// parked in Read.
//
// The upstream defect this works around is unchanged: SafeEmulator does
// not override (*vt.Emulator).Close, so calling it directly reaches the
// promoted method, which writes e.closed with no lock at all -- not even
// se.mu, which Write takes but Close never does. SafeEmulator.Read is
// unsynchronized too, and deliberately so: unlike every other method,
// it does not take se.mu, because Read can block indefinitely and a
// blocked holder of se.mu would freeze every other emulator call this
// pane needs. So Close's unsynchronized write and Read's unsynchronized
// read of the same field race by construction whenever Close runs in a
// different goroutine than the reader pump, which is every real call
// site here. Confirmed under -race at x/vt's emulator.go:264 (Close's
// write) vs. :251 (Read's read) -- not merely the Close-vs-Write pairing
// this comment used to describe.
//
// This call deliberately never reaches (*vt.Emulator).Close. Closing
// e.pw is one of two things that method does; the other is the e.closed
// write itself, which is what makes a subsequent Write short-circuit to
// (0, io.ErrClosedPipe) at emulator.go:269-271 instead of parsing the
// bytes. Bypassing Close preserves the first effect -- e.pw.Close()
// closes the same *io.PipeWriter Read drains from, documented safe for
// concurrent Read/Close (unlike Emulator's redundant e.closed flag on
// top of it), reached via InputPipe's io.Writer through the io.Closer it
// happens to satisfy, so Read still returns io.EOF exactly as it would
// have -- but not the second: e.closed stays false, so a Write arriving
// after this runs is no longer rejected early and instead parses its
// bytes as usual, potentially trying to reply into the now-closed pipe.
//
// That gap is inert for the one real caller. The pty-reader pump is the
// only thing that ever calls Write, and Pane.Close's pty.Kill runs
// before this, closing Master; the pump's blocking call is Master.Read,
// not Write, so it observes the read error and exits without ever
// reaching Write again. Even if some future caller managed one more
// Write here, a write into an already-closed io.Pipe returns
// io.ErrClosedPipe immediately rather than blocking, so it cannot wedge
// anything -- it would just be wasted parsing work on bytes nobody
// reads.
//
// Calling Grid.Close from anywhere the pump could still be mid-Read
// concurrently with something other than this bypass would reintroduce
// the race; this is the one caller, and it must stay that way.
func (g *vtGrid) Close() error {
	if closer, ok := g.em.InputPipe().(io.Closer); ok {
		return closer.Close()
	}
	// Fallback if InputPipe's concrete type ever stops being
	// *io.PipeWriter: not exercised today, and knowingly reintroduces
	// the race above rather than silently doing nothing.
	return g.em.Close()
}
