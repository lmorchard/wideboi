// Package protocol defines codec-neutral message types for communication
// between client and server across the transport boundary.
package protocol

import (
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
	// VerbToggleCards switches between the scrolling strip and the
	// card fan. Appended, not inserted: the value crosses the wire.
	VerbToggleCards
	VerbGrowWidth
	VerbShrinkWidth
)

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

// LineData represents a horizontal row of cells.
type LineData []CellData

// MsgPaneUpdate carries a pane's rendered cell buffer and cursor state.
type MsgPaneUpdate struct {
	PaneID        int
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
	Verb VerbType
}

// MsgFocusPane asks the server to focus a specific pane. A mouse click
// names its target, unlike the relative focus verbs, so it cannot ride
// on MsgVerb without giving every other verb a field it ignores.
type MsgFocusPane struct {
	PaneID int
}

// MouseKind says which of uv's mouse event types a MsgMouse carries.
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

// MsgShutdown asks the server to end the session: reap every pane, hang
// up on every client, and exit. The hang-up is the acknowledgement --
// it happens only after the reaping has finished.
type MsgShutdown struct{}

// LayoutMode is which Strategy the session is using.
//
// Shared session state, like focus: two clients of different sizes
// compute their own placements, but they must agree on the mode or
// they disagree about what the session looks like. The zero value is
// the scrolling strip, so a server that never sets it behaves exactly
// as it did before the field existed.
type LayoutMode int

const (
	// LayoutScroll is the horizontal strip: columns scroll out of view.
	LayoutScroll LayoutMode = iota
	// LayoutCards fans off-screen columns into overlapping cards.
	LayoutCards
)

// MsgLayoutSnapshot is sent by the server to update the client on placements, focus, and statuses.
type MsgLayoutSnapshot struct {
	Columns      []ColumnData
	Placements   []PlacementData
	FocusPaneID  int
	PaneStatuses map[int]string
	// PaneTitles is each pane's terminal title, for chrome that wants
	// to say what a pane is doing rather than show a sliver of it.
	PaneTitles map[int]string
	Layout     LayoutMode
}

// MsgPaneClosed notifies the client that a pane's process died or was reaped.
type MsgPaneClosed struct {
	PaneID   int
	ExitCode int
}
