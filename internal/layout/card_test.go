package layout_test

import (
	"image"
	"testing"

	"github.com/lmorchard/wideboi/internal/layout"
	"pgregory.net/rapid"
)

func TestCardStrategyUnit(t *testing.T) {
	s := layout.NewStrip()
	s.SetStrategy(layout.CardStrategy{SliverWidth: 4})

	s.AddColumn(1, 40, 20)
	s.AddColumn(2, 40, 20)
	s.AddColumn(3, 40, 20)

	s.FocusLeft() // Focus on pane 2

	placements := s.ComputePlacements(80, 24)
	if len(placements) != 3 {
		t.Fatalf("got %d placements, want 3", len(placements))
	}

	// Pane 1 (left sliver): X=[0..4], Z=0
	p1 := placements[0]
	if p1.PaneID != 1 || p1.Dst != image.Rect(0, 0, 4, 23) || p1.Z != 0 {
		t.Errorf("p1 = %+v, want Dst (0,0,4,23) Z=0", p1)
	}

	// Pane 2 (focused card): X=[4..44], Z=1
	p2 := placements[1]
	if p2.PaneID != 2 || p2.Dst != image.Rect(4, 0, 44, 23) || p2.Z != 1 {
		t.Errorf("p2 = %+v, want Dst (4,0,44,23) Z=1", p2)
	}

	// Pane 3 (right sliver): X=[44..48], Z=0
	p3 := placements[2]
	if p3.PaneID != 3 || p3.Dst != image.Rect(44, 0, 48, 23) || p3.Z != 0 {
		t.Errorf("p3 = %+v, want Dst (44,0,48,23) Z=0", p3)
	}
}

func TestCardStrategyPropertyInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, initialWidths := genStrip(t)
		s.SetStrategy(layout.CardStrategy{SliverWidth: 4})

		vw := rapid.IntRange(30, 200).Draw(t, "viewportWidth")
		vh := rapid.IntRange(5, 60).Draw(t, "viewportHeight")

		placements := s.ComputePlacements(vw, vh)
		focusedID := s.FocusedPaneID()

		// Invariant 1: Focused pane is visible and has Z = 1
		var focusedP *layout.Placement
		for i := range placements {
			if placements[i].PaneID == focusedID {
				focusedP = &placements[i]
				break
			}
		}
		if focusedP == nil {
			t.Fatalf("focused pane %d missing from placements", focusedID)
		}
		if focusedP.Z != 1 {
			t.Fatalf("focused pane Z = %d, want 1", focusedP.Z)
		}
		if focusedP.Dst.Min.X < 0 || focusedP.Dst.Max.X > vw {
			t.Fatalf("focused pane Dst X %v outside viewport [0, %d]", focusedP.Dst, vw)
		}

		// Invariant 4: Logical column width remains unchanged
		for _, paneID := range s.PaneIDs() {
			colW, ok := s.ColumnWidth(paneID)
			if !ok {
				t.Fatalf("ColumnWidth(%d) not found", paneID)
			}
			if colW != initialWidths[paneID] {
				t.Fatalf("pane %d ColumnWidth=%d, want assigned %d", paneID, colW, initialWidths[paneID])
			}
		}
	})
}