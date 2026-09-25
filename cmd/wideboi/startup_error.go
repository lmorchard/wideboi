package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lmorchard/wideboi/internal/logger"
)

// startupExitError forms an error explaining that the spawned server exited during startup,
// extracting any fatal or error message found in its log.
func startupExitError(socket string) error {
	logPath := logger.Path(socket, "server")
	reason := extractStartupReason(logPath)
	if reason != "" {
		return fmt.Errorf("wideboi server exited during startup: %s; see %s", reason, logPath)
	}
	return fmt.Errorf("wideboi server exited during startup; see %s", logPath)
}

// extractStartupReason scans the server log for the last error, panic, or fatal line.
func extractStartupReason(logPath string) string {
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "panic:"); idx != -1 {
			return strings.TrimSpace(line[idx:])
		}
		if strings.HasPrefix(line, "wideboi:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "wideboi:"))
		}
		if strings.Contains(line, "level=ERROR") {
			msg := parseSlogAttr(line, "msg")
			errStr := parseSlogAttr(line, "err")
			if msg != "" && errStr != "" {
				return msg + ": " + errStr
			}
			if errStr != "" {
				return errStr
			}
			if msg != "" {
				return msg
			}
			return line
		}
	}
	return ""
}

// parseSlogAttr extracts the value of a key=value attribute from a log line formatted by slog text handler.
func parseSlogAttr(line, key string) string {
	target := key + "="
	idx := strings.Index(line, target)
	if idx == -1 {
		return ""
	}
	val := line[idx+len(target):]
	if len(val) == 0 {
		return ""
	}
	if val[0] == '"' {
		var sb strings.Builder
		escaped := false
		for i := 1; i < len(val); i++ {
			c := val[i]
			if escaped {
				sb.WriteByte(c)
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				return sb.String()
			} else {
				sb.WriteByte(c)
			}
		}
		return sb.String()
	}
	if end := strings.IndexAny(val, " \t\r\n"); end != -1 {
		return val[:end]
	}
	return val
}
