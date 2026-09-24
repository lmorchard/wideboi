package layout_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// A card sliver and a pane clipped by the viewport edge are the same
// shape -- a narrow Dst over a cropped Src, both at Z=0 -- so the
// renderer cannot tell them apart from geometry. Kind is what marks
// the difference, and these are the tests that keep the two honest.

// ScrollStrategy clips; it never occludes. Every placement it emits is
// a pane rendering its own content, however little of it fits. This is
// the layout-level guard for smoke.py's
// case_partly_clipped_pane_keeps_full_width -- if a clipped pane were
// ever marked a sliver, the client would paint chrome over real output.
func TestScrollStrategyNeverEmitsSlivers(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, _ := genStrip(t)
		// Deliberately includes widths narrow enough to clip.
		vw := rapid.IntRange(20, 200).Draw(t, "viewportWidth")
		vh := rapid.IntRange(5, 60).Draw(t, "viewportHeight")

		for _, p := range s.ComputePlacements(vw, vh) {
			if p.Kind != protocol.PlacementFull {
				t.Fatalf("pane %d has Kind=%v under ScrollStrategy at %dx%d; "+
					"clipping is not occlusion", p.PaneID, p.Kind, vw, vh)
			}
		}
	})
}

// In a fan with genuine overlapping, ALL cards show their content, so they are all Full.
func TestCardStrategyMarksAllAsFull(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 22, 0)
	s.AddColumn(2, 40, 22, 0)
	s.AddColumn(3, 40, 22, 0)
	s.SetStrategy(layout.CardStrategy{SliverWidth: 4})
	s.FocusPaneID(2)

	placements := s.ComputePlacements(120, 24)
	if len(placements) != 3 {
		t.Fatalf("got %d placements, want 3", len(placements))
	}

	for _, p := range placements {
		if p.Kind != protocol.PlacementFull {
			t.Errorf("pane %d Kind = %v, want %v", p.PaneID, p.Kind, protocol.PlacementFull)
		}
	}
}

// A lone column fills the viewport. It is not occluded by anything, so
// it is not a sliver.
func TestCardStrategySingleColumnIsFull(t *testing.T) {
	s := layout.NewStrip()
	s.AddColumn(1, 40, 22, 0)
	s.SetStrategy(layout.CardStrategy{SliverWidth: 4})

	placements := s.ComputePlacements(120, 24)
	if len(placements) != 1 {
		t.Fatalf("got %d placements, want 1", len(placements))
	}
	if placements[0].Kind != protocol.PlacementFull {
		t.Errorf("single column Kind = %v, want %v", placements[0].Kind, protocol.PlacementFull)
	}
}

// Kind has to survive the conversion, or the client never sees it.
func TestToProtocolCarriesKind(t *testing.T) {
	placements := []layout.Placement{
		{PaneID: 1, Kind: protocol.PlacementSliver},
	}
	data := layout.ToProtocol(placements)
	if len(data) != 1 || data[0].Kind != protocol.PlacementSliver {
		t.Error("ToProtocol dropped Kind")
	}
}
