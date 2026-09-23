package client_test

import (
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

	cols := []*protocol.ColumnData{
		&protocol.ColumnData{PaneId: 1, Width: 40, Height: 28},
		&protocol.ColumnData{PaneId: 2, Width: 40, Height: 28},
	}

	snap := &protocol.ServerEnvelope{Payload: &protocol.ServerEnvelope_LayoutSnapshot{LayoutSnapshot: &protocol.MsgLayoutSnapshot{
		Columns:     cols,
		FocusPaneId: 1}}}

	cli.HandleServerMsg(snap)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cli.SendResize(ctx, 120, 30)

	if cli.FocusPaneID() != 1 {
		t.Errorf("FocusPaneID = %d, want 1", cli.FocusPaneID())
	}
}
