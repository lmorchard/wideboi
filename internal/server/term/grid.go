// Package term wraps the VT emulator behind an interface wideboi owns.
//
// charmbracelet/x/vt has no tagged release. This interface is the seam
// that keeps an upstream break confined to one file, and the reason a
// different emulator could be substituted without touching callers.
package term

import (
	"encoding/base64"
	"fmt"
	"image"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/lmorchard/wideboi/internal/protocol"
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

	// MouseTracking reports whether the child has asked for mouse
	// events -- any of DEC 9, 1000, 1002 or 1003 set. Tracked from vt's
	// mode callbacks, the same way CursorVisible is.
	MouseTracking() bool

	// SendMouse encodes a mouse event in the child's requested mode and
	// writes it to the child. Like SendKey it writes to an io.Pipe and
	// blocks until something reads.
	SendMouse(m uv.MouseEvent)

	// Title reports the pane's terminal title, or "" if the child has
	// never set one. OSC 0/1/2; x/vt parses it either way, this just
	// surfaces what was already being discarded.
	Title() string

	// CWD reports the pane's current working directory if announced via
	// OSC 7, or "" if not set.
	CWD() string

	// UserVars returns a snapshot of agent or child metadata set via
	// OSC 1337 SetUserVar.
	UserVars() map[string]string

	// Status reports the current agent/command status derived from OSC 133
	// sequences or output heuristics.
	Status() protocol.PaneStatus

	ScrollbackLen() int
	HistoryRows() (int, []string)
	ScrollOffset() int
	SetScrollOffset(offset int)

	// Generation advances whenever anything a pane update carries may
	// have changed: cells, cursor position or visibility, mouse modes,
	// size, or scroll offset. Only inequality is meaningful. The server
	// reads it *before* rendering, so a change racing the render errs
	// toward one update too many, never one too few.
	//
	// Every new path that mutates what Draw, CursorPosition,
	// CursorVisible or MouseTracking report must bump it. One that
	// doesn't leaves every attached client stale until something
	// unrelated changes the pane.
	Generation() uint64

	// OutputGen advances strictly when new bytes are written to the
	// emulator via Write, distinguishing child process terminal output
	// from layout resizes and view adjustments.
	OutputGen() uint64

	// Resize changes the emulator's dimensions, reflowing the visible
	// screen so narrowing does not destroy text.
	Resize(cols, rows int)
	Draw(dst uv.Screen, area image.Rectangle)
	DrawAt(dst uv.Screen, area image.Rectangle, offset int)
	CellAt(x, y int) *uv.Cell
	CaptureText(scrollback bool, maxLines int) string
	Size() (cols, rows int)
	Close() error
}

// Snapshotter is implemented by Grids that support serializing and restoring state.
type Snapshotter interface {
	ExportSnapshot() *GridSnapshot
	RestoreSnapshot(snap *GridSnapshot)
}

type GridSnapshot struct {
	Cols          int                 `json:"cols"`
	Rows          int                 `json:"rows"`
	CursorX       int                 `json:"cursor_x"`
	CursorY       int                 `json:"cursor_y"`
	CursorVisible bool                `json:"cursor_visible"`
	MouseModes    uint32              `json:"mouse_modes"`
	Status        int32               `json:"status"`
	Title         string              `json:"title"`
	CWD           string              `json:"cwd"`
	UserVars      map[string]string   `json:"user_vars"`
	ScrollOffset  int                 `json:"scroll_offset"`
	Scrollback    []protocol.LineData `json:"scrollback"`
	Screen        []protocol.LineData `json:"screen"`
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

// vtGrid adapts x/vt's SafeEmulator to Grid. SafeEmulator rather than
// Emulator because a pane's PTY reader goroutine writes to it while the
// compositor reads from it.
type vtGrid struct {
	em            *vt.SafeEmulator
	cursorVisible atomic.Bool
	// mouseModes is a bitmask of the child's mouse tracking modes that
	// are currently set; see mouseModeBits.
	mouseModes    atomic.Uint32
	status        atomic.Int32
	lastWriteTime atomic.Pointer[time.Time]

	// title is written from the emulator's parse path (inside Write)
	// and read from the broadcast path, so it is atomic for the same
	// reason cursorVisible is.
	title                  atomic.Pointer[string]
	cwd                    atomic.Pointer[string]
	sawAuthoritativeStatus atomic.Bool

	userVarsMu sync.Mutex
	userVars   map[string]string

	// osc repairs OSC strings x/ansi would cut at a 0x9C byte (#175).
	// Only Write touches it, under writeResizeMu.
	osc          oscScanner
	scrollOffset atomic.Int32

	// generation backs Generation; see the Grid interface.
	generation atomic.Uint64
	outputGen  atomic.Uint64

	// idleTimeout is how long Status waits before the fallback calls a
	// pane idle. Zero means DefaultIdleTimeout; resolved in Status
	// rather than here so a zero-valued vtGrid cannot silently report
	// idle immediately.
	idleTimeout time.Duration

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
	// Three methods hold it: Write, Resize, and DrawAt's scrollback
	// branch (reached directly or via Draw) -- which holds it longest,
	// for a whole frame's worth of ScrollbackCellAt/CellAt pointers
	// handed to dst.SetCell, plus the scrollback-length and offset
	// samples that decide where the history/live boundary falls.
	// DrawAt's fast path (offset <= 0) is deliberately outside it.
	//
	// Read is deliberately excluded -- it can block indefinitely (the
	// same reason SafeEmulator's own Read is unlocked), and
	// Write/Resize/Draw/DrawAt all terminate on their own, so nothing here can
	// wedge against it.
	writeResizeMu sync.Mutex
}

// DefaultIdleTimeout is how long a pane may go without output before the
// status fallback calls it idle. Consulted only when no OSC 133 has been
// seen -- a shell that reports its own status is authoritative.
const DefaultIdleTimeout = 3 * time.Second

// NewVT returns a Grid backed by charmbracelet/x/vt.
func NewVT(cols, rows int) Grid {
	return NewVTWithIdleTimeout(cols, rows, DefaultIdleTimeout)
}

// NewVTWithIdleTimeout is NewVT with the status fallback's idle window
// overridden.
//
// A second constructor rather than a setter: NewVT returns the Grid
// interface, so a setter would have to go on that interface and be
// implemented by every hand-rolled fake (statusGrid in the server
// package's status_test.go, newBlockingGrid in pane_wedge_test.go).
// Widening NewVT itself would touch its ~20 existing call sites to serve
// one test.
func NewVTWithIdleTimeout(cols, rows int, idle time.Duration) Grid {
	g := &vtGrid{em: vt.NewSafeEmulator(cols, rows), idleTimeout: idle}
	g.cursorVisible.Store(true)
	g.status.Store(int32(protocol.StatusIdle))

	g.em.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { g.cursorVisible.Store(visible) },
		// x/vt has parsed OSC 0/1/2 into a title all along; nobody
		// registered the callback, so it was thrown away. An agent
		// harness keeps this current -- Claude Code writes a spinner
		// and a summary of the turn into it -- which makes it the
		// most informative thing a card sliver can show.
		Title: func(s string) { g.title.Store(&s) },
		// vt already honours these modes in SendMouse; wideboi needs
		// them too, to know whether a click in this pane belongs to the
		// child or to its own selection.
		EnableMode:  func(m ansi.Mode) { g.trackMouseMode(m, true) },
		DisableMode: func(m ansi.Mode) { g.trackMouseMode(m, false) },
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

		var st protocol.PaneStatus
		switch parts[1] {
		case "A", "B":
			// A is prompt-start, B is prompt-end, and a shell emits
			// both back to back on every prompt. Mapping B to Working
			// would clobber A microseconds later and leave an idle
			// shell reading as busy, so both mean "waiting on you".
			st = protocol.StatusNeedsInput
		case "C":
			st = protocol.StatusWorking
		case "D":
			// Bare "D" and "D;0" are success; any other exit-code
			// field is a failure. Split rather than match a ";0"
			// suffix: the payload may carry trailing key=value
			// fields, so "133;D;0;aid=1" is still a success.
			st = protocol.StatusDone
			if len(parts) > 2 && parts[2] != "" && parts[2] != "0" {
				st = protocol.StatusFailed
			}
		default:
			// Unrecognised. Let vt log it as unhandled, and leave
			// sawAuthoritativeStatus clear -- the latch also disables the activity
			// fallback in Write and the idle timeout in Status, and
			// one malformed sequence should not switch those off
			// permanently.
			return false
		}

		g.status.Store(int32(st))
		g.sawAuthoritativeStatus.Store(true)
		return true
	})

	g.em.RegisterOscHandler(9, func(data []byte) bool {
		// ConEmu / Windows Terminal progress reporting protocol:
		//   ESC ] 9 ; 4 ; <state> [; <progress>] BEL
		// x/vt hands the handler the full payload, e.g. "9;4;3;".
		// Non-progress OSC 9 sequences (such as iTerm2 notifications)
		// and malformed sequences leave sawAuthoritativeStatus untouched.
		parts := strings.Split(string(data), ";")
		if len(parts) < 3 || parts[1] != "4" {
			return false
		}

		// State 1 is normal progress and requires a progress value.
		if parts[2] == "1" && (len(parts) < 4 || parts[3] == "") {
			return false
		}
		// If a progress value is present in parts[3], it must be an integer 0..100.
		if len(parts) > 3 && parts[3] != "" {
			pct, err := strconv.Atoi(parts[3])
			if err != nil || pct < 0 || pct > 100 {
				return false
			}
		}

		var st protocol.PaneStatus
		switch parts[2] {
		case "0":
			st = protocol.StatusDone
		case "1", "3":
			st = protocol.StatusWorking
		case "2":
			st = protocol.StatusFailed
		case "4":
			st = protocol.StatusNeedsInput
		default:
			return false
		}

		g.status.Store(int32(st))
		g.sawAuthoritativeStatus.Store(true)
		return true
	})

	g.em.RegisterOscHandler(7, func(data []byte) bool {
		parts := strings.SplitN(string(data), ";", 2)
		if len(parts) < 2 {
			return false
		}
		rawURL := parts[1]
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme != "file" {
			return false
		}
		host := strings.ToLower(u.Hostname())
		if host != "" && host != "localhost" {
			localHost, err := os.Hostname()
			if err != nil || !strings.EqualFold(host, localHost) {
				return false
			}
		}
		path := u.Path
		if path == "" || !strings.HasPrefix(path, "/") {
			return false
		}
		cleaned := filepath.Clean(path)
		for i := 0; i < len(cleaned); i++ {
			b := cleaned[i]
			if b < 0x20 || b == 0x7f {
				return false
			}
		}
		g.cwd.Store(&cleaned)
		return true
	})

	g.em.RegisterOscHandler(1337, func(data []byte) bool {
		parts := strings.SplitN(string(data), ";", 2)
		if len(parts) < 2 {
			return false
		}
		cmd := parts[1]
		if !strings.HasPrefix(cmd, "SetUserVar=") {
			return false
		}
		kv := strings.TrimPrefix(cmd, "SetUserVar=")
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			return false
		}
		name := kv[:eq]
		valBase64 := kv[eq+1:]

		if len(name) == 0 || len(name) > 64 {
			return false
		}
		for i := 0; i < len(name); i++ {
			b := name[i]
			if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-') {
				return false
			}
		}

		if valBase64 == "" {
			g.userVarsMu.Lock()
			delete(g.userVars, name)
			g.userVarsMu.Unlock()
			return true
		}

		if len(valBase64) > base64.StdEncoding.EncodedLen(4096) {
			return false
		}

		decoded, err := base64.StdEncoding.DecodeString(valBase64)
		if err != nil {
			return false
		}
		if len(decoded) == 0 {
			g.userVarsMu.Lock()
			delete(g.userVars, name)
			g.userVarsMu.Unlock()
			return true
		}
		if len(decoded) > 4096 || !utf8.Valid(decoded) {
			return false
		}

		g.userVarsMu.Lock()
		defer g.userVarsMu.Unlock()
		if g.userVars == nil {
			g.userVars = make(map[string]string)
		}
		if _, exists := g.userVars[name]; !exists && len(g.userVars) >= 64 {
			return false
		}
		g.userVars[name] = string(decoded)
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
	if !g.sawAuthoritativeStatus.Load() {
		g.status.Store(int32(protocol.StatusWorking))
	}
	var err error
	g.osc.write(p, func(b []byte) {
		if err == nil {
			_, err = g.em.Write(b)
		}
	}, func(title string) { g.title.Store(&title) })
	g.generation.Add(1)
	g.outputGen.Add(1)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Title reports the pane's terminal title. See the Grid interface.
func (g *vtGrid) Title() string {
	if t := g.title.Load(); t != nil {
		return *t
	}
	return ""
}

// CWD reports the pane's current working directory. See the Grid interface.
func (g *vtGrid) CWD() string {
	if p := g.cwd.Load(); p != nil {
		return *p
	}
	return ""
}

// UserVars reports the pane's agent metadata. See the Grid interface.
func (g *vtGrid) UserVars() map[string]string {
	g.userVarsMu.Lock()
	defer g.userVarsMu.Unlock()
	if len(g.userVars) == 0 {
		return nil
	}
	out := make(map[string]string, len(g.userVars))
	for k, v := range g.userVars {
		out[k] = v
	}
	return out
}

func (g *vtGrid) Status() protocol.PaneStatus {
	st := protocol.PaneStatus(g.status.Load())
	if !g.sawAuthoritativeStatus.Load() && st == protocol.StatusWorking {
		idle := g.idleTimeout
		if idle <= 0 {
			idle = DefaultIdleTimeout
		}
		if t := g.lastWriteTime.Load(); t != nil && time.Since(*t) > idle {
			return protocol.StatusIdle
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

	// Workaround for charmbracelet/x/vt: its SendKey method compares the entire
	// struct, so fields like Text and IsRepeat prevent modified keys (like Ctrl+C)
	// from matching its switch cases.
	key.Text = ""
	key.IsRepeat = false

	g.em.SendKey(uv.KeyPressEvent(key))
}

func (g *vtGrid) SendText(text string) { g.em.SendText(text) }

// mouseModeBits maps each DEC mouse tracking mode to its bit in
// vtGrid.mouseModes. A bitmask rather than a bool because a child can
// set several at once, and clearing one leaves the others in force.
// Encodings (1006 SGR and friends) are deliberately absent: they say
// how to report, not whether to.
var mouseModeBits = map[ansi.DECMode]uint32{
	ansi.ModeMouseX10:         1 << 0,
	ansi.ModeMouseNormal:      1 << 1,
	ansi.ModeMouseButtonEvent: 1 << 2,
	ansi.ModeMouseAnyEvent:    1 << 3,
}

func (g *vtGrid) trackMouseMode(m ansi.Mode, on bool) {
	dm, ok := m.(ansi.DECMode)
	if !ok {
		return
	}
	bit, ok := mouseModeBits[dm]
	if !ok {
		return
	}
	for {
		old := g.mouseModes.Load()
		next := old &^ bit
		if on {
			next = old | bit
		}
		if g.mouseModes.CompareAndSwap(old, next) {
			return
		}
	}
}

func (g *vtGrid) MouseTracking() bool { return g.mouseModes.Load() != 0 }

func (g *vtGrid) SendMouse(m uv.MouseEvent) { g.em.SendMouse(m) }

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
	// Both the alt-screen and reflow paths below change the grid.
	defer g.generation.Add(1)
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

// HistoryRows copies physical terminal rows under the same lock used by
// DrawAt. Soft wraps remain separate rows: the emulator exposes no wrap bit.
func (g *vtGrid) HistoryRows() (int, []string) {
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()
	sbLen := g.em.ScrollbackLen()
	cols, screenRows := g.em.Width(), g.em.Height()
	rows := make([]string, 0, sbLen+screenRows)
	for y := 0; y < sbLen+screenRows; y++ {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			var cell *uv.Cell
			if y < sbLen {
				cell = g.em.ScrollbackCellAt(x, y)
			} else {
				cell = g.em.CellAt(x, y-sbLen)
			}
			if cell == nil || cell.Content == "" {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(cell.Content)
			if cell.Width > 1 {
				x += cell.Width - 1
			}
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return sbLen, rows
}
func (g *vtGrid) ScrollOffset() int { return int(g.scrollOffset.Load()) }

func (g *vtGrid) SetScrollOffset(offset int) {
	maxOffset := g.em.ScrollbackLen()
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if g.scrollOffset.Swap(int32(offset)) != int32(offset) {
		g.generation.Add(1)
	}
}

func (g *vtGrid) Generation() uint64 { return g.generation.Load() }
func (g *vtGrid) OutputGen() uint64  { return g.outputGen.Load() }

// Draw delegates to DrawAt with the grid's current scroll offset.
// See DrawAt for fast-path vs. scrollback locking behavior.
func (g *vtGrid) Draw(dst uv.Screen, area image.Rectangle) {
	g.DrawAt(dst, area, int(g.scrollOffset.Load()))
}

// DrawAt renders the grid into dst at the given scroll offset from bottom.
// When offset is <= 0, live terminal cells are drawn via the fast path without
// writeResizeMu contention. Positive offsets draw rows from scrollback history
// under writeResizeMu to serialize against concurrent writes and resizes.
func (g *vtGrid) DrawAt(dst uv.Screen, area image.Rectangle, offset int) {
	if offset <= 0 {
		g.em.Draw(dst, area)
		return
	}

	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()

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

// CaptureText extracts lines of text from the emulator. If scrollback is
// true, lines from the scrollback history are included before the visible screen.
// Trailing whitespace is trimmed from each line, and trailing empty lines at
// the bottom of the viewport are trimmed. If maxLines > 0, at most maxLines
// recent lines are returned.
func (g *vtGrid) ExportSnapshot() *GridSnapshot {
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()

	cols, rows := g.em.Width(), g.em.Height()
	sbLen := g.em.ScrollbackLen()

	scrollback := make([]protocol.LineData, sbLen)
	for y := 0; y < sbLen; y++ {
		line := make(protocol.LineData, cols)
		for x := 0; x < cols; x++ {
			c := g.em.ScrollbackCellAt(x, y)
			if c == nil {
				line[x] = protocol.CellData{Content: " ", Width: 1}
				continue
			}
			line[x] = protocol.CellData{
				Content: c.Content,
				Width:   c.Width,
				Style:   protocol.EncodeStyle(c.Style),
			}
		}
		scrollback[y] = line
	}

	screen := make([]protocol.LineData, rows)
	for y := 0; y < rows; y++ {
		line := make(protocol.LineData, cols)
		for x := 0; x < cols; x++ {
			c := g.em.CellAt(x, y)
			if c == nil {
				line[x] = protocol.CellData{Content: " ", Width: 1}
				continue
			}
			line[x] = protocol.CellData{
				Content: c.Content,
				Width:   c.Width,
				Style:   protocol.EncodeStyle(c.Style),
			}
		}
		screen[y] = line
	}

	cp := g.em.CursorPosition()
	snap := &GridSnapshot{
		Cols:          cols,
		Rows:          rows,
		CursorX:       cp.X,
		CursorY:       cp.Y,
		CursorVisible: g.cursorVisible.Load(),
		MouseModes:    g.mouseModes.Load(),
		Status:        g.status.Load(),
		Title:         g.Title(),
		CWD:           g.CWD(),
		UserVars:      g.UserVars(),
		ScrollOffset:  int(g.scrollOffset.Load()),
		Scrollback:    scrollback,
		Screen:        screen,
	}
	return snap
}

func (g *vtGrid) RestoreSnapshot(snap *GridSnapshot) {
	if snap == nil {
		return
	}
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()

	// 1. Populate scrollback
	sb := g.em.Scrollback()
	sb.Clear()
	for _, lineData := range snap.Scrollback {
		uvLine := make(uv.Line, len(lineData))
		for i, cd := range lineData {
			uvLine[i] = uv.Cell{
				Content: cd.Content,
				Width:   cd.Width,
				Style:   cd.Style.Decode(),
			}
		}
		sb.Push(uvLine)
	}

	// 2. Populate screen
	for y, lineData := range snap.Screen {
		if y >= g.em.Height() {
			break
		}
		for x := 0; x < len(lineData) && x < g.em.Width(); x++ {
			cd := lineData[x]
			// Skip continuation cells so we do not clobber the wide glyph that was set
			// by the preceding cell (whose Width > 1).
			if cd.Width == 0 && cd.Content == "" {
				continue
			}
			cell := uv.Cell{
				Content: cd.Content,
				Width:   cd.Width,
				Style:   cd.Style.Decode(),
			}
			g.em.SetCell(x, y, &cell)
		}
	}

	// 3. Position cursor
	// ANSI cursor position is 1-indexed: \033[y;xH
	g.em.Write([]byte(fmt.Sprintf("\033[%d;%dH", snap.CursorY+1, snap.CursorX+1)))

	// 4. Cursor visibility & mouse modes
	g.cursorVisible.Store(snap.CursorVisible)
	g.mouseModes.Store(snap.MouseModes)
	if snap.MouseModes != 0 {
		var modeSeq strings.Builder
		if snap.MouseModes&(1<<0) != 0 {
			modeSeq.WriteString("\033[?9h")
		}
		if snap.MouseModes&(1<<1) != 0 {
			modeSeq.WriteString("\033[?1000h")
		}
		if snap.MouseModes&(1<<2) != 0 {
			modeSeq.WriteString("\033[?1002h")
		}
		if snap.MouseModes&(1<<3) != 0 {
			modeSeq.WriteString("\033[?1003h")
		}
		modeSeq.WriteString("\033[?1006h") // SGR encoding
		_, _ = g.em.Write([]byte(modeSeq.String()))
	}
	g.status.Store(snap.Status)
	if snap.Title != "" {
		t := snap.Title
		g.title.Store(&t)
	}
	if snap.CWD != "" {
		c := snap.CWD
		g.cwd.Store(&c)
	}
	if len(snap.UserVars) > 0 {
		g.userVarsMu.Lock()
		g.userVars = make(map[string]string, len(snap.UserVars))
		for k, v := range snap.UserVars {
			g.userVars[k] = v
		}
		g.userVarsMu.Unlock()
	}
	g.scrollOffset.Store(int32(snap.ScrollOffset))
	g.generation.Add(1)
}

func (g *vtGrid) CaptureText(scrollback bool, maxLines int) string {
	g.writeResizeMu.Lock()
	defer g.writeResizeMu.Unlock()

	cols, rows := g.em.Width(), g.em.Height()
	sbLen := g.em.ScrollbackLen()

	totalLines := rows
	if scrollback {
		totalLines += sbLen
	}

	startLine := 0
	if maxLines > 0 && totalLines > rows+maxLines {
		startLine = totalLines - (rows + maxLines)
	}

	var lines []string
	for idx := startLine; idx < totalLines; idx++ {
		var sb strings.Builder
		inScrollback := scrollback && idx < sbLen
		row := idx
		if scrollback && !inScrollback {
			row = idx - sbLen
		}
		for x := 0; x < cols; {
			var cell *uv.Cell
			if inScrollback {
				cell = g.em.ScrollbackCellAt(x, row)
			} else {
				cell = g.em.CellAt(x, row)
			}
			if cell == nil || cell.Content == "" {
				sb.WriteByte(' ')
				x++
				continue
			}
			sb.WriteString(cell.Content)
			w := cell.Width
			if w <= 0 {
				w = 1
			}
			x += w
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}

	// Trim trailing empty lines at the bottom of the viewport
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
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
