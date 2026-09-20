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
}

// PlacementData describes where a pane's content buffer is cropped from (Src)
// and where on the host screen surface it blits (Dst), plus layer depth Z.
type PlacementData struct {
	PaneID int
	Src    image.Rectangle
	Dst    image.Rectangle
	Z      int
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

// MsgLayoutSnapshot is sent by the server to update the client on placements, focus, and statuses.
type MsgLayoutSnapshot struct {
	Columns      []ColumnData
	Placements   []PlacementData
	FocusPaneID  int
	PaneStatuses map[int]string
}

// MsgPaneClosed notifies the client that a pane's process died or was reaped.
type MsgPaneClosed struct {
	PaneID   int
	ExitCode int
}
