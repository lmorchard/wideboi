package main

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol"
)

func testFrame(paneID int, gen uint64, fill string) protocol.MsgPaneUpdate {
	const cols, rows = 8, 4
	lines := make([]protocol.LineData, rows)
	for y := range lines {
		line := make(protocol.LineData, cols)
		for x := range line {
			line[x] = protocol.CellData{Content: fill, Width: 1}
		}
		lines[y] = line
	}
	return protocol.MsgPaneUpdate{PaneID: paneID, Generation: gen, Cols: cols, Rows: rows, Lines: lines}
}

func mustMarshal(t *testing.T, msg any) []byte {
	t.Helper()
	b, err := protocol.MarshalServer(msg)
	if err != nil {
		t.Fatalf("MarshalServer(%T): %v", msg, err)
	}
	return b
}

func TestSinkAppliesPatchesAndRequestsResync(t *testing.T) {
	s := newSink()
	full := testFrame(1, 1, "a")
	next := testFrame(1, 2, "a")
	next.Lines[0] = testFrame(1, 2, "b").Lines[0]
	patch, ok := protocol.BuildPanePatch(full, next)
	if !ok || patch.ShiftRows != 0 || len(patch.ChangedRows) != 1 {
		t.Fatalf("fixture: want a one-row patch, got ok=%v %+v", ok, patch)
	}
	// Built against generation 1, but the sink now holds generation 2.
	stale := patch
	stale.Generation = 3

	for i, msg := range []any{full, patch} {
		reply, err := s.handle(mustMarshal(t, msg))
		if err != nil || reply != nil {
			t.Fatalf("message %d: reply=%v err=%v, want nil, nil", i, reply, err)
		}
	}
	reply, err := s.handle(mustMarshal(t, stale))
	if err != nil {
		t.Fatalf("stale patch: %v", err)
	}
	if got, ok := reply.(protocol.MsgPaneResync); !ok || got.PaneID != 1 {
		t.Fatalf("stale patch reply = %#v, want MsgPaneResync{PaneID: 1}", reply)
	}
	got := s.sum
	if got.Messages != 3 || got.PaneUpdates != 1 || got.RowPatches != 1 || got.ShiftPatches != 0 || got.Resyncs != 1 {
		t.Fatalf("counts = %+v, want Messages:3 PaneUpdates:1 RowPatches:1 Resyncs:1", got)
	}
	if _, held := s.frames[1]; held {
		t.Fatalf("pane 1 baseline still held after a failed patch; it must wait for a full snapshot")
	}
}

// distinctFrame gives every row different content, so a shift of the rows
// is unambiguous to BuildPanePatch.
func distinctFrame(gen uint64, fills ...string) protocol.MsgPaneUpdate {
	f := testFrame(1, gen, "")
	for y, fill := range fills {
		f.Lines[y] = testFrame(1, gen, fill).Lines[0]
	}
	return f
}

func TestSinkSplitsPayloadBytesByKind(t *testing.T) {
	s := newSink()
	full := distinctFrame(1, "a", "b", "c", "d")
	shifted := distinctFrame(2, "b", "c", "d", "e") // scrolled up one row
	shift, ok := protocol.BuildPanePatch(full, shifted)
	if !ok || shift.ShiftRows == 0 {
		t.Fatalf("fixture: want a shift patch, got ok=%v %+v", ok, shift)
	}
	edited := distinctFrame(3, "b", "c", "d", "f")
	row, ok := protocol.BuildPanePatch(shifted, edited)
	if !ok || row.ShiftRows != 0 {
		t.Fatalf("fixture: want a row patch, got ok=%v %+v", ok, row)
	}
	var want [3]uint64
	for i, msg := range []any{full, shift, row, protocol.MsgPaneClosed{PaneID: 1}} {
		b := mustMarshal(t, msg)
		if i < 3 {
			want[i] = uint64(len(b))
		}
		if _, err := s.handle(b); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	got := s.summary()
	if got.FullBytes != want[0] || got.ShiftPatchBytes != want[1] || got.RowPatchBytes != want[2] {
		t.Fatalf("Full/Shift/RowPatchBytes = %d/%d/%d, want %d/%d/%d",
			got.FullBytes, got.ShiftPatchBytes, got.RowPatchBytes, want[0], want[1], want[2])
	}
	if got.ShiftPatches != 1 || got.RowPatches != 1 || got.PaneUpdates != 1 {
		t.Fatalf("counts = %+v, want one of each kind", got)
	}
	// The pane-closed message is in PayloadBytes but in no kind.
	if kinds := want[0] + want[1] + want[2]; got.PayloadBytes <= kinds {
		t.Fatalf("PayloadBytes=%d, want more than the pane kinds' %d", got.PayloadBytes, kinds)
	}
}

func TestSinkEstimatesStreamingDeflate(t *testing.T) {
	s := newSink()
	for gen := uint64(1); gen <= 20; gen++ {
		if _, err := s.handle(mustMarshal(t, testFrame(1, gen, "x"))); err != nil {
			t.Fatalf("frame %d: %v", gen, err)
		}
	}
	got := s.summary()
	if got.DeflateBytes == 0 || got.DeflateBytes >= got.PayloadBytes {
		t.Fatalf("DeflateBytes=%d PayloadBytes=%d, want 0 < deflate < payload for repeated frames",
			got.DeflateBytes, got.PayloadBytes)
	}
	if got.DeflateNoCtxBytes == 0 || got.DeflateNoCtxBytes >= got.PayloadBytes {
		t.Fatalf("DeflateNoCtxBytes=%d PayloadBytes=%d, want 0 < deflate < payload",
			got.DeflateNoCtxBytes, got.PayloadBytes)
	}
	// Repeated frames are exactly what a shared history exploits; starting
	// each message from nothing cannot do better.
	if got.DeflateNoCtxBytes < got.DeflateBytes {
		t.Fatalf("DeflateNoCtxBytes=%d < DeflateBytes=%d, want no-context >= context takeover",
			got.DeflateNoCtxBytes, got.DeflateBytes)
	}
}

func TestSinkForgetsClosedPanes(t *testing.T) {
	s := newSink()
	if _, err := s.handle(mustMarshal(t, testFrame(1, 1, "a"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handle(mustMarshal(t, protocol.MsgPaneClosed{PaneID: 1})); err != nil {
		t.Fatal(err)
	}
	if _, held := s.frames[1]; held {
		t.Fatalf("pane 1 frame still held after MsgPaneClosed")
	}
}

func TestSinkSetsTrafficRepliesAside(t *testing.T) {
	s := newSink()
	var got []protocol.MsgTrafficStats
	var sizes []int
	s.onTraffic = func(ts protocol.MsgTrafficStats, n int) {
		got = append(got, ts)
		sizes = append(sizes, n)
	}
	payload := mustMarshal(t, protocol.MsgTrafficStats{UptimeMillis: 42})
	if reply, err := s.handle(payload); err != nil || reply != nil {
		t.Fatalf("reply=%v err=%v, want nil, nil", reply, err)
	}
	if len(got) != 1 || got[0].UptimeMillis != 42 || sizes[0] != len(payload) {
		t.Fatalf("onTraffic got %+v sizes %v, want one report of %d bytes", got, sizes, len(payload))
	}
	if sum := s.summary(); sum != (Summary{}) {
		t.Fatalf("summary = %+v, want the traffic reply left uncounted", sum)
	}
}
