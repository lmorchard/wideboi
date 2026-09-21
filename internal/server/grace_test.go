package server

import (
	"testing"
	"time"
)

// A Pane built as a struct literal -- which every white-box fixture in
// this package does (status_test.go, transport_close_test.go,
// pane_wedge_test.go) -- has a zero closeGrace. Zero must mean the
// production default, or those fixtures would silently start tearing
// down with no grace at all and stop exercising the real path.
//
// This is the reason graceOrDefault resolves at the point of use rather
// than in a constructor: a constructor cannot reach a struct literal.
func TestZeroCloseGraceResolvesToTheDefault(t *testing.T) {
	if got := (&Pane{}).graceOrDefault(); got != CloseGrace {
		t.Errorf("(&Pane{}).graceOrDefault() = %v, want the default %v", got, CloseGrace)
	}
	if got := (&Pane{closeGrace: 50 * time.Millisecond}).graceOrDefault(); got != 50*time.Millisecond {
		t.Errorf("explicit grace = %v, want 50ms", got)
	}
}
