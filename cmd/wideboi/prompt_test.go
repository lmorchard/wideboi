package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptCancelEsc(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("\x1b") // Esc key
	cfg := paletteTestConfig(t)

	err := runPrompt(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPrompt with Esc returned error: %v", err)
	}
}

func TestPromptCancelCtrlC(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("\x03") // Ctrl+C
	cfg := paletteTestConfig(t)

	err := runPrompt(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPrompt with Ctrl+C returned error: %v", err)
	}
}

func TestPromptHelpExecution(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("help\n")
	cfg := paletteTestConfig(t)

	err := runPrompt(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPrompt help returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Available Commands:") {
		t.Errorf("output missing help text, got:\n%s", out.String())
	}
}

func TestPromptDetachFile(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	dir := shortTempDir(t)
	detachFile := filepath.Join(dir, "detach.tmp")

	in.WriteString("detach\n")
	cfg := paletteTestConfig(t)

	err := runPrompt(cfg, []string{"--caller-pane=1", "--detach-file=" + detachFile}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPrompt failed: %v", err)
	}

	data, err := os.ReadFile(detachFile)
	if err != nil {
		t.Fatalf("reading detachFile: %v", err)
	}
	if !strings.Contains(string(data), "detach") {
		t.Errorf("detachFile content = %q, want 'detach'", string(data))
	}
}
