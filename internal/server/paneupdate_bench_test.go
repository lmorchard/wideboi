package server

import (
	"fmt"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// Isolates the full-grid copy cost that formerly ran under Server.mu.
func BenchmarkPaneUpdateRender(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {160, 48}, {240, 72}} {
		b.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(b *testing.B) {
			p := &Pane{id: 1, grid: newStatusGrid(protocol.StatusIdle), cols: size[0], rows: size[1]}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = p.UpdateMessage()
			}
		})
	}
}

// Compares the protobuf envelopes sent by socket and WebSocket transports.
// Scroll-like changes fall back to full snapshots; the all-row patch
// measures why that fallback helps.
func BenchmarkPaneWirePayload(b *testing.B) {
	for _, tc := range []struct {
		name                string
		cols, rows, changed int
	}{
		{"typing_80x24", 80, 24, 1},
		{"scroll_80x24", 80, 24, 24},
		{"typing_160x48", 160, 48, 1},
	} {
		lines := make([]protocol.LineData, tc.rows)
		for y := range lines {
			lines[y] = make(protocol.LineData, tc.cols)
			for x := range lines[y] {
				lines[y][x] = protocol.CellData{Content: "x", Width: 1}
			}
		}
		full := protocol.MsgPaneUpdate{PaneID: 1, Generation: 2, Cols: tc.cols, Rows: tc.rows, Lines: lines, CursorVisible: true}
		patch := protocol.MsgPanePatch{PaneID: 1, Cols: tc.cols, Rows: tc.rows,
			BaseGeneration: 1, Generation: 2, CursorVisible: true}
		for y := 0; y < tc.changed; y++ {
			patch.ChangedRows = append(patch.ChangedRows, protocol.PaneRow{Y: y, Cells: lines[y]})
		}
		for _, variant := range []struct {
			name  string
			value any
		}{
			{"full", full}, {"rows", patch},
		} {
			b.Run(tc.name+"/"+variant.name, func(b *testing.B) {
				data, err := protocol.MarshalServer(variant.value)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(data)), "wire_B")
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := protocol.MarshalServer(variant.value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
