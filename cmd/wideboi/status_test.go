package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func mockServer(t *testing.T, dir string, snap protocol.MsgLayoutSnapshot, metas ...protocol.MsgPaneMetadata) string {
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		if _, err := transport.Handshake(conn); err != nil {
			conn.Close()
			return
		}
		sc := transport.NewServerSocketConn(conn, 16)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		sc.RunPumps(ctx)

		// Wait for client requests
		for {
			select {
			case msg, ok := <-sc.ClientSendChan():
				if !ok {
					return
				}
				switch msg.(type) {
				case protocol.MsgStatusRequest:
					sc.SendServer(ctx, snap)
					for _, meta := range metas {
						sc.SendServer(ctx, meta)
					}
				case protocol.MsgWebServerControlRequest:
					sc.SendServer(ctx, protocol.MsgWebServerControlResponse{})
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return sockPath
}

func TestRunStatus(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wb-status")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	snap := protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 1, Width: 80, Height: 24},
			{PaneID: 2, Width: 40, Height: 24},
		},
		PaneStatuses: map[int]protocol.PaneStatus{
			1: protocol.StatusDone,
			2: protocol.StatusWorking,
		},
		PaneTitles: map[int]string{
			1: "vim",
			2: "npm start",
		},
	}

	metas := []protocol.MsgPaneMetadata{
		{PaneID: 1, CWD: "/home/user/vim", UserVars: map[string]string{"foo": "bar"}},
		{PaneID: 2, CWD: "", UserVars: nil},
	}

	sockPath := mockServer(t, dir, snap, metas...)

	var buf bytes.Buffer
	cfg := config.Config{Socket: sockPath}

	err = runStatus(cfg, false, &buf)
	if err != nil {
		t.Fatalf("runStatus error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "PANE ID") || !strings.Contains(out, "CWD") {
		t.Errorf("expected header with CWD in output, got:\n%s", out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "vim") || !strings.Contains(out, "done") || !strings.Contains(out, "/home/user/vim") {
		t.Errorf("expected pane 1 info with CWD in output, got:\n%s", out)
	}
	if !strings.Contains(out, "2") || !strings.Contains(out, "npm start") || !strings.Contains(out, "working") || !strings.Contains(out, "-") {
		t.Errorf("expected pane 2 info with unset CWD in output, got:\n%s", out)
	}
}

func TestRunStatusJSON(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wb-status")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	snap := protocol.MsgLayoutSnapshot{
		Columns: []protocol.ColumnData{
			{PaneID: 3, Width: 100, Height: 50},
		},
		PaneStatuses: map[int]protocol.PaneStatus{3: protocol.StatusIdle},
		PaneTitles:   map[int]string{3: "bash"},
	}

	metas := []protocol.MsgPaneMetadata{
		{PaneID: 3, CWD: "/home/user/bash", UserVars: map[string]string{"agent": "claude"}},
	}

	sockPath := mockServer(t, dir, snap, metas...)

	var buf bytes.Buffer
	cfg := config.Config{Socket: sockPath}

	err = runStatus(cfg, true, &buf)
	if err != nil {
		t.Fatalf("runStatus error: %v", err)
	}

	var parsed struct {
		Columns      []protocol.ColumnData            `json:"columns"`
		PaneStatuses map[int]protocol.PaneStatus      `json:"pane_statuses"`
		PaneTitles   map[int]string                   `json:"pane_titles"`
		PaneMetadata map[int]protocol.MsgPaneMetadata `json:"pane_metadata"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nOutput was:\n%s", err, buf.String())
	}
	if len(parsed.Columns) != 1 || parsed.PaneStatuses[3] != protocol.StatusIdle {
		t.Errorf("parsed JSON did not match expected structure: %+v", parsed)
	}
	if parsed.PaneMetadata[3].CWD != "/home/user/bash" {
		t.Errorf("parsed JSON CWD = %q, want /home/user/bash", parsed.PaneMetadata[3].CWD)
	}
	if parsed.PaneMetadata[3].UserVars["agent"] != "claude" {
		t.Errorf("parsed JSON UserVars[agent] = %q, want claude", parsed.PaneMetadata[3].UserVars["agent"])
	}

	// Verify exact snake_case JSON field names
	var raw map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"columns", "pane_statuses", "pane_titles", "pane_metadata"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("JSON output missing top-level key %q", key)
		}
	}
	// Statuses are names, not the enum's integers (#227).
	if got := raw["pane_statuses"].(map[string]any)["3"]; got != "idle" {
		t.Errorf("pane_statuses[3] = %#v, want \"idle\"", got)
	}
	col := raw["columns"].([]any)[0].(map[string]any)
	for _, key := range []string{"pane_id", "width", "height"} {
		if _, ok := col[key]; !ok {
			t.Errorf("columns[0] missing key %q: %+v", key, col)
		}
	}
	paneMeta := raw["pane_metadata"].(map[string]any)["3"].(map[string]any)
	for _, key := range []string{"pane_id", "cwd", "user_vars"} {
		if _, ok := paneMeta[key]; !ok {
			t.Errorf("pane_metadata missing key %q: %+v", key, paneMeta)
		}
	}

	// Verify legacy unmarshal into MsgLayoutSnapshot still works
	var legacy protocol.MsgLayoutSnapshot
	if err := json.Unmarshal(buf.Bytes(), &legacy); err != nil {
		t.Fatalf("failed to unmarshal into MsgLayoutSnapshot: %v", err)
	}
	if len(legacy.Columns) != 1 || legacy.PaneStatuses[3] != protocol.StatusIdle {
		t.Errorf("legacy unmarshal failed: %+v", legacy)
	}
}

func TestFormatTraffic(t *testing.T) {
	stats := protocol.MsgTrafficStats{
		UptimeMillis: 60000,
		Clients: []protocol.ClientTraffic{{
			ClientID: 1, Transport: "socket", ConnectedMillis: 10000,
			FullUpdates: 2, RowPatches: 15, ShiftPatches: 3, ChangedRows: 40,
			ResyncRequests: 1, PanePayloadBytes: 2000,
		}},
	}
	var buf bytes.Buffer
	if err := formatTraffic(&buf, stats); err != nil {
		t.Fatal(err)
	}
	// UPD/S = (2+15+3)/10s; B/UPD = 2000/20. No departed row: nobody left.
	want := "" +
		"CLIENT  TRANSPORT  SECS  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  FAIL  PAYLOAD  WIRE  B/UPD  ENC µs\n" +
		"1       socket     10.0  2.0    2     15   3      40    1       0     0        0     100    -\n" +
		"uptime 60.0s | timing off (WIDEBOI_TRAFFIC_TIMING=1)\n"
	if got := buf.String(); got != want {
		t.Errorf("formatTraffic:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatTrafficDepartedAndTiming(t *testing.T) {
	stats := protocol.MsgTrafficStats{
		UptimeMillis:  1000,
		TimingEnabled: true,
		Clients: []protocol.ClientTraffic{{
			ClientID: 2, Transport: "websocket", ConnectedMillis: 1000, RowPatches: 3,
			Encode: protocol.TimingStat{Count: 3, TotalNanos: 7500, MaxNanos: 4000},
		}},
		Departed: protocol.ClientTraffic{
			FullUpdates: 4, PanePayloadBytes: 400,
			Encode: protocol.TimingStat{Count: 1, TotalNanos: 500, MaxNanos: 500},
		},
		Render:     protocol.TimingStat{Count: 2, TotalNanos: 3000, MaxNanos: 2000},
		BuildPatch: protocol.TimingStat{Count: 1, TotalNanos: 500, MaxNanos: 500},
	}
	var buf bytes.Buffer
	if err := formatTraffic(&buf, stats); err != nil {
		t.Fatal(err)
	}
	// ENC is per client (7500ns/3, 500ns/1); the session's encode sums
	// attached and departed: n=4, 8000ns/4, max 4µs.
	want := "" +
		"CLIENT    TRANSPORT  SECS  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  FAIL  PAYLOAD  WIRE  B/UPD  ENC µs\n" +
		"2         websocket  1.0   3.0    0     3    0      0     0       0     0        0     0      2.5\n" +
		"departed  -          -     -      4     0    0      0     0       0     0        0     100    0.5\n" +
		"uptime 1.0s | render n=2 avg=1.5µs max=2µs | build n=1 avg=500ns max=500ns | encode n=4 avg=2µs max=4µs\n"
	if got := buf.String(); got != want {
		t.Errorf("formatTraffic:\n%s\nwant:\n%s", got, want)
	}
}

// The requesting connection also receives broadcasts, so the reply may
// arrive behind a layout snapshot or pane update.
func TestRunTrafficStatusSkipsBroadcasts(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wb-status")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	stats := protocol.MsgTrafficStats{UptimeMillis: 5, Clients: []protocol.ClientTraffic{{ClientID: 7, Transport: "websocket", RowPatches: 3}}}
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		if _, err := transport.Handshake(conn); err != nil {
			conn.Close()
			return
		}
		sc := transport.NewServerSocketConn(conn, 4)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		sc.RunPumps(ctx)
		select {
		case msg := <-sc.ClientSendChan():
			if _, ok := msg.(protocol.MsgTrafficRequest); ok {
				sc.SendServer(ctx, protocol.MsgLayoutSnapshot{})
				sc.SendServer(ctx, stats)
			}
		case <-ctx.Done():
		}
	}()

	var buf bytes.Buffer
	if err := runTrafficStatus(config.Config{Socket: sockPath}, true, &buf); err != nil {
		t.Fatalf("runTrafficStatus: %v", err)
	}
	assertTrafficJSONKeys(t, buf.Bytes())
	var parsed protocol.MsgTrafficStats
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, buf.String())
	}
	if len(parsed.Clients) != 1 || parsed.Clients[0].ClientID != 7 || parsed.Clients[0].RowPatches != 3 {
		t.Errorf("parsed %+v", parsed)
	}
}

// assertTrafficJSONKeys checks `status --traffic --json` uses the repo's
// snake_case schema at every level, not Go field names. scripts/traffic.py
// reads these keys.
func assertTrafficJSONKeys(t *testing.T, data []byte) {
	t.Helper()
	timing := []string{"count", "max_nanos", "total_nanos"}
	client := []string{"changed_rows", "client_id", "connected_millis", "encode", "full_updates",
		"messages", "pane_payload_bytes", "payload_bytes", "resync_requests", "row_patches",
		"send_failures", "shift_patches", "transport", "wire_bytes"}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, data)
	}
	checkKeys(t, "top level", top, []string{"build_patch", "clients", "departed", "render",
		"timing_enabled", "uptime_millis"})
	for _, name := range []string{"render", "build_patch"} {
		checkKeys(t, name, object(t, top[name]), timing)
	}
	departed := object(t, top["departed"])
	checkKeys(t, "departed", departed, client)
	checkKeys(t, "departed.encode", object(t, departed["encode"]), timing)
	var clients []map[string]json.RawMessage
	if err := json.Unmarshal(top["clients"], &clients); err != nil || len(clients) == 0 {
		t.Fatalf("clients: %v %s", err, top["clients"])
	}
	checkKeys(t, "clients[0]", clients[0], client)
}

func object(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not an object: %v %s", err, raw)
	}
	return m
}

func checkKeys(t *testing.T, where string, m map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("%s keys = %v, want %v", where, got, want)
	}
}
