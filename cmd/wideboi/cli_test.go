package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseCLIDefault(t *testing.T) {
	opts, err := parseCLI(nil)
	if err != nil {
		t.Fatalf("parseCLI(nil) error: %v", err)
	}
	if opts.subcommand != "" {
		t.Errorf("subcommand = %q, want empty", opts.subcommand)
	}
	if opts.showVer || opts.showHelp {
		t.Errorf("unexpected showVer=%v, showHelp=%v", opts.showVer, opts.showHelp)
	}
}

func TestParseCLISubcommands(t *testing.T) {
	cases := []struct {
		args     []string
		wantSub  string
		wantVer  bool
		wantHelp bool
	}{
		{args: []string{"server"}, wantSub: "server"},
		{args: []string{"attach"}, wantSub: "attach"},
		{args: []string{"version"}, wantSub: "version", wantVer: true},
		{args: []string{"--version"}, wantVer: true},
		{args: []string{"-v"}, wantVer: true},
		{args: []string{"help"}, wantSub: "help", wantHelp: true},
		{args: []string{"--help"}, wantHelp: true},
		{args: []string{"-h"}, wantHelp: true},
	}

	for _, tc := range cases {
		opts, err := parseCLI(tc.args)
		if err != nil {
			t.Errorf("parseCLI(%v) error: %v", tc.args, err)
			continue
		}
		if tc.wantSub != "" && opts.subcommand != tc.wantSub {
			t.Errorf("parseCLI(%v) subcommand = %q, want %q", tc.args, opts.subcommand, tc.wantSub)
		}
		if opts.showVer != tc.wantVer {
			t.Errorf("parseCLI(%v) showVer = %v, want %v", tc.args, opts.showVer, tc.wantVer)
		}
		if opts.showHelp != tc.wantHelp {
			t.Errorf("parseCLI(%v) showHelp = %v, want %v", tc.args, opts.showHelp, tc.wantHelp)
		}
	}
}

func TestParseCLIFlags(t *testing.T) {
	args := []string{
		"-c", "test.toml",
		"-l", "scroll",
		"-p", "ctrl+space",
		"-s", "/tmp/custom.sock",
		"--shell", "/bin/zsh",
	}
	opts, err := parseCLI(args)
	if err != nil {
		t.Fatalf("parseCLI(%v) error: %v", args, err)
	}
	if opts.flags.ConfigFile != "test.toml" {
		t.Errorf("ConfigFile = %q, want test.toml", opts.flags.ConfigFile)
	}
	if opts.flags.Layout != "scroll" {
		t.Errorf("Layout = %q, want scroll", opts.flags.Layout)
	}
	if opts.flags.Prefix != "ctrl+space" {
		t.Errorf("Prefix = %q, want ctrl+space", opts.flags.Prefix)
	}
	if opts.flags.Socket != "/tmp/custom.sock" {
		t.Errorf("Socket = %q, want /tmp/custom.sock", opts.flags.Socket)
	}
	if opts.flags.Shell != "/bin/zsh" {
		t.Errorf("Shell = %q, want /bin/zsh", opts.flags.Shell)
	}
}

func TestParseCLIFlagsWithSubcommands(t *testing.T) {
	// Flags after subcommand
	opts, err := parseCLI([]string{"attach", "-s", "/tmp/custom.sock"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if opts.subcommand != "attach" {
		t.Errorf("subcommand = %q, want attach", opts.subcommand)
	}
	if opts.flags.Socket != "/tmp/custom.sock" {
		t.Errorf("Socket = %q, want /tmp/custom.sock", opts.flags.Socket)
	}

	// Flags before subcommand
	opts, err = parseCLI([]string{"-s", "/tmp/server.sock", "server"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if opts.subcommand != "server" {
		t.Errorf("subcommand = %q, want server", opts.subcommand)
	}
	if opts.flags.Socket != "/tmp/server.sock" {
		t.Errorf("Socket = %q, want /tmp/server.sock", opts.flags.Socket)
	}
}

func TestPrintHelp(t *testing.T) {
	var buf bytes.Buffer
	printHelp(&buf)
	out := buf.String()

	requiredStrings := []string{
		"Usage:",
		"wideboi [flags]",
		"wideboi [flags] server",
		"wideboi [flags] attach",
		"-c, --config",
		"-l, --layout",
		"-p, --prefix",
		"-s, --socket",
		"--shell",
		"WIDEBOI_LAYOUT",
		"WIDEBOI_PREFIX",
		"WIDEBOI_SOCK",
		"SHELL",
	}

	for _, req := range requiredStrings {
		if !strings.Contains(out, req) {
			t.Errorf("printHelp output missing %q:\n%s", req, out)
		}
	}
}
