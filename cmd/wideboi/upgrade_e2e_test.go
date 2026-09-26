package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lmorchard/wideboi/internal/server/ptyx"
)

func TestUpgradeServerE2E(t *testing.T) {
	bin := buildWideboiBinary(t)
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("wbu-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sockPath)
	defer os.Remove(sockPath + ".lock")
	dir := t.TempDir()
	serverLogPath := filepath.Join(dir, "server.log")

	srvLog, err := os.Create(serverLogPath)
	if err != nil {
		t.Fatalf("create server log: %v", err)
	}
	defer srvLog.Close()

	srvCmd := exec.Command(bin, "-s", sockPath, "server")
	srvCmd.Stdout = srvLog
	srvCmd.Stderr = srvLog

	if err := srvCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	originalPID := srvCmd.Process.Pid

	defer func() {
		// Clean up the server process after test
		_ = exec.Command(bin, "-s", sockPath, "kill-session").Run()
		time.Sleep(100 * time.Millisecond)
		_ = srvCmd.Process.Kill()
	}()

	// Wait for server to bind the socket
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("unix", sockPath)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			logData, _ := os.ReadFile(serverLogPath)
			t.Fatalf("server failed to start within 5s; log:\n%s", string(logData))
		}
		time.Sleep(20 * time.Millisecond)
	}

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

	// 1. Split a kept pane that runs bash and echoes a unique token
	splitOut, splitErr, splitCode := runCLI("split", "--keep", "bash")
	if splitCode != 0 {
		t.Fatalf("split failed: %s (stderr: %s)", splitOut, splitErr)
	}
	paneID := strings.TrimSpace(splitOut)
	if paneID == "" {
		t.Fatalf("empty pane ID from split")
	}

	// Send an echo command to the pane
	sendOut, sendErr, sendCode := runCLI("send", "-e", paneID, "echo wideboi-upgrade-test-token")
	if sendCode != 0 {
		t.Fatalf("send failed: %s (stderr: %s)", sendOut, sendErr)
	}

	// Wait for output to appear in capture
	time.Sleep(200 * time.Millisecond)
	capOut, capErr, capCode := runCLI("capture", paneID)
	if capCode != 0 || !strings.Contains(capOut, "wideboi-upgrade-test-token") {
		t.Fatalf("capture before upgrade did not contain token: code=%d err=%s out=%s", capCode, capErr, capOut)
	}

	// 2. Perform the in-place upgrade
	upOut, upErr, upCode := runCLI("upgrade-server", bin)
	if upCode != 0 {
		logData, _ := os.ReadFile(serverLogPath)
		t.Fatalf("upgrade-server failed: code=%d out=%s err=%s\nserver log:\n%s", upCode, upOut, upErr, string(logData))
	}

	// 3. Wait for the new server instance to resume listening
	time.Sleep(300 * time.Millisecond)
	deadline = time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("unix", sockPath)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			logData, _ := os.ReadFile(serverLogPath)
			t.Fatalf("server failed to rebind socket after upgrade; log:\n%s", string(logData))
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 4. Verify PID did not change (in-place exec preserves PID)
	if err := srvCmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("server process died: %v", err)
	}
	if srvCmd.Process.Pid != originalPID {
		t.Fatalf("server PID changed: original=%d current=%d", originalPID, srvCmd.Process.Pid)
	}

	// 5. Verify the pane survived the upgrade and still contains our terminal history
	capAfterOut, capAfterErr, capAfterCode := runCLI("capture", paneID)
	if capAfterCode != 0 || !strings.Contains(capAfterOut, "wideboi-upgrade-test-token") {
		logData, _ := os.ReadFile(serverLogPath)
		t.Fatalf("capture after upgrade missing token: code=%d err=%s out=%s\nserver log:\n%s", capAfterCode, capAfterErr, capAfterOut, string(logData))
	}

	// 6. Verify we can still send input to the pane after the upgrade
	sendAfterOut, sendAfterErr, sendAfterCode := runCLI("send", "-e", paneID, "echo post-upgrade-success")
	if sendAfterCode != 0 {
		t.Fatalf("send after upgrade failed: %s (stderr: %s)", sendAfterOut, sendAfterErr)
	}

	time.Sleep(200 * time.Millisecond)
	capFinalOut, capFinalErr, capFinalCode := runCLI("capture", paneID)
	if capFinalCode != 0 || !strings.Contains(capFinalOut, "post-upgrade-success") {
		logData, _ := os.ReadFile(serverLogPath)
		t.Fatalf("capture final missing post-upgrade output: code=%d err=%s out=%s\nserver log:\n%s", capFinalCode, capFinalErr, capFinalOut, string(logData))
	}
}

func TestUpgradeServerWithAttachedClientE2E(t *testing.T) {
	bin := buildWideboiBinary(t)
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("wbu-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sockPath)
	defer os.Remove(sockPath + ".lock")

	srvCmd := exec.Command(bin, "-s", sockPath, "server")
	var srvErr bytes.Buffer
	srvCmd.Stdout = &srvErr
	srvCmd.Stderr = &srvErr
	if err := srvCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = exec.Command(bin, "-s", sockPath, "kill-session").Run()
		time.Sleep(100 * time.Millisecond)
		_ = srvCmd.Process.Kill()
	}()

	// Wait for server socket
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("unix", sockPath)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server failed to bind socket within 5s; out:\n%s", srvErr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Spawn an actual attached client in a PTY, running runClient!
	clientPane, err := ptyx.Spawn([]string{bin, "-s", sockPath, "attach"}, 80, 24, "")
	if err != nil {
		t.Fatalf("spawn attached client: %v", err)
	}
	defer clientPane.Close()

	// Wait for client to attach and receive initial screen
	time.Sleep(500 * time.Millisecond)

	// Trigger upgrade-server
	upCmd := exec.Command(bin, "-s", sockPath, "upgrade-server", bin)
	if out, err := upCmd.CombinedOutput(); err != nil {
		t.Fatalf("upgrade-server failed: %v\noutput: %s", err, string(out))
	}

	// Wait for server to resume after upgrade
	time.Sleep(500 * time.Millisecond)

	// Type text into the attached client's PTY
	_, err = clientPane.Master.Write([]byte("echo client-reconnected-ok\r"))
	if err != nil {
		t.Fatalf("write to attached client PTY: %v", err)
	}

	// Read from the attached client's PTY until we see client-reconnected-ok
	buf := make([]byte, 4096)
	found := false
	readDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(readDeadline) {
		_ = clientPane.Master.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := clientPane.Master.Read(buf)
		if n > 0 && strings.Contains(string(buf[:n]), "client-reconnected-ok") {
			found = true
			break
		}
		if err != nil && !os.IsTimeout(err) {
			break
		}
	}

	if !found {
		t.Fatalf("attached client did not automatically reconnect and receive typed input")
	}
}
