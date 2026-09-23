package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/config"
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
		{args: []string{"kill-session"}, wantSub: "kill-session"},
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

func TestParseCLIList(t *testing.T) {
	for _, arg := range []string{"ls", "list-sessions"} {
		opts, err := parseCLI([]string{arg})
		if err != nil {
			t.Fatalf("parseCLI(%s) error: %v", arg, err)
		}
		if opts.subcommand != "ls" {
			t.Errorf("parseCLI(%s) subcommand = %q, want ls", arg, opts.subcommand)
		}
	}
}

func TestParseCLISession(t *testing.T) {
	for _, args := range [][]string{
		{"-L", "work"},
		{"--session", "work"},
		{"-L", "work", "attach"},
		{"attach", "-L", "work"},
		{"kill-session", "--session", "work"},
	} {
		opts, err := parseCLI(args)
		if err != nil {
			t.Fatalf("parseCLI(%v) error: %v", args, err)
		}
		if opts.flags.Session != "work" {
			t.Errorf("parseCLI(%v) Session = %q, want work", args, opts.flags.Session)
		}
	}
	// The name is a flag value, never a subcommand, even when it spells one.
	opts, err := parseCLI([]string{"-L", "attach"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if opts.subcommand != "" || opts.flags.Session != "attach" {
		t.Errorf("-L attach: subcommand = %q, Session = %q; want none, attach", opts.subcommand, opts.flags.Session)
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
		"wideboi ls",
		"-c, --config",
		"-l, --layout",
		"-p, --prefix",
		"-s, --socket",
		"-L, --session",
		"--shell",
		"WIDEBOI_LAYOUT",
		"WIDEBOI_PREFIX",
		"WIDEBOI_SOCK",
		"WIDEBOI_SESSION",
		"SHELL",
	}

	for _, req := range requiredStrings {
		if !strings.Contains(out, req) {
			t.Errorf("printHelp output missing %q:\n%s", req, out)
		}
	}
}

// --owner-fd is how a plain wideboi hands the server it spawned the
// private connection it owns the session through. Its value must not be
// mistaken for a subcommand, and the user's own flags ride along after
// it.
func TestParseCLIOwnerFD(t *testing.T) {
	opts, err := parseCLI([]string{"server", "--owner-fd", "3", "-l", "scroll"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if opts.subcommand != "server" {
		t.Errorf("subcommand = %q, want server", opts.subcommand)
	}
	if opts.ownerFD != 3 {
		t.Errorf("ownerFD = %d, want 3", opts.ownerFD)
	}
	if opts.flags.Layout != "scroll" {
		t.Errorf("layout = %q, want scroll", opts.flags.Layout)
	}

	opts, err = parseCLI(nil)
	if err != nil {
		t.Fatalf("parseCLI(nil) error: %v", err)
	}
	if opts.ownerFD != -1 {
		t.Errorf("default ownerFD = %d, want -1 (no owner)", opts.ownerFD)
	}
}

// The detach notice's commands must reach the same session: plain on
// the default session, -L on another named session, -s on any other
// socket.
func TestDetachNoticeNamesTheSocketOnlyWhenNeeded(t *testing.T) {
	var buf bytes.Buffer
	printDetachNotice(&buf, config.DefaultSocketPath())
	if strings.Contains(buf.String(), " -s ") {
		t.Errorf("default-socket notice carries -s:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), config.DefaultSocketPath()) {
		t.Errorf("notice does not name the socket:\n%s", buf.String())
	}

	buf.Reset()
	printDetachNotice(&buf, config.SessionSocketPath("work"))
	for _, want := range []string{"wideboi -L work ", "wideboi -L work kill-session"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("named-session notice lacks %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), " -s ") {
		t.Errorf("named-session notice carries -s:\n%s", buf.String())
	}

	buf.Reset()
	printDetachNotice(&buf, "/tmp/elsewhere.sock")
	for _, want := range []string{"wideboi -s /tmp/elsewhere.sock", "wideboi -s /tmp/elsewhere.sock kill-session"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("custom-socket notice lacks %q:\n%s", want, buf.String())
		}
	}
}

// The spawned server must own the session through the fd spawnServer
// gives it, whatever the user typed. A user-supplied --owner-fd would
// override ours if it came later, and a stray positional would stop the
// flag parser before ours if ours came later -- so ours goes first and
// any of theirs is dropped.
func TestServerArgsAlwaysNameOurOwnerFD(t *testing.T) {
	for _, user := range [][]string{
		nil,
		{"-l", "scroll"},
		{"--owner-fd", "9"},
		{"-owner-fd", "9", "-l", "scroll"},
		{"--owner-fd=9"},
		{"-owner-fd=9"},
		{"stray", "-l", "scroll"},
	} {
		opts, err := parseCLI(serverArgs(user))
		if err != nil {
			t.Errorf("serverArgs(%q): parse error %v", user, err)
			continue
		}
		if opts.subcommand != "server" || opts.ownerFD != 3 {
			t.Errorf("serverArgs(%q) = %q: subcommand %q ownerFD %d, want server and 3",
				user, serverArgs(user), opts.subcommand, opts.ownerFD)
		}
	}
}
