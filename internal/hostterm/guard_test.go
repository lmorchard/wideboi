package hostterm

import (
	"errors"
	"sync"
	"testing"
)

var errSentinel = errors.New("sentinel")

func TestGuardStopsExactlyOnce(t *testing.T) {
	var calls int
	g := NewGuard(func() error { calls++; return nil })

	for i := 0; i < 3; i++ {
		if err := g.Stop(); err != nil {
			t.Fatalf("Stop() returned %v", err)
		}
	}

	if calls != 1 {
		t.Fatalf("stop func called %d times, want 1", calls)
	}
}

func TestGuardStopIsConcurrencySafe(t *testing.T) {
	var mu sync.Mutex
	var calls int
	g := NewGuard(func() error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = g.Stop() }()
	}
	wg.Wait()

	if calls != 1 {
		t.Fatalf("stop func called %d times, want 1", calls)
	}
}

func TestGuardReturnsUnderlyingError(t *testing.T) {
	want := errSentinel
	g := NewGuard(func() error { return want })
	if got := g.Stop(); got != want {
		t.Fatalf("Stop() = %v, want %v", got, want)
	}
	if got := g.Stop(); got != nil {
		t.Fatalf("second Stop() = %v, want nil", got)
	}
}
