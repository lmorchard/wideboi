package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"image"
	"reflect"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

// Drives the real emulator, server patch selection, and protobuf codec for
// one simulated second of 33 ms frame ticks. The input cadence is fixed so
// traffic and update rate can be reproduced without scheduler noise.
func TestPaneTrafficWorkloads(t *testing.T) {
	for _, tc := range []struct {
		name                string
		cols, rows, clients int
		prepare             func(term.Grid)
		write               func(term.Grid, int)
		wantUpdates         int
		wantPatches         bool
	}{
		{
			name: "typing_80x24", cols: 80, rows: 24, clients: 1,
			write: func(g term.Grid, tick int) {
				if tick%2 == 0 {
					_, _ = g.Write([]byte("x"))
				}
			},
			wantUpdates: 15, wantPatches: true,
		},
		{
			name: "scroll_80x24", cols: 80, rows: 24, clients: 1,
			prepare: func(g term.Grid) {
				for row := 0; row < 24; row++ {
					_, _ = g.Write([]byte(fmt.Sprintf("%03d %s\r\n", row, strings.Repeat("x", 70))))
				}
			},
			write: func(g term.Grid, tick int) {
				_, _ = g.Write([]byte(fmt.Sprintf("%03d %s\r\n", tick+24, strings.Repeat("x", 70))))
			},
			wantUpdates: 30, wantPatches: true,
		},
		{
			name: "scrollback_80x24", cols: 80, rows: 24, clients: 1,
			prepare: func(g term.Grid) {
				for row := 0; row < 30; row++ {
					_, _ = g.Write([]byte(fmt.Sprintf("history %03d\r\n", row)))
				}
			},
			write:       func(g term.Grid, tick int) { g.SetScrollOffset(1 - tick%2) },
			wantUpdates: 30, wantPatches: true,
		},
		{
			name: "typing_160x48_four_clients", cols: 160, rows: 48, clients: 4,
			write:       func(g term.Grid, tick int) { _, _ = g.Write([]byte("x")) },
			wantUpdates: 30, wantPatches: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grid := term.NewVT(tc.cols, tc.rows)
			t.Cleanup(func() { _ = grid.Close() })
			if tc.prepare != nil {
				tc.prepare(grid)
			}
			pane := &Pane{id: 1, grid: grid, cols: tc.cols, rows: tc.rows}
			s := &Server{panes: map[int]*Pane{1: pane}}
			channels := make([]*transport.InProcChannel, tc.clients)
			mirrors := make([]protocol.MsgPaneUpdate, tc.clients)
			for i := range channels {
				channels[i] = transport.NewInProcChannel(2)
				s.transports = append(s.transports, channels[i])
			}
			ctx := context.Background()
			s.broadcastPaneUpdates(ctx, false)
			var initialBytes int
			for i, ch := range channels {
				msg := <-ch.ServerSend
				initial, ok := msg.(protocol.MsgPaneUpdate)
				if !ok {
					t.Fatalf("initial message %T is not a snapshot", msg)
				}
				mirrors[i] = initial
				payload, err := protocol.MarshalServer(msg)
				if err != nil {
					t.Fatal(err)
				}
				initialBytes += len(payload)
			}

			var updates, patches, snapshots, sentBytes, fullBytes, gzipBytes, fullGzipBytes int
			for tick := 0; tick < 30; tick++ {
				tc.write(grid, tick)
				s.broadcastPaneUpdates(ctx, false)
				var frameMessages []any
				for _, ch := range channels {
					select {
					case msg := <-ch.ServerSend:
						frameMessages = append(frameMessages, msg)
					default:
					}
				}
				if len(frameMessages) == 0 {
					continue
				}
				if len(frameMessages) != tc.clients {
					t.Fatalf("tick %d delivered %d/%d client messages", tick, len(frameMessages), tc.clients)
				}
				full, ok := pane.UpdateMessage()
				if !ok {
					t.Fatal("pane closed during measurement")
				}
				full.Generation = pane.Generation()
				fullPayload, err := protocol.MarshalServer(full)
				if err != nil {
					t.Fatal(err)
				}
				for i, msg := range frameMessages {
					payload, err := protocol.MarshalServer(msg)
					if err != nil {
						t.Fatal(err)
					}
					updates++
					sentBytes += len(payload)
					fullBytes += len(fullPayload)
					gzipBytes += gzipSize(t, payload)
					fullGzipBytes += gzipSize(t, fullPayload)
					switch msg.(type) {
					case protocol.MsgPanePatch:
						patches++
						var valid bool
						mirrors[i], valid = protocol.ApplyPanePatch(mirrors[i], msg.(protocol.MsgPanePatch))
						if !valid {
							t.Fatalf("tick %d client %d rejected its patch", tick, i)
						}
					case protocol.MsgPaneUpdate:
						snapshots++
						mirrors[i] = msg.(protocol.MsgPaneUpdate)
					default:
						t.Fatalf("unexpected pane message %T", msg)
					}
					if !reflect.DeepEqual(mirrors[i], full) {
						t.Fatalf("tick %d client %d mirror differs from full render", tick, i)
					}
				}
			}
			if updates != tc.wantUpdates*tc.clients {
				t.Fatalf("delivered %d updates, want %d", updates, tc.wantUpdates*tc.clients)
			}
			if tc.wantPatches && patches != updates || !tc.wantPatches && snapshots != updates {
				t.Fatalf("patches=%d snapshots=%d updates=%d", patches, snapshots, updates)
			}
			if tc.wantPatches && sentBytes >= fullBytes {
				t.Fatalf("patch traffic %d B did not beat full traffic %d B", sentBytes, fullBytes)
			}
			t.Logf("initial=%d B, steady=%d B, full_equivalent=%d B, gzip_steady=%d B, gzip_full_equivalent=%d B, updates=%d, patches=%d, snapshots=%d, updates_per_client_per_s=%.2f, bytes_per_update=%.1f", initialBytes, sentBytes, fullBytes, gzipBytes, fullGzipBytes, updates, patches, snapshots, float64(updates)/float64(tc.clients)/0.99, float64(sentBytes)/float64(updates))
		})
	}
}

func gzipSize(t *testing.T, payload []byte) int {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Len()
}

type midWriteGrid struct {
	term.Grid
	onDraw func()
}

func (g *midWriteGrid) Draw(dst uv.Screen, area image.Rectangle) {
	if g.onDraw != nil {
		g.onDraw()
	}
	g.Grid.Draw(dst, area)
}

func (g *midWriteGrid) DrawAt(dst uv.Screen, area image.Rectangle, offset int) {
	if g.onDraw != nil {
		g.onDraw()
	}
	g.Grid.DrawAt(dst, area, offset)
}

func TestPaneUpdatePatchBaselineRetainedOnMidSendWrite(t *testing.T) {
	vt := term.NewVT(80, 24)
	t.Cleanup(func() { _ = vt.Close() })
	_, _ = vt.Write([]byte("initial line\r\n"))

	var writeDuringDraw bool
	grid := &midWriteGrid{
		Grid: vt,
		onDraw: func() {
			if writeDuringDraw {
				writeDuringDraw = false
				_, _ = vt.Write([]byte("second line written mid-draw\r\n"))
			}
		},
	}
	pane := &Pane{id: 1, grid: grid, cols: 80, rows: 24}
	s := &Server{panes: map[int]*Pane{1: pane}}
	ch := transport.NewInProcChannel(4)
	s.transports = []transport.Transport{ch}

	ctx := context.Background()

	// 1. Initial broadcast: delivers full snapshot (no baseline yet).
	s.broadcastPaneUpdates(ctx, false)
	msg1 := <-ch.ServerSend
	snap1, ok := msg1.(protocol.MsgPaneUpdate)
	if !ok {
		t.Fatalf("expected initial MsgPaneUpdate, got %T", msg1)
	}
	clientMirror := snap1

	// 2. Second broadcast: write occurs before and during DrawAt, advancing Generation mid-send.
	_, _ = vt.Write([]byte("line 2\r\n"))
	writeDuringDraw = true
	s.broadcastPaneUpdates(ctx, false)
	msg2 := <-ch.ServerSend
	switch m := msg2.(type) {
	case protocol.MsgPaneUpdate:
		clientMirror = m
	case protocol.MsgPanePatch:
		var valid bool
		clientMirror, valid = protocol.ApplyPanePatch(clientMirror, m)
		if !valid {
			t.Fatalf("patch rejected on second broadcast: %+v", m)
		}
	default:
		t.Fatalf("unexpected message type %T", msg2)
	}

	// 3. Third broadcast: pane generation was advanced by the mid-send write.
	// Because the frame from broadcast 2 was delivered, broadcast 3 should send
	// a patch against broadcast 2's frame, NOT a full snapshot.
	s.broadcastPaneUpdates(ctx, false)
	select {
	case msg3 := <-ch.ServerSend:
		patch, ok := msg3.(protocol.MsgPanePatch)
		if !ok {
			t.Fatalf("expected MsgPanePatch after mid-send write, got %T", msg3)
		}
		var valid bool
		clientMirror, valid = protocol.ApplyPanePatch(clientMirror, patch)
		if !valid {
			t.Fatalf("client failed to apply patch: %+v", patch)
		}
		full, ok := pane.UpdateMessage()
		if !ok {
			t.Fatal("pane.UpdateMessage failed")
		}
		full.Generation = pane.Generation()
		if !reflect.DeepEqual(clientMirror, full) {
			t.Fatalf("reconstructed mirror differs from full render:\nmirror: %+v\nfull: %+v", clientMirror, full)
		}
	default:
		t.Fatal("no update sent on third broadcast")
	}
}
