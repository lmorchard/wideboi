package client

import (
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/transport"
)

func TestPromptInputEditAndStatus(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	c := NewClient(tp, 80, 24, "C-b")

	if c.InPrompt() {
		t.Fatal("expected not InPrompt initially")
	}

	c.StartPrompt()
	if !c.InPrompt() {
		t.Fatal("expected InPrompt after StartPrompt")
	}

	c.PromptEdit("new", false)
	c.PromptEdit("x", false)
	c.PromptEdit("", true) // Backspace

	if got := c.PromptQuery(); got != "new" {
		t.Fatalf("PromptQuery = %q, want 'new'", got)
	}

	c.mu.Lock()
	status := c.promptStatusLocked()
	c.mu.Unlock()
	if !strings.Contains(status, ": new_") {
		t.Fatalf("promptStatusLocked = %q, want containing ': new_'", status)
	}

	c.PromptCancel()
	if c.InPrompt() {
		t.Fatal("expected not InPrompt after PromptCancel")
	}
}
