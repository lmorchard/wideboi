package client_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestClientCalculatesPlacementsLocallyFromColumns(t *testing.T) {
	tp := transport.NewInProcChannel(16)
	cli := client.NewClient(tp, 100, 30, "C-b")

	cols := []protocol.ColumnData{
		{PaneID: 1, Width: 40, Height: 28},
		{PaneID: 2, Width: 40, Height: 28},
	}

	snap := protocol.MsgLayoutSnapshot{
		Columns: cols,
	}

	cli.HandleServerMsg(snap)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cli.SendResize(ctx, 120, 30)

	if cli.FocusedPaneID() != 1 {
		t.Errorf("FocusedPaneID after resize = %d, want 1", cli.FocusedPaneID())
	}
}

func TestClientsKeepIndependentFocusAcrossSnapshots(t *testing.T) {
	first := client.NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	second := client.NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	snapshot := protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{
		{PaneID: 1, Width: 40, Height: 22},
		{PaneID: 2, Width: 40, Height: 22},
	}}
	first.HandleServerMsg(snapshot)
	second.HandleServerMsg(snapshot)
	first.SendVerb(context.Background(), protocol.VerbFocusRight)
	if first.FocusedPaneID() != 2 || second.FocusedPaneID() != 1 {
		t.Fatalf("focus after local move: first=%d second=%d", first.FocusedPaneID(), second.FocusedPaneID())
	}

	// A status broadcast must preserve both clients' choices.
	first.HandleServerMsg(snapshot)
	second.HandleServerMsg(snapshot)
	if first.FocusedPaneID() != 2 || second.FocusedPaneID() != 1 {
		t.Fatalf("focus after broadcast: first=%d second=%d", first.FocusedPaneID(), second.FocusedPaneID())
	}

	// Only the requester hears that it created pane 3.
	first.HandleServerMsg(protocol.MsgPaneCreated{PaneID: 3})
	snapshot.Columns = append(snapshot.Columns, protocol.ColumnData{PaneID: 3, Width: 40, Height: 22})
	first.HandleServerMsg(snapshot)
	second.HandleServerMsg(snapshot)
	if first.FocusedPaneID() != 3 || second.FocusedPaneID() != 1 {
		t.Fatalf("focus after new pane: first=%d second=%d", first.FocusedPaneID(), second.FocusedPaneID())
	}

	// Closing each focused pane selects a live pane locally.
	snapshot.Columns = snapshot.Columns[:2]
	first.HandleServerMsg(snapshot)
	if first.FocusedPaneID() != 2 {
		t.Fatalf("focus after pane 3 closed = %d, want 2", first.FocusedPaneID())
	}
}

func TestMsgFocusPaneSwitchesFocus(t *testing.T) {
	cli := client.NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{
		{PaneID: 1, Width: 40, Height: 22},
		{PaneID: 2, Width: 40, Height: 22},
	}})

	if got := cli.FocusedPaneID(); got != 1 {
		t.Fatalf("initial focus = %d, want 1", got)
	}

	cli.HandleServerMsg(protocol.MsgFocusPane{PaneID: 2})
	if got := cli.FocusedPaneID(); got != 2 {
		t.Fatalf("after MsgFocusPane: focus = %d, want 2", got)
	}
}

func TestClientDesktopNotifications(t *testing.T) {
	cli := client.NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")
	var notifs [][2]string
	cli.SetNotificationEmitter(func(title, msg string) {
		notifs = append(notifs, [2]string{title, msg})
	})

	// Initial snapshot: pane 1 (focused) is Working, pane 2 (unfocused) is Working
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusWorking,
			2: protocol.StatusWorking,
		},
		PaneTitles: map[int]string{
			1: "Editor",
			2: "Compiler",
		},
	})

	if len(notifs) != 0 {
		t.Fatalf("unexpected notifications on initial snapshot: %v", notifs)
	}

	// Snapshot: pane 1 (focused) finishes -> NO notification because focused!
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusDone,
			2: protocol.StatusWorking,
		},
		PaneTitles: map[int]string{
			1: "Editor",
			2: "Compiler",
		},
	})
	if len(notifs) != 0 {
		t.Fatalf("focused pane finished should not emit notification: %v", notifs)
	}

	// Snapshot: pane 2 (unfocused) finishes -> EMIT notification!
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusDone,
			2: protocol.StatusDone,
		},
		PaneTitles: map[int]string{
			1: "Editor",
			2: "Compiler",
		},
	})
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got: %v", notifs)
	}
	if notifs[0][0] != "Compiler" || notifs[0][1] != "Finished successfully" {
		t.Errorf("got notification %v, want Compiler: Finished successfully", notifs[0])
	}

	// Bell on pane 2 (unfocused) -> EMIT notification!
	cli.HandleServerMsg(protocol.MsgPaneNotification{
		PaneID:  2,
		Title:   "Compiler",
		Message: "Alert",
	})
	if len(notifs) != 2 {
		t.Fatalf("expected 2 notifications, got: %v", notifs)
	}
	if notifs[1][0] != "Compiler" || notifs[1][1] != "Alert" {
		t.Errorf("got notification %v, want Compiler: Alert", notifs[1])
	}

	// Bell on pane 1 (focused) -> NO notification!
	cli.HandleServerMsg(protocol.MsgPaneNotification{
		PaneID:  1,
		Title:   "Editor",
		Message: "Alert",
	})
	if len(notifs) != 2 {
		t.Fatalf("bell on focused pane should not emit notification: %v", notifs)
	}
}

func TestEmitHostNotificationModesAndSanitization(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		title   string
		msg     string
		env     map[string]string
		wantOut string
	}{
		{
			name:    "off",
			mode:    "off",
			title:   "Test",
			msg:     "Message",
			wantOut: "",
		},
		{
			name:    "bell",
			mode:    "bell",
			title:   "Test",
			msg:     "Message",
			wantOut: "\a",
		},
		{
			name:    "osc9",
			mode:    "osc9",
			title:   "Build",
			msg:     "Done",
			wantOut: "\x1b]9;Build: Done\x07",
		},
		{
			name:    "osc99",
			mode:    "osc99",
			title:   "Build",
			msg:     "Done",
			wantOut: "\x1b]99;;Build: Done\x07",
		},
		{
			name:    "auto_standard",
			mode:    "auto",
			title:   "Build",
			msg:     "Done",
			env:     map[string]string{"TERM": "xterm-256color"},
			wantOut: "\x1b]9;Build: Done\x07",
		},
		{
			name:    "auto_kitty_window_id",
			mode:    "auto",
			title:   "Build",
			msg:     "Done",
			env:     map[string]string{"KITTY_WINDOW_ID": "42"},
			wantOut: "\x1b]99;;Build: Done\x07",
		},
		{
			name:    "auto_kitty_term",
			mode:    "auto",
			title:   "Build",
			msg:     "Done",
			env:     map[string]string{"TERM": "xterm-kitty"},
			wantOut: "\x1b]99;;Build: Done\x07",
		},
		{
			name:    "sanitization_strips_control_characters",
			mode:    "osc9",
			title:   "Evil\x1bTitle\x07\r\n",
			msg:     "Body\x00\x1f\x7f\x80\x9fInjected",
			wantOut: "\x1b]9;Evil Title: Body     Injected\x07",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			getenv := func(key string) string {
				if tc.env != nil {
					return tc.env[key]
				}
				return ""
			}
			client.EmitHostNotificationForTest(&buf, getenv, tc.mode, tc.title, tc.msg)
			if got := buf.String(); got != tc.wantOut {
				t.Errorf("got %q, want %q", got, tc.wantOut)
			}
		})
	}
}

func TestClientUnseenCompletionBadging(t *testing.T) {
	cli := client.NewClient(transport.NewInProcChannel(16), 100, 24, "C-b")

	// Initial snapshot: pane 1 (focused) and pane 2 (unfocused) are both working
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusWorking,
			2: protocol.StatusWorking,
		},
	})

	if cli.FocusedPaneID() != 1 {
		t.Fatalf("focused pane = %d, want 1", cli.FocusedPaneID())
	}

	// Unfocused pane 2 transitions Working -> Idle: should become StatusDone
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusWorking,
			2: protocol.StatusIdle,
		},
	})

	// Pane 2 should be displayed as StatusDone
	if got := cli.DisplayStatus(2); got != protocol.StatusDone {
		t.Fatalf("pane 2 status = %v, want %v", got, protocol.StatusDone)
	}

	// Smart jump should pick pane 2
	ctx := context.Background()
	cli.SendVerb(ctx, protocol.VerbSmartJump)
	if got := cli.FocusedPaneID(); got != 2 {
		t.Fatalf("after smart jump: focus = %d, want 2", got)
	}

	// Now that pane 2 is focused, its status should transition back to StatusIdle
	if got := cli.DisplayStatus(2); got != protocol.StatusIdle {
		t.Fatalf("pane 2 status after focus = %v, want %v", got, protocol.StatusIdle)
	}

	// If focused pane 2 transitions Working -> Idle while focused:
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusIdle,
			2: protocol.StatusWorking,
		},
	})
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 40, Height: 22},
			{PaneID: 2, Width: 40, Height: 22},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusIdle,
			2: protocol.StatusIdle,
		},
	})
	// Focused pane 2 was seen live, so it must be StatusIdle, not StatusDone
	if got := cli.DisplayStatus(2); got != protocol.StatusIdle {
		t.Fatalf("focused pane 2 status = %v, want %v", got, protocol.StatusIdle)
	}
}
