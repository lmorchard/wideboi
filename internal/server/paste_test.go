package server

import (
	"context"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

type bracketedGrid struct {
	statusGrid
	bracketed bool
}

func (g *bracketedGrid) BracketedPaste() bool { return g.bracketed }

func TestHandleInputBracketedPaste(t *testing.T) {
	ctx := context.Background()
	s := NewServer(nil, "/bin/sh", "")

	gridUnbracketed := &bracketedGrid{bracketed: false}
	pane1 := &Pane{id: 1, grid: gridUnbracketed, input: make(chan uv.Event, 8)}
	s.panes[1] = pane1

	gridBracketed := &bracketedGrid{bracketed: true}
	pane2 := &Pane{id: 2, grid: gridBracketed, input: make(chan uv.Event, 8)}
	s.panes[2] = pane2

	tp := transport.NewInProcChannel(16)

	// 1. Unbracketed pane receives raw bytes even with Paste: true
	s.handleClientMsg(ctx, tp, protocol.MsgInput{PaneID: 1, Data: []byte("echo hi\n"), Paste: true})
	select {
	case ev := <-pane1.input:
		rb, ok := ev.(RawBytes)
		if !ok {
			t.Fatalf("expected RawBytes, got %T", ev)
		}
		if string(rb) != "echo hi\n" {
			t.Errorf("got %q, want %q", string(rb), "echo hi\n")
		}
	default:
		t.Fatal("expected input queued on pane 1, got none")
	}

	// 2. Bracketed pane receives raw bytes when Paste is false (e.g. macro or IME)
	s.handleClientMsg(ctx, tp, protocol.MsgInput{PaneID: 2, Data: []byte("echo hi\n"), Paste: false})
	select {
	case ev := <-pane2.input:
		rb, ok := ev.(RawBytes)
		if !ok {
			t.Fatalf("expected RawBytes, got %T", ev)
		}
		if string(rb) != "echo hi\n" {
			t.Errorf("got %q, want %q", string(rb), "echo hi\n")
		}
	default:
		t.Fatal("expected input queued on pane 2, got none")
	}

	// 3. Bracketed pane receives bracketed paste wrapped bytes when Paste is true
	s.handleClientMsg(ctx, tp, protocol.MsgInput{PaneID: 2, Data: []byte("echo hi\n"), Paste: true})
	select {
	case ev := <-pane2.input:
		rb, ok := ev.(RawBytes)
		if !ok {
			t.Fatalf("expected RawBytes, got %T", ev)
		}
		want := "\x1b[200~echo hi\n\x1b[201~"
		if string(rb) != want {
			t.Errorf("got %q, want %q", string(rb), want)
		}
	default:
		t.Fatal("expected input queued on pane 2, got none")
	}
}
