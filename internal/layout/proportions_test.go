package layout_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// stripOf builds a card-mode strip of n equally wide columns, focused
// on focusPane.
func stripOf(n, width, focusPane int) *layout.Strip {
	s := layout.NewStrip()
	for i := 1; i <= n; i++ {
		s.AddColumn(i, width, 22)
	}
	s.SetStrategy(layout.CardStrategy{})
	s.FocusPaneID(focusPane)
	return s
}

// coveredWidth is the rightmost column any placement reaches.
func coveredWidth(ps []layout.Placement) int {
	maxX := 0
	for _, p := range ps {
		if p.Dst.Max.X > maxX {
			maxX = p.Dst.Max.X
		}
	}
	return maxX
}

// slotStart is where a card's slot begins: its border cell, the one
// before Dst, for every card but the leftmost.
func slotStart(p layout.Placement) int {
	return max(p.Dst.Min.X-1, 0)
}

// gaps reports any column in [0, upTo) that no card's content or
// border covers.
func gaps(ps []layout.Placement, upTo int) []int {
	covered := make([]bool, upTo)
	for _, p := range ps {
		for x := slotStart(p); x < p.Dst.Max.X && x < upTo; x++ {
			covered[x] = true
		}
	}
	var out []int
	for x, ok := range covered {
		if !ok {
			out = append(out, x)
		}
	}
	return out
}

// Les's worked example: a 90-cell window with a 60-cell focused pane
// and three others gives each of them about (90-60)/3 = 10. The focused
// card's own border costs one cell, so the shares are 10, 10 and 9.
//
// The fixed DefaultSliverWidth this replaces knew nothing about the
// viewport, so the fan simply stopped partway across -- a 120-column
// window with three 30-wide panes used 50 columns and left 70 dead.
func TestCardsFillTheViewport(t *testing.T) {
	s := stripOf(4, 60, 2)
	ps := s.ComputePlacements(90, 24)

	if len(ps) != 4 {
		t.Fatalf("got %d placements, want 4", len(ps))
	}
	if got := coveredWidth(ps); got != 90 {
		t.Errorf("fan reaches column %d, want the full 90", got)
	}
	if g := gaps(ps, 90); len(g) > 0 {
		t.Errorf("columns left uncovered: %v", g)
	}
	wantStarts := []int{0, 10, 71, 81}
	for i, want := range wantStarts {
		if got := slotStart(ps[i]); got != want {
			t.Errorf("pane %d starts at %d, want %d", ps[i].PaneID, got, want)
		}
	}
}

// A remainder that does not divide evenly is spread one cell at a
// time rather than truncated, or the fan stops short of the edge.
func TestCardShareRemainderIsDistributed(t *testing.T) {
	s := stripOf(4, 60, 2)
	ps := s.ComputePlacements(92, 24) // 92-60-1 = 31 over 3 slivers

	// In overlapping card layout, the sliver share is reflected in the
	// slot starts: 31 cells, after the focused card's border, over 3
	// slivers gives shares 11, 10, 10, so the slots start at 0, 11, 72
	// and 82.
	wantStarts := []int{0, 11, 72, 82}
	for i, want := range wantStarts {
		if got := slotStart(ps[i]); got != want {
			t.Errorf("pane %d starts at column %d, want %d", ps[i].PaneID, got, want)
		}
	}

	if got := coveredWidth(ps); got != 92 {
		t.Errorf("fan reaches column %d, want 92", got)
	}
}

// If a card's share covers its whole pane there is nothing occluded,
// so it shows content rather than a spine and a title. A 45-cell
// "sliver" of chrome wastes what it was given.
func TestWideShareRendersFullNotSliver(t *testing.T) {
	s := stripOf(3, 30, 2)
	ps := s.ComputePlacements(120, 24) // share = (120-30)/2 = 45 > 30

	for _, p := range ps {
		if p.Kind != protocol.PlacementFull {
			t.Errorf("pane %d is %v at a share wider than the pane; want Full", p.PaneID, p.Kind)
		}
		if w := p.Dst.Dx(); w != 30 {
			t.Errorf("pane %d is %d cells, want its own width 30", p.PaneID, w)
		}
	}
}

// With enough columns the even share rounds below what chrome needs.
// Rather than emitting unreadable one-cell cards, show as many as
// clear the floor and let the rest overflow -- the +N marker already
// reports them.
func TestNarrowSharesOverflowRatherThanStarve(t *testing.T) {
	s := stripOf(10, 20, 1) // focus leftmost, so every sliver is to the right
	ps := s.ComputePlacements(40, 24)

	slivers := 0
	for _, p := range ps {
		if p.Z == 0 {
			slivers++
		}
	}
	if slivers == 0 {
		t.Fatal("no unfocused panes emitted at all")
	}
	if slivers == 9 {
		t.Error("every unfocused pane was emitted; at this width they cannot all clear the floor")
	}
}

// When cards are dropped, keep the ones nearest the focused pane --
// those are the neighbours you are most likely to want next.
func TestDroppedCardsAreTheOutermost(t *testing.T) {
	s := stripOf(10, 20, 5) // focus in the middle
	ps := s.ComputePlacements(40, 24)

	present := map[int]bool{}
	for _, p := range ps {
		present[p.PaneID] = true
	}
	if !present[5] {
		t.Fatal("the focused pane is missing")
	}
	// Whatever survived must be a contiguous run around pane 5.
	lo, hi := 5, 5
	for present[lo-1] {
		lo--
	}
	for present[hi+1] {
		hi++
	}
	for id := range present {
		if id < lo || id > hi {
			t.Errorf("pane %d survived outside the contiguous run [%d,%d] around focus", id, lo, hi)
		}
	}
}

// The no-shrink premise, and the thing a geometry rewrite is most
// likely to break: what is visible never changes what a pane is.
func TestCardColumnWidthsAreUntouched(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 12).Draw(t, "numCols")
		w := rapid.IntRange(10, 80).Draw(t, "colWidth")
		focus := rapid.IntRange(1, n).Draw(t, "focusPane")
		s := stripOf(n, w, focus)

		vw := rapid.IntRange(10, 220).Draw(t, "viewportWidth")
		vh := rapid.IntRange(5, 60).Draw(t, "viewportHeight")
		s.ComputePlacements(vw, vh)

		for _, id := range s.PaneIDs() {
			got, ok := s.ColumnWidth(id)
			if !ok {
				t.Fatalf("pane %d lost its column", id)
			}
			if got != w {
				t.Fatalf("pane %d width %d -> %d at viewport %dx%d", id, w, got, vw, vh)
			}
		}
	})
}

// No placement may run past the viewport, whatever the shares work
// out to.
func TestCardPlacementsStayInsideTheViewport(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 12).Draw(t, "numCols")
		w := rapid.IntRange(10, 80).Draw(t, "colWidth")
		focus := rapid.IntRange(1, n).Draw(t, "focusPane")
		s := stripOf(n, w, focus)

		vw := rapid.IntRange(10, 220).Draw(t, "viewportWidth")
		vh := rapid.IntRange(5, 60).Draw(t, "viewportHeight")

		for _, p := range s.ComputePlacements(vw, vh) {
			if p.Dst.Min.X < 0 || p.Dst.Max.X > vw {
				t.Fatalf("pane %d Dst %v outside [0,%d]", p.PaneID, p.Dst, vw)
			}
			if p.Dst.Dx() != p.Src.Dx() || p.Dst.Dy() != p.Src.Dy() {
				t.Fatalf("pane %d Src %v and Dst %v differ in size", p.PaneID, p.Src, p.Dst)
			}
		}
	})
}
