//go:build desktop

package main

import (
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
)

func TestAvailableSessionNameFitsSocketAndCollision(t *testing.T) {
	base := sessionBase(strings.Repeat("a", 250))
	first, err := availableSessionName(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := availableSessionName(base, map[string]bool{first: true})
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasSuffix(second, "-2") {
		t.Fatalf("first=%q second=%q", first, second)
	}
	for _, name := range []string{first, second} {
		if !config.ValidSessionName(name) || len(config.SessionSocketPath(name)) > 103 {
			t.Fatalf("invalid session socket path: %q", config.SessionSocketPath(name))
		}
	}
}
