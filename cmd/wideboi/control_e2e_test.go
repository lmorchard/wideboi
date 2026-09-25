package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
	stdout, stderr, code := runCLI("split", "echo agent-script-output && sleep 10")
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
			out, errOut, exit := runCLI("split", "echo "+marker+" && sleep 5")
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

// runWideboi runs the built binary against sock and reports stdout,
// stderr, and the process exit code.
func runWideboi(t *testing.T, bin, sock string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"-s", sock}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("running %v: %v", args, err)
	return "", "", 0
}

// TestWaitE2E pins `wideboi wait` as a process: it exits with the pane
// process's own code, 124 on --timeout, and 1 for an unknown pane.
func TestWaitE2E(t *testing.T) {
	bin := buildWideboiBinary(t)
	sockPath := filepath.Join(t.TempDir(), "wait.sock")
	sl, err := transport.NewSocketListener(sockPath)
	if err != nil {
		t.Fatalf("NewSocketListener failed: %v", err)
	}
	defer sl.Close()
	srv := server.NewServer(nil, "/bin/sh", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.ListenSocket(ctx, sl)
	go func() { _ = srv.Run(ctx) }()

	out, errOut, code := runWideboi(t, bin, sockPath, "split", "sleep 30")
	if code != 0 {
		t.Fatalf("split anchor: exit %d, %s", code, errOut)
	}
	sleeper := strings.TrimSpace(out)

	out, errOut, code = runWideboi(t, bin, sockPath, "split", "--keep", "exit 4")
	if code != 0 {
		t.Fatalf("split --keep: exit %d, %s", code, errOut)
	}
	kept := strings.TrimSpace(out)

	if _, errOut, code := runWideboi(t, bin, sockPath, "wait", kept); code != 4 {
		t.Errorf("wait on 'exit 4': process exit %d (stderr %q), want 4", code, errOut)
	}

	start := time.Now()
	_, errOut, code = runWideboi(t, bin, sockPath, "wait", "--timeout", "200ms", sleeper)
	if code != 124 || !strings.Contains(errOut, "timed out") {
		t.Errorf("wait --timeout on a sleeper: exit %d, stderr %q; want 124 and a timeout message", code, errOut)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("wait --timeout 200ms took %v", d)
	}

	if _, errOut, code := runWideboi(t, bin, sockPath, "wait", "999"); code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("wait on unknown pane: exit %d, stderr %q; want 1 and not found", code, errOut)
	}

	runWideboi(t, bin, sockPath, "close", kept)
	runWideboi(t, bin, sockPath, "close", sleeper)
}

// TestSplitAutoSpawnE2E pins split starting a session when none is
// running: the pane works end to end, the server it spawned outlives
// split (detached), and it goes away when its last pane closes. A split
// that fails takes its freshly spawned server with it.
func TestSplitAutoSpawnE2E(t *testing.T) {
	bin := buildWideboiBinary(t)
	// The spawned server runs split's commands under $SHELL; pin it, so a
	// themed login shell cannot shape the output (docs/LESSONS.md).
	t.Setenv("SHELL", "/bin/sh")
	sock := filepath.Join(t.TempDir(), "auto.sock")
	t.Cleanup(func() { runWideboi(t, bin, sock, "kill-session") })

	serverGone := func(sock string) bool {
		_, _, code := runWideboi(t, bin, sock, "status")
		return code != 0
	}
	if !serverGone(sock) {
		t.Fatal("precondition: a server is already answering on the test socket")
	}

	out, errOut, code := runWideboi(t, bin, sock, "split", "--keep", "echo auto-spawned; exit 2")
	if code != 0 {
		t.Fatalf("split with no server: exit %d, stderr %q", code, errOut)
	}
	id := strings.TrimSpace(out)

	if _, errOut, code := runWideboi(t, bin, sock, "wait", id); code != 2 {
		t.Errorf("wait: exit %d (stderr %q), want 2", code, errOut)
	}
	if out, errOut, _ := runWideboi(t, bin, sock, "capture", id); !strings.Contains(out, "auto-spawned") {
		t.Errorf("capture = %q (stderr %q), want it to contain auto-spawned", out, errOut)
	}
	if _, errOut, code := runWideboi(t, bin, sock, "close", id); code != 0 {
		t.Fatalf("close: exit %d, stderr %q", code, errOut)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !serverGone(sock) {
		if time.Now().After(deadline) {
			t.Fatal("auto-spawned server still answering 5s after its last pane closed")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A split that fails must not leave an empty server behind.
	failSock := filepath.Join(t.TempDir(), "fail.sock")
	t.Cleanup(func() { runWideboi(t, bin, failSock, "kill-session") })
	if _, _, code := runWideboi(t, bin, failSock, "split", "--after", "999", "true"); code == 0 {
		t.Error("split --after 999 on a fresh session succeeded, want an error")
	}
	deadline = time.Now().Add(5 * time.Second)
	for !serverGone(failSock) {
		if time.Now().After(deadline) {
			t.Fatal("a server spawned for a failed split is still answering")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// startWait runs `wideboi wait id` in the background and returns a
// function that collects its exit code and stderr.
func startWait(t *testing.T, bin, sock, id string) func() (int, string) {
	t.Helper()
	cmd := exec.Command(bin, "-s", sock, "wait", id)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start wait: %v", err)
	}
	return func() (int, string) {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatal("wait never returned")
		}
		return cmd.ProcessState.ExitCode(), stderr.String()
	}
}

// TestWaitAnsweredWhenSessionEnds pins that a waiter hears an answer --
// the pane's code or why there is none -- rather than a dropped
// connection, both when kill-session ends the session and when closing
// the last pane does while its process is slow to die.
func TestWaitAnsweredWhenSessionEnds(t *testing.T) {
	bin := buildWideboiBinary(t)
	t.Setenv("SHELL", "/bin/sh")
	dir, err := os.MkdirTemp("", "wbend")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	// kill-session: the hangup kills sleep, so the waiter gets SIGHUP's 129.
	sock := filepath.Join(dir, "k.sock")
	t.Cleanup(func() { runWideboi(t, bin, sock, "kill-session") })
	out, errOut, code := runWideboi(t, bin, sock, "split", "--keep", "sleep 30")
	if code != 0 {
		t.Fatalf("split: exit %d, %s", code, errOut)
	}
	collect := startWait(t, bin, sock, strings.TrimSpace(out))
	waitForWaiter()
	runWideboi(t, bin, sock, "kill-session")
	if code, errOut := collect(); code != 129 {
		t.Errorf("wait across kill-session: exit %d, stderr %q; want 129 (SIGHUP)", code, errOut)
	}

	// Last pane closed, process ignoring the hangup: Close waits out the
	// grace, and the waiter must hear why there is no code before the
	// session's teardown closes its connection.
	sock2 := filepath.Join(dir, "l.sock")
	t.Cleanup(func() { runWideboi(t, bin, sock2, "kill-session") })
	out, errOut, code = runWideboi(t, bin, sock2, "split", "--keep", "trap '' HUP; sleep 5")
	if code != 0 {
		t.Fatalf("split: exit %d, %s", code, errOut)
	}
	id := strings.TrimSpace(out)
	collect = startWait(t, bin, sock2, id)
	waitForWaiter()
	runWideboi(t, bin, sock2, "close", id)
	if code, errOut := collect(); code == 0 || !strings.Contains(errOut, "closed before its process exited") {
		t.Errorf("wait across last-pane close: exit %d, stderr %q; want the server's explanation, not a dropped connection", code, errOut)
	}
}

// waitForWaiter gives a just-started `wideboi wait` time to connect and
// register. There is no observable state to poll for from outside the
// server, so this one is a fixed pause; too short only makes the test
// fail with "pane not found", never pass wrongly.
func waitForWaiter() { time.Sleep(time.Second) }
