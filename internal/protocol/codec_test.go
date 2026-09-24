package protocol

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol/wirepb"
)

// The codec converts enums with plain casts, so the Go constants and the
// generated wire values must agree numerically. The Go round-trip tests
// cannot catch a drift -- both directions use the same cast -- but the
// browser uses the generated names directly, so a mismatch would send it
// the wrong verb or mouse kind.
func TestEnumsMatchWireSchema(t *testing.T) {
	verbs := map[VerbType]wirepb.VerbType{
		VerbFocusLeft:   wirepb.VerbType_VERB_TYPE_FOCUS_LEFT,
		VerbFocusRight:  wirepb.VerbType_VERB_TYPE_FOCUS_RIGHT,
		VerbNewColumn:   wirepb.VerbType_VERB_TYPE_NEW_COLUMN,
		VerbCycleWidth:  wirepb.VerbType_VERB_TYPE_CYCLE_WIDTH,
		VerbKillPane:    wirepb.VerbType_VERB_TYPE_KILL_PANE,
		VerbSmartJump:   wirepb.VerbType_VERB_TYPE_SMART_JUMP,
		VerbToggleCards: wirepb.VerbType_VERB_TYPE_TOGGLE_CARDS,
		VerbGrowWidth:   wirepb.VerbType_VERB_TYPE_GROW_WIDTH,
		VerbShrinkWidth: wirepb.VerbType_VERB_TYPE_SHRINK_WIDTH,
		VerbMoveLeft:    wirepb.VerbType_VERB_TYPE_MOVE_LEFT,
		VerbMoveRight:   wirepb.VerbType_VERB_TYPE_MOVE_RIGHT,
		VerbFocusLast:   wirepb.VerbType_VERB_TYPE_FOCUS_LAST,
	}
	for goVal, wireVal := range verbs {
		if int32(goVal) != int32(wireVal) {
			t.Errorf("verb %v: Go %d, wire %d", wireVal, goVal, wireVal)
		}
	}
	// The schema adds VERB_TYPE_UNSPECIFIED on top of the Go verbs.
	if got, want := len(wirepb.VerbType_name), len(verbs)+1; got != want {
		t.Errorf("wire schema has %d verbs, table maps %d: a verb was added on one side only", got, want)
	}

	mice := map[MouseKind]wirepb.MouseKind{
		MousePress:   wirepb.MouseKind_MOUSE_KIND_PRESS,
		MouseRelease: wirepb.MouseKind_MOUSE_KIND_RELEASE,
		MouseMotion:  wirepb.MouseKind_MOUSE_KIND_MOTION,
		MouseWheel:   wirepb.MouseKind_MOUSE_KIND_WHEEL,
	}
	for goVal, wireVal := range mice {
		if int32(goVal) != int32(wireVal) {
			t.Errorf("mouse kind %v: Go %d, wire %d", wireVal, goVal, wireVal)
		}
	}
	if got, want := len(wirepb.MouseKind_name), len(mice); got != want {
		t.Errorf("wire schema has %d mouse kinds, table maps %d", got, want)
	}

	colors := map[ColorKind]wirepb.ColorKind{
		ColorNone:    wirepb.ColorKind_COLOR_KIND_NONE,
		ColorBasic:   wirepb.ColorKind_COLOR_KIND_BASIC,
		ColorIndexed: wirepb.ColorKind_COLOR_KIND_INDEXED,
		ColorRGBA:    wirepb.ColorKind_COLOR_KIND_RGBA,
	}
	for goVal, wireVal := range colors {
		if int32(goVal) != int32(wireVal) {
			t.Errorf("color kind %v: Go %d, wire %d", wireVal, goVal, wireVal)
		}
	}
	if got, want := len(wirepb.ColorKind_name), len(colors); got != want {
		t.Errorf("wire schema has %d color kinds, table maps %d", got, want)
	}
}
