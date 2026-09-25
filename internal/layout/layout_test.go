package layout_test

import (
	"image"
	"testing"

	"github.com/lmorchard/wideboi/internal/layout"
	"pgregory.net/rapid"
)

func TestStripInitializesWithOneColumn(t *testing.T) {
	s := layout.NewStrip()
	if s.ColCount() != 0 {
		t.Fatalf("ColCount() = %d, want 0", s.ColCount())
	}

	s.AddColumn(1, 40, 20, 0)
	if s.ColCount() != 1 {
		t.Fatalf("ColCount() = %d, want 1", s.ColCount())
	}
	if got := s.FocusedPaneID(); got != 1 {
		t.Fatalf("FocusedPaneID() = %d, want 1", got)
	}
}

func TestStripNavigationLeftRight(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 20, 0)
	s.AddColumn(2, 40, 20, 0)

	if got := s.FocusedPaneID(); got != 2 {
		t.Fatalf("FocusedPaneID() after 2nd add = %d, want 2", got)
	}

	s.FocusLeft()
	if got := s.FocusedPaneID(); got != 1 {
		t.Fatalf("FocusedPaneID() after FocusLeft = %d, want 1", got)
	}

	s.FocusLeft() // stays at 1
	if got := s.FocusedPaneID(); got != 1 {
		t.Fatalf("FocusedPaneID() at left boundary = %d, want 1", got)
	}

	s.FocusRight()
	if got := s.FocusedPaneID(); got != 2 {
		t.Fatalf("FocusedPaneID() after FocusRight = %d, want 2", got)
	}
}

func TestStripComputesPlacements(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 23, 0)
	s.AddColumn(2, 39, 23, 0) // 40 + 1 divider + 39 = 80 total width
	s.FocusLeft()             // focus column 1

	placements := s.ComputePlacements(80, 24)
	if len(placements) != 2 {
		t.Fatalf("got %d placements, want 2", len(placements))
	}

	// Two columns filling 80x24 viewport
	p1 := placements[0]
	if p1.PaneID != 1 || p1.Dst != image.Rect(0, 1, 40, 23) {
		t.Errorf("p1 = %+v, want Dst (0,1,40,23)", p1)
	}

	p2 := placements[1]
	if p2.PaneID != 2 || p2.Dst != image.Rect(41, 1, 80, 23) {
		t.Errorf("p2 = %+v, want Dst (41,1,80,23)", p2)
	}
}

func TestStripScrollsToKeepFocusVisible(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 50, 23, 0)
	s.AddColumn(2, 50, 23, 0)
	s.AddColumn(3, 50, 23, 0) // Total strip width = 150, viewport = 80

	// Focused on column 3 (rightmost)
	placements := s.ComputePlacements(80, 24)

	// Column 3 must be visible on screen
	var p3 *layout.Placement
	for i := range placements {
		if placements[i].PaneID == 3 {
			p3 = &placements[i]
		}
	}
	if p3 == nil {
		t.Fatal("pane 3 missing from placements")
	}
	if p3.Dst.Max.X > 80 || p3.Dst.Min.X < 0 {
		t.Errorf("focused pane 3 Dst %v not fully visible in 80-wide viewport", p3.Dst)
	}
}

func TestStripKillPaneAdjustsFocus(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 20, 0)
	s.AddColumn(2, 40, 20, 0)
	s.AddColumn(3, 40, 20, 0)

	s.FocusLeft() // focus pane 2
	s.KillPane(2)

	if s.ColCount() != 2 {
		t.Fatalf("ColCount() = %d, want 2", s.ColCount())
	}
	// Focus should move to adjacent pane
	if got := s.FocusedPaneID(); got != 1 && got != 3 {
		t.Fatalf("FocusedPaneID() = %d, want 1 or 3", got)
	}
}

func TestCycleWidthTransitionsPresets(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 99, 20, 0) // Custom initial width (e.g. 200-col host)

	s.CycleWidth(1)
	if w, _ := s.ColumnWidth(1); w != 40 {
		t.Errorf("after 1st cycle from 99: width = %d, want 40", w)
	}

	s.CycleWidth(1)
	if w, _ := s.ColumnWidth(1); w != 60 {
		t.Errorf("after 2nd cycle: width = %d, want 60", w)
	}

	s.CycleWidth(1)
	if w, _ := s.ColumnWidth(1); w != 80 {
		t.Errorf("after 3rd cycle: width = %d, want 80", w)
	}

	s.CycleWidth(1)
	if w, _ := s.ColumnWidth(1); w != 40 {
		t.Errorf("after 4th cycle: width = %d, want 40", w)
	}

	// Also verify 50 (100-col host spawn width)
	s2 := layout.NewStrip()
	s2.AddColumn(1, 50, 20, 0)
	s2.CycleWidth(1)
	if w, _ := s2.ColumnWidth(1); w != 60 {
		t.Errorf("from 50: width = %d, want 60", w)
	}
}

func TestCustomWidthPresets(t *testing.T) {
	s := layout.NewStrip()
	s.SetWidthPresets([]int{50, 100, 150})
	s.AddColumn(1, 40, 20, 0)

	// 40 -> next higher preset is 50
	s.CycleWidth(1)
	if w, _ := s.ColumnWidth(1); w != 50 {
		t.Errorf("width = %d, want 50", w)
	}
	// 50 -> 100
	s.CycleWidth(1)
	if w, _ := s.ColumnWidth(1); w != 100 {
		t.Errorf("width = %d, want 100", w)
	}
	// 100 -> 150
	s.CycleWidth(1)
	if w, _ := s.ColumnWidth(1); w != 150 {
		t.Errorf("width = %d, want 150", w)
	}
	// 150 -> wraps to 50
	s.CycleWidth(1)
	if w, _ := s.ColumnWidth(1); w != 50 {
		t.Errorf("width = %d, want 50", w)
	}

	// Empty presets fallback to default
	s.SetWidthPresets([]int{5, 10}) // below MinColumnWidth (20)
	presets := s.WidthPresets()
	if len(presets) != len(layout.DefaultWidthPresets) {
		t.Fatalf("expected fallback to DefaultWidthPresets, got %v", presets)
	}
}

func TestGrowAndShrinkWidth(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 50, 20, 0)

	s.GrowWidth(1, 10)
	if w, _ := s.ColumnWidth(1); w != 60 {
		t.Errorf("after GrowWidth(10): width = %d, want 60", w)
	}

	s.ShrinkWidth(1, 10)
	if w, _ := s.ColumnWidth(1); w != 50 {
		t.Errorf("after ShrinkWidth(10): width = %d, want 50", w)
	}

	// Shrink clamped to MinColumnWidth
	s.ShrinkWidth(1, 40)
	if w, _ := s.ColumnWidth(1); w != layout.MinColumnWidth {
		t.Errorf("after excessive shrink: width = %d, want MinColumnWidth (%d)", w, layout.MinColumnWidth)
	}

	// Non-positive delta no-ops
	s.GrowWidth(1, 0)
	s.GrowWidth(1, -5)
	s.ShrinkWidth(1, 0)
	s.ShrinkWidth(1, -5)
	if w, _ := s.ColumnWidth(1); w != layout.MinColumnWidth {
		t.Errorf("after no-op grow/shrink: width = %d, want %d", w, layout.MinColumnWidth)
	}
}

func genStrip(t *rapid.T) (*layout.Strip, map[int]int) {
	numCols := rapid.IntRange(1, 10).Draw(t, "numCols")
	s := layout.NewStrip()
	initialWidths := make(map[int]int)
	for i := 1; i <= numCols; i++ {
		w := rapid.IntRange(20, 100).Draw(t, "colWidth")
		h := rapid.IntRange(10, 50).Draw(t, "colHeight")
		s.AddColumn(i, w, h, 0)
		initialWidths[i] = w
	}
	moves := rapid.IntRange(0, 15).Draw(t, "focusMoves")
	for i := 0; i < moves; i++ {
		if rapid.Bool().Draw(t, "focusLeft") {
			s.FocusLeft()
		} else {
			s.FocusRight()
		}
	}
	return s, initialWidths
}

func TestLayoutPropertyInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, initialWidths := genStrip(t)
		vw := rapid.IntRange(20, 200).Draw(t, "viewportWidth")
		vh := rapid.IntRange(5, 60).Draw(t, "viewportHeight")

		placements := s.ComputePlacements(vw, vh)
		focusedID := s.FocusedPaneID()

		// Invariant 1: Focused pane is fully visible in viewport
		var focusedPlacement *layout.Placement
		for i := range placements {
			if placements[i].PaneID == focusedID {
				focusedPlacement = &placements[i]
				break
			}
		}
		if focusedPlacement == nil {
			t.Fatalf("focused pane %d has no placement in %dx%d viewport", focusedID, vw, vh)
		}
		if focusedPlacement.Dst.Min.X < 0 || focusedPlacement.Dst.Max.X > vw {
			t.Fatalf("focused pane Dst X %v outside viewport [0, %d]", focusedPlacement.Dst, vw)
		}

		// Invariant 2: Non-overlapping destination rectangles
		for i := 0; i < len(placements); i++ {
			for j := i + 1; j < len(placements); j++ {
				if inter := placements[i].Dst.Intersect(placements[j].Dst); !inter.Empty() {
					t.Fatalf("placements %d (pane %d) and %d (pane %d) overlap at %v",
						i, placements[i].PaneID, j, placements[j].PaneID, inter)
				}
			}
		}

		// Invariant 3: Cropping equivalence (Src and Dst size match)
		for _, p := range placements {
			if p.Src.Dx() != p.Dst.Dx() || p.Src.Dy() != p.Dst.Dy() {
				t.Fatalf("pane %d Src size (%d,%d) != Dst size (%d,%d)",
					p.PaneID, p.Src.Dx(), p.Src.Dy(), p.Dst.Dx(), p.Dst.Dy())
			}
		}

		// Invariant 4: Logical column width remains equal to assigned width
		for _, paneID := range s.PaneIDs() {
			colW, ok := s.ColumnWidth(paneID)
			if !ok {
				t.Fatalf("ColumnWidth(%d) not found", paneID)
			}
			if colW != initialWidths[paneID] {
				t.Fatalf("pane %d ColumnWidth=%d, want assigned width %d", paneID, colW, initialWidths[paneID])
			}
		}
	})
}

// A move swaps the focused column with its neighbour, focus follows the
// column, and the width travels with it: order is presentation, and a
// pane's logical width is its column's width wherever the column sits.
func TestMoveLeftAndRightSwapWithNeighbourAndKeepFocus(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 20, 0)
	s.AddColumn(2, 50, 20, 0)
	s.AddColumn(3, 60, 20, 0) // focus on 3

	steps := []struct {
		move func()
		want []int
	}{
		{func() { s.MoveLeft(3) }, []int{1, 3, 2}},
		{func() { s.MoveLeft(3) }, []int{3, 1, 2}},
		{func() { s.MoveLeft(3) }, []int{3, 1, 2}}, // left edge: no-op
		{func() { s.MoveRight(3) }, []int{1, 3, 2}},
		{func() { s.MoveRight(3) }, []int{1, 2, 3}},
		{func() { s.MoveRight(3) }, []int{1, 2, 3}}, // right edge: no-op
	}
	for i, st := range steps {
		st.move()
		got := s.PaneIDs()
		if !equalInts(got, st.want) {
			t.Fatalf("step %d: PaneIDs() = %v, want %v", i, got, st.want)
		}
		if f := s.FocusedPaneID(); f != 3 {
			t.Fatalf("step %d: focus = %d, want 3 (focus follows the column)", i, f)
		}
		if w, _ := s.ColumnWidth(3); w != 60 {
			t.Fatalf("step %d: ColumnWidth(3) = %d, want 60", i, w)
		}
	}

	empty := layout.NewStrip()
	empty.MoveLeft(1)
	empty.MoveRight(1)
	if empty.ColCount() != 0 {
		t.Fatalf("moves on an empty strip changed it")
	}
}

// FocusLast returns to the previous pane, and the pane it leaves becomes
// the new previous one, so pressing it twice is a round trip.
func TestFocusLastTogglesBetweenTwoPanes(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 20, 0)
	s.AddColumn(2, 40, 20, 0)
	s.AddColumn(3, 40, 20, 0) // focus 3

	s.FocusPaneID(1)
	s.FocusLast()
	if got := s.FocusedPaneID(); got != 3 {
		t.Fatalf("after FocusLast: focus = %d, want 3", got)
	}
	s.FocusLast()
	if got := s.FocusedPaneID(); got != 1 {
		t.Fatalf("after second FocusLast: focus = %d, want 1", got)
	}
}

// Every way focus can move feeds the record -- keys, clicks, attention
// jumps and digit jumps all end in one of these.
func TestFocusLastTracksEveryFocusMove(t *testing.T) {
	for name, move := range map[string]func(*layout.Strip){
		"FocusLeft":   func(s *layout.Strip) { s.FocusLeft() },
		"FocusRight":  func(s *layout.Strip) { s.FocusRight() },
		"FocusPaneID": func(s *layout.Strip) { s.FocusPaneID(3) },
		"AddColumn":   func(s *layout.Strip) { s.AddColumn(4, 40, 20, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			s := layout.NewStrip()
			s.AddColumn(1, 40, 20, 0)
			s.AddColumn(2, 40, 20, 0)
			s.AddColumn(3, 40, 20, 0)
			s.FocusPaneID(2)

			move(s)

			if got := s.LastFocusPaneID(); got != 2 {
				t.Errorf("LastFocusPaneID() = %d, want 2", got)
			}
		})
	}
}

// Nothing that leaves the focused pane where it was may overwrite the
// record: not a move at an edge, not re-focusing the same pane, not an
// unknown ID, and not reordering (the focused pane is the same pane).
func TestFocusLastIgnoresNoOpsAndMoves(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 20, 0)
	s.AddColumn(2, 40, 20, 0)
	s.FocusPaneID(1) // record: 2
	// Ordered: after MoveRight, pane 1 is no longer at the left edge.
	for _, step := range []struct {
		name string
		noop func()
	}{
		{"FocusLeft at edge", s.FocusLeft},
		{"same pane", func() { s.FocusPaneID(1) }},
		{"unknown pane", func() { s.FocusPaneID(99) }},
		{"MoveRight", func() { s.MoveRight(3) }},
		{"MoveLeft", func() { s.MoveLeft(3) }},
	} {
		step.noop()
		if got := s.LastFocusPaneID(); got != 2 {
			t.Errorf("%s: LastFocusPaneID() = %d, want 2", step.name, got)
		}
	}
}

// A dead pane cannot be returned to. Kill clears the record rather than
// leaving tab to silently do nothing forever.
func TestKillingLastFocusedPaneForgetsIt(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 20, 0)
	s.AddColumn(2, 40, 20, 0)
	s.AddColumn(3, 40, 20, 0)
	s.FocusPaneID(1)
	s.FocusPaneID(3) // record: 1

	s.KillPane(1)

	if got := s.LastFocusPaneID(); got != 0 {
		t.Errorf("LastFocusPaneID() after killing it = %d, want 0", got)
	}
	s.FocusLast()
	if got := s.FocusedPaneID(); got != 3 {
		t.Errorf("FocusLast with no record moved focus to %d", got)
	}
}

func TestComputePlacementsAnchorsToBottomWhenTaller(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 40, 0)
	// Viewport height 22 -> AvailHeight 20
	ps := s.ComputePlacements(80, 22)
	if len(ps) != 1 {
		t.Fatalf("placements = %d, want 1", len(ps))
	}
	if got, want := ps[0].Src.Min.Y, 20; got != want {
		t.Errorf("Src.Min.Y = %d, want %d", got, want)
	}
	if got, want := ps[0].Src.Dy(), 20; got != want {
		t.Errorf("Src.Dy() = %d, want %d", got, want)
	}
	if got, want := ps[0].Dst.Dy(), 20; got != want {
		t.Errorf("Dst.Dy() = %d, want %d", got, want)
	}
}
