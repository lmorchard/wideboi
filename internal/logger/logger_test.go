package logger

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":      slog.LevelInfo,
		"trace": LevelTrace,
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"DEBUG": slog.LevelDebug,
		" Info": slog.LevelInfo,
	}
	for in, want := range cases {
		got, err := ParseLevel(in)
		if err != nil {
			t.Errorf("ParseLevel(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

// A typo must be an error, not a silent fallback: an unmatchable value
// that behaves exactly like an absent one is how the pgdn binding
// shipped dead (docs/LESSONS.md).
func TestParseLevelRejectsUnknown(t *testing.T) {
	for _, in := range []string{"verbose", "warning", "dbg", "5"} {
		if _, err := ParseLevel(in); err == nil {
			t.Errorf("ParseLevel(%q) accepted an unknown level", in)
		}
	}
}

// slog names an unknown level relative to its neighbour, so Trace would
// print as "DEBUG-4" -- which reads as a debug line and greps as one.
func TestTraceIsNamedTrace(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, LevelTrace))
	log.Log(context.Background(), LevelTrace, "per-message detail")
	if !strings.Contains(buf.String(), "level=TRACE") {
		t.Errorf("trace line not labelled TRACE: %q", buf.String())
	}
}

// The default verbosity must not record per-message traffic: a session
// left running for a day wrote about 500MB of it.
func TestInfoDropsTraceAndDebug(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newHandler(&buf, slog.LevelInfo))
	log.Log(context.Background(), LevelTrace, "trace")
	log.Debug("debug")
	log.Info("info")
	if strings.Contains(buf.String(), "trace") || strings.Contains(buf.String(), "debug") {
		t.Errorf("info-level handler recorded trace or debug: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "info") {
		t.Errorf("info-level handler dropped an info line: %q", buf.String())
	}
}
