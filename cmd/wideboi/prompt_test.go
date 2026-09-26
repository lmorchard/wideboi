package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
)

func TestPromptCancelEsc(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("\x1b") // Esc key
	cfg := config.Config{}

	err := runPrompt(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPrompt with Esc returned error: %v", err)
	}
}

func TestPromptCancelCtrlC(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("\x03") // Ctrl+C
	cfg := config.Config{}

	err := runPrompt(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPrompt with Ctrl+C returned error: %v", err)
	}
}

func TestPromptHelpExecution(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("help\n")
	cfg := config.Config{}

	err := runPrompt(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPrompt help returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Available Commands:") {
		t.Errorf("output missing help text, got:\n%s", out.String())
	}
}
