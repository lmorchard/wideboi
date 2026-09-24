package term_test

import (
	"os"
	"strings"
	"testing"

	"github.com/lmorchard/wideboi/internal/server/term"
)

func TestOSC7TracksCWD(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}

	cases := []struct {
		name    string
		payload string
		wantCWD string
	}{
		{
			name:    "empty hostname file URI with BEL",
			payload: "\x1b]7;file:///Users/les/wideboi\x07",
			wantCWD: "/Users/les/wideboi",
		},
		{
			name:    "localhost hostname file URI with ST",
			payload: "\x1b]7;file://localhost/Users/les/wideboi\x1b\\",
			wantCWD: "/Users/les/wideboi",
		},
		{
			name:    "local machine hostname file URI",
			payload: "\x1b]7;file://" + hostname + "/Users/les/wideboi\x07",
			wantCWD: "/Users/les/wideboi",
		},
		{
			name:    "percent-decoded URI with spaces",
			payload: "\x1b]7;file:///Users/les/my%20wideboi%20project\x07",
			wantCWD: "/Users/les/my wideboi project",
		},
		{
			name:    "root path",
			payload: "\x1b]7;file:///\x07",
			wantCWD: "/",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := term.NewVT(40, 10)
			defer g.Close()

			if _, err := g.Write([]byte(tc.payload)); err != nil {
				t.Fatalf("Write(%q): %v", tc.payload, err)
			}
			if got := g.CWD(); got != tc.wantCWD {
				t.Errorf("CWD() after %q = %q, want %q", tc.payload, got, tc.wantCWD)
			}
		})
	}
}

func TestOSC7RejectsInvalidURIs(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{
			name:    "foreign remote hostname",
			payload: "\x1b]7;file://foreign.remote.host/Users/les\x07",
		},
		{
			name:    "http scheme instead of file",
			payload: "\x1b]7;http://localhost/Users/les\x07",
		},
		{
			name:    "no scheme",
			payload: "\x1b]7;/Users/les/path\x07",
		},
		{
			name:    "relative path in file URI",
			payload: "\x1b]7;file://localhost\x07",
		},
		{
			name:    "empty payload",
			payload: "\x1b]7;\x07",
		},
		{
			name:    "path containing escape control character",
			payload: "\x1b]7;file:///tmp/%1b%5d0;injected%07\x07",
		},
		{
			name:    "path containing newline",
			payload: "\x1b]7;file:///tmp/%0ainjected\x07",
		},
		{
			name:    "path containing tab",
			payload: "\x1b]7;file:///tmp/%09injected\x07",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := term.NewVT(40, 10)
			defer g.Close()

			// Pre-set a valid CWD
			if _, err := g.Write([]byte("\x1b]7;file:///valid/path\x07")); err != nil {
				t.Fatalf("Write valid: %v", err)
			}
			if g.CWD() != "/valid/path" {
				t.Fatalf("initial CWD = %q, want /valid/path", g.CWD())
			}

			// Feed invalid payload
			if _, err := g.Write([]byte(tc.payload)); err != nil {
				t.Fatalf("Write(%q): %v", tc.payload, err)
			}
			if got := g.CWD(); got != "/valid/path" {
				t.Errorf("CWD() changed after invalid %q to %q", tc.payload, got)
			}
		})
	}
}

func TestOSC1337SetUserVar(t *testing.T) {
	g := term.NewVT(40, 10)
	defer g.Close()

	// bWFpbg== is base64 for "main"
	// Y2xhdWRl is base64 for "claude"
	seq1 := "\x1b]1337;SetUserVar=git_branch=bWFpbg==\x07"
	seq2 := "\x1b]1337;SetUserVar=agent_name=Y2xhdWRl\x07"

	if _, err := g.Write([]byte(seq1 + seq2)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	vars := g.UserVars()
	if vars["git_branch"] != "main" {
		t.Errorf("vars[git_branch] = %q, want %q", vars["git_branch"], "main")
	}
	if vars["agent_name"] != "claude" {
		t.Errorf("vars[agent_name] = %q, want %q", vars["agent_name"], "claude")
	}

	// Removal via empty value: SetUserVar=git_branch=
	seqRemove := "\x1b]1337;SetUserVar=git_branch=\x07"
	if _, err := g.Write([]byte(seqRemove)); err != nil {
		t.Fatalf("Write removal: %v", err)
	}

	varsAfter := g.UserVars()
	if _, ok := varsAfter["git_branch"]; ok {
		t.Errorf("git_branch was not removed after empty SetUserVar: %+v", varsAfter)
	}
	if varsAfter["agent_name"] != "claude" {
		t.Errorf("agent_name modified unexpectedly: %+v", varsAfter)
	}
}

func TestOSC1337SetUserVarValidationAndLimits(t *testing.T) {
	g := term.NewVT(40, 10)
	defer g.Close()

	// 1. Invalid base64
	if _, err := g.Write([]byte("\x1b]1337;SetUserVar=key1=not-valid-base64!@#\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok := g.UserVars()["key1"]; ok {
		t.Errorf("invalid base64 was accepted")
	}

	// 2. Invalid UTF-8 in base64: base64 of "\xff\xfe" is "//4="
	if _, err := g.Write([]byte("\x1b]1337;SetUserVar=key2=//4=\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok := g.UserVars()["key2"]; ok {
		t.Errorf("invalid UTF-8 was accepted")
	}

	// 3. Invalid key name characters (spaces or symbols)
	if _, err := g.Write([]byte("\x1b]1337;SetUserVar=bad key=bWFpbg==\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(g.UserVars()) != 0 {
		t.Errorf("invalid key name was accepted: %+v", g.UserVars())
	}

	// 4. Key name too long (>64 bytes)
	longKey := strings.Repeat("k", 65)
	if _, err := g.Write([]byte("\x1b]1337;SetUserVar=" + longKey + "=bWFpbg==\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok := g.UserVars()[longKey]; ok {
		t.Errorf("key > 64 chars was accepted")
	}

	// 5. Value too long (>4096 bytes)
	// 4096 decoded bytes requires ~5464 base64 chars
	oversizedB64 := strings.Repeat("YWJj", 1400) // 5600 chars of base64 -> 4200 decoded bytes
	if _, err := g.Write([]byte("\x1b]1337;SetUserVar=toobig=" + oversizedB64 + "\x07")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok := g.UserVars()["toobig"]; ok {
		t.Errorf("value > 4096 bytes was accepted")
	}

	// 6. Max 64 variables per pane
	for i := 0; i < 70; i++ {
		key := "k" + strings.Repeat("x", i)
		if _, err := g.Write([]byte("\x1b]1337;SetUserVar=" + key + "=YQ==\x07")); err != nil {
			t.Fatalf("Write var %d: %v", i, err)
		}
	}
	if count := len(g.UserVars()); count > 64 {
		t.Errorf("got %d variables stored, want at most 64", count)
	}
}
