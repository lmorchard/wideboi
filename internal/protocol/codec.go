package protocol

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lmorchard/wideboi/internal/protocol/wirepb"
	"google.golang.org/protobuf/proto"
)

// MarshalClient, UnmarshalClient, MarshalServer and UnmarshalServer are the
// only conversion points between the application's messages and the
// generated wire schema. The Go structs stay the in-process model because
// the patch code copies and compares them by value, which generated
// messages do not support. Unknown messages are errors instead of silently
// disappearing from a transport. TestCodecRoundTripsEveryField fails if a
// field added to a message is not converted here.
func MarshalClient(msg any) ([]byte, error) {
	env := &wirepb.ClientMessage{}
	switch m := msg.(type) {
	case MsgAttach:
		env.Msg = &wirepb.ClientMessage_Attach{Attach: &wirepb.MsgAttach{Cols: int32(m.Cols), Rows: int32(m.Rows)}}
	case MsgVerb:
		widths := make(map[int32]int32, len(m.Widths))
		for id, width := range m.Widths {
			widths[int32(id)] = int32(width)
		}
		if m.Widths == nil {
			widths = nil
		}
		env.Msg = &wirepb.ClientMessage_Verb{Verb: &wirepb.MsgVerb{Verb: wirepb.VerbType(m.Verb), PaneId: int32(m.PaneID), Widths: widths}}
	case MsgSetPaneWidth:
		env.Msg = &wirepb.ClientMessage_SetPaneWidth{SetPaneWidth: &wirepb.MsgSetPaneWidth{PaneId: int32(m.PaneID), Width: int32(m.Width)}}
	case MsgMouse:
		env.Msg = &wirepb.ClientMessage_Mouse{Mouse: &wirepb.MsgMouse{PaneId: int32(m.PaneID), Kind: wirepb.MouseKind(m.Kind), X: int32(m.X), Y: int32(m.Y), Button: int32(m.Button), Mod: int32(m.Mod)}}
	case MsgInput:
		env.Msg = &wirepb.ClientMessage_Input{Input: &wirepb.MsgInput{PaneId: int32(m.PaneID), Key: encodeKey(m.Key), Data: m.Data}}
	case MsgResize:
		env.Msg = &wirepb.ClientMessage_Resize{Resize: &wirepb.MsgResize{Cols: int32(m.Cols), Rows: int32(m.Rows)}}
	case MsgScroll:
		env.Msg = &wirepb.ClientMessage_Scroll{Scroll: &wirepb.MsgScroll{PaneId: int32(m.PaneID), Delta: int32(m.Delta), SetAbsolute: m.SetAbsolute, Offset: int32(m.Offset), AnchorHistory: m.AnchorHistory, HistoryLen: int32(m.HistoryLen)}}
	case MsgHistoryRequest:
		env.Msg = &wirepb.ClientMessage_HistoryRequest{HistoryRequest: &wirepb.MsgHistoryRequest{PaneId: int32(m.PaneID)}}
	case MsgPaneResync:
		env.Msg = &wirepb.ClientMessage_PaneResync{PaneResync: &wirepb.MsgPaneResync{PaneId: int32(m.PaneID)}}
	case MsgDetach:
		env.Msg = &wirepb.ClientMessage_Detach{Detach: &wirepb.MsgDetach{}}
	case MsgShutdown:
		env.Msg = &wirepb.ClientMessage_Shutdown{Shutdown: &wirepb.MsgShutdown{}}
	case MsgStatusRequest:
		env.Msg = &wirepb.ClientMessage_StatusRequest{StatusRequest: &wirepb.MsgStatusRequest{}}
	case MsgTrafficRequest:
		env.Msg = &wirepb.ClientMessage_TrafficRequest{TrafficRequest: &wirepb.MsgTrafficRequest{}}
	case MsgSplitRequest:
		env.Msg = &wirepb.ClientMessage_SplitRequest{SplitRequest: &wirepb.MsgSplitRequest{Command: validUTF8(m.Command), Cwd: validUTF8(m.Cwd), AfterPaneId: int32(m.AfterPaneID), Keep: m.Keep}}
	case MsgSendInputRequest:
		env.Msg = &wirepb.ClientMessage_SendInputRequest{SendInputRequest: &wirepb.MsgSendInputRequest{PaneId: int32(m.PaneID), Data: m.Data}}
	case MsgCaptureRequest:
		env.Msg = &wirepb.ClientMessage_CaptureRequest{CaptureRequest: &wirepb.MsgCaptureRequest{PaneId: int32(m.PaneID), Scrollback: m.Scrollback, Lines: int32(m.Lines)}}
	case MsgClosePaneRequest:
		env.Msg = &wirepb.ClientMessage_ClosePaneRequest{ClosePaneRequest: &wirepb.MsgClosePaneRequest{PaneId: int32(m.PaneID)}}
	case MsgWaitRequest:
		env.Msg = &wirepb.ClientMessage_WaitRequest{WaitRequest: &wirepb.MsgWaitRequest{PaneId: int32(m.PaneID)}}
	case MsgSaveMacros:
		pbMacros := make([]*wirepb.Macro, len(m.Macros))
		for i, macro := range m.Macros {
			steps := make([]*wirepb.MacroStep, len(macro.Steps))
			for j, step := range macro.Steps {
				steps[j] = &wirepb.MacroStep{
					Text:  validUTF8(step.Text),
					Key:   validUTF8(step.Key),
					Code:  validUTF8(step.Code),
					Ctrl:  step.Ctrl,
					Alt:   step.Alt,
					Shift: step.Shift,
				}
			}
			pbMacros[i] = &wirepb.Macro{
				Name:  validUTF8(macro.Name),
				Steps: steps,
			}
		}
		env.Msg = &wirepb.ClientMessage_SaveMacros{SaveMacros: &wirepb.MsgSaveMacros{Macros: pbMacros}}
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
		var widths map[int]int
		if m.Verb.Widths != nil {
			widths = make(map[int]int, len(m.Verb.Widths))
			for id, width := range m.Verb.Widths {
				widths[int(id)] = int(width)
			}
		}
		return MsgVerb{Verb: VerbType(m.Verb.Verb), PaneID: int(m.Verb.PaneId), Widths: widths}, nil
	case *wirepb.ClientMessage_SetPaneWidth:
		return MsgSetPaneWidth{PaneID: int(m.SetPaneWidth.PaneId), Width: int(m.SetPaneWidth.Width)}, nil
	case *wirepb.ClientMessage_Mouse:
		return MsgMouse{PaneID: int(m.Mouse.PaneId), Kind: MouseKind(m.Mouse.Kind), X: int(m.Mouse.X), Y: int(m.Mouse.Y), Button: int(m.Mouse.Button), Mod: int(m.Mouse.Mod)}, nil
	case *wirepb.ClientMessage_Input:
		return MsgInput{PaneID: int(m.Input.PaneId), Key: decodeKey(m.Input.Key), Data: m.Input.Data}, nil
	case *wirepb.ClientMessage_Resize:
		return MsgResize{Cols: int(m.Resize.Cols), Rows: int(m.Resize.Rows)}, nil
	case *wirepb.ClientMessage_Scroll:
		return MsgScroll{PaneID: int(m.Scroll.PaneId), Delta: int(m.Scroll.Delta), SetAbsolute: m.Scroll.SetAbsolute, Offset: int(m.Scroll.Offset), AnchorHistory: m.Scroll.AnchorHistory, HistoryLen: int(m.Scroll.HistoryLen)}, nil
	case *wirepb.ClientMessage_HistoryRequest:
		return MsgHistoryRequest{PaneID: int(m.HistoryRequest.PaneId)}, nil
	case *wirepb.ClientMessage_PaneResync:
		return MsgPaneResync{PaneID: int(m.PaneResync.PaneId)}, nil
	case *wirepb.ClientMessage_Detach:
		return MsgDetach{}, nil
	case *wirepb.ClientMessage_Shutdown:
		return MsgShutdown{}, nil
	case *wirepb.ClientMessage_StatusRequest:
		return MsgStatusRequest{}, nil
	case *wirepb.ClientMessage_TrafficRequest:
		return MsgTrafficRequest{}, nil
	case *wirepb.ClientMessage_SplitRequest:
		return MsgSplitRequest{Command: m.SplitRequest.Command, Cwd: m.SplitRequest.Cwd, AfterPaneID: int(m.SplitRequest.AfterPaneId), Keep: m.SplitRequest.Keep}, nil
	case *wirepb.ClientMessage_SendInputRequest:
		return MsgSendInputRequest{PaneID: int(m.SendInputRequest.PaneId), Data: m.SendInputRequest.Data}, nil
	case *wirepb.ClientMessage_CaptureRequest:
		return MsgCaptureRequest{PaneID: int(m.CaptureRequest.PaneId), Scrollback: m.CaptureRequest.Scrollback, Lines: int(m.CaptureRequest.Lines)}, nil
	case *wirepb.ClientMessage_ClosePaneRequest:
		return MsgClosePaneRequest{PaneID: int(m.ClosePaneRequest.PaneId)}, nil
	case *wirepb.ClientMessage_WaitRequest:
		return MsgWaitRequest{PaneID: int(m.WaitRequest.PaneId)}, nil
	case *wirepb.ClientMessage_SaveMacros:
		macros := make([]Macro, len(m.SaveMacros.Macros))
		for i, macro := range m.SaveMacros.Macros {
			steps := make([]MacroStep, len(macro.Steps))
			for j, step := range macro.Steps {
				steps[j] = MacroStep{
					Text:  step.Text,
					Key:   step.Key,
					Code:  step.Code,
					Ctrl:  step.Ctrl,
					Alt:   step.Alt,
					Shift: step.Shift,
				}
			}
			macros[i] = Macro{
				Name:  macro.Name,
				Steps: steps,
			}
		}
		return MsgSaveMacros{Macros: macros}, nil
	default:
		return nil, fmt.Errorf("unknown client message %T", env.Msg)
	}
}

func MarshalServer(msg any) ([]byte, error) {
	env := &wirepb.ServerMessage{}
	switch m := msg.(type) {
	case MsgLayoutSnapshot:
		snap := &wirepb.MsgLayoutSnapshot{}
		for _, c := range m.Columns {
			snap.Columns = append(snap.Columns, &wirepb.ColumnData{PaneId: int32(c.PaneID), Width: int32(c.Width), Height: int32(c.Height)})
		}
		if m.PaneStatuses != nil {
			snap.PaneStatuses = make(map[int32]wirepb.PaneStatus, len(m.PaneStatuses))
			for id, status := range m.PaneStatuses {
				snap.PaneStatuses[int32(id)] = wirepb.PaneStatus(status)
			}
		}
		if m.PaneTitles != nil {
			snap.PaneTitles = make(map[int32]string, len(m.PaneTitles))
			for id, title := range m.PaneTitles {
				snap.PaneTitles[int32(id)] = validUTF8(title)
			}
		}
		env.Msg = &wirepb.ServerMessage_LayoutSnapshot{LayoutSnapshot: snap}
	case MsgPaneCreated:
		env.Msg = &wirepb.ServerMessage_PaneCreated{PaneCreated: &wirepb.MsgPaneCreated{PaneId: int32(m.PaneID)}}
	case MsgHistorySnapshot:
		rows := make([]string, len(m.Rows))
		for i, row := range m.Rows {
			rows[i] = validUTF8(row)
		}
		env.Msg = &wirepb.ServerMessage_HistorySnapshot{HistorySnapshot: &wirepb.MsgHistorySnapshot{PaneId: int32(m.PaneID), ScrollbackLen: int32(m.ScrollbackLen), Rows: rows}}
	case MsgPaneUpdate:
		update := &wirepb.MsgPaneUpdate{
			PaneId:        int32(m.PaneID),
			Generation:    m.Generation,
			Cols:          int32(m.Cols),
			Rows:          int32(m.Rows),
			CursorX:       int32(m.CursorX),
			CursorY:       int32(m.CursorY),
			CursorVisible: m.CursorVisible,
			MouseTracking: m.MouseTracking,
			ScrollOffset:  int32(m.ScrollOffset),
			ScrollbackLen: int32(m.ScrollbackLen),
			UnreadOutput:  m.UnreadOutput,
		}
		for _, line := range m.Lines {
			update.Lines = append(update.Lines, &wirepb.LineData{Cells: encodeLine(line)})
		}
		env.Msg = &wirepb.ServerMessage_PaneUpdate{PaneUpdate: update}
	case MsgPanePatch:
		patch := &wirepb.MsgPanePatch{
			PaneId:         int32(m.PaneID),
			Cols:           int32(m.Cols),
			Rows:           int32(m.Rows),
			BaseGeneration: m.BaseGeneration,
			Generation:     m.Generation,
			ShiftRows:      int32(m.ShiftRows),
			CursorX:        int32(m.CursorX),
			CursorY:        int32(m.CursorY),
			CursorVisible:  m.CursorVisible,
			MouseTracking:  m.MouseTracking,
			ScrollOffset:   int32(m.ScrollOffset),
			ScrollbackLen:  int32(m.ScrollbackLen),
			UnreadOutput:   m.UnreadOutput,
		}
		for _, row := range m.ChangedRows {
			patch.ChangedRows = append(patch.ChangedRows, &wirepb.PaneRow{Y: int32(row.Y), Cells: encodeLine(row.Cells)})
		}
		env.Msg = &wirepb.ServerMessage_PanePatch{PanePatch: patch}
	case MsgPaneClosed:
		env.Msg = &wirepb.ServerMessage_PaneClosed{PaneClosed: &wirepb.MsgPaneClosed{PaneId: int32(m.PaneID), ExitCode: int32(m.ExitCode)}}
	case MsgPaneMetadata:
		meta := &wirepb.MsgPaneMetadata{
			PaneId:   int32(m.PaneID),
			Cwd:      validUTF8(m.CWD),
			Exited:   m.Exited,
			ExitCode: int32(m.ExitCode),
		}
		if m.UserVars != nil {
			meta.UserVars = make(map[string]string, len(m.UserVars))
			for k, v := range m.UserVars {
				meta.UserVars[validUTF8(k)] = validUTF8(v)
			}
		}
		env.Msg = &wirepb.ServerMessage_PaneMetadata{PaneMetadata: meta}
	case MsgTrafficStats:
		stats := &wirepb.MsgTrafficStats{UptimeMillis: m.UptimeMillis, TimingEnabled: m.TimingEnabled, Departed: encodeClientTraffic(m.Departed), Render: encodeTiming(m.Render), BuildPatch: encodeTiming(m.BuildPatch)}
		for _, c := range m.Clients {
			stats.Clients = append(stats.Clients, encodeClientTraffic(c))
		}
		env.Msg = &wirepb.ServerMessage_TrafficStats{TrafficStats: stats}
	case MsgFocusPane:
		env.Msg = &wirepb.ServerMessage_FocusPane{FocusPane: &wirepb.MsgFocusPane{PaneId: int32(m.PaneID)}}
	case MsgSplitResponse:
		env.Msg = &wirepb.ServerMessage_SplitResponse{SplitResponse: &wirepb.MsgSplitResponse{PaneId: int32(m.PaneID), Error: validUTF8(m.Error)}}
	case MsgSendInputResponse:
		env.Msg = &wirepb.ServerMessage_SendInputResponse{SendInputResponse: &wirepb.MsgSendInputResponse{PaneId: int32(m.PaneID), Error: validUTF8(m.Error)}}
	case MsgCaptureResponse:
		env.Msg = &wirepb.ServerMessage_CaptureResponse{CaptureResponse: &wirepb.MsgCaptureResponse{PaneId: int32(m.PaneID), Text: validUTF8(m.Text), Error: validUTF8(m.Error)}}
	case MsgClosePaneResponse:
		env.Msg = &wirepb.ServerMessage_ClosePaneResponse{ClosePaneResponse: &wirepb.MsgClosePaneResponse{PaneId: int32(m.PaneID), Error: validUTF8(m.Error)}}
	case MsgWaitResponse:
		env.Msg = &wirepb.ServerMessage_WaitResponse{WaitResponse: &wirepb.MsgWaitResponse{PaneId: int32(m.PaneID), ExitCode: int32(m.ExitCode), Error: validUTF8(m.Error)}}
	case MsgMacrosSnapshot:
		pbMacros := make([]*wirepb.Macro, len(m.Macros))
		for i, macro := range m.Macros {
			steps := make([]*wirepb.MacroStep, len(macro.Steps))
			for j, step := range macro.Steps {
				steps[j] = &wirepb.MacroStep{
					Text:  validUTF8(step.Text),
					Key:   validUTF8(step.Key),
					Code:  validUTF8(step.Code),
					Ctrl:  step.Ctrl,
					Alt:   step.Alt,
					Shift: step.Shift,
				}
			}
			pbMacros[i] = &wirepb.Macro{
				Name:  validUTF8(macro.Name),
				Steps: steps,
			}
		}
		env.Msg = &wirepb.ServerMessage_MacrosSnapshot{MacrosSnapshot: &wirepb.MsgMacrosSnapshot{Macros: pbMacros}}
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
		src := m.LayoutSnapshot
		var snap MsgLayoutSnapshot
		for _, c := range src.Columns {
			snap.Columns = append(snap.Columns, ColumnData{PaneID: int(c.PaneId), Width: int(c.Width), Height: int(c.Height)})
		}
		if src.PaneStatuses != nil {
			snap.PaneStatuses = make(map[int]PaneStatus, len(src.PaneStatuses))
			for id, status := range src.PaneStatuses {
				snap.PaneStatuses[int(id)] = PaneStatus(status)
			}
		}
		if src.PaneTitles != nil {
			snap.PaneTitles = make(map[int]string, len(src.PaneTitles))
			for id, title := range src.PaneTitles {
				snap.PaneTitles[int(id)] = title
			}
		}
		return snap, nil
	case *wirepb.ServerMessage_PaneCreated:
		return MsgPaneCreated{PaneID: int(m.PaneCreated.PaneId)}, nil
	case *wirepb.ServerMessage_HistorySnapshot:
		return MsgHistorySnapshot{PaneID: int(m.HistorySnapshot.PaneId), ScrollbackLen: int(m.HistorySnapshot.ScrollbackLen), Rows: m.HistorySnapshot.Rows}, nil
	case *wirepb.ServerMessage_PaneUpdate:
		src := m.PaneUpdate
		update := MsgPaneUpdate{
			PaneID:        int(src.PaneId),
			Generation:    src.Generation,
			Cols:          int(src.Cols),
			Rows:          int(src.Rows),
			CursorX:       int(src.CursorX),
			CursorY:       int(src.CursorY),
			CursorVisible: src.CursorVisible,
			MouseTracking: src.MouseTracking,
			ScrollOffset:  int(src.ScrollOffset),
			ScrollbackLen: int(src.ScrollbackLen),
			UnreadOutput:  src.UnreadOutput,
		}
		for _, row := range src.Lines {
			update.Lines = append(update.Lines, decodeLine(row.Cells))
		}
		return update, nil
	case *wirepb.ServerMessage_PanePatch:
		src := m.PanePatch
		patch := MsgPanePatch{
			PaneID:         int(src.PaneId),
			Cols:           int(src.Cols),
			Rows:           int(src.Rows),
			BaseGeneration: src.BaseGeneration,
			Generation:     src.Generation,
			ShiftRows:      int(src.ShiftRows),
			CursorX:        int(src.CursorX),
			CursorY:        int(src.CursorY),
			CursorVisible:  src.CursorVisible,
			MouseTracking:  src.MouseTracking,
			ScrollOffset:   int(src.ScrollOffset),
			ScrollbackLen:  int(src.ScrollbackLen),
			UnreadOutput:   src.UnreadOutput,
		}
		for _, row := range src.ChangedRows {
			patch.ChangedRows = append(patch.ChangedRows, PaneRow{Y: int(row.Y), Cells: decodeLine(row.Cells)})
		}
		return patch, nil
	case *wirepb.ServerMessage_PaneClosed:
		return MsgPaneClosed{PaneID: int(m.PaneClosed.PaneId), ExitCode: int(m.PaneClosed.ExitCode)}, nil
	case *wirepb.ServerMessage_PaneMetadata:
		src := m.PaneMetadata
		meta := MsgPaneMetadata{
			PaneID:   int(src.PaneId),
			CWD:      src.Cwd,
			Exited:   src.Exited,
			ExitCode: int(src.ExitCode),
		}
		if src.UserVars != nil {
			meta.UserVars = make(map[string]string, len(src.UserVars))
			for k, v := range src.UserVars {
				meta.UserVars[k] = v
			}
		}
		return meta, nil
	case *wirepb.ServerMessage_TrafficStats:
		src := m.TrafficStats
		stats := MsgTrafficStats{UptimeMillis: src.UptimeMillis, TimingEnabled: src.TimingEnabled, Departed: decodeClientTraffic(src.Departed), Render: decodeTiming(src.Render), BuildPatch: decodeTiming(src.BuildPatch)}
		for _, c := range src.Clients {
			stats.Clients = append(stats.Clients, decodeClientTraffic(c))
		}
		return stats, nil
	case *wirepb.ServerMessage_FocusPane:
		return MsgFocusPane{PaneID: int(m.FocusPane.PaneId)}, nil
	case *wirepb.ServerMessage_SplitResponse:
		return MsgSplitResponse{PaneID: int(m.SplitResponse.PaneId), Error: m.SplitResponse.Error}, nil
	case *wirepb.ServerMessage_SendInputResponse:
		return MsgSendInputResponse{PaneID: int(m.SendInputResponse.PaneId), Error: m.SendInputResponse.Error}, nil
	case *wirepb.ServerMessage_CaptureResponse:
		return MsgCaptureResponse{PaneID: int(m.CaptureResponse.PaneId), Text: m.CaptureResponse.Text, Error: m.CaptureResponse.Error}, nil
	case *wirepb.ServerMessage_ClosePaneResponse:
		return MsgClosePaneResponse{PaneID: int(m.ClosePaneResponse.PaneId), Error: m.ClosePaneResponse.Error}, nil
	case *wirepb.ServerMessage_WaitResponse:
		return MsgWaitResponse{PaneID: int(m.WaitResponse.PaneId), ExitCode: int(m.WaitResponse.ExitCode), Error: m.WaitResponse.Error}, nil
	case *wirepb.ServerMessage_MacrosSnapshot:
		macros := make([]Macro, len(m.MacrosSnapshot.Macros))
		for i, macro := range m.MacrosSnapshot.Macros {
			steps := make([]MacroStep, len(macro.Steps))
			for j, step := range macro.Steps {
				steps[j] = MacroStep{
					Text:  step.Text,
					Key:   step.Key,
					Code:  step.Code,
					Ctrl:  step.Ctrl,
					Alt:   step.Alt,
					Shift: step.Shift,
				}
			}
			macros[i] = Macro{
				Name:  macro.Name,
				Steps: steps,
			}
		}
		return MsgMacrosSnapshot{Macros: macros}, nil
	default:
		return nil, fmt.Errorf("unknown server message %T", env.Msg)
	}
}

// validUTF8 returns s unchanged when it is valid UTF-8, the common case,
// and otherwise replaces each invalid byte sequence with U+FFFD. Protobuf
// string fields refuse invalid UTF-8, and one bad pane title used to make
// a whole snapshot unencodable (#175).
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// encodeLine and decodeLine convert one row; full updates and patches
// share them so a cell field is mapped in exactly one place.
func encodeLine(line LineData) []*wirepb.CellData {
	cells := make([]*wirepb.CellData, 0, len(line))
	for _, cell := range line {
		cells = append(cells, &wirepb.CellData{Content: validUTF8(cell.Content), Width: int32(cell.Width), Style: encodeStyle(cell.Style)})
	}
	return cells
}

func decodeLine(cells []*wirepb.CellData) LineData {
	line := make(LineData, 0, len(cells))
	for _, cell := range cells {
		line = append(line, CellData{Content: cell.Content, Width: int(cell.Width), Style: decodeStyle(cell.Style)})
	}
	return line
}

// Zero-valued styles, colours and keys encode as absent sub-messages and
// decode back to the zero value: on the wire, absence means zero.
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
	return &wirepb.KeyData{Text: validUTF8(k.Text), Mod: int32(k.Mod), Code: int32(k.Code), ShiftedCode: int32(k.ShiftedCode), BaseCode: int32(k.BaseCode), IsRepeat: k.IsRepeat}
}

func decodeKey(k *wirepb.KeyData) KeyData {
	if k == nil {
		return KeyData{}
	}
	return KeyData{Text: k.Text, Mod: int(k.Mod), Code: rune(k.Code), ShiftedCode: rune(k.ShiftedCode), BaseCode: rune(k.BaseCode), IsRepeat: k.IsRepeat}
}

func encodeTiming(t TimingStat) *wirepb.TimingStat {
	if t == (TimingStat{}) {
		return nil
	}
	return &wirepb.TimingStat{Count: t.Count, TotalNanos: t.TotalNanos, MaxNanos: t.MaxNanos}
}

func decodeTiming(t *wirepb.TimingStat) TimingStat {
	if t == nil {
		return TimingStat{}
	}
	return TimingStat{Count: t.Count, TotalNanos: t.TotalNanos, MaxNanos: t.MaxNanos}
}

func encodeClientTraffic(c ClientTraffic) *wirepb.ClientTraffic {
	return &wirepb.ClientTraffic{ClientId: int32(c.ClientID), Transport: validUTF8(c.Transport), ConnectedMillis: c.ConnectedMillis,
		FullUpdates: c.FullUpdates, RowPatches: c.RowPatches, ShiftPatches: c.ShiftPatches, ChangedRows: c.ChangedRows,
		ResyncRequests: c.ResyncRequests, SendFailures: c.SendFailures,
		Messages: c.Messages, PayloadBytes: c.PayloadBytes, PanePayloadBytes: c.PanePayloadBytes, WireBytes: c.WireBytes,
		Encode: encodeTiming(c.Encode)}
}

func decodeClientTraffic(c *wirepb.ClientTraffic) ClientTraffic {
	if c == nil {
		return ClientTraffic{}
	}
	return ClientTraffic{ClientID: int(c.ClientId), Transport: c.Transport, ConnectedMillis: c.ConnectedMillis,
		FullUpdates: c.FullUpdates, RowPatches: c.RowPatches, ShiftPatches: c.ShiftPatches, ChangedRows: c.ChangedRows,
		ResyncRequests: c.ResyncRequests, SendFailures: c.SendFailures,
		Messages: c.Messages, PayloadBytes: c.PayloadBytes, PanePayloadBytes: c.PanePayloadBytes, WireBytes: c.WireBytes,
		Encode: decodeTiming(c.Encode)}
}
