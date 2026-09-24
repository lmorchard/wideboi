package protocol

import (
	"fmt"

	"github.com/lmorchard/wideboi/internal/protocol/wirepb"
	"google.golang.org/protobuf/proto"
)

// MarshalClient and MarshalServer are the only conversion points between the
// application's messages and the generated wire schema. Unknown messages are
// errors instead of silently disappearing from a transport.
func MarshalClient(msg any) ([]byte, error) {
	env := &wirepb.ClientMessage{}
	switch m := msg.(type) {
	case MsgAttach:
		env.Msg = &wirepb.ClientMessage_Attach{Attach: &wirepb.MsgAttach{Cols: int32(m.Cols), Rows: int32(m.Rows)}}
	case MsgVerb:
		env.Msg = &wirepb.ClientMessage_Verb{Verb: &wirepb.MsgVerb{Verb: wirepb.VerbType(m.Verb)}}
	case MsgFocusPane:
		env.Msg = &wirepb.ClientMessage_FocusPane{FocusPane: &wirepb.MsgFocusPane{PaneId: int32(m.PaneID)}}
	case MsgMouse:
		env.Msg = &wirepb.ClientMessage_Mouse{Mouse: &wirepb.MsgMouse{PaneId: int32(m.PaneID), Kind: wirepb.MouseKind(m.Kind), X: int32(m.X), Y: int32(m.Y), Button: int32(m.Button), Mod: int32(m.Mod)}}
	case MsgInput:
		env.Msg = &wirepb.ClientMessage_Input{Input: &wirepb.MsgInput{PaneId: int32(m.PaneID), Key: encodeKey(m.Key), Data: m.Data}}
	case MsgResize:
		env.Msg = &wirepb.ClientMessage_Resize{Resize: &wirepb.MsgResize{Cols: int32(m.Cols), Rows: int32(m.Rows)}}
	case MsgScroll:
		env.Msg = &wirepb.ClientMessage_Scroll{Scroll: &wirepb.MsgScroll{PaneId: int32(m.PaneID), Delta: int32(m.Delta)}}
	case MsgDetach:
		env.Msg = &wirepb.ClientMessage_Detach{Detach: &wirepb.MsgDetach{}}
	case MsgShutdown:
		env.Msg = &wirepb.ClientMessage_Shutdown{Shutdown: &wirepb.MsgShutdown{}}
	case MsgStatusRequest:
		env.Msg = &wirepb.ClientMessage_StatusRequest{StatusRequest: &wirepb.MsgStatusRequest{}}
	default:
		return nil, fmt.Errorf("unsupported client message %T", msg)
	}
	return proto.Marshal(env)
}

func UnmarshalClient(data []byte) (any, error) {
	var env wirepb.ClientMessage
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	switch m := env.Msg.(type) {
	case *wirepb.ClientMessage_Attach:
		return MsgAttach{Cols: int(m.Attach.Cols), Rows: int(m.Attach.Rows)}, nil
	case *wirepb.ClientMessage_Verb:
		return MsgVerb{Verb: VerbType(m.Verb.Verb)}, nil
	case *wirepb.ClientMessage_FocusPane:
		return MsgFocusPane{PaneID: int(m.FocusPane.PaneId)}, nil
	case *wirepb.ClientMessage_Mouse:
		return MsgMouse{PaneID: int(m.Mouse.PaneId), Kind: MouseKind(m.Mouse.Kind), X: int(m.Mouse.X), Y: int(m.Mouse.Y), Button: int(m.Mouse.Button), Mod: int(m.Mouse.Mod)}, nil
	case *wirepb.ClientMessage_Input:
		return MsgInput{PaneID: int(m.Input.PaneId), Key: decodeKey(m.Input.Key), Data: m.Input.Data}, nil
	case *wirepb.ClientMessage_Resize:
		return MsgResize{Cols: int(m.Resize.Cols), Rows: int(m.Resize.Rows)}, nil
	case *wirepb.ClientMessage_Scroll:
		return MsgScroll{PaneID: int(m.Scroll.PaneId), Delta: int(m.Scroll.Delta)}, nil
	case *wirepb.ClientMessage_Detach:
		return MsgDetach{}, nil
	case *wirepb.ClientMessage_Shutdown:
		return MsgShutdown{}, nil
	case *wirepb.ClientMessage_StatusRequest:
		return MsgStatusRequest{}, nil
	default:
		return nil, fmt.Errorf("unknown client message %T", env.Msg)
	}
}

func MarshalServer(msg any) ([]byte, error) {
	env := &wirepb.ServerMessage{}
	switch m := msg.(type) {
	case MsgLayoutSnapshot:
		snap := &wirepb.MsgLayoutSnapshot{FocusPaneId: int32(m.FocusPaneID)}
		for _, c := range m.Columns {
			snap.Columns = append(snap.Columns, &wirepb.ColumnData{PaneId: int32(c.PaneID), Width: int32(c.Width), Height: int32(c.Height)})
		}
		if m.PaneStatuses != nil {
			snap.PaneStatuses = make(map[int32]string, len(m.PaneStatuses))
			for id, status := range m.PaneStatuses {
				snap.PaneStatuses[int32(id)] = status
			}
		}
		if m.PaneTitles != nil {
			snap.PaneTitles = make(map[int32]string, len(m.PaneTitles))
			for id, title := range m.PaneTitles {
				snap.PaneTitles[int32(id)] = title
			}
		}
		env.Msg = &wirepb.ServerMessage_LayoutSnapshot{LayoutSnapshot: snap}
	case MsgPaneUpdate:
		update := &wirepb.MsgPaneUpdate{PaneId: int32(m.PaneID), Cols: int32(m.Cols), Rows: int32(m.Rows), CursorX: int32(m.CursorX), CursorY: int32(m.CursorY), CursorVisible: m.CursorVisible, MouseTracking: m.MouseTracking}
		for _, line := range m.Lines {
			row := &wirepb.LineData{}
			for _, cell := range line {
				row.Cells = append(row.Cells, &wirepb.CellData{Content: cell.Content, Width: int32(cell.Width), Style: encodeStyle(cell.Style)})
			}
			update.Lines = append(update.Lines, row)
		}
		env.Msg = &wirepb.ServerMessage_PaneUpdate{PaneUpdate: update}
	case MsgPaneClosed:
		env.Msg = &wirepb.ServerMessage_PaneClosed{PaneClosed: &wirepb.MsgPaneClosed{PaneId: int32(m.PaneID), ExitCode: int32(m.ExitCode)}}
	default:
		return nil, fmt.Errorf("unsupported server message %T", msg)
	}
	return proto.Marshal(env)
}

func UnmarshalServer(data []byte) (any, error) {
	var env wirepb.ServerMessage
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	switch m := env.Msg.(type) {
	case *wirepb.ServerMessage_LayoutSnapshot:
		snap := MsgLayoutSnapshot{FocusPaneID: int(m.LayoutSnapshot.FocusPaneId)}
		for _, c := range m.LayoutSnapshot.Columns {
			snap.Columns = append(snap.Columns, ColumnData{PaneID: int(c.PaneId), Width: int(c.Width), Height: int(c.Height)})
		}
		if m.LayoutSnapshot.PaneStatuses != nil {
			snap.PaneStatuses = make(map[int]string, len(m.LayoutSnapshot.PaneStatuses))
			for id, status := range m.LayoutSnapshot.PaneStatuses {
				snap.PaneStatuses[int(id)] = status
			}
		}
		if m.LayoutSnapshot.PaneTitles != nil {
			snap.PaneTitles = make(map[int]string, len(m.LayoutSnapshot.PaneTitles))
			for id, title := range m.LayoutSnapshot.PaneTitles {
				snap.PaneTitles[int(id)] = title
			}
		}
		return snap, nil
	case *wirepb.ServerMessage_PaneUpdate:
		src := m.PaneUpdate
		update := MsgPaneUpdate{PaneID: int(src.PaneId), Cols: int(src.Cols), Rows: int(src.Rows), CursorX: int(src.CursorX), CursorY: int(src.CursorY), CursorVisible: src.CursorVisible, MouseTracking: src.MouseTracking}
		for _, row := range src.Lines {
			line := make(LineData, 0, len(row.Cells))
			for _, cell := range row.Cells {
				line = append(line, CellData{Content: cell.Content, Width: int(cell.Width), Style: decodeStyle(cell.Style)})
			}
			update.Lines = append(update.Lines, line)
		}
		return update, nil
	case *wirepb.ServerMessage_PaneClosed:
		return MsgPaneClosed{PaneID: int(m.PaneClosed.PaneId), ExitCode: int(m.PaneClosed.ExitCode)}, nil
	default:
		return nil, fmt.Errorf("unknown server message %T", env.Msg)
	}
}

func encodeColor(c ColorData) *wirepb.ColorData {
	if c == (ColorData{}) {
		return nil
	}
	return &wirepb.ColorData{Kind: wirepb.ColorKind(c.Kind), Index: uint32(c.Index), R: uint32(c.R), G: uint32(c.G), B: uint32(c.B), A: uint32(c.A)}
}
func decodeColor(c *wirepb.ColorData) ColorData {
	if c == nil {
		return ColorData{}
	}
	return ColorData{Kind: ColorKind(c.Kind), Index: uint8(c.Index), R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: uint8(c.A)}
}
func encodeStyle(s StyleData) *wirepb.StyleData {
	if s == (StyleData{}) {
		return nil
	}
	return &wirepb.StyleData{Fg: encodeColor(s.Fg), Bg: encodeColor(s.Bg), UnderlineColor: encodeColor(s.UnderlineColor), Underline: uint32(s.Underline), Attrs: uint32(s.Attrs)}
}
func decodeStyle(s *wirepb.StyleData) StyleData {
	if s == nil {
		return StyleData{}
	}
	return StyleData{Fg: decodeColor(s.Fg), Bg: decodeColor(s.Bg), UnderlineColor: decodeColor(s.UnderlineColor), Underline: uint8(s.Underline), Attrs: uint8(s.Attrs)}
}
func encodeKey(k KeyData) *wirepb.KeyData {
	if k == (KeyData{}) {
		return nil
	}
	return &wirepb.KeyData{Text: k.Text, Mod: int32(k.Mod), Code: int32(k.Code), ShiftedCode: int32(k.ShiftedCode), BaseCode: int32(k.BaseCode), IsRepeat: k.IsRepeat}
}
func decodeKey(k *wirepb.KeyData) KeyData {
	if k == nil {
		return KeyData{}
	}
	return KeyData{Text: k.Text, Mod: int(k.Mod), Code: rune(k.Code), ShiftedCode: rune(k.ShiftedCode), BaseCode: rune(k.BaseCode), IsRepeat: k.IsRepeat}
}
