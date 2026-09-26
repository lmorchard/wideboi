package main

import (
	"bytes"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnnounceWebClientRedactsPersistentLog(t *testing.T) {
	for _, tc := range []struct {
		generated  bool
		tlsEnabled bool
		wantScheme string
	}{
		{true, true, "https"},
		{true, false, "http"},
		{false, true, "https"},
		{false, false, "http"},
	} {
		var stderr, logOutput bytes.Buffer
		log := slog.New(slog.NewTextHandler(&logOutput, nil))
		announceWebClient(&stderr, log, "localhost:8080", ":8080", "secret123", tc.generated, tc.tlsEnabled)

		if strings.Contains(logOutput.String(), "secret123") {
			t.Fatalf("generated=%t tls=%t: token in persistent log: %s", tc.generated, tc.tlsEnabled, logOutput.String())
		}
		expectedPrefix := "wideboi: web client listening at " + tc.wantScheme + "://"
		if !strings.HasPrefix(stderr.String(), expectedPrefix) {
			t.Fatalf("stderr does not have prefix %q: %q", expectedPrefix, stderr.String())
		}
		if tc.generated {
			if !strings.Contains(stderr.String(), "#token=secret123") {
				t.Fatalf("generated link missing fragment token: %q", stderr.String())
			}
		} else if strings.Contains(stderr.String(), "secret123") {
			t.Fatalf("configured token leaked to stderr: %q", stderr.String())
		}
	}
}

func TestWriteWebToken(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "session.sock")
	for _, token := range []string{"first", "rotated"} {
		if err := writeWebToken(socket, token); err != nil {
			t.Fatal(err)
		}
		path := webTokenPath(socket)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != token+"\n" {
			t.Fatalf("token file = %q, want %q", contents, token)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("token file permissions = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestWarnIfWebClientExposed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ip         net.IP
		tlsEnabled bool
		warn       bool
	}{
		{"IPv4 loopback no tls", net.ParseIP("127.0.0.1"), false, false},
		{"IPv6 loopback no tls", net.ParseIP("::1"), false, false},
		{"IPv4 wildcard no tls", net.IPv4zero, false, true},
		{"IPv6 wildcard no tls", net.IPv6zero, false, true},
		{"network address no tls", net.ParseIP("192.0.2.1"), false, true},
		{"IPv4 wildcard with tls", net.IPv4zero, true, false},
		{"IPv6 wildcard with tls", net.IPv6zero, true, false},
		{"network address with tls", net.ParseIP("192.0.2.1"), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr, logOutput bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logOutput, nil))
			warnIfWebClientExposed(&stderr, log, &net.TCPAddr{IP: tc.ip, Port: 8080}, tc.tlsEnabled)
			if got := stderr.Len() > 0; got != tc.warn {
				t.Errorf("stderr warning = %t, want %t", got, tc.warn)
			}
			if got := logOutput.Len() > 0; got != tc.warn {
				t.Errorf("log warning = %t, want %t", got, tc.warn)
			}
		})
	}
}
