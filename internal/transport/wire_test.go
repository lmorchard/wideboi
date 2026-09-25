package transport

import (
	"bytes"
	"image/color"
	"reflect"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// roundtrip sends v through the same path the socket pumps use -- the
// codec, then a length-prefixed frame -- and decodes it back. Client
// messages go through the client envelope, everything else the server's.
func roundtrip(t *testing.T, v any) any {
	t.Helper()
	marshal, unmarshal := protocol.MarshalClient, protocol.UnmarshalClient
	if _, err := marshal(v); err != nil {
		marshal, unmarshal = protocol.MarshalServer, protocol.UnmarshalServer
	}
	payload, err := marshal(v)
	if err != nil {
		t.Fatalf("encode %T: %v", v, err)
	}
	var wire bytes.Buffer
	if err := writeFrame(&wire, payload); err != nil {
		t.Fatalf("frame %T: %v", v, err)
	}
	framed, err := readFrame(&wire)
	if err != nil {
		t.Fatalf("read frame %T: %v", v, err)
	}
	out, err := unmarshal(framed)
	if err != nil {
		t.Fatalf("decode %T: %v", v, err)
	}
	return out
}

// TestPaneUpdateSurvivesEveryColorKind is the regression test for the
// defect that made `wideboi attach` unusable: the first colored cell a
// child emitted -- a shell prompt, within a second or two of attaching --
// failed to encode, the write pump returned, the socket closed, and the
// attached client exited with no message and status 0.
//
// The colors enumerated here are not a sample. They are every concrete
// color.Color that uv.ReadStyle and ansi.ReadStyleColor can put in a
// cell: BasicColor for SGR 30-37/40-47, IndexedColor for 38;5, RGBA for
// 38;2, CMYK for the 38;3 and 38;4 direct-color forms, and Transparent
// for colorType 1.
func TestPaneUpdateSurvivesEveryColorKind(t *testing.T) {
	colors := map[string]color.Color{
		"nil":         nil,
		"basic":       ansi.BasicColor(1),
		"indexed":     ansi.IndexedColor(200),
		"rgba":        color.RGBA{R: 12, G: 34, B: 56, A: 0xff},
		"cmyk":        color.CMYK{C: 10, M: 20, Y: 30, K: 40},
		"transparent": color.Transparent,
		"rgbcolor":    ansi.RGBColor{R: 1, G: 2, B: 3},
	}

	for name, c := range colors {
		t.Run(name, func(t *testing.T) {
			style := protocol.EncodeStyle(uv.Style{
				Fg:             c,
				Bg:             c,
				UnderlineColor: c,
				Underline:      ansi.UnderlineCurly,
				Attrs:          uv.AttrBold | uv.AttrReverse,
			})
			msg := protocol.MsgPaneUpdate{
				PaneID: 7, Cols: 1, Rows: 1,
				Lines:         []protocol.LineData{{{Content: "x", Width: 1, Style: style}}},
				CursorX:       0,
				CursorY:       0,
				CursorVisible: true,
			}

			got, ok := roundtrip(t, msg).(protocol.MsgPaneUpdate)
			if !ok {
				t.Fatalf("decoded to wrong type")
			}
			if !reflect.DeepEqual(got, msg) {
				t.Errorf("roundtrip changed the message:\n got %+v\nwant %+v", got, msg)
			}

			// The decoded style must still describe the same colour to
			// the renderer. Comparing the wire struct alone would pass
			// even if Decode threw the value away.
			gotStyle := got.Lines[0][0].Style.Decode()
			wantStyle := style.Decode()
			if !sameColor(gotStyle.Fg, wantStyle.Fg) {
				t.Errorf("Fg lost in transit: got %#v want %#v", gotStyle.Fg, wantStyle.Fg)
			}
			if gotStyle.Attrs != wantStyle.Attrs || gotStyle.Underline != wantStyle.Underline {
				t.Errorf("attrs lost: got %+v want %+v", gotStyle, wantStyle)
			}
		})
	}
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// TestKeyInputSurvivesTheWire is the regression test for the second half
// of the same defect. MsgInput.Key was a uv.KeyEvent -- an interface --
// so the very first keystroke a user typed failed to encode on the
// client side and closed the socket from the other end.
//
// The cases cover what the encoding actually turns on: plain text, a
// modifier (ModShift is the one LESSONS.md records as silently dropped
// by SendKey), a named key with no text, and a key carrying the Kitty
// protocol's extra codes.
func TestKeyInputSurvivesTheWire(t *testing.T) {
	keys := map[string]uv.Key{
		"plain":   {Code: 'a', Text: "a"},
		"shifted": {Code: 'a', ShiftedCode: 'A', Text: "A", Mod: uv.ModShift},
		"ctrl":    {Code: 'c', Mod: uv.ModCtrl},
		"named":   {Code: uv.KeyEnter},
		"kitty":   {Code: 'q', BaseCode: 'a', ShiftedCode: 'Q', Mod: uv.ModAlt, IsRepeat: true},
	}

	for name, k := range keys {
		t.Run(name, func(t *testing.T) {
			msg := protocol.MsgInput{PaneID: 3, Key: protocol.EncodeKey(uv.KeyPressEvent(k))}

			got, ok := roundtrip(t, msg).(protocol.MsgInput)
			if !ok {
				t.Fatalf("decoded to wrong type")
			}
			if !reflect.DeepEqual(got, msg) {
				t.Errorf("roundtrip changed the message:\n got %+v\nwant %+v", got, msg)
			}
			if decoded := got.Key.Decode().Key(); !reflect.DeepEqual(decoded, k) {
				t.Errorf("key lost in transit:\n got %+v\nwant %+v", decoded, k)
			}
		})
	}
}

// TestEveryMessageTypeRoundtrips enumerates the protocol rather than
// sampling it: a message type that nothing here encodes is a message
// type whose first real send is its first test.
func TestEveryMessageTypeRoundtrips(t *testing.T) {
	msgs := []any{
		protocol.MsgAttach{Cols: 80, Rows: 24},
		protocol.MsgStatusRequest{},
		protocol.MsgVerb{Verb: protocol.VerbSmartJump},
		protocol.MsgMouse{PaneID: 2, Kind: protocol.MouseRelease, X: 4, Y: 5, Button: 1, Mod: 1},
		protocol.MsgInput{PaneID: 1, Data: []byte("hi")},
		protocol.MsgInput{PaneID: 1, Key: protocol.EncodeKey(uv.KeyPressEvent{Code: 'z', Text: "z"})},
		protocol.MsgResize{Cols: 100, Rows: 40},
		protocol.MsgScroll{PaneID: 2, Delta: -3},
		protocol.MsgShutdown{},
		protocol.MsgDetach{},
		protocol.MsgLayoutSnapshot{
			Columns:      []protocol.ColumnData{{PaneID: 1, Width: 40, Height: 22}},
			PaneStatuses: map[int]protocol.PaneStatus{1: protocol.StatusWorking},
		},
		protocol.MsgPaneCreated{PaneID: 3},
		protocol.MsgPaneUpdate{
			PaneID: 1, Cols: 2, Rows: 1,
			Lines: []protocol.LineData{{
				{Content: "世", Width: 2, Style: protocol.EncodeStyle(uv.Style{Fg: ansi.BasicColor(4)})},
			}},
		},
		protocol.MsgPanePatch{PaneID: 1, Cols: 2, Rows: 2, BaseGeneration: 3, Generation: 4,
			ChangedRows: []protocol.PaneRow{{Y: 0, Cells: protocol.LineData{{Content: "世", Width: 2}}}}},
		protocol.MsgPaneResync{PaneID: 1},
		protocol.MsgPaneClosed{PaneID: 4, ExitCode: 130},
		protocol.MsgSplitRequest{Command: "ls", Cwd: "/tmp", AfterPaneID: 2, Keep: true},
		protocol.MsgSplitResponse{PaneID: 3, Error: "something"},
		protocol.MsgSendInputRequest{PaneID: 1, Data: []byte("date\n")},
		protocol.MsgSendInputResponse{PaneID: 1, Error: ""},
		protocol.MsgCaptureRequest{PaneID: 2, Scrollback: true, Lines: 50},
		protocol.MsgCaptureResponse{PaneID: 2, Text: "hello\nworld\n", Error: ""},
		protocol.MsgClosePaneRequest{PaneID: 4},
		protocol.MsgClosePaneResponse{PaneID: 4, Error: ""},
		protocol.MsgWaitRequest{PaneID: 5},
		protocol.MsgWaitResponse{PaneID: 5, ExitCode: 143, Error: "gone"},
	}

	for _, msg := range msgs {
		name := reflect.TypeOf(msg).Name()
		t.Run(name, func(t *testing.T) {
			if got := roundtrip(t, msg); !reflect.DeepEqual(got, msg) {
				t.Errorf("roundtrip changed %s:\n got %+v\nwant %+v", name, got, msg)
			}
		})
	}
}
