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

	s.AddColumn(1, 40, 20, 0)
	s.AddColumn(2, 40, 20, 0)
	s.AddColumn(3, 40, 20, 0)

	s.FocusLeft() // Focus on pane 2

	placements := s.ComputePlacements(80, 24)
	if len(placements) != 3 {
		t.Fatalf("got %d placements, want 3", len(placements))
	}

	// Pane 1 (left card, overlapping): starts at X=0, width=40 (clipped to viewport if necessary), Z=0
	p1 := placements[0]
	if p1.PaneID != 1 || p1.Dst != image.Rect(0, 1, 40, 23) || p1.Z != 0 {
		t.Errorf("p1 = %+v, want Dst (0,1,40,23) Z=0", p1)
	}

	// Pane 2 (focused card): content from X=4, width=40, Z=1. Its
	// border is at X=3, the last cell of pane 1's slot.
	p2 := placements[1]
	if p2.PaneID != 2 || p2.Dst != image.Rect(4, 1, 44, 23) || p2.Z != 1 {
		t.Errorf("p2 = %+v, want Dst (4,1,44,23) Z=1", p2)
	}

	// Pane 3 (right card): its border at X=44, the first cell of its
	// own slot; content from X=45, clipped to 80, Z=0
	p3 := placements[2]
	if p3.PaneID != 3 || p3.Dst != image.Rect(45, 1, 80, 23) || p3.Z != 0 {
		t.Errorf("p3 = %+v, want Dst (45,1,80,23) Z=0", p3)
	}
}

// The focused card's border must not cost the slivers a cell of
// budget. Charging it there meant a narrow fan with room for exactly one
// sliver showed none, leaving the spare columns dead.
func TestFocusedCardBorderDoesNotCrowdOutASliver(t *testing.T) {
	s := layout.NewStrip()
	for i := 1; i <= 3; i++ {
		s.AddColumn(i, 60, 20, 0)
	}
	s.SetStrategy(layout.CardStrategy{})
	s.FocusPaneID(2)

	// 4 spare columns: exactly one MinSliverWidth sliver. Twice, so the
	// window the first call remembers is the one the second starts from.
	for range 2 {
		if got := len(s.ComputePlacements(64, 24)); got != 2 {
			t.Fatalf("got %d placements, want the focused card and one sliver", got)
		}
	}
}

func TestCardStrategyPropertyInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, initialWidths := genStrip(t)
		// 0 is production's proportional shares; 4 pins them.
		sw := rapid.SampledFrom([]int{0, 4}).Draw(t, "sliverWidth")
		s.SetStrategy(layout.CardStrategy{SliverWidth: sw})

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

		// Invariant: the focused card shows all of its content. A card
		// away from the left edge has its border in the cell before its
		// Dst, and none of those borders may land on the focused card.
		focusedW, _ := s.ColumnWidth(focusedID)
		if got, want := focusedP.Dst.Dx(), min(focusedW, vw); got != want {
			t.Fatalf("focused pane shows %d columns, want %d", got, want)
		}
		for _, p := range placements {
			if border := p.Dst.Min.X - 1; p.Dst.Min.X > 0 &&
				border >= focusedP.Dst.Min.X && border < focusedP.Dst.Max.X {
				t.Fatalf("pane %d's border at %d lands inside the focused card %v",
					p.PaneID, border, focusedP.Dst)
			}
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

// placedIDs lists the panes a set of card placements shows, left to
// right. CardStrategy emits them in that order.
func placedIDs(ps []layout.Placement) []int {
	ids := make([]int, len(ps))
	for i, p := range ps {
		ids[i] = p.PaneID
	}
	return ids
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// twentyCards is a strip of 20 panes, 30 wide, pinned at 4-cell
// slivers. With 60 viewport columns that leaves 30 after the focused
// pane: 7 slivers, so a window of 8 cards.
func twentyCards() *layout.Strip {
	s := layout.NewStrip()
	s.SetStrategy(layout.CardStrategy{SliverWidth: 4})
	for i := 1; i <= 20; i++ {
		s.AddColumn(i, 30, 20, 0)
	}
	s.FocusPaneID(1)
	return s
}

// The window of cards is sticky, like ScrollStrategy's scrollX: moving
// focus inside it leaves the visible set alone -- issue #20. It used
// to re-centre on every move, so the set shifted on both sides.
func TestCardWindowIsStickyWhileFocusStaysInside(t *testing.T) {
	s := twentyCards()
	want := []int{1, 2, 3, 4, 5, 6, 7, 8}
	if got := placedIDs(s.ComputePlacements(60, 24)); !equalInts(got, want) {
		t.Fatalf("initial window = %v, want %v", got, want)
	}

	// Pane 7 still has pane 8 beside it inside the window.
	for range 6 {
		s.FocusRight()
		if got := placedIDs(s.ComputePlacements(60, 24)); !equalInts(got, want) {
			t.Fatalf("focus on pane %d moved the window to %v, want %v",
				s.FocusedPaneID(), got, want)
		}
	}
}

// Once focus reaches the window's margin the window slides by as much
// as it needs to keep a neighbour visible, and no further. Going back
// the way it came does not slide it again until the other margin.
func TestCardWindowSlidesAtTheMargin(t *testing.T) {
	s := twentyCards()
	for range 7 { // focus on pane 8, the window's last card
		s.FocusRight()
		s.ComputePlacements(60, 24)
	}
	want := []int{2, 3, 4, 5, 6, 7, 8, 9}
	if got := placedIDs(s.ComputePlacements(60, 24)); !equalInts(got, want) {
		t.Fatalf("focus on pane 8: window = %v, want %v", got, want)
	}

	for range 5 { // back to pane 3, which still has pane 2 beside it
		s.FocusLeft()
		if got := placedIDs(s.ComputePlacements(60, 24)); !equalInts(got, want) {
			t.Fatalf("focus on pane %d moved the window to %v, want %v",
				s.FocusedPaneID(), got, want)
		}
	}

	s.FocusLeft() // pane 2: pane 1 has to come back into view
	want = []int{1, 2, 3, 4, 5, 6, 7, 8}
	if got := placedIDs(s.ComputePlacements(60, 24)); !equalInts(got, want) {
		t.Fatalf("focus on pane 2: window = %v, want %v", got, want)
	}
}

// Whatever path focus takes, the cards on screen are one unbroken run
// of the strip that includes the focused card and, whenever the window
// has room for three, both of its neighbours.
func TestCardWindowPropertyInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(t, "numCols")
		s := layout.NewStrip()
		s.SetStrategy(layout.CardStrategy{})
		for i := 1; i <= n; i++ {
			s.AddColumn(i, rapid.IntRange(20, 100).Draw(t, "colWidth"), 20, 0)
		}
		s.FocusPaneID(rapid.IntRange(1, n).Draw(t, "startFocus"))
		vw := rapid.IntRange(30, 200).Draw(t, "viewportWidth")

		steps := rapid.IntRange(1, 40).Draw(t, "steps")
		for range steps {
			switch rapid.IntRange(0, 2).Draw(t, "move") {
			case 0:
				s.FocusLeft()
			case 1:
				s.FocusRight()
			default:
				s.FocusPaneID(rapid.IntRange(1, n).Draw(t, "jump"))
			}

			ids := placedIDs(s.ComputePlacements(vw, 24))
			focus := s.FocusedPaneID()
			// AddColumn in order leaves pane i at index i-1, so a run
			// of the strip is a run of consecutive IDs.
			for i := 1; i < len(ids); i++ {
				if ids[i] != ids[i-1]+1 {
					t.Fatalf("placed cards %v are not contiguous", ids)
				}
			}
			if len(ids) == 0 || focus < ids[0] || focus > ids[len(ids)-1] {
				t.Fatalf("focused pane %d not among placed cards %v", focus, ids)
			}
			if len(ids) >= 3 {
				if focus > 1 && focus == ids[0] {
					t.Fatalf("focused pane %d has no left neighbour in %v", focus, ids)
				}
				if focus < n && focus == ids[len(ids)-1] {
					t.Fatalf("focused pane %d has no right neighbour in %v", focus, ids)
				}
			}
		}
	})
}

func TestCardComputePlacementsAnchorsToBottomWhenTaller(t *testing.T) {
	s := layout.NewStrip()
	s.SetStrategy(layout.CardStrategy{})
	s.AddColumn(1, 40, 50, 0)
	s.AddColumn(2, 40, 50, 0)
	// Viewport height 22 -> AvailHeight 20
	ps := s.ComputePlacements(80, 22)
	if len(ps) != 2 {
		t.Fatalf("placements = %d, want 2", len(ps))
	}
	for _, p := range ps {
		if got, want := p.Src.Min.Y, 30; got != want {
			t.Errorf("pane %d Src.Min.Y = %d, want %d", p.PaneID, got, want)
		}
		if got, want := p.Src.Dy(), 20; got != want {
			t.Errorf("pane %d Src.Dy() = %d, want %d", p.PaneID, got, want)
		}
		if got, want := p.Dst.Dy(), 20; got != want {
			t.Errorf("pane %d Dst.Dy() = %d, want %d", p.PaneID, got, want)
		}
	}
}
