package protocol

import (
	"fmt"
	"reflect"
	"testing"
)

func BenchmarkPanePatchBuildAndApply(b *testing.B) {
	for _, size := range [][2]int{{80, 24}, {160, 48}} {
		base := MsgPaneUpdate{PaneID: 1, Generation: 1, Cols: size[0], Rows: size[1], Lines: make([]LineData, size[1])}
		for y := range base.Lines {
			base.Lines[y] = make(LineData, size[0])
			for x := range base.Lines[y] {
				base.Lines[y][x] = CellData{Content: "x", Width: 1}
			}
		}
		next := base
		next.Generation = 2
		next.Lines = append([]LineData(nil), base.Lines...)
		next.Lines[0] = append(LineData(nil), base.Lines[0]...)
		next.Lines[0][0].Content = "y"
		patch, ok := BuildPanePatch(base, next)
		if !ok {
			b.Fatal("one-row patch not built")
		}
		b.Run(fmt.Sprintf("%dx%d/build", size[0], size[1]), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = BuildPanePatch(base, next)
			}
		})
		b.Run(fmt.Sprintf("%dx%d/apply", size[0], size[1]), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = ApplyPanePatch(base, patch)
			}
		})
	}
}

func paneFixture() MsgPaneUpdate {
	lines := make([]LineData, 4)
	for y := range lines {
		lines[y] = make(LineData, 4)
		for x := range lines[y] {
			lines[y][x] = CellData{Content: " ", Width: 1}
		}
	}
	return MsgPaneUpdate{PaneID: 7, Generation: 1, Cols: 4, Rows: 4, Lines: lines, CursorVisible: true}
}

func TestPanePatchReconstructsStyledWideAndCursorChanges(t *testing.T) {
	base := paneFixture()
	next := paneFixture()
	next.Generation = 2
	next.Lines[1][0] = CellData{Content: "世", Width: 2, Style: StyleData{Fg: ColorData{Kind: ColorIndexed, Index: 42}, Attrs: 1}}
	next.Lines[1][1] = CellData{Content: " ", Width: 1}
	next.CursorX, next.CursorY = 2, 1
	next.MouseTracking = true
	patch, ok := BuildPanePatch(base, next)
	if !ok || len(patch.ChangedRows) != 1 || patch.ChangedRows[0].Y != 1 {
		t.Fatalf("build patch = %+v, %v", patch, ok)
	}
	got, ok := ApplyPanePatch(base, patch)
	if !ok || !reflect.DeepEqual(got, next) {
		t.Fatalf("reconstructed pane differs: got %+v, want %+v", got, next)
	}
	if !reflect.DeepEqual(base, paneFixture()) {
		t.Fatal("patch mutated its baseline")
	}

	cursorOnly := next
	cursorOnly.Generation = 3
	cursorOnly.CursorVisible = false
	patch, ok = BuildPanePatch(next, cursorOnly)
	if !ok || len(patch.ChangedRows) != 0 {
		t.Fatalf("cursor-only patch = %+v, %v", patch, ok)
	}
	got, ok = ApplyPanePatch(next, patch)
	if !ok || !reflect.DeepEqual(got, cursorOnly) {
		t.Fatalf("cursor-only reconstruction differs: got %+v, want %+v", got, cursorOnly)
	}
}

func TestPanePatchFallsBackOrRejectsInvalidBaseline(t *testing.T) {
	base := paneFixture()
	next := paneFixture()
	next.Generation = 2
	for y := range next.Lines {
		next.Lines[y][0].Content = "x"
	}
	if _, ok := BuildPanePatch(base, next); ok {
		t.Fatal("scroll-like update should use a full snapshot")
	}
	next.Rows = 5
	if _, ok := BuildPanePatch(base, next); ok {
		t.Fatal("resize should use a full snapshot")
	}
	next = paneFixture()
	next.Generation = 2
	next.Lines[0][0].Content = "x"
	patch, ok := BuildPanePatch(base, next)
	if !ok {
		t.Fatal("one-row patch was not built")
	}
	stale := base
	stale.Generation = 0
	if _, ok := ApplyPanePatch(stale, patch); ok {
		t.Fatal("stale baseline accepted a patch")
	}
	patch.ChangedRows = append(patch.ChangedRows, patch.ChangedRows[0])
	if _, ok := ApplyPanePatch(base, patch); ok {
		t.Fatal("duplicate row accepted")
	}
}
