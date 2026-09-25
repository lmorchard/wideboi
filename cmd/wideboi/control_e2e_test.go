package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server"
	"github.com/lmorchard/wideboi/internal/transport"
)

func buildWideboiBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "wideboi")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}
	return binPath
}

func TestControlSubcommandsE2E(t *testing.T) {
	bin := buildWideboiBinary(t)
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "e2e.sock")

	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()

	srv := server.NewServer(nil, "/bin/sh", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv.ListenSocket(ctx, sl)
	go func() {
		_ = srv.Run(ctx)
	}()

	runCLI := func(args ...string) (string, string, int) {
		cmd := exec.Command(bin, append([]string{"-s", sockPath}, args...)...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		return stdout.String(), stderr.String(), exitCode
	}

	// 1. Create two panes via split (keeping one alive prevents server shutdown on pane close)
	stdout, stderr, code := runCLI("split", "echo", "agent-script-output", "&&", "sleep", "10")
	if code != 0 {
		t.Fatalf("split 1 failed (exit %d): stderr=%q", code, stderr)
	}
	paneIDStr := strings.TrimSpace(stdout)
	paneID, err := strconv.Atoi(paneIDStr)
	if err != nil || paneID <= 0 {
		t.Fatalf("invalid pane id returned by split: %q", stdout)
	}

	stdout2, stderr2, code2 := runCLI("split", "sleep", "10")
	if code2 != 0 {
		t.Fatalf("split 2 failed (exit %d): stderr=%q", code2, stderr2)
	}
	paneID2Str := strings.TrimSpace(stdout2)

	// 2. Capture output from the pane
	deadline := time.Now().Add(3 * time.Second)
	found := false
	var capturedText string
	for time.Now().Before(deadline) {
		stdout, stderr, code = runCLI("capture", paneIDStr)
		if code == 0 && strings.Contains(stdout, "agent-script-output") {
			found = true
			capturedText = stdout
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatalf("expected capture to include 'agent-script-output', got stdout=%q, stderr=%q, code=%d", capturedText, stderr, code)
	}

	// 3. Send input to the pane
	stdout, stderr, code = runCLI("send", paneIDStr, "date", "--enter")
	if code != 0 {
		t.Fatalf("send failed (exit %d): stderr=%q", code, stderr)
	}

	// 4. Close the pane
	stdout, stderr, code = runCLI("close", paneIDStr)
	if code != 0 {
		t.Fatalf("close failed (exit %d): stderr=%q", code, stderr)
	}

	// 5. Verify operations on closed pane report errors and non-zero exit codes
	_, stderr, code = runCLI("close", paneIDStr)
	if code == 0 {
		t.Fatalf("expected error closing closed pane, got exit 0")
	}
	if !strings.Contains(stderr, fmt.Sprintf("pane %d not found", paneID)) {
		t.Fatalf("expected 'pane %d not found' in stderr, got %q", paneID, stderr)
	}

	_, stderr, code = runCLI("capture", paneIDStr)
	if code == 0 {
		t.Fatalf("expected error capturing closed pane, got exit 0")
	}
	if !strings.Contains(stderr, fmt.Sprintf("pane %d not found", paneID)) {
		t.Fatalf("expected 'pane %d not found' in stderr, got %q", paneID, stderr)
	}

	_, stderr, code = runCLI("send", paneIDStr, "test")
	if code == 0 {
		t.Fatalf("expected error sending to closed pane, got exit 0")
	}
	if !strings.Contains(stderr, fmt.Sprintf("pane %d not found", paneID)) {
		t.Fatalf("expected 'pane %d not found' in stderr, got %q", paneID, stderr)
	}

	// 6. Test unreachable server
	cmd := exec.Command(bin, "-s", filepath.Join(dir, "nonexistent.sock"), "capture", "1")
	var badErr bytes.Buffer
	cmd.Stderr = &badErr
	if err := cmd.Run(); err == nil {
		t.Fatal("expected command against nonexistent socket to fail, got success")
	}
	if !strings.Contains(badErr.String(), "no wideboi server running") {
		t.Fatalf("expected 'no wideboi server running' in stderr, got %q", badErr.String())
	}

	// 7. Test concurrent client operations
	const concurrent = 5
	var wg sync.WaitGroup
	errCh := make(chan error, concurrent*2)

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			marker := fmt.Sprintf("marker-%d", idx)
			out, errOut, exit := runCLI("split", "echo", marker, "&&", "sleep", "5")
			if exit != 0 {
				errCh <- fmt.Errorf("concurrent split %d failed: %s", idx, errOut)
				return
			}
			pID := strings.TrimSpace(out)

			// Capture until marker is seen
			d := time.Now().Add(3 * time.Second)
			mFound := false
			for time.Now().Before(d) {
				cOut, _, cExit := runCLI("capture", pID)
				if cExit == 0 && strings.Contains(cOut, marker) {
					mFound = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !mFound {
				errCh <- fmt.Errorf("marker %s not found in concurrent capture", marker)
				return
			}

			// Close
			_, cErr, cExit := runCLI("close", pID)
			if cExit != 0 {
				errCh <- fmt.Errorf("concurrent close %s failed: %s", pID, cErr)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// Finally close pane 2
	_, _, _ = runCLI("close", paneID2Str)
}
