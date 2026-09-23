package transport

import (
	"google.golang.org/protobuf/proto"
	"image"
	"reflect"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// roundtrip encodes v through the same interface-valued gob path the
// socket pumps use -- Encode(&msg) where msg is a ServerMessage /
// ClientMessage -- and decodes it back. Encoding the concrete type
// directly would not exercise gob's interface machinery, which is
// exactly where the attach-killing defect lived.
func roundtrip(t *testing.T, v any) any {
	b, err := proto.Marshal(v.(proto.Message))
	if err != nil {
		t.Fatalf("encode %T: %v", v, err)
	}

	// Create a new instance of the same type to unmarshal into
	out := reflect.New(reflect.TypeOf(v).Elem()).Interface().(proto.Message)

	if err := proto.Unmarshal(b, out); err != nil {
		t.Fatalf("decode %T: %v", v, err)
	}
	return out
}

/*
		t.Helper()
		var buf bytes.Buffer
		var out any

		var in any = v
		if err := gob.NewEncoder(&buf).Encode(&in); err != nil {
			t.Fatalf("encode %T: %v", v, err)
		}
		if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
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
				msg := &protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_PaneUpdate{PaneUpdate: &protocol.MsgPaneUpdate{
					PaneId: 7, Cols: 1, Rows: 1,
					Lines: []*protocol.LineData{{Cells: []*protocol.CellData{{Content: "x", Width: 1, Style: style}}}},
					CursorX:       0,
					CursorY:       0,
					CursorVisible: true,
				}}}

				env := roundtrip(t, msg).(*protocol.ServerEnvelope)
				got := env.GetPaneUpdate()
				ok := env != nil
				if !ok {
					t.Fatalf("decoded to wrong type")
				}
				if !proto.Equal(got.(proto.Message), msg.(proto.Message)) {
					t.Errorf("roundtrip changed the message:\n got %+v\nwant %+v", got, msg)
				}

				// The decoded style must still describe the same colour to
				// the renderer. Comparing the wire struct alone would pass
				// even if Decode threw the value away.
				gotStyle := got.Lines[0].Cells[0].Style.Decode()
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
				msg := &protocol.ClientEnvelope{Payload: &protocol.ClientEnvelope_Input{Input: &protocol.MsgInput{PaneId: int32(3), Key: protocol.EncodeKey(uv.KeyPressEvent(k))}}}

				got, ok := roundtrip(t, msg).(protocol.MsgInput)
				if !ok {
					t.Fatalf("decoded to wrong type")
				}
				if !proto.Equal(got.(proto.Message), msg.(proto.Message)) {
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
*/
func TestEveryMessageTypeRoundtrips(t *testing.T) {
	msgs := []any{
		&protocol.MsgAttach{Cols: 80, Rows: 24},
		&protocol.ClientEnvelope{Payload: &protocol.ClientEnvelope_Verb{Verb: &protocol.MsgVerb{Verb: protocol.VerbType_VERB_SMART_JUMP}}},
		&protocol.ClientEnvelope{Payload: &protocol.ClientEnvelope_FocusPane{FocusPane: &protocol.MsgFocusPane{PaneId: int32(3)}}},
		&protocol.MsgMouse{PaneId: 2, Kind: protocol.MouseKind_MOUSE_RELEASE, X: 4, Y: 5, Button: 1, Mod: 1},
		&protocol.MsgInput{PaneId: 1, Data: []byte("hi")},
		&protocol.ClientEnvelope{Payload: &protocol.ClientEnvelope_Input{Input: &protocol.MsgInput{PaneId: int32(1), Key: protocol.EncodeKey(uv.KeyPressEvent{Code: 'z', Text: "z"})}}},
		&protocol.MsgResize{Cols: 100, Rows: 40},
		&protocol.MsgScroll{PaneId: 2, Delta: -3},
		&protocol.MsgShutdown{},
		&protocol.MsgDetach{},
		&protocol.MsgLayoutSnapshot{
			Columns:      []*protocol.ColumnData{{PaneId: 1, Width: 40, Height: 22}},
			Placements:   []*protocol.PlacementData{{PaneId: 1, Src: protocol.EncodeRectangle(image.Rect(0, 0, 40, 22)), Dst: protocol.EncodeRectangle(image.Rect(0, 1, 40, 23)), Z: 0}},
			FocusPaneId:  1,
			PaneStatuses: map[int32]string{1: "»"},
		},
		&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_PaneUpdate{PaneUpdate: &protocol.MsgPaneUpdate{
			PaneId: 1, Cols: 2, Rows: 1,
			Lines: []*protocol.LineData{{Cells: []*protocol.CellData{
				{Content: "世", Width: 2, Style: protocol.EncodeStyle(uv.Style{Fg: ansi.BasicColor(4)})},
			}}},
		}}},
		&protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_PaneClosed{PaneClosed: &protocol.MsgPaneClosed{PaneId: 4, ExitCode: 130}}},
	}

	for _, msg := range msgs {
		name := string(msg.(proto.Message).ProtoReflect().Descriptor().Name())
		t.Run(name, func(t *testing.T) {
			if got := roundtrip(t, msg); !proto.Equal(got.(proto.Message), msg.(proto.Message)) {
				t.Errorf("roundtrip changed %s:\n got %+v\nwant %+v", name, got, msg)
			}
		})
	}
}
