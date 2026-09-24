package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

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
			wantUpdates: 30,
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
			for i := range channels {
				channels[i] = transport.NewInProcChannel(2)
				s.transports = append(s.transports, channels[i])
			}
			ctx := context.Background()
			s.broadcastPaneUpdates(ctx, false)
			var initialBytes int
			for _, ch := range channels {
				msg := <-ch.ServerSend
				if _, ok := msg.(protocol.MsgPaneUpdate); !ok {
					t.Fatalf("initial message %T is not a snapshot", msg)
				}
				payload, err := protocol.MarshalServer(msg)
				if err != nil {
					t.Fatal(err)
				}
				initialBytes += len(payload)
			}

			var updates, patches, snapshots, sentBytes, fullBytes int
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
				for _, msg := range frameMessages {
					payload, err := protocol.MarshalServer(msg)
					if err != nil {
						t.Fatal(err)
					}
					updates++
					sentBytes += len(payload)
					fullBytes += len(fullPayload)
					switch msg.(type) {
					case protocol.MsgPanePatch:
						patches++
					case protocol.MsgPaneUpdate:
						snapshots++
					default:
						t.Fatalf("unexpected pane message %T", msg)
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
			t.Logf("initial=%d B, steady=%d B, full_equivalent=%d B, updates=%d, patches=%d, snapshots=%d, updates_per_client_per_s=%.2f, bytes_per_update=%.1f", initialBytes, sentBytes, fullBytes, updates, patches, snapshots, float64(updates)/float64(tc.clients)/0.99, float64(sentBytes)/float64(updates))
		})
	}
}
