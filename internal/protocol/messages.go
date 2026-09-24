// Package protocol defines codec-neutral message types for communication
// between client and server across the transport boundary.
package protocol

import (
	"fmt"
	"image"
)

type VerbType int

const (
	VerbFocusLeft VerbType = iota + 1
	VerbFocusRight
	VerbNewColumn
	VerbCycleWidth
	VerbKillPane
	VerbSmartJump
	// VerbToggleCards is reserved. Layout is client-local since #92, so
	// no client sends it and the server ignores it; the slot stays so
	// the verbs after it keep their wire values.
	VerbToggleCards
	VerbGrowWidth
	VerbShrinkWidth
	VerbMoveLeft
	VerbMoveRight
	VerbFocusLast
)

// PaneStatus represents the current state of a pane's process.
type PaneStatus int

const (
	StatusIdle PaneStatus = iota
	StatusWorking
	StatusNeedsInput
	StatusDone
	StatusFailed
)

// Glyph returns a single-character representation of the status for display.
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

// ColumnData describes a column's logical width and height.
type ColumnData struct {
	PaneID int
	Width  int
	Height int
}

// CellData carries one cell's text content, width, and style across transport.
//
// Style is protocol.StyleData, not uv.Style: see wire.go for why an
// upstream style cannot cross this boundary.
type CellData struct {
	Content string
	Width   int
	Style   StyleData
}

// LineData represents a horizontal row indexed by terminal column. A wide
// glyph occupies its starting cell and a continuation cell at the next
// index; renderers must advance by one index, not by CellData.Width.
type LineData []CellData

// MsgPaneUpdate carries a pane's rendered cell buffer and cursor state.
type MsgPaneUpdate struct {
	PaneID        int
	Generation    uint64
	Cols          int
	Rows          int
	Lines         []LineData
	CursorX       int
	CursorY       int
	CursorVisible bool
	// MouseTracking is true while the pane's child has asked for mouse
	// events. It rides on the pane update rather than the layout
	// snapshot because it changes when the child writes bytes, and
	// writing bytes is what sends a pane update.
	MouseTracking bool
}

// PaneRow replaces one complete row. Complete rows keep wide glyph
// continuation cells and all style fields together.
type PaneRow struct {
	Y     int
	Cells LineData
}

// MsgPanePatch changes a pane relative to an exact client baseline.
// Rows may be empty for cursor or mouse-mode-only changes.
type MsgPanePatch struct {
	PaneID         int
	Cols, Rows     int
	BaseGeneration uint64
	Generation     uint64
	ChangedRows    []PaneRow
	CursorX        int
	CursorY        int
	CursorVisible  bool
	MouseTracking  bool
}

// MsgPaneResync asks for a full snapshot after a missing or stale patch.
type MsgPaneResync struct{ PaneID int }

// PlacementKind says what a placement represents, which the renderer
// cannot infer from geometry: a card sliver and a pane clipped by the
// viewport edge are both a narrow Dst over a cropped Src, and Z does
// not separate them either -- ScrollStrategy emits Z=0 for everything.
//
// Without this the client would paint chrome over the visible edge of a
// legitimately clipped pane, which is exactly what smoke.py's
// case_partly_clipped_pane_keeps_full_width exists to prevent.
type PlacementKind int

const (
	// PlacementFull is a pane rendering its own content, whether or not
	// the viewport clips it. Clipping is not occlusion.
	PlacementFull PlacementKind = iota
	// PlacementSliver is an occluded card, drawn as chrome rather than
	// as a peek at its content.
	PlacementSliver
)

// PlacementData describes where a pane's content buffer is cropped from (Src)
// and where on the host screen surface it blits (Dst), plus layer depth Z.
type PlacementData struct {
	PaneID int
	Src    image.Rectangle
	Dst    image.Rectangle
	Z      int
	Kind   PlacementKind
}

// MsgAttach is sent by the client upon connecting to report viewport geometry.
type MsgAttach struct {
	Cols int
	Rows int
}

// MsgVerb is sent by the client to request a layout navigation or action.
type MsgVerb struct {
	Verb   VerbType
	PaneID int
}

// MouseKind identifies the kind of mouse event forwarded to a pane.
type MouseKind int

const (
	MousePress MouseKind = iota
	MouseRelease
	MouseMotion
	MouseWheel
)

// MsgMouse forwards a mouse event to a pane whose child has enabled
// mouse tracking. X and Y are pane-local cells, already translated from
// the screen by the client.
//
// Concrete fields rather than uv.MouseEvent: that is an interface, and
// interfaces cannot cross the wire (TestWireTypesCarryNoInterfaces).
type MsgMouse struct {
	PaneID int
	Kind   MouseKind
	X, Y   int
	Button int
	Mod    int
}

// MsgInput carries decoded key events or pasted text destined for a
// specific pane's PTY. Data wins when it is non-empty; otherwise Key is
// replayed into the pane's emulator.
//
// Key is protocol.KeyData, not uv.KeyEvent: see wire.go.
type MsgInput struct {
	PaneID int
	Key    KeyData
	Data   []byte
}

// MsgResize reports a change in host terminal window dimensions.
type MsgResize struct {
	Cols int
	Rows int
}

// MsgScroll requests a change in scrollback offset for a pane.
type MsgScroll struct {
	PaneID int
	Delta  int
}

// MsgDetach tells the server this client is leaving and the session is
// not. The server hangs up on the sender; for the client that owns the
// session it also gives the ownership up, for good.
type MsgDetach struct{}

// MsgShutdown asks the server to end the session: hang up every pane,
// hang up on every client, and exit. The client hang-up is the
// acknowledgement, and it happens only after every pane has been hung
// up.
type MsgShutdown struct{}

// LayoutMode is which Strategy a client is presenting. It is per-client
// presentation, not session state, and does not cross the wire (#92):
// each client resolves its own from config and toggles it locally. The
// type lives here because both config and layout name it.
type LayoutMode int

const (
	// LayoutScroll is the horizontal strip: columns scroll out of view.
	LayoutScroll LayoutMode = iota
	// LayoutCards fans off-screen columns into overlapping cards.
	LayoutCards
)

// String is the mode's name as config spells it and the status line
// shows it (#91).
func (m LayoutMode) String() string {
	switch m {
	case LayoutScroll:
		return "scroll"
	case LayoutCards:
		return "cards"
	}
	return fmt.Sprintf("LayoutMode(%d)", int(m))
}

// MsgStatusRequest is sent by a client to request a MsgLayoutSnapshot without altering layout or panes.
type MsgStatusRequest struct{}

// MsgLayoutSnapshot broadcasts shared columns and statuses. Focus, placements,
// and layout mode belong to each client.
type MsgLayoutSnapshot struct {
	Columns      []ColumnData
	PaneStatuses map[int]PaneStatus
	// PaneTitles is each pane's terminal title, for chrome that wants
	// to say what a pane is doing rather than show a sliver of it.
	PaneTitles map[int]string
}

// MsgPaneCreated tells only the requesting client which pane its new-column
// verb created. That client can focus it when the next snapshot arrives.
type MsgPaneCreated struct {
	PaneID int
}

// MsgPaneClosed notifies the client that a pane's process died or was reaped.
type MsgPaneClosed struct {
	PaneID   int
	ExitCode int
}
