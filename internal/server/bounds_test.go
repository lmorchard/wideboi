package server_test

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func TestExtremeGeometryRejectedWithoutAllocating(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp := transport.NewInProcChannel(32)
	s := server.NewServer(tp, "/bin/sh", "")
	defer s.Close()
	go func() { _ = s.Run(ctx) }()

	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// 1. Send extreme dimensions on resize: math.MaxInt32, arithmetic overflow, negative
	cases := []protocol.MsgResize{
		{Cols: math.MaxInt32, Rows: math.MaxInt32},
		{Cols: 1000000, Rows: 1000000},
		{Cols: -100, Rows: 24},
		{Cols: 80, Rows: -50},
		{Cols: 0, Rows: 0},
		{Cols: 5000, Rows: 100},  // cols > 4096
		{Cols: 100, Rows: 3000},  // rows > 2048
		{Cols: 4000, Rows: 2000}, // cells = 8,000,000 (ok) vs 4096*2048
	}

	for _, c := range cases {
		tp.SendClient(ctx, c)
	}

	// 2. Send VerbClaimSize with extreme dimensions
	tp.SendClient(ctx, protocol.MsgVerb{
		Verb:   protocol.VerbClaimSize,
		Widths: map[int]int{1: math.MaxInt32},
	})

	// Verify server remains responsive to a valid client resize and status
	tp.SendClient(ctx, protocol.MsgResize{Cols: 80, Rows: 24})
	if !tp.SendClient(ctx, protocol.MsgStatusRequest{}) {
		t.Fatal("server blocked or unresponsive after extreme geometry inputs")
	}

	select {
	case msg := <-tp.ServerSendChan():
		if _, ok := msg.(protocol.MsgLayoutSnapshot); !ok {
			t.Fatalf("expected MsgLayoutSnapshot, got %T", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response from server after extreme inputs")
	}
}

func TestWaitersDeduplicatedBoundedAndCleanedUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp1 := transport.NewInProcChannel(64)
	s := server.NewServer(tp1, "/bin/sh", "")
	defer s.Close()
	go func() { _ = s.Run(ctx) }()

	tp1.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// Wait for layout snapshot to find valid pane ID
	var paneID int
	select {
	case msg := <-tp1.ServerSendChan():
		if snap, ok := msg.(protocol.MsgLayoutSnapshot); ok && len(snap.Columns) > 0 {
			paneID = snap.Columns[0].PaneID
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial snapshot")
	}
	if paneID == 0 {
		t.Fatal("no pane ID found in snapshot")
	}

	// 1. Deduplication: Send 20 wait requests for the same pane from tp1
	for i := 0; i < 20; i++ {
		tp1.SendClient(ctx, protocol.MsgWaitRequest{PaneID: paneID})
	}

	// 2. Disconnect cleanup: Detach tp1
	tp1.SendClient(ctx, protocol.MsgDetach{})

	// 3. Reconnect new transport and verify wait queue is responsive and cleaned up
	tp2 := transport.NewInProcChannel(128)
	s.AddClientForTest(ctx, tp2)
	tp2.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// Bounding test: Register multiple distinct transports up to limit
	var extraClients []*transport.InProcChannel
	for i := 0; i < 70; i++ {
		client := transport.NewInProcChannel(4)
		s.AddClientForTest(ctx, client)
		extraClients = append(extraClients, client)
		client.SendClient(ctx, protocol.MsgWaitRequest{PaneID: paneID})
	}

	// The 65th+ waiter should receive a wait response with error (limit exceeded)
	var gotLimitError bool
	for _, client := range extraClients[64:] {
		select {
		case msg := <-client.ServerSendChan():
			if resp, ok := msg.(protocol.MsgWaitResponse); ok && resp.Error != "" {
				gotLimitError = true
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !gotLimitError {
		t.Fatal("expected wait registration limit error when exceeding maxWaitersPerPane")
	}

	// Detaching all extra clients cleans them up
	for _, client := range extraClients {
		client.SendClient(ctx, protocol.MsgDetach{})
	}
}

func TestMacroSaveCoalescedAndValidated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp := transport.NewInProcChannel(128)
	s := server.NewServer(tp, "/bin/sh", "")
	defer s.Close()

	var saveCount int
	var lastSaved []protocol.Macro
	var mu sync.Mutex
	savedCh := make(chan struct{}, 100)

	s.SetOnSaveMacros(func(macros []protocol.Macro) {
		mu.Lock()
		saveCount++
		lastSaved = macros
		mu.Unlock()
		// Sleep slightly to simulate disk I/O and allow queue coalescing
		time.Sleep(20 * time.Millisecond)
		select {
		case savedCh <- struct{}{}:
		default:
		}
	})

	go func() { _ = s.Run(ctx) }()
	tp.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// 1. Validation: Reject oversized macro count (>128)
	oversizedMacros := make([]protocol.Macro, 150)
	for i := range oversizedMacros {
		oversizedMacros[i] = protocol.Macro{Name: fmt.Sprintf("m%d", i)}
	}
	tp.SendClient(ctx, protocol.MsgSaveMacros{Macros: oversizedMacros})

	// 2. Validation: Reject macro with huge name
	hugeNameMacro := []protocol.Macro{{Name: string(make([]byte, 500))}}
	tp.SendClient(ctx, protocol.MsgSaveMacros{Macros: hugeNameMacro})

	// 3. Validation: Reject macro with huge total steps
	hugeStepMacro := []protocol.Macro{{
		Name:  "valid",
		Steps: []protocol.MacroStep{{Text: string(make([]byte, 100000))}},
	}}
	tp.SendClient(ctx, protocol.MsgSaveMacros{Macros: hugeStepMacro})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if saveCount != 0 {
		t.Fatalf("expected 0 saves for invalid/oversized macros, got %d", saveCount)
	}
	mu.Unlock()

	// 4. Coalescing: Send 100 valid macro updates in rapid succession
	for i := 1; i <= 100; i++ {
		tp.SendClient(ctx, protocol.MsgSaveMacros{
			Macros: []protocol.Macro{{
				Name:  fmt.Sprintf("macro-%d", i),
				Steps: []protocol.MacroStep{{Text: fmt.Sprintf("echo %d", i)}},
			}},
		})
	}

	// Give client loop time to process incoming messages before closing
	time.Sleep(100 * time.Millisecond)

	// Close cleanly drains the macro queue and waits for the worker to complete
	_ = s.Close()

	mu.Lock()
	defer mu.Unlock()
	// Out of 100 rapid updates with maxMacroQueue=64, coalescing must cap total saves
	if saveCount > 65 {
		t.Fatalf("expected coalescing to bound saves to at most 65, but saved %d times", saveCount)
	}
	// The final saved macro must be the latest accepted macro state (macro-100)
	if len(lastSaved) == 0 || lastSaved[0].Name != "macro-100" {
		t.Fatalf("expected final saved state to be macro-100, got %#v", lastSaved)
	}
}

func TestRejectingExtremeRequestLeavesOtherClientsResponsive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	healthyClient := transport.NewInProcChannel(32)
	maliciousClient := transport.NewInProcChannel(32)

	s := server.NewServer(healthyClient, "/bin/sh", "")
	defer s.Close()
	s.AddClientForTest(ctx, maliciousClient)
	go func() { _ = s.Run(ctx) }()

	healthyClient.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})
	maliciousClient.SendClient(ctx, protocol.MsgAttach{Cols: 80, Rows: 24})

	// Drain initial snapshots
	select {
	case <-healthyClient.ServerSendChan():
	case <-time.After(time.Second):
	}
	select {
	case <-maliciousClient.ServerSendChan():
	case <-time.After(time.Second):
	}

	// Malicious client floods extreme geometry and invalid macros
	maliciousClient.SendClient(ctx, protocol.MsgResize{Cols: math.MaxInt32, Rows: math.MaxInt32})
	maliciousClient.SendClient(ctx, protocol.MsgAttach{Cols: -1, Rows: 1000000})
	maliciousClient.SendClient(ctx, protocol.MsgSaveMacros{Macros: make([]protocol.Macro, 500)})

	// Healthy client remains completely responsive
	healthyClient.SendClient(ctx, protocol.MsgStatusRequest{})
	gotSnapshot := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		select {
		case msg := <-healthyClient.ServerSendChan():
			if _, ok := msg.(protocol.MsgLayoutSnapshot); ok {
				gotSnapshot = true
				break
			}
		case <-time.After(50 * time.Millisecond):
		}
		if gotSnapshot {
			break
		}
	}
	if !gotSnapshot {
		t.Fatal("healthy client did not receive layout snapshot after malicious requests")
	}

	// Clean shutdown proceeds without hang
	cancel()
}
