package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var getenvFunc = os.Getenv

func isSSHSession(getenv func(string) string) bool {
	return getenv("SSH_CLIENT") != "" || getenv("SSH_TTY") != "" || getenv("SSH_CONNECTION") != ""
}

func localClipboardCmd(goos string, lookPath func(string) (string, error)) (string, []string) {
	switch goos {
	case "darwin":
		return "pbcopy", nil
	case "linux":
		if _, err := lookPath("wl-copy"); err == nil {
			return "wl-copy", nil
		}
		if _, err := lookPath("xclip"); err == nil {
			return "xclip", []string{"-selection", "clipboard"}
		}
		if _, err := lookPath("xsel"); err == nil {
			return "xsel", []string{"--clipboard", "--input"}
		}
	}
	return "", nil
}

var localClipboardRunner = defaultLocalClipboardRunner

func defaultLocalClipboardRunner(ctx context.Context, text string) error {
	prog, args := localClipboardCmd(runtime.GOOS, exec.LookPath)
	if prog == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// writeLocalClipboard writes text to the local system clipboard in the
// background if not running in an SSH session.
func writeLocalClipboard(text string) {
	if text == "" || isSSHSession(getenvFunc) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = localClipboardRunner(ctx, text)
	}()
}
