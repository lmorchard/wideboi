package client

import (
	"context"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
)

func TestLocalWidthPanClaimAndCursorReveal(t *testing.T) {
	cli, ch := newMouseClient(t)
	ctx := context.Background()

	cli.SendVerb(ctx, protocol.VerbShrinkWidth) // 40 -> 30, local only
	if got := cli.displayWidths[1]; got != 30 {
		t.Fatalf("local width = %d, want 30", got)
	}
	if got := sent(ch); len(got) != 1 {
		t.Fatalf("width edit sent %d messages, want one exact request", len(got))
	}

	cli.PanFocused(1)
	if got := cli.panX[1]; got != 10 {
		t.Fatalf("pan = %d, want 10", got)
	}
	cli.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{
		{PaneID: 1, Width: 60, Height: 22}, {PaneID: 2, Width: 40, Height: 22},
	}})
	if cli.displayWidths[1] != 30 || cli.panX[1] != 10 {
		t.Fatal("remote resize changed local viewport")
	}

	cli.SendVerb(ctx, protocol.VerbClaimSize)
	msgs := sent(ch)
	claim, ok := msgs[0].(protocol.MsgVerb)
	if !ok || claim.Widths[1] != 30 || claim.Widths[2] != 40 {
		t.Fatalf("claim widths = %#v", msgs)
	}

	cli.HandleServerMsg(protocol.MsgPaneUpdate{PaneID: 1, Cols: 60, Rows: 22, CursorX: 55, CursorVisible: true})
	if cli.panX[1] != 10 {
		t.Fatal("background output moved pan")
	}
	cli.SendKey(ctx, uv.KeyPressEvent{Code: 'a'})
	if cli.panX[1] != 26 {
		t.Fatalf("typing pan = %d, want 26", cli.panX[1])
	}
	cli.HandleServerMsg(protocol.MsgPaneUpdate{PaneID: 1, Cols: 60, Rows: 22, CursorX: 58, CursorVisible: true})
	if cli.panX[1] != 29 {
		t.Fatalf("resulting cursor pan = %d, want 29", cli.panX[1])
	}

	cli.ToggleFollowPTY()
	if cli.displayWidths[1] != 60 || cli.panX[1] != 0 {
		t.Fatal("follow PTY did not synchronize widths and pan")
	}
}
