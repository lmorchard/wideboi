// Package protocol defines codec-neutral message types for communication
// between client and server across the transport boundary.
package protocol

import (
	"image"

	uv "github.com/charmbracelet/ultraviolet"
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

// MsgInput carries decoded key events or pasted text destined for a specific pane's PTY.
type MsgInput struct {
	PaneID int
	Key    uv.KeyEvent
	Data   []byte
}

// MsgResize reports a change in host terminal window dimensions.
type MsgResize struct {
	Cols int
	Rows int
}

// MsgLayoutSnapshot is sent by the server to update the client on placements and focus.
type MsgLayoutSnapshot struct {
	Placements  []PlacementData
	FocusPaneID int
}

// MsgPaneClosed notifies the client that a pane's process died or was reaped.
type MsgPaneClosed struct {
	PaneID   int
	ExitCode int
}
