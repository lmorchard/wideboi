package server

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestAttachReceivesConfigSnapshot(t *testing.T) {
	tp := transport.NewInProcChannel(16)
	s := NewServer(tp, "/bin/sh", "")
	defer s.Close()

	customPresets := []int{50, 75, 100}
	s.SetWidthPresets(customPresets)

	customBindings := []protocol.KeyBinding{
		{
			ActionName: "focus_left",
			Key:        "h",
			Action:     1,
			Verb:       protocol.VerbFocusLeft,
			Long:       "focus left",
		},
	}
	s.SetBindings(customBindings)

	ctx := context.Background()
	s.handleClientMsg(ctx, tp, protocol.MsgAttach{Cols: 80, Rows: 24})

	var foundSnapshot bool
	deadline := time.After(500 * time.Millisecond)
	for !foundSnapshot {
		select {
		case msg := <-tp.ServerSend:
			if snap, ok := msg.(protocol.MsgConfigSnapshot); ok {
				foundSnapshot = true
				if len(snap.WidthPresets) != 3 || snap.WidthPresets[0] != 50 || snap.WidthPresets[1] != 75 || snap.WidthPresets[2] != 100 {
					t.Fatalf("unexpected width presets: %v, want %v", snap.WidthPresets, customPresets)
				}
				if snap.MinColumnWidth != layout.MinColumnWidth || snap.MaxColumnWidth != layout.MaxColumnWidth {
					t.Fatalf("unexpected limits: min=%d, max=%d", snap.MinColumnWidth, snap.MaxColumnWidth)
				}
				if len(snap.Bindings) != 1 || snap.Bindings[0].ActionName != "focus_left" {
					t.Fatalf("unexpected bindings: %+v", snap.Bindings)
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for MsgConfigSnapshot on attach")
		}
	}
}
