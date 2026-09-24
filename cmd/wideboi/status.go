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

type statusOutput struct {
	Columns      []protocol.ColumnData            `json:"columns"`
	PaneStatuses map[int]protocol.PaneStatus      `json:"pane_statuses"`
	PaneTitles   map[int]string                   `json:"pane_titles"`
	PaneMetadata map[int]protocol.MsgPaneMetadata `json:"pane_metadata"`
}

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

	var snap *protocol.MsgLayoutSnapshot
	metas := make(map[int]protocol.MsgPaneMetadata)
	deadline := time.After(2 * time.Second)

	for snap == nil {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				return fmt.Errorf("server closed connection before sending state")
			}
			switch m := msg.(type) {
			case protocol.MsgLayoutSnapshot:
				snap = &m
			case protocol.MsgPaneMetadata:
				metas[m.PaneID] = m
			}
		case <-deadline:
			return fmt.Errorf("timeout waiting for server state")
		}
	}

	// Collect metadata for all panes in the snapshot
	needed := make(map[int]bool, len(snap.Columns))
	for _, col := range snap.Columns {
		if _, ok := metas[col.PaneID]; !ok {
			needed[col.PaneID] = true
		}
	}

	for len(needed) > 0 {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				return fmt.Errorf("server closed connection before sending metadata")
			}
			if m, ok := msg.(protocol.MsgPaneMetadata); ok {
				metas[m.PaneID] = m
				delete(needed, m.PaneID)
			}
		case <-deadline:
			return fmt.Errorf("timeout waiting for server state")
		}
	}

	if jsonOut {
		out := statusOutput{
			Columns:      snap.Columns,
			PaneStatuses: snap.PaneStatuses,
			PaneTitles:   snap.PaneTitles,
			PaneMetadata: metas,
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "PANE ID\tWIDTH\tHEIGHT\tSTATUS\tTITLE\tCWD")
	for _, col := range snap.Columns {
		status := snap.PaneStatuses[col.PaneID]
		title := snap.PaneTitles[col.PaneID]
		cwd := "-"
		if m, ok := metas[col.PaneID]; ok && m.CWD != "" {
			cwd = m.CWD
		}
		fmt.Fprintf(tw, "%d\t%d\t%d\t%s\t%s\t%s\n", col.PaneID, col.Width, col.Height, statusName(status), title, cwd)
	}
	return tw.Flush()
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
