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

// runTrafficStatus asks the server for its traffic counters (#179) and
// prints them. This connection is an ordinary client transport, so
// broadcasts can arrive ahead of the reply; they are skipped.
func runTrafficStatus(cfg config.Config, jsonOut bool, w io.Writer) error {
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
	cc.SendClient(ctx, protocol.MsgTrafficRequest{})

	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg, ok := <-cc.ServerSendChan():
			if !ok {
				return fmt.Errorf("server closed connection before sending traffic stats")
			}
			stats, ok := msg.(protocol.MsgTrafficStats)
			if !ok {
				continue
			}
			if jsonOut {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(stats)
			}
			return formatTraffic(w, stats)
		case <-timeout:
			return fmt.Errorf("timeout waiting for traffic stats")
		}
	}
}

// formatTraffic prints one row per attached client, a departed row when
// clients have left, and a session line with uptime and timing. The
// session's encode timing sums attached and departed clients.
func formatTraffic(w io.Writer, stats protocol.MsgTrafficStats) error {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "CLIENT\tTRANSPORT\tSECS\tUPD/S\tFULL\tROW\tSHIFT\tROWS\tRESYNC\tFAIL\tPAYLOAD\tWIRE\tB/UPD\tENC µs")
	row := func(client, transport, secs, rate string, c protocol.ClientTraffic) {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			client, transport, secs, rate, c.FullUpdates, c.RowPatches, c.ShiftPatches, c.ChangedRows,
			c.ResyncRequests, c.SendFailures, c.PayloadBytes, c.WireBytes, perUpdate(c.PanePayloadBytes, c),
			avgMicros(c.Encode))
	}
	encode := stats.Departed.Encode
	for _, c := range stats.Clients {
		encode.Merge(c.Encode)
		secs := float64(c.ConnectedMillis) / 1000
		rate := 0.0
		if secs > 0 {
			rate = float64(updates(c)) / secs
		}
		row(fmt.Sprint(c.ClientID), c.Transport, fmt.Sprintf("%.1f", secs), fmt.Sprintf("%.1f", rate), c)
	}
	if stats.Departed != (protocol.ClientTraffic{}) {
		row("departed", "-", "-", "-", stats.Departed)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	session := fmt.Sprintf("uptime %.1fs | ", float64(stats.UptimeMillis)/1000)
	if stats.TimingEnabled {
		session += "render " + formatTiming(stats.Render) + " | build " + formatTiming(stats.BuildPatch) +
			" | encode " + formatTiming(encode)
	} else {
		session += "timing off (WIDEBOI_TRAFFIC_TIMING=1)"
	}
	_, err := fmt.Fprintln(w, session)
	return err
}

// updates is every pane delivery a client accepted, whatever its kind.
func updates(c protocol.ClientTraffic) uint64 {
	return c.FullUpdates + c.RowPatches + c.ShiftPatches
}

func perUpdate(bytes uint64, c protocol.ClientTraffic) uint64 {
	if n := updates(c); n > 0 {
		return bytes / n
	}
	return 0
}

// avgMicros is the mean duration in microseconds, "-" when nothing was
// timed.
func avgMicros(t protocol.TimingStat) string {
	if t.Count == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", float64(t.TotalNanos)/float64(t.Count)/1000)
}

func formatTiming(t protocol.TimingStat) string {
	var avg time.Duration
	if t.Count > 0 {
		avg = time.Duration(t.TotalNanos / t.Count)
	}
	return fmt.Sprintf("n=%d avg=%s max=%s", t.Count, avg, time.Duration(t.MaxNanos))
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
