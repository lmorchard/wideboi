package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
)

func TestPaletteCancelEsc(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("\x1b") // Esc key
	cfg := config.Config{}

	err := runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPalette with Esc returned error: %v", err)
	}
}

func TestPaletteCancelCtrlC(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	in.WriteString("\x03") // Ctrl+C
	cfg := config.Config{}

	err := runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	if err != nil {
		t.Fatalf("runPalette with Ctrl+C returned error: %v", err)
	}
}

func TestPaletteFilterAndRender(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	// Type "quit" and press Enter
	in.WriteString("quit\n")
	cfg := config.Config{}

	_ = runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	// Render output should display query and matching command
	output := out.String()
	if !strings.Contains(output, "quit") {
		t.Errorf("palette output missing 'quit', got:\n%s", output)
	}
}

func TestPaletteFuzzySubsequence(t *testing.T) {
	var in bytes.Buffer
	var out, errOut bytes.Buffer

	// Type subsequence "ncl" which should match "new-column"
	in.WriteString("ncl\n")
	cfg := config.Config{}

	_ = runPalette(cfg, []string{"--caller-pane=1"}, &in, &out, &errOut)
	output := out.String()
	if !strings.Contains(output, "new-column") {
		t.Errorf("palette output missing 'new-column' for fuzzy query 'ncl', got:\n%s", output)
	}
}
