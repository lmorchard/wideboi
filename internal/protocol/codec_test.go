package protocol

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol/wirepb"
)

// fill sets every exported field reachable from v to a distinct non-zero
// value, so a field the codec drops cannot compare equal after a round trip.
func fill(v reflect.Value, n *int) {
	*n++
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), n)
			}
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 { // []byte
			v.SetBytes([]byte{byte(*n), 0xff})
			return
		}
		s := reflect.MakeSlice(v.Type(), 2, 2)
		fill(s.Index(0), n)
		fill(s.Index(1), n)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k, n)
		fill(e, n)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.String:
		v.SetString(fmt.Sprintf("s%d", *n))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int32, reflect.Int64:
		// Large and alternately negative, still inside int32: a sign or
		// width mistake in a conversion must not round-trip by luck.
		m := int64(1<<30 + *n)
		if *n%2 == 1 {
			m = -m
		}
		v.SetInt(m)
	case reflect.Uint8:
		v.SetUint(uint64(200 + *n%56)) // high bit set: no sign confusion
	case reflect.Uint64:
		v.SetUint(1<<40 + uint64(*n)) // beyond 32 bits: a generation must not truncate
	default:
		panic(fmt.Sprintf("fill: unhandled kind %s at %s", v.Kind(), v.Type()))
	}
}

// roundtrip sends v through whichever envelope accepts it.
func roundtrip(t *testing.T, v any) any {
	t.Helper()
	if data, err := MarshalClient(v); err == nil {
		got, err := UnmarshalClient(data)
		if err != nil {
			t.Fatalf("decode client %T: %v", v, err)
		}
		return got
	}
	data, err := MarshalServer(v)
	if err != nil {
		t.Fatalf("%T is in neither envelope: %v", v, err)
	}
	got, err := UnmarshalServer(data)
	if err != nil {
		t.Fatalf("decode server %T: %v", v, err)
	}
	return got
}

// The codec is hand-written, so its failure mode is a field added to a Go
// message and never converted: it would vanish on the wire while every
// in-process test passed. Filling every field makes that a test failure.
func TestCodecRoundTripsEveryField(t *testing.T) {
	for _, proto := range wireTypes {
		ptr := reflect.New(reflect.TypeOf(proto))
		n := 0
		fill(ptr.Elem(), &n)
		want := ptr.Elem().Interface()
		t.Run(reflect.TypeOf(proto).Name(), func(t *testing.T) {
			if got := roundtrip(t, want); !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip lost data:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

// wireTypes is the Go list; the schema's oneofs are the wire list. They
// must name the same number of messages, or one side has a type the other
// cannot carry.
func TestWireSchemaCoversEveryWireType(t *testing.T) {
	client := (&wirepb.ClientMessage{}).ProtoReflect().Descriptor().Oneofs().ByName("msg").Fields().Len()
	server := (&wirepb.ServerMessage{}).ProtoReflect().Descriptor().Oneofs().ByName("msg").Fields().Len()
	if client+server != len(wireTypes) {
		t.Fatalf("schema carries %d client + %d server messages, wireTypes lists %d", client, server, len(wireTypes))
	}
}

// The codec converts enums with plain casts, so the Go constants and the
// generated wire values must agree numerically. The round-trip tests
// cannot catch a drift -- both directions use the same cast -- but the
// browser uses the generated names directly, so a mismatch would send it
// the wrong verb or mouse kind.
func TestEnumsMatchWireSchema(t *testing.T) {
	verbs := map[VerbType]wirepb.VerbType{
		VerbFocusLeft:    wirepb.VerbType_VERB_TYPE_FOCUS_LEFT,
		VerbFocusRight:   wirepb.VerbType_VERB_TYPE_FOCUS_RIGHT,
		VerbNewColumn:    wirepb.VerbType_VERB_TYPE_NEW_COLUMN,
		VerbCycleWidth:   wirepb.VerbType_VERB_TYPE_CYCLE_WIDTH,
		VerbKillPane:     wirepb.VerbType_VERB_TYPE_KILL_PANE,
		VerbSmartJump:    wirepb.VerbType_VERB_TYPE_SMART_JUMP,
		VerbToggleCards:  wirepb.VerbType_VERB_TYPE_TOGGLE_CARDS,
		VerbGrowWidth:    wirepb.VerbType_VERB_TYPE_GROW_WIDTH,
		VerbShrinkWidth:  wirepb.VerbType_VERB_TYPE_SHRINK_WIDTH,
		VerbMoveLeft:     wirepb.VerbType_VERB_TYPE_MOVE_LEFT,
		VerbMoveRight:    wirepb.VerbType_VERB_TYPE_MOVE_RIGHT,
		VerbFocusLast:    wirepb.VerbType_VERB_TYPE_FOCUS_LAST,
		VerbToggleStatus: wirepb.VerbType_VERB_TYPE_TOGGLE_STATUS,
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

	statuses := map[PaneStatus]wirepb.PaneStatus{
		StatusIdle:       wirepb.PaneStatus_PANE_STATUS_IDLE,
		StatusWorking:    wirepb.PaneStatus_PANE_STATUS_WORKING,
		StatusNeedsInput: wirepb.PaneStatus_PANE_STATUS_NEEDS_INPUT,
		StatusDone:       wirepb.PaneStatus_PANE_STATUS_DONE,
		StatusFailed:     wirepb.PaneStatus_PANE_STATUS_FAILED,
	}
	for goVal, wireVal := range statuses {
		if int32(goVal) != int32(wireVal) {
			t.Errorf("pane status %v: Go %d, wire %d", wireVal, goVal, wireVal)
		}
	}
	if got, want := len(wirepb.PaneStatus_name), len(statuses); got != want {
		t.Errorf("wire schema has %d pane statuses, table maps %d", got, want)
	}
}

// Proto3 omits a false bool entirely, so a patch that hides the cursor
// carries no cursor_visible field. That absence must mean "false", not
// "unchanged", or a hidden cursor would stay drawn.
func TestPatchHidingTheCursorDecodesAsHidden(t *testing.T) {
	sent := MsgPanePatch{PaneID: 1, Cols: 2, Rows: 2, BaseGeneration: 1, Generation: 2}
	data, err := MarshalServer(sent)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalServer(data)
	if err != nil {
		t.Fatal(err)
	}
	patch, ok := decoded.(MsgPanePatch)
	if !ok || patch.CursorVisible || patch.MouseTracking || len(patch.ChangedRows) != 0 {
		t.Fatalf("decoded %#v, want cursor hidden, no tracking, no rows", decoded)
	}
	row := LineData{{Content: " ", Width: 1}, {Content: " ", Width: 1}}
	base := MsgPaneUpdate{PaneID: 1, Generation: 1, Cols: 2, Rows: 2,
		Lines: []LineData{row, row}, CursorVisible: true, MouseTracking: true}
	next, ok := ApplyPanePatch(base, patch)
	if !ok || next.CursorVisible || next.MouseTracking {
		t.Fatalf("applied %#v (ok=%v), want cursor hidden and tracking off", next, ok)
	}
}

// A plain terminal grid is the large, frequent payload that motivated #127.
func TestPaneUpdateProtobufIsSmallerThanJSON(t *testing.T) {
	lines := make([]LineData, 24)
	for y := range lines {
		lines[y] = make(LineData, 80)
		for x := range lines[y] {
			lines[y][x] = CellData{Content: " ", Width: 1}
		}
	}
	msg := MsgPaneUpdate{PaneID: 1, Cols: 80, Rows: 24, Lines: lines}
	binary, err := MarshalServer(msg)
	if err != nil {
		t.Fatal(err)
	}
	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(binary)*3 >= len(jsonBytes) {
		t.Fatalf("protobuf grid %d bytes, JSON grid %d bytes: want at least 3x reduction", len(binary), len(jsonBytes))
	}
}

// web/src/protobuf.test.ts decodes these exact bytes in the browser. This
// keeps them pinned to what Go's codec actually produces.
func TestBrowserFixturesMatchGo(t *testing.T) {
	attach, err := MarshalClient(MsgAttach{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(attach), "0a0408501018"; got != want {
		t.Errorf("attach = %s, protobuf.test.ts expects %s", got, want)
	}
	update, err := MarshalServer(MsgPaneUpdate{PaneID: 7, Generation: 3, Cols: 1, Rows: 1,
		Lines:         []LineData{{{Content: "x", Width: 1, Style: StyleData{Fg: ColorData{Kind: ColorIndexed, Index: 4}}}}},
		CursorVisible: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(update), "0a1b08071003180120012a0f0a0d0a017810011a060a04080210044001"; got != want {
		t.Errorf("pane update = %s, protobuf.test.ts expects %s", got, want)
	}
}

// Protobuf string fields refuse invalid UTF-8, and one bad pane title --
// x/ansi cut Claude Code's "✳ ..." after its first byte -- made a whole
// snapshot unencodable, which closed the owner's connection and ended the
// session (#175). Invalid bytes must become U+FFFD instead.
func TestMarshalReplacesInvalidUTF8(t *testing.T) {
	snap := MsgLayoutSnapshot{PaneTitles: map[int]string{1: "\xe2", 2: "ok ✳"}}
	data, err := MarshalServer(snap)
	if err != nil {
		t.Fatalf("snapshot with an invalid title: %v", err)
	}
	got, err := UnmarshalServer(data)
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if titles := got.(MsgLayoutSnapshot).PaneTitles; titles[1] != "\uFFFD" || titles[2] != "ok ✳" {
		t.Errorf("titles = %v, want the invalid one replaced and the valid one untouched", titles)
	}

	// Patches share encodeLine with updates, so this covers both.
	upd := MsgPaneUpdate{PaneID: 1, Cols: 1, Rows: 1, Lines: []LineData{{{Content: "\xff", Width: 1}}}}
	data, err = MarshalServer(upd)
	if err != nil {
		t.Fatalf("update with an invalid cell: %v", err)
	}
	got, err = UnmarshalServer(data)
	if err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if c := got.(MsgPaneUpdate).Lines[0][0].Content; c != "\uFFFD" {
		t.Errorf("cell content = %q, want U+FFFD", c)
	}

	if _, err := MarshalClient(MsgInput{PaneID: 1, Key: KeyData{Text: "\xc3"}}); err != nil {
		t.Fatalf("input with invalid key text: %v", err)
	}
}
