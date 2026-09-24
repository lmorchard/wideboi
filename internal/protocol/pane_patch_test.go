package protocol

import (
	"bytes"
	"compress/gzip"
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

func BenchmarkScrollPatchVsSnapshot(b *testing.B) {
	base := MsgPaneUpdate{PaneID: 1, Generation: 1, Cols: 80, Rows: 24, Lines: make([]LineData, 24)}
	for y := range base.Lines {
		base.Lines[y] = make(LineData, 80)
		for x := range base.Lines[y] {
			base.Lines[y][x] = CellData{Content: "x", Width: 1}
		}
		base.Lines[y][0].Content = fmt.Sprint(y)
	}
	next := base
	next.Generation = 2
	next.Lines = append([]LineData(nil), base.Lines[1:]...)
	next.Lines = append(next.Lines, append(LineData(nil), base.Lines[0]...))
	next.Lines[23][0].Content = "new"
	patch, ok := BuildPanePatch(base, next)
	if !ok || patch.ShiftRows != -1 {
		b.Fatal("fixture did not produce a shift patch")
	}
	for _, tc := range []struct {
		name string
		msg  any
	}{{"snapshot", next}, {"shift", patch}} {
		b.Run(tc.name+"/marshal", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := MarshalServer(tc.msg); err != nil {
					b.Fatal(err)
				}
			}
		})
		payload, err := MarshalServer(tc.msg)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(tc.name+"/gzip", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				_, _ = w.Write(payload)
				if err := w.Close(); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(buf.Len()), "gzip_B")
			}
		})
	}
	b.Run("build_shift", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = BuildPanePatch(base, next)
		}
	})
	b.Run("apply_shift", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = ApplyPanePatch(base, patch)
		}
	})
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
	next.ScrollOffset = 5
	next.ScrollbackLen = 42
	next.UnreadOutput = true
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

func TestPanePatchWholePaneShifts(t *testing.T) {
	base := paneFixture()
	for y := range base.Lines {
		base.Lines[y][0].Content = fmt.Sprint(y)
	}
	base.Lines[1][1] = CellData{Content: "世", Width: 2, Style: StyleData{Fg: ColorData{Kind: ColorIndexed, Index: 42}}}
	for _, shift := range []int{-1, 1, -2, 2} {
		next := paneFixture()
		next.Generation = 2
		for y := range next.Lines {
			from := y - shift
			if from >= 0 && from < base.Rows {
				next.Lines[y] = base.Lines[from]
			} else {
				next.Lines[y][0].Content = fmt.Sprintf("new%d", y)
			}
		}
		patch, ok := BuildPanePatch(base, next)
		if !ok || patch.ShiftRows != shift {
			t.Fatalf("shift %d: patch=%+v ok=%v", shift, patch, ok)
		}
		wire, err := MarshalServer(patch)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := UnmarshalServer(wire)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := ApplyPanePatch(base, decoded.(MsgPanePatch))
		if !ok || !reflect.DeepEqual(got, next) {
			t.Fatalf("shift %d reconstructed %+v, want %+v", shift, got, next)
		}
		stale := base
		stale.Generation = 0
		if _, ok := ApplyPanePatch(stale, patch); ok {
			t.Fatal("stale shift patch accepted")
		}
		patch.ChangedRows = nil
		if _, ok := ApplyPanePatch(base, patch); ok {
			t.Fatal("incomplete shift patch accepted")
		}
	}
}

func TestPanePatchAcceptsWiderEdgeBandButRejectsInteriorReplacement(t *testing.T) {
	base := paneFixture()
	for y := range base.Lines {
		base.Lines[y][0].Content = fmt.Sprint(y)
	}
	next := paneFixture()
	next.Generation = 2
	next.Lines[0] = base.Lines[1]
	next.Lines[1] = base.Lines[2]
	next.Lines[2][0].Content = "new 2"
	next.Lines[3][0].Content = "new 3"
	patch, ok := BuildPanePatch(base, next)
	if !ok || patch.ShiftRows != -1 || len(patch.ChangedRows) != 2 {
		t.Fatalf("wider edge band: patch=%+v ok=%v", patch, ok)
	}
	got, ok := ApplyPanePatch(base, patch)
	if !ok || !reflect.DeepEqual(got, next) {
		t.Fatalf("wider edge band reconstructed %+v, want %+v", got, next)
	}
	patch.ChangedRows[0].Y = 1
	if _, ok := ApplyPanePatch(base, patch); ok {
		t.Fatal("interior replacement accepted for a shift patch")
	}
}

func TestPanePatchShiftNeedsExactUnambiguousInterior(t *testing.T) {
	base := paneFixture()
	for y := range base.Lines {
		base.Lines[y][0].Content = fmt.Sprint(y)
	}
	next := paneFixture()
	next.Generation = 2
	for y := 0; y < 3; y++ {
		next.Lines[y] = base.Lines[y+1]
	}
	next.Lines[3][0].Content = "new"
	next.Lines[1] = append(LineData(nil), next.Lines[1]...)
	next.Lines[1][1].Content = "edited"
	if _, ok := BuildPanePatch(base, next); ok {
		t.Fatal("concurrent interior edit should fall back to a snapshot")
	}
	blank := paneFixture()
	blank.Generation = 2
	for y := range blank.Lines {
		blank.Lines[y][0].Content = "same"
	}
	base = blank
	base.Generation = 1
	if _, ok := BuildPanePatch(base, blank); !ok {
		t.Fatal("unchanged rows should permit a cursor-only patch")
	}
	partial := paneFixture()
	partial.Generation = 2
	for y := range partial.Lines {
		partial.Lines[y][0].Content = "changed"
	}
	if _, ok := BuildPanePatch(base, partial); ok {
		t.Fatal("unrelated rows should fall back to a snapshot")
	}
}
