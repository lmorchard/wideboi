package client

import (
	"image"
	"sort"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// motionFrames is how many frames a layout change animates over. At
// cmd/wideboi/main.go's 16ms render tick that is about 128ms.
const motionFrames = 8

// motion animates placements from one layout to the next.
//
// It replaces a directional column wipe, which blitted the new layout
// on one side of a moving seam and the old layout on the other.
// Nothing actually moved: content teleported in vertical bands, and
// in card mode -- where a focus change resizes and repositions every
// pane -- the screen showed two entirely different geometries at the
// same time, which read as noise rather than as feedback.
//
// Interpolating the rects instead means panes slide and resize, which
// is what makes a re-dealing card fan legible. It is also less
// machinery than the wipe was: no composed frame snapshots, no
// viewport-fit check, no direction.
//
// This is the first cut of the spring design in issue #19, with
// eased interpolation standing in for real springs. Retargeting
// mid-flight is handled by restarting from the current interpolated
// rects rather than by carrying velocity.
type motion struct {
	from  []protocol.PlacementData
	to    []protocol.PlacementData
	step  int
	total int
}

// at returns the placement set for the animation's current step.
func (m *motion) at() []protocol.PlacementData {
	if m.total <= 0 {
		return m.to
	}
	return interpolate(m.from, m.to, easeOutCubic(float64(m.step)/float64(m.total)))
}

// done reports whether the animation has reached its target.
func (m *motion) done() bool { return m.step >= m.total }

// easeOutCubic maps linear progress onto a decelerating curve, so the
// motion settles rather than stopping dead on the last frame.
func easeOutCubic(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := 1 - t
	return 1 - u*u*u
}

func lerp(a, b int, t float64) int {
	return a + int(float64(b-a)*t+0.5)
}

// interpolate returns the placement set part-way from a to b.
//
// A pane present in only one side animates from a zero-width rect at
// its own edge, so a column that was just opened grows into place and
// one that was killed collapses, rather than appearing or vanishing
// at full size part-way through.
//
// Src, Z and Kind come from whichever side the pane will end up on.
// Kind in particular is deliberately not interpolated: a card that is
// becoming a sliver should start drawing chrome immediately, because
// there is no halfway between content and a spine.
func interpolate(from, to []protocol.PlacementData, t float64) []protocol.PlacementData {
	byID := make(map[int]protocol.PlacementData, len(from))
	for _, p := range from {
		byID[p.PaneID] = p
	}
	seen := make(map[int]bool, len(to))

	out := make([]protocol.PlacementData, 0, len(to)+len(from))
	for _, dst := range to {
		seen[dst.PaneID] = true
		src, ok := byID[dst.PaneID]
		if !ok {
			src = dst
			src.Dst = collapsed(dst.Dst)
		}
		out = append(out, blend(src, dst, dst, t))
	}

	// Panes only in the outgoing layout collapse toward their own
	// left edge and stop being drawn once they reach zero width.
	for _, src := range from {
		if seen[src.PaneID] {
			continue
		}
		gone := src
		gone.Dst = collapsed(src.Dst)
		p := blend(src, gone, src, t)
		if p.Dst.Dx() <= 0 {
			continue
		}
		out = append(out, p)
	}

	// Back to front, because part-way through a transition these
	// rects overlap -- a focused pane expanding leftward crosses the
	// sliver shrinking out of its way -- and composeFrameLocked
	// paints in slice order. Without this, a Z=0 sliver appearing
	// later in the slice paints over the Z=1 pane the user is
	// looking at, and a collapsing pane appended above would paint
	// over everything.
	//
	// Stable, so panes at equal Z keep their left-to-right order:
	// the divider logic reads neighbours positionally.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Z < out[j].Z })
	return out
}

// collapsed is a rect with no width, pinned to its own left edge.
func collapsed(r image.Rectangle) image.Rectangle {
	return image.Rect(r.Min.X, r.Min.Y, r.Min.X, r.Max.Y)
}

// blend interpolates a's rect toward b's, taking everything else from
// meta.
func blend(a, b, meta protocol.PlacementData, t float64) protocol.PlacementData {
	dst := image.Rect(
		lerp(a.Dst.Min.X, b.Dst.Min.X, t),
		lerp(a.Dst.Min.Y, b.Dst.Min.Y, t),
		lerp(a.Dst.Max.X, b.Dst.Max.X, t),
		lerp(a.Dst.Max.Y, b.Dst.Max.Y, t),
	)
	// Src has to track Dst's size or the compositor crops wrongly --
	// every strategy emits Src and Dst the same size, and the layout
	// property tests assert it.
	src := image.Rect(
		meta.Src.Min.X, meta.Src.Min.Y,
		meta.Src.Min.X+dst.Dx(), meta.Src.Min.Y+dst.Dy(),
	)
	kind := meta.Kind
	if t < 1.0 && (a.Kind == protocol.PlacementFull || b.Kind == protocol.PlacementFull) && dst.Dx() >= layout.MinSliverWidth {
		kind = protocol.PlacementFull
	}
	z := meta.Z
	if t < 1.0 {
		if a.Z < b.Z {
			z = a.Z
		} else {
			z = b.Z
		}
	}
	return protocol.PlacementData{
		PaneID: meta.PaneID,
		Src:    src,
		Dst:    dst,
		Z:      z,
		Kind:   kind,
	}
}

// placementsEqual reports whether two placement sets would render
// identically, so a snapshot that changed only a status glyph does
// not start an animation. Those arrive whenever a pane writes.
func placementsEqual(a, b []protocol.PlacementData) bool {
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
