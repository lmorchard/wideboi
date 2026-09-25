package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isSessionActive checks if the socket answers a dial.
func isSessionActive(sock string) bool {
	conn, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		return true
	}
	return false
}

// cleanupDeadArtifacts removes artifacts of dead sessions in dir. If includeLogs is true,
// it also removes dead session logs (*.server.log and *.client.log).
func cleanupDeadArtifacts(w io.Writer, dir string, includeLogs bool) error {
	// 1. Unconditionally remove legacy shared logs.
	legacy := []string{"server.log", "client.log"}
	for _, l := range legacy {
		path := filepath.Join(dir, l)
		if err := os.Remove(path); err == nil {
			fmt.Fprintf(w, "removed legacy %s\n", l)
		}
	}

	// Track which sessions are active to avoid repeated dials.
	activeCache := make(map[string]bool)
	isActive := func(name string) bool {
		if active, ok := activeCache[name]; ok {
			return active
		}
		active := isSessionActive(filepath.Join(dir, name+".sock"))
		activeCache[name] = active
		return active
	}

	// 2. Clean up dead sockets.
	socks, err := filepath.Glob(filepath.Join(dir, "*.sock"))
	if err == nil {
		for _, sock := range socks {
			name := strings.TrimSuffix(filepath.Base(sock), ".sock")
			if !isActive(name) {
				if err := os.Remove(sock); err == nil {
					fmt.Fprintf(w, "removed dead socket %s\n", filepath.Base(sock))
				}
			}
		}
	}

	// 3. Clean up dead logs only when requested (e.g. manual 'wideboi cleanup').
	// Automatic exit cleanup preserves other dead sessions' logs for post-mortem analysis.
	if includeLogs {
		logs, err := filepath.Glob(filepath.Join(dir, "*.log"))
		if err == nil {
			for _, log := range logs {
				base := filepath.Base(log)
				// e.g. default.server.log -> default
				name := strings.TrimSuffix(base, ".server.log")
				name = strings.TrimSuffix(name, ".client.log")

				// If trim didn't change anything, it's not a session log (or was legacy, already gone)
				if name == base {
					continue
				}

				if !isActive(name) {
					if err := os.Remove(log); err == nil {
						fmt.Fprintf(w, "removed dead log %s\n", base)
					}
				}
			}
		}
	}

	// 4. Remove credentials left behind by servers that exited abruptly.
	tokens, err := filepath.Glob(filepath.Join(dir, "*.web-token"))
	if err == nil {
		for _, token := range tokens {
			name := strings.TrimSuffix(filepath.Base(token), ".web-token")
			if !isActive(name) {
				if err := os.Remove(token); err == nil {
					fmt.Fprintf(w, "removed dead web token %s\n", filepath.Base(token))
				}
			}
		}
	}

	return nil
}

// runCleanup removes legacy logs and artifacts of dead sessions in dir, including logs.
func runCleanup(w io.Writer, dir string) error {
	return cleanupDeadArtifacts(w, dir, true)
}

// runAutoCleanupSweep removes dead sockets and tokens in dir, leaving logs intact.
func runAutoCleanupSweep(dir string) error {
	return cleanupDeadArtifacts(io.Discard, dir, false)
}
