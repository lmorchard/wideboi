package server

import (
	"bytes"
	"testing"
	"time"
)

func TestPaneTapDelivery(t *testing.T) {
	p := &Pane{
		closed: make(chan struct{}),
	}

	ch := make(chan []byte, 10)
	tapID, ok := p.AddTap(ch)
	if !ok || tapID == 0 {
		t.Fatalf("AddTap failed: ok=%v, tapID=%d", ok, tapID)
	}

	data1 := []byte("hello world")
	p.broadcastRawBytes(data1)

	select {
	case got := <-ch:
		if !bytes.Equal(got, data1) {
			t.Fatalf("got %q, want %q", got, data1)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tapped chunk")
	}

	// Remove tap
	p.RemoveTap(tapID)
	data2 := []byte("after remove")
	p.broadcastRawBytes(data2)

	select {
	case got := <-ch:
		t.Fatalf("unexpected chunk received after RemoveTap: %q", got)
	default:
	}
}

func TestPaneTapMultipleSubscribers(t *testing.T) {
	p := &Pane{
		closed: make(chan struct{}),
	}

	ch1 := make(chan []byte, 10)
	ch2 := make(chan []byte, 10)

	id1, ok1 := p.AddTap(ch1)
	id2, ok2 := p.AddTap(ch2)
	if !ok1 || !ok2 {
		t.Fatalf("AddTap failed: ok1=%v, ok2=%v", ok1, ok2)
	}

	msg := []byte("broadcast test")
	p.broadcastRawBytes(msg)

	for i, ch := range []chan []byte{ch1, ch2} {
		select {
		case got := <-ch:
			if !bytes.Equal(got, msg) {
				t.Fatalf("ch%d got %q, want %q", i+1, got, msg)
			}
		case <-time.After(time.Second):
			t.Fatalf("ch%d timed out waiting for chunk", i+1)
		}
	}

	p.RemoveTap(id1)
	p.RemoveTap(id2)
}

func TestPaneTapDropOnOverflow(t *testing.T) {
	p := &Pane{
		closed: make(chan struct{}),
	}

	// Buffer capacity 1
	ch := make(chan []byte, 1)
	id, ok := p.AddTap(ch)
	if !ok {
		t.Fatal("AddTap failed")
	}
	defer p.RemoveTap(id)

	// Fill buffer and overflow
	for i := 0; i < 10; i++ {
		p.broadcastRawBytes([]byte{byte(i)})
	}

	// First chunk should be present
	select {
	case got := <-ch:
		if len(got) != 1 || got[0] != 0 {
			t.Fatalf("expected first chunk [0], got %v", got)
		}
	default:
		t.Fatal("expected at least 1 chunk in buffer")
	}

	// Channel should now be empty (subsequent 9 were dropped)
	select {
	case got := <-ch:
		t.Fatalf("unexpected chunk in buffer: %v", got)
	default:
	}
}

func TestPaneTapCloseOnExit(t *testing.T) {
	p := &Pane{
		closed: make(chan struct{}),
	}

	ch := make(chan []byte, 10)
	_, ok := p.AddTap(ch)
	if !ok {
		t.Fatal("AddTap failed")
	}

	p.closeTaps()

	select {
	case _, open := <-ch:
		if open {
			t.Fatal("expected channel to be closed, got chunk")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tap channel close")
	}

	// AddTap after closeTaps (e.g. kept pane after EOF) must be rejected
	ch2 := make(chan []byte, 10)
	id2, ok2 := p.AddTap(ch2)
	if ok2 || id2 != 0 {
		t.Fatalf("expected AddTap after closeTaps to fail, got id=%d, ok=%v", id2, ok2)
	}
}
