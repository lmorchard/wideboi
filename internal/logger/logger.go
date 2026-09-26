package logger

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LevelTrace is below Debug, for lines that fire per message or per
// frame. slog has no trace level of its own; its levels are integers
// with room between them for exactly this.
//
// The distinction matters because the log files are appended to
// forever: a per-pane-update line at Debug wrote about 60 lines a
// second for as long as a session ran.
const LevelTrace = slog.LevelDebug - 4

// ParseLevel maps a configured name onto a level. Empty means Info.
//
// An unknown name is an error rather than a fallback: a typo that
// quietly behaves like the default is indistinguishable from not
// having set anything (docs/LESSONS.md).
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("log level %q: want trace, debug, info, warn or error", name)
	}
}

// Path is where component's log for the session at socket lives:
// beside the socket, named after it. Per session, so several sessions
// do not interleave in one file, and a test harness's private socket
// directory gets private logs. Exposed so an error can point at the log
// of a process whose stderr goes nowhere.
func Path(socket, component string) string {
	return strings.TrimSuffix(socket, ".sock") + "." + component + ".log"
}

// ExitsPath is the exits log for the sessions in socket's directory.
// One file for all of them, and never removed by any cleanup: a clean
// exit deletes the session's own logs, so this is the only record of
// why a session ended that is sure to outlive it.
func ExitsPath(socket string) string {
	return filepath.Join(filepath.Dir(socket), "exits.log")
}

// SessionOf names the session served at socket, for exits records.
func SessionOf(socket string) string {
	return strings.TrimSuffix(filepath.Base(socket), ".sock")
}

// AppendExit appends one record of a session event -- a start, an exit,
// a signal, a sweep -- to the exits log beside socket. Opened, written
// once and closed per record: several processes append to the same
// file, and one short O_APPEND write keeps each line whole.
//
// Never pass a token or a command line: this file is kept forever.
func AppendExit(socket, component, event string, args ...any) error {
	var buf bytes.Buffer
	attrs := append([]any{"session", SessionOf(socket), "component", component, "pid", os.Getpid()}, args...)
	slog.New(newHandler(&buf, slog.LevelInfo)).Info(event, attrs...)

	f, err := os.OpenFile(ExitsPath(socket), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(buf.Bytes())
	return err
}

// Init initializes file-based structured logging to path, recording
// level and above.
func Init(path string, level slog.Level, teeToConsole bool) (*os.File, error) {
	_ = os.MkdirAll(filepath.Dir(path), 0700)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}

	var w io.Writer = f
	if teeToConsole {
		w = io.MultiWriter(f, os.Stderr)
	}

	slog.SetDefault(slog.New(newHandler(w, level)))
	return f, nil
}

func newHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		// Without this, LevelTrace prints as "DEBUG-4": it reads as a
		// debug line and greps as one.
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey && len(groups) == 0 {
				if l, ok := a.Value.Any().(slog.Level); ok && l == LevelTrace {
					a.Value = slog.StringValue("TRACE")
				}
			}
			return a
		},
	})
}
