package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"text/tabwriter"
	"time"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// runStatus connects to the server and outputs the current layout snapshot.
func runStatus(cfg config.Config, jsonOut bool, w io.Writer) error {
	conn, err := net.Dial("unix", cfg.Socket)
	if err != nil {
		return fmt.Errorf("no wideboi server running at %s: %w", cfg.Socket, err)
	}
	if err := handshakeServer(conn, cfg.Socket); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)

	// Ask the server for the current layout state
	cc.SendClient(ctx, protocol.MsgStatusRequest{})

	// Await the first message, which should be MsgLayoutSnapshot
	select {
	case msg, ok := <-cc.ServerSendChan():
		if !ok {
			return fmt.Errorf("server closed connection before sending state")
		}
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			return fmt.Errorf("expected MsgLayoutSnapshot, got %T", msg)
		}

		if jsonOut {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(snap)
		}

		tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "PANE ID\tWIDTH\tHEIGHT\tSTATUS\tTITLE")
		for _, col := range snap.Columns {
			status := snap.PaneStatuses[col.PaneID]
			title := snap.PaneTitles[col.PaneID]
			fmt.Fprintf(tw, "%d\t%d\t%d\t%s\t%s\n", col.PaneID, col.Width, col.Height, statusName(status), title)
		}
		return tw.Flush()

	case <-time.After(2 * time.Second):
		return fmt.Errorf("timeout waiting for server state")
	}
}

func statusName(status protocol.PaneStatus) string {
	switch status {
	case protocol.StatusWorking:
		return "working"
	case protocol.StatusNeedsInput:
		return "needs input"
	case protocol.StatusDone:
		return "done"
	case protocol.StatusFailed:
		return "failed"
	default:
		return "idle"
	}
}
