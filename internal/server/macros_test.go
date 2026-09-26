package server

import (
	"context"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestMacrosBroadcastOnAttachAndSave(t *testing.T) {
	tp1 := transport.NewInProcChannel(16)
	tp2 := transport.NewInProcChannel(16)

	s := NewServer(tp1, "/bin/sh", "")
	s.transports = append(s.transports, tp2)

	initialMacros := []protocol.Macro{
		{
			Name: "Initial",
			Steps: []protocol.MacroStep{
				{Key: "r", Code: "KeyR", Ctrl: true},
			},
		},
	}
	s.SetMacros(initialMacros)

	var savedMacros []protocol.Macro
	savedCh := make(chan []protocol.Macro, 1)
	s.SetOnSaveMacros(func(m []protocol.Macro) {
		savedCh <- m
	})

	ctx := context.Background()

	// 1. Client 1 attaches: should receive MsgMacrosSnapshot
	s.handleClientMsg(ctx, tp1, protocol.MsgAttach{Cols: 80, Rows: 24})

	var foundSnapshot bool
	for {
		select {
		case msg := <-tp1.ServerSend:
			if snap, ok := msg.(protocol.MsgMacrosSnapshot); ok {
				foundSnapshot = true
				if len(snap.Macros) != 1 || snap.Macros[0].Name != "Initial" {
					t.Fatalf("unexpected macros on attach: %+v", snap.Macros)
				}
				break
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for MsgMacrosSnapshot on attach")
		}
		if foundSnapshot {
			break
		}
	}

	// 2. Client 1 sends MsgSaveMacros with new macros
	updatedMacros := []protocol.Macro{
		{
			Name: "Updated 1",
			Steps: []protocol.MacroStep{
				{Text: "git status"},
				{Key: "Enter", Code: "Enter"},
			},
		},
		{
			Name: "Updated 2",
			Steps: []protocol.MacroStep{
				{Key: "c", Code: "KeyC", Ctrl: true},
			},
		},
	}
	s.handleClientMsg(ctx, tp1, protocol.MsgSaveMacros{Macros: updatedMacros})

	// Check onSaveMacros callback
	select {
	case savedMacros = <-savedCh:
		if len(savedMacros) != 2 || savedMacros[0].Name != "Updated 1" {
			t.Fatalf("unexpected saved macros from callback: %+v", savedMacros)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for onSaveMacros callback")
	}

	// Check tp2 received broadcast of updated macros
	foundBroadcast := false
	for {
		select {
		case msg := <-tp2.ServerSend:
			if snap, ok := msg.(protocol.MsgMacrosSnapshot); ok {
				foundBroadcast = true
				if len(snap.Macros) != 2 || snap.Macros[1].Name != "Updated 2" {
					t.Fatalf("unexpected broadcast macros: %+v", snap.Macros)
				}
				break
			}
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for MsgMacrosSnapshot broadcast on tp2")
		}
		if foundBroadcast {
			break
		}
	}

	// Verify server in-memory Macros()
	srvMacros := s.Macros()
	if len(srvMacros) != 2 || srvMacros[0].Name != "Updated 1" {
		t.Fatalf("unexpected s.Macros(): %+v", srvMacros)
	}
}
