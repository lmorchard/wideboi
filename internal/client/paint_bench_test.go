package client

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

// benchRing is how many distinct transitions each case cycles through.
// Messages are built before the timer starts; a ring keeps memory flat
// however large b.N gets. It is larger than any benchmarked pane height,
// so no line repeats within one frame and a shift is never ambiguous.
const benchRing = 128

// BenchmarkClientApplyAndDraw measures what one delivered message costs the
// terminal client end to end: ApplyPanePatch (inside HandleServerMsg), the
// full-mirror rewrite in applyPaneUpdateLocked, and the next Draw.
//
// "full" sends a complete MsgPaneUpdate, "row" a patch that replaces one
// row (a ticking status line), "shift" a patch that scrolls a log up by
// one line and supplies the new bottom row.
func BenchmarkClientApplyAndDraw(b *testing.B) {
	for _, size := range []struct{ cols, rows int }{{80, 24}, {160, 48}} {
		for _, kind := range []string{"full", "row", "shift"} {
			b.Run(fmt.Sprintf("%s_%dx%d", kind, size.cols, size.rows), func(b *testing.B) {
				tp := transport.NewInProcChannel(1024)
				cli := NewClient(tp, size.cols+2, size.rows+2, "C-b")
				cli.SetLayoutMode(protocol.LayoutScroll)
				cli.HandleServerMsg(protocol.MsgLayoutSnapshot{Columns: []protocol.ColumnData{{PaneID: 1, Width: size.cols, Height: size.rows}}})

				frames := make([]protocol.MsgPaneUpdate, benchRing)
				for k := range frames {
					frames[k] = benchFrame(kind, size.cols, size.rows, k)
				}
				ring := make([]any, benchRing)
				for k := range ring {
					ring[k] = benchMessage(b, kind, frames[k], frames[(k+1)%benchRing])
				}

				cli.HandleServerMsg(frames[0])
				scr := newFakeHostScreen(size.cols+2, size.rows+2)
				cli.Draw(scr)
				if !strings.Contains(strings.Join(scr.text(), "\n"), "INFO") {
					b.Fatal("pane content is not on screen after setup")
				}

				gen := frames[0].Generation
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// Restamp generations so every message chains from the
					// last accepted one; the struct copy shares Lines.
					switch m := ring[i%benchRing].(type) {
					case protocol.MsgPaneUpdate:
						m.Generation = gen + 1
						cli.HandleServerMsg(m)
					case protocol.MsgPanePatch:
						m.BaseGeneration, m.Generation = gen, gen+1
						cli.HandleServerMsg(m)
					}
					gen++
					cli.Draw(scr)
				}
				b.StopTimer()

				select {
				case msg := <-tp.ClientSend:
					b.Fatalf("client sent %#v during the benchmark; a patch failed to apply", msg)
				default:
				}
				if got := cli.paneUpdates[1].Generation; got != gen {
					b.Fatalf("client generation = %d, want %d", got, gen)
				}
			})
		}
	}
}

// benchFrame builds frame k of a kind's cycle as styled log lines. For
// "shift" row y shows log line k+y, so frame k+1 is frame k scrolled up
// by one. For "row" only row 0 (a status line) varies with k. "full"
// reuses the scrolling content: every row differs between frames.
func benchFrame(kind string, cols, rows, k int) protocol.MsgPaneUpdate {
	lines := make([]protocol.LineData, rows)
	for y := range lines {
		switch {
		case kind == "row" && y == 0:
			lines[y] = benchLine(cols, fmt.Sprintf("status tick %04d", k), k)
		case kind == "row":
			lines[y] = benchLine(cols, "", y)
		default:
			lines[y] = benchLine(cols, "", (k+y)%benchRing)
		}
	}
	return protocol.MsgPaneUpdate{
		PaneID: 1, Generation: uint64(k) + 1, Cols: cols, Rows: rows, Lines: lines,
		CursorX: 0, CursorY: rows - 1, CursorVisible: true,
	}
}

var benchLevels = []struct {
	name  string
	color uint8
}{{"INFO ", 2}, {"WARN ", 3}, {"DEBUG", 4}, {"ERROR", 1}}

// benchLine renders log line n the way a coloured prompt or build log
// looks: a dim timestamp, a bold coloured level, then plain text padded
// with spaces to the full width. extra, if set, prefixes the message.
func benchLine(cols int, extra string, n int) protocol.LineData {
	level := benchLevels[n%len(benchLevels)]
	stamp := fmt.Sprintf("12:%02d:%02d ", n/60%60, n%60)
	msg := fmt.Sprintf("%s line %d: compiling internal/pkg%d/file%d.go ok", extra, n, n%7, n%13)

	line := make(protocol.LineData, 0, cols)
	add := func(text string, style protocol.StyleData) {
		for _, r := range text {
			if len(line) == cols {
				return
			}
			line = append(line, protocol.CellData{Content: string(r), Width: 1, Style: style})
		}
	}
	add(stamp, protocol.StyleData{Fg: protocol.ColorData{Kind: protocol.ColorIndexed, Index: 244}})
	add(level.name, protocol.StyleData{
		Fg:    protocol.ColorData{Kind: protocol.ColorBasic, Index: level.color},
		Attrs: uint8(uv.AttrBold),
	})
	add(" "+msg, protocol.StyleData{})
	for len(line) < cols {
		line = append(line, protocol.CellData{Content: " ", Width: 1})
	}
	return line
}

// benchMessage returns what the server would send to move a client from
// base to next, and fails the benchmark if BuildPanePatch does not pick
// the kind the case claims to measure.
func benchMessage(b *testing.B, kind string, base, next protocol.MsgPaneUpdate) any {
	b.Helper()
	if kind == "full" {
		return next
	}
	// Generations here only have to satisfy BuildPanePatch; the loop
	// restamps them.
	next.Generation = base.Generation + 1
	patch, ok := protocol.BuildPanePatch(base, next)
	if !ok {
		b.Fatalf("%s: BuildPanePatch declined; a full update would be sent", kind)
	}
	switch kind {
	case "row":
		if patch.ShiftRows != 0 || len(patch.ChangedRows) != 1 {
			b.Fatalf("row: got ShiftRows=%d with %d changed rows, want 0 and 1", patch.ShiftRows, len(patch.ChangedRows))
		}
	case "shift":
		if patch.ShiftRows == 0 {
			b.Fatalf("shift: got a row patch with %d changed rows", len(patch.ChangedRows))
		}
	}
	return patch
}
