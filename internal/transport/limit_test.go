package transport_test

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/transport"
)

func TestLimitListenerBoundsConnections(t *testing.T) {
	rawListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rawListener.Close()

	limited := transport.LimitListener(rawListener, 2)
	defer limited.Close()

	addr := limited.Addr().String()

	var accepted []net.Conn
	var mu sync.Mutex
	stop := make(chan struct{})

	go func() {
		for {
			c, err := limited.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, c)
			mu.Unlock()
		}
	}()

	// Connect 2 clients: should succeed immediately
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()

	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	// Wait for 2 accepted
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(accepted)
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	if len(accepted) != 2 {
		mu.Unlock()
		t.Fatalf("expected 2 accepted connections, got %d", len(accepted))
	}
	mu.Unlock()

	// Third connection is dialed: raw TCP handshake may complete in OS backlog,
	// but Accept() must not accept it while 2 are held.
	c3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(accepted) != 2 {
		mu.Unlock()
		t.Fatalf("expected still 2 accepted connections before close, got %d", len(accepted))
	}
	mu.Unlock()

	// Closing c1 frees a slot, allowing c3 to be accepted
	c1.Close()
	mu.Lock()
	if len(accepted) > 0 {
		accepted[0].Close()
	}
	mu.Unlock()

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(accepted)
		mu.Unlock()
		if n == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	if len(accepted) != 3 {
		mu.Unlock()
		t.Fatalf("expected 3 accepted connections after releasing slot, got %d", len(accepted))
	}
	mu.Unlock()

	close(stop)
}
