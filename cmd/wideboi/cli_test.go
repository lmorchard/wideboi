package main

import (
	"bytes"
	"path/filepath"
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
		{args: []string{"status"}, wantSub: "status"},
		{args: []string{"ls"}, wantSub: "ls"},
		{args: []string{"list-sessions"}, wantSub: "ls"},
		{args: []string{"version"}, wantSub: "version", wantVer: true},
		{args: []string{"--version"}, wantVer: true},
		{args: []string{"-v"}, wantVer: true},
		{args: []string{"help"}, wantSub: "help", wantHelp: true},
		{args: []string{"--help"}, wantHelp: true},
		{args: []string{"-h"}, wantHelp: true},
		{args: []string{"trust"}, wantSub: "trust"},
		{args: []string{"untrust"}, wantSub: "untrust"},
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
		"--trust-project",
		"--json",
	}
	opts, err := parseCLI(args)
	if err != nil {
		t.Fatalf("parseCLI(%v) error: %v", args, err)
	}
	if !opts.flags.TrustProject {
		t.Errorf("TrustProject = %v, want true", opts.flags.TrustProject)
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
	if !opts.jsonOut {
		t.Errorf("jsonOut = %v, want true", opts.jsonOut)
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

func TestParseCLIValueFlagsSubcommandValue(t *testing.T) {
	tests := []struct {
		flag       string
		subAsValue string
		check      func(opts cliOptions) string
	}{
		{"-c", "server", func(o cliOptions) string { return o.flags.ConfigFile }},
		{"-config", "status", func(o cliOptions) string { return o.flags.ConfigFile }},
		{"--config", "attach", func(o cliOptions) string { return o.flags.ConfigFile }},
		{"-l", "split", func(o cliOptions) string { return o.flags.Layout }},
		{"-layout", "send", func(o cliOptions) string { return o.flags.Layout }},
		{"--layout", "capture", func(o cliOptions) string { return o.flags.Layout }},
		{"-p", "close", func(o cliOptions) string { return o.flags.Prefix }},
		{"-prefix", "wait", func(o cliOptions) string { return o.flags.Prefix }},
		{"--prefix", "upgrade-server", func(o cliOptions) string { return o.flags.Prefix }},
		{"-s", "web", func(o cliOptions) string { return o.flags.Socket }},
		{"-socket", "prompt", func(o cliOptions) string { return o.flags.Socket }},
		{"--socket", "palette", func(o cliOptions) string { return o.flags.Socket }},
		{"-L", "server", func(o cliOptions) string { return o.flags.Session }},
		{"-session", "status", func(o cliOptions) string { return o.flags.Session }},
		{"--session", "attach", func(o cliOptions) string { return o.flags.Session }},
		{"-websocket", "kill-session", func(o cliOptions) string { return o.flags.Websocket }},
		{"--websocket", "cleanup", func(o cliOptions) string { return o.flags.Websocket }},
		{"-websocket-token", "version", func(o cliOptions) string { return o.flags.WebsocketToken }},
		{"--websocket-token", "help", func(o cliOptions) string { return o.flags.WebsocketToken }},
		{"-tls-cert", "desktop", func(o cliOptions) string { return o.flags.TLSCert }},
		{"--tls-cert", "ls", func(o cliOptions) string { return o.flags.TLSCert }},
		{"-tls-key", "list-sessions", func(o cliOptions) string { return o.flags.TLSKey }},
		{"--tls-key", "server", func(o cliOptions) string { return o.flags.TLSKey }},
		{"-shell", "status", func(o cliOptions) string { return o.flags.Shell }},
		{"--shell", "attach", func(o cliOptions) string { return o.flags.Shell }},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			opts, err := parseCLI([]string{tt.flag, tt.subAsValue})
			if err != nil {
				t.Fatalf("parseCLI(%s, %s) error: %v", tt.flag, tt.subAsValue, err)
			}
			if opts.subcommand != "" {
				t.Errorf("parseCLI(%s, %s).subcommand = %q, want empty", tt.flag, tt.subAsValue, opts.subcommand)
			}
			if got := tt.check(opts); got != tt.subAsValue {
				t.Errorf("parseCLI(%s, %s) flag value = %q, want %q", tt.flag, tt.subAsValue, got, tt.subAsValue)
			}
		})
	}

	// Also verify that when followed by an actual subcommand, the flag value is preserved
	// and the subsequent subcommand is recognized.
	opts, err := parseCLI([]string{"--websocket-token", "status", "server"})
	if err != nil {
		t.Fatalf("parseCLI with trailing subcommand error: %v", err)
	}
	if opts.flags.WebsocketToken != "status" {
		t.Errorf("WebsocketToken = %q, want status", opts.flags.WebsocketToken)
	}
	if opts.subcommand != "server" {
		t.Errorf("subcommand = %q, want server", opts.subcommand)
	}

	// Verify that boolean flags do not consume a following subcommand name as a value.
	opts, err = parseCLI([]string{"--disable-tls", "server"})
	if err != nil {
		t.Fatalf("parseCLI with bool flag error: %v", err)
	}
	if !opts.flags.DisableTLS {
		t.Errorf("DisableTLS = false, want true")
	}
	if opts.subcommand != "server" {
		t.Errorf("subcommand = %q, want server", opts.subcommand)
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
		"--disable-auto-cleanup",
		"--allow-nested",
		"--notifications",
		"WIDEBOI_LAYOUT",
		"WIDEBOI_PREFIX",
		"WIDEBOI_SOCK",
		"WIDEBOI_SESSION",
		"WIDEBOI_ALLOW_NESTED",
		"WIDEBOI_NOTIFICATIONS",
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

func TestParseCLIDisableAutoCleanup(t *testing.T) {
	opts, err := parseCLI([]string{"--disable-auto-cleanup", "-L", "test"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if !opts.flags.DisableAutoCleanup {
		t.Errorf("DisableAutoCleanup = false, want true")
	}

	// Also verify it passes through serverArgs
	args := serverArgs([]string{"--disable-auto-cleanup", "-L", "test"})
	serverOpts, err := parseCLI(args)
	if err != nil {
		t.Fatalf("parseCLI(serverArgs) error: %v", err)
	}
	if !serverOpts.flags.DisableAutoCleanup {
		t.Errorf("serverOpts.flags.DisableAutoCleanup = false, want true")
	}
}

func TestParseCLIEndSessionOnOwnerLoss(t *testing.T) {
	opts, err := parseCLI([]string{"--end-session-on-owner-loss", "-L", "test"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if !opts.flags.EndSessionOnOwnerLoss {
		t.Errorf("EndSessionOnOwnerLoss = false, want true")
	}

	// The spawned server must see the same setting: it decides what an
	// owner's EOF means, and the client decides what a SIGHUP means.
	serverOpts, err := parseCLI(serverArgs([]string{"--end-session-on-owner-loss", "-L", "test"}))
	if err != nil {
		t.Fatalf("parseCLI(serverArgs) error: %v", err)
	}
	if !serverOpts.flags.EndSessionOnOwnerLoss {
		t.Errorf("serverOpts.flags.EndSessionOnOwnerLoss = false, want true")
	}
}

func TestParseCLITLSFlags(t *testing.T) {
	opts, err := parseCLI([]string{
		"--disable-tls",
		"--tls-cert", "/tmp/cert.pem",
		"--tls-key", "/tmp/key.pem",
		"--websocket", "127.0.0.1:8080",
	})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if !opts.flags.DisableTLS {
		t.Errorf("DisableTLS = false, want true")
	}
	if opts.flags.TLSCert != "/tmp/cert.pem" {
		t.Errorf("TLSCert = %q, want /tmp/cert.pem", opts.flags.TLSCert)
	}
	if opts.flags.TLSKey != "/tmp/key.pem" {
		t.Errorf("TLSKey = %q, want /tmp/key.pem", opts.flags.TLSKey)
	}

	opts2, err := parseCLI([]string{"--tls"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if !opts2.flags.TLS {
		t.Errorf("TLS = false, want true")
	}
}

func TestParseCLIWebSubcommand(t *testing.T) {
	opts, err := parseCLI([]string{"web", "start", "--addr", "127.0.0.1:9090", "--rotate-token"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if opts.subcommand != "web" {
		t.Errorf("subcommand = %q, want web", opts.subcommand)
	}
	wantArgs := []string{"start", "--addr", "127.0.0.1:9090", "--rotate-token"}
	if len(opts.subcommandArgs) != len(wantArgs) {
		t.Fatalf("subcommandArgs = %v, want %v", opts.subcommandArgs, wantArgs)
	}
	for i := range wantArgs {
		if opts.subcommandArgs[i] != wantArgs[i] {
			t.Errorf("subcommandArgs[%d] = %q, want %q", i, opts.subcommandArgs[i], wantArgs[i])
		}
	}
}

// A session that ended under the user says so: which session, why as
// far as the client knows, and where the record is.
func TestPrintEndNotice(t *testing.T) {
	var buf bytes.Buffer
	printEndNotice(&buf, "/tmp/wb/work.sock", "server signal: terminated")
	got := buf.String()
	for _, want := range []string{"/tmp/wb/work.sock", "server signal: terminated", "/tmp/wb/exits.log"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q lacks %q", got, want)
		}
	}
}

func TestParseCLIAllowNested(t *testing.T) {
	opts, err := parseCLI([]string{"--allow-nested"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if !opts.flags.AllowNested {
		t.Errorf("opts.flags.AllowNested = false, want true")
	}

	opts2, err := parseCLI([]string{"attach", "--allow-nested"})
	if err != nil {
		t.Fatalf("parseCLI error: %v", err)
	}
	if !opts2.flags.AllowNested {
		t.Errorf("opts2.flags.AllowNested = false, want true")
	}
}

func TestCheckNestedSession(t *testing.T) {
	mockEnv := func(env map[string]string) func(string) string {
		return func(k string) string {
			return env[k]
		}
	}

	cleanEnv := mockEnv(map[string]string{})
	nestedCases := []struct {
		name string
		env  map[string]string
	}{
		{"WIDEBOI", map[string]string{"WIDEBOI": "1"}},
		{"LC_WIDEBOI", map[string]string{"LC_WIDEBOI": "1"}},
		{"WIDEBOI_PANE_ID", map[string]string{"WIDEBOI_PANE_ID": "2"}},
	}

	for _, tc := range nestedCases {
		t.Run("nested_by_"+tc.name, func(t *testing.T) {
			env := mockEnv(tc.env)

			// Default subcommand (plain wideboi)
			opts := cliOptions{ownerFD: -1}
			if err := checkNestedSession(opts, env); err == nil {
				t.Errorf("checkNestedSession() want error for plain wideboi in nested session, got nil")
			}

			// attach subcommand
			attachOpts := cliOptions{subcommand: "attach", ownerFD: -1}
			if err := checkNestedSession(attachOpts, env); err == nil {
				t.Errorf("checkNestedSession() want error for attach in nested session, got nil")
			}

			// standalone server subcommand
			serverOpts := cliOptions{subcommand: "server", ownerFD: -1}
			if err := checkNestedSession(serverOpts, env); err == nil {
				t.Errorf("checkNestedSession() want error for server in nested session, got nil")
			}

			// desktop subcommand
			desktopOpts := cliOptions{subcommand: "desktop", ownerFD: -1}
			if err := checkNestedSession(desktopOpts, env); err == nil {
				t.Errorf("checkNestedSession() want error for desktop in nested session, got nil")
			}

			// Internal server spawned with ownerFD != -1 must NOT be blocked
			spawnedServerOpts := cliOptions{subcommand: "server", ownerFD: 3}
			if err := checkNestedSession(spawnedServerOpts, env); err != nil {
				t.Errorf("checkNestedSession() spawned server want nil, got: %v", err)
			}

			// Subcommands like split, ls, capture, status must NOT be blocked
			for _, sub := range []string{"split", "ls", "status", "capture", "close", "wait"} {
				subOpts := cliOptions{subcommand: sub, ownerFD: -1}
				if err := checkNestedSession(subOpts, env); err != nil {
					t.Errorf("checkNestedSession() for subcommand %q want nil, got: %v", sub, err)
				}
			}

			// --allow-nested flag allows running
			allowedOpts := cliOptions{flags: config.ConfigFlags{AllowNested: true}, ownerFD: -1}
			if err := checkNestedSession(allowedOpts, env); err != nil {
				t.Errorf("checkNestedSession() with AllowNested flag want nil, got: %v", err)
			}

			// WIDEBOI_ALLOW_NESTED=1 environment variable allows running
			envWithAllow := map[string]string{}
			for k, v := range tc.env {
				envWithAllow[k] = v
			}
			envWithAllow["WIDEBOI_ALLOW_NESTED"] = "1"
			if err := checkNestedSession(opts, mockEnv(envWithAllow)); err != nil {
				t.Errorf("checkNestedSession() with WIDEBOI_ALLOW_NESTED=1 want nil, got: %v", err)
			}
		})
	}

	t.Run("clean_environment", func(t *testing.T) {
		opts := cliOptions{ownerFD: -1}
		if err := checkNestedSession(opts, cleanEnv); err != nil {
			t.Errorf("checkNestedSession() in clean environment want nil, got: %v", err)
		}
	})
}

func TestConnectOrSpawnRefusesNestedServerSpawn(t *testing.T) {
	t.Setenv("WIDEBOI", "1")
	cfg := config.Config{Socket: filepath.Join(t.TempDir(), "nonexistent.sock")}
	_, _, err := connectOrSpawn(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to spawn a nested session") {
		t.Errorf("connectOrSpawn want refusal to spawn nested session, got: %v", err)
	}

	t.Setenv("WIDEBOI_ALLOW_NESTED", "1")
	_, _, err = connectOrSpawn(cfg, nil)
	if err != nil && strings.Contains(err.Error(), "refusing to spawn a nested session") {
		t.Errorf("connectOrSpawn with WIDEBOI_ALLOW_NESTED=1 should not be rejected by nested guard, got: %v", err)
	}
}
