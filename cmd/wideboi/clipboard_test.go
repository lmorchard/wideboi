package main

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

func TestIsSSHSession(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want bool
	}{
		{env: map[string]string{}, want: false},
		{env: map[string]string{"SSH_CLIENT": "1.2.3.4 5678 22"}, want: true},
		{env: map[string]string{"SSH_TTY": "/dev/pts/1"}, want: true},
		{env: map[string]string{"SSH_CONNECTION": "1.2.3.4 5678 10.0.0.1 22"}, want: true},
	}

	for _, tc := range cases {
		getenv := func(key string) string { return tc.env[key] }
		if got := isSSHSession(getenv); got != tc.want {
			t.Errorf("isSSHSession(%v) = %v, want %v", tc.env, got, tc.want)
		}
	}
}

func TestLocalClipboardCmd(t *testing.T) {
	// Darwin always uses pbcopy
	prog, args := localClipboardCmd("darwin", func(string) (string, error) { return "", nil })
	if prog != "pbcopy" || len(args) != 0 {
		t.Errorf("darwin got %s %v, want pbcopy", prog, args)
	}

	// Linux with wl-copy
	prog, args = localClipboardCmd("linux", func(p string) (string, error) {
		if p == "wl-copy" {
			return "/usr/bin/wl-copy", nil
		}
		return "", io.EOF
	})
	if prog != "wl-copy" {
		t.Errorf("linux with wl-copy got %s %v, want wl-copy", prog, args)
	}

	// Linux with xclip
	prog, args = localClipboardCmd("linux", func(p string) (string, error) {
		if p == "xclip" {
			return "/usr/bin/xclip", nil
		}
		return "", io.EOF
	})
	if prog != "xclip" || len(args) != 2 || args[0] != "-selection" || args[1] != "clipboard" {
		t.Errorf("linux with xclip got %s %v, want xclip -selection clipboard", prog, args)
	}
}

func TestWriteLocalClipboardRunsRunner(t *testing.T) {
	origRunner := localClipboardRunner
	origGetenv := getenvFunc
	defer func() {
		localClipboardRunner = origRunner
		getenvFunc = origGetenv
	}()

	getenvFunc = func(string) string { return "" } // not SSH

	var mu sync.Mutex
	var gotText string
	done := make(chan struct{})

	localClipboardRunner = func(ctx context.Context, text string) error {
		mu.Lock()
		gotText = text
		mu.Unlock()
		close(done)
		return nil
	}

	writeLocalClipboard("copied payload")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for clipboard runner")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotText != "copied payload" {
		t.Errorf("got payload %q, want %q", gotText, "copied payload")
	}
}

func TestWriteLocalClipboardSkipsInSSH(t *testing.T) {
	origRunner := localClipboardRunner
	origGetenv := getenvFunc
	defer func() {
		localClipboardRunner = origRunner
		getenvFunc = origGetenv
	}()

	getenvFunc = func(k string) string {
		if k == "SSH_CLIENT" {
			return "remote"
		}
		return ""
	}

	called := false
	localClipboardRunner = func(ctx context.Context, text string) error {
		called = true
		return nil
	}

	writeLocalClipboard("should skip")
	time.Sleep(50 * time.Millisecond)
	if called {
		t.Error("expected runner not to be called in SSH session")
	}
}
