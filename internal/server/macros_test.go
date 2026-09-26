package server

import (
	"context"
	"fmt"
	"sync"
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

func TestMacrosSequentialPersistenceOrder(t *testing.T) {
	tp := transport.NewInProcChannel(16)
	s := NewServer(tp, "/bin/sh", "")
	ctx := context.Background()

	var order []string
	var mu sync.Mutex
	done := make(chan struct{}, 10)

	s.SetOnSaveMacros(func(macros []protocol.Macro) {
		time.Sleep(10 * time.Millisecond) // simulate disk I/O
		mu.Lock()
		if len(macros) > 0 {
			order = append(order, macros[0].Name)
		}
		mu.Unlock()
		done <- struct{}{}
	})

	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("Save-%d", i)
		s.handleClientMsg(ctx, tp, protocol.MsgSaveMacros{
			Macros: []protocol.Macro{{Name: name}},
		})
	}

	for i := 0; i < 5; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for save %d", i+1)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for i, name := range order {
		want := fmt.Sprintf("Save-%d", i+1)
		if name != want {
			t.Errorf("step %d: got %s, want %s (order: %v)", i, name, want, order)
		}
	}
}

func TestMacrosNoDroppedSavesOnBurst(t *testing.T) {
	tp := transport.NewInProcChannel(16)
	s := NewServer(tp, "/bin/sh", "")
	ctx := context.Background()

	const count = 50
	var order []string
	var mu sync.Mutex
	done := make(chan struct{}, count)

	s.SetOnSaveMacros(func(macros []protocol.Macro) {
		mu.Lock()
		if len(macros) > 0 {
			order = append(order, macros[0].Name)
		}
		mu.Unlock()
		done <- struct{}{}
	})

	for i := 1; i <= count; i++ {
		name := fmt.Sprintf("Burst-%d", i)
		s.handleClientMsg(ctx, tp, protocol.MsgSaveMacros{
			Macros: []protocol.Macro{{Name: name}},
		})
	}

	for i := 0; i < count; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for burst save %d", i+1)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != count {
		t.Fatalf("got %d saved macros, want %d", len(order), count)
	}
	for i, name := range order {
		want := fmt.Sprintf("Burst-%d", i+1)
		if name != want {
			t.Errorf("step %d: got %s, want %s", i, name, want)
		}
	}
}

func TestMacrosReconfigureCallback(t *testing.T) {
	tp := transport.NewInProcChannel(16)
	s := NewServer(tp, "/bin/sh", "")
	ctx := context.Background()

	var fn1Called, fn2Called bool
	var mu sync.Mutex
	ch := make(chan struct{}, 2)

	s.SetOnSaveMacros(func(m []protocol.Macro) {
		mu.Lock()
		fn1Called = true
		mu.Unlock()
		ch <- struct{}{}
	})

	s.handleClientMsg(ctx, tp, protocol.MsgSaveMacros{Macros: []protocol.Macro{{Name: "One"}}})
	<-ch

	s.SetOnSaveMacros(func(m []protocol.Macro) {
		mu.Lock()
		fn2Called = true
		mu.Unlock()
		ch <- struct{}{}
	})

	s.handleClientMsg(ctx, tp, protocol.MsgSaveMacros{Macros: []protocol.Macro{{Name: "Two"}}})
	<-ch

	mu.Lock()
	defer mu.Unlock()
	if !fn1Called || !fn2Called {
		t.Errorf("fn1Called=%v, fn2Called=%v, want both true", fn1Called, fn2Called)
	}
}

func TestMacrosCloseDrainsQueue(t *testing.T) {
	tp := transport.NewInProcChannel(16)
	s := NewServer(tp, "/bin/sh", "")
	ctx := context.Background()

	var saved []string
	var mu sync.Mutex

	s.SetOnSaveMacros(func(m []protocol.Macro) {
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		if len(m) > 0 {
			saved = append(saved, m[0].Name)
		}
		mu.Unlock()
	})

	for i := 1; i <= 5; i++ {
		s.handleClientMsg(ctx, tp, protocol.MsgSaveMacros{
			Macros: []protocol.Macro{{Name: fmt.Sprintf("Close-%d", i)}},
		})
	}

	_ = s.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(saved) != 5 {
		t.Fatalf("Close did not drain queue: got %d saves, want 5", len(saved))
	}
}
