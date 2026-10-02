package server

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/layout"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server/term"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestPaneCustomTitleOverrideAndFallback(t *testing.T) {
	g := term.NewVT(80, 24)
	defer g.Close()

	p := &Pane{
		id:   1,
		grid: g,
		cols: 80,
		rows: 24,
	}

	if got := p.Title(); got != "" {
		t.Fatalf("expected initial title to be empty, got %q", got)
	}

	// Child process sets title via OSC 0
	_, err := g.Write([]byte("\x1b]0;Child Title\x07"))
	if err != nil {
		t.Fatalf("failed to write OSC 0: %v", err)
	}
	if got := p.Title(); got != "Child Title" {
		t.Fatalf("expected title %q, got %q", "Child Title", got)
	}

	// Set custom title override
	p.SetCustomTitle("Custom Header")
	if custom, ok := p.CustomTitle(); !ok || custom != "Custom Header" {
		t.Fatalf("expected CustomTitle to return %q, true; got %q, %v", "Custom Header", custom, ok)
	}
	if got := p.Title(); got != "Custom Header" {
		t.Fatalf("expected Title to return overridden title %q, got %q", "Custom Header", got)
	}

	// Child process updates terminal title in the background
	_, err = g.Write([]byte("\x1b]0;Updated Child Title\x07"))
	if err != nil {
		t.Fatalf("failed to write OSC 0: %v", err)
	}

	// Title should still be the custom override
	if got := p.Title(); got != "Custom Header" {
		t.Fatalf("expected Title to remain %q while override active, got %q", "Custom Header", got)
	}

	// Clear custom title override
	p.ClearCustomTitle()
	if custom, ok := p.CustomTitle(); ok || custom != "" {
		t.Fatalf("expected CustomTitle to be clear, got %q, %v", custom, ok)
	}

	// Title should now immediately reflect the latest child title
	if got := p.Title(); got != "Updated Child Title" {
		t.Fatalf("expected Title to fall back to %q, got %q", "Updated Child Title", got)
	}
}

func TestRenamePaneHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Server{
		panes:      make(map[int]*Pane),
		cols:       80,
		rows:       24,
		stopCh:     make(chan struct{}),
		shell:      "/bin/sh",
		cwd:        t.TempDir(),
		closeGrace: 10 * time.Millisecond,
		strip:      layout.NewStrip(),
	}

	tp := transport.NewInProcChannel(64)
	s.transports = append(s.transports, tp)

	g1 := term.NewVT(40, 20)
	defer g1.Close()
	_, _ = g1.Write([]byte("\x1b]0;Initial Child\x07"))

	p1 := &Pane{
		id:     1,
		grid:   g1,
		cols:   40,
		rows:   20,
		closed: make(chan struct{}),
	}
	s.panes[1] = p1
	s.strip.AddColumn(1, 40, 20, 0)

	// 1. Rename existing pane
	s.handleClientMsg(ctx, tp, protocol.MsgRenamePaneRequest{
		PaneID: 1,
		Title:  "My Custom Pane",
	})
	resp := takeResponse[protocol.MsgRenamePaneResponse](t, tp)
	if resp.Error != "" {
		t.Fatalf("unexpected error renaming pane: %s", resp.Error)
	}
	if resp.PaneID != 1 {
		t.Fatalf("expected PaneID 1, got %d", resp.PaneID)
	}
	if got := p1.Title(); got != "My Custom Pane" {
		t.Fatalf("expected pane title %q, got %q", "My Custom Pane", got)
	}

	// 2. Rename non-existent pane returns error
	s.handleClientMsg(ctx, tp, protocol.MsgRenamePaneRequest{
		PaneID: 999,
		Title:  "Nowhere",
	})
	errResp := takeResponse[protocol.MsgRenamePaneResponse](t, tp)
	if errResp.Error != "pane 999 not found" {
		t.Fatalf("expected 'pane 999 not found', got %q", errResp.Error)
	}

	// 3. Clear custom title via Clear: true
	s.handleClientMsg(ctx, tp, protocol.MsgRenamePaneRequest{
		PaneID: 1,
		Clear:  true,
	})
	clearResp := takeResponse[protocol.MsgRenamePaneResponse](t, tp)
	if clearResp.Error != "" {
		t.Fatalf("unexpected error clearing pane title: %s", clearResp.Error)
	}
	if got := p1.Title(); got != "Initial Child" {
		t.Fatalf("expected title to restore to child %q, got %q", "Initial Child", got)
	}

	// 4. Clear custom title via empty title string
	s.handleClientMsg(ctx, tp, protocol.MsgRenamePaneRequest{
		PaneID: 1,
		Title:  "Another Title",
	})
	_ = takeResponse[protocol.MsgRenamePaneResponse](t, tp)
	if got := p1.Title(); got != "Another Title" {
		t.Fatalf("expected title %q, got %q", "Another Title", got)
	}

	s.handleClientMsg(ctx, tp, protocol.MsgRenamePaneRequest{
		PaneID: 1,
		Title:  "",
	})
	_ = takeResponse[protocol.MsgRenamePaneResponse](t, tp)
	if got := p1.Title(); got != "Initial Child" {
		t.Fatalf("expected title to restore to child %q on empty string, got %q", "Initial Child", got)
	}
}
