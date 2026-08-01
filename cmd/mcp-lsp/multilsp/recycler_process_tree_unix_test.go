//go:build darwin || linux

package multilsp

import (
	"os"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

const recyclerTreeChildBytes = 64 << 20

func TestProcessRSSBytesIncludesLanguageServerDescendants(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	dir := t.TempDir()
	childPIDPath := dir + "/child.pid"
	readyPath := dir + "/ready"
	cmd, processTree, stdin, stdout, _, err := startTransport(transportOptions{
		Binary: "/bin/sh",
		Args: []string{
			"-c",
			`"$MCP_LSP_TEST_BINARY" -test.run '^TestLanguageServerRSSChildHelper$' & child=$!; printf '%s' "$child" > "$MCP_LSP_TEST_CHILD_PID"; wait`,
		},
		Env: []string{
			"MCP_LSP_TEST_BINARY=" + binary,
			"MCP_LSP_TEST_CHILD_PID=" + childPIDPath,
			"MCP_LSP_RSS_CHILD=1",
			"MCP_LSP_RSS_READY=" + readyPath,
		},
	})
	if err != nil {
		t.Fatalf("startTransport() error = %v", err)
	}
	tr := &transport{cmd: cmd, processTree: processTree}
	t.Cleanup(func() {
		_ = tr.killProcess()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Wait()
	})

	childPID := waitForTestChildPID(t, childPIDPath)
	waitForRSSChildReady(t, readyPath)
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("RSS helper process is not alive: %v", err)
	}

	directRSS, err := directProcessRSSBytesForTest(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("direct process RSS: %v", err)
	}
	treeRSS, err := tr.processTreeRSSBytes()
	if err != nil {
		t.Fatalf("processTreeRSSBytes() error = %v", err)
	}
	if treeRSS < directRSS+(recyclerTreeChildBytes/2) {
		t.Fatalf("processTreeRSSBytes() = %d, direct parent RSS = %d; descendant allocation was not counted", treeRSS, directRSS)
	}
}

func TestLanguageServerRSSChildHelper(t *testing.T) {
	if os.Getenv("MCP_LSP_RSS_CHILD") != "1" {
		t.Skip("helper process")
	}
	payload := make([]byte, recyclerTreeChildBytes)
	for offset := 0; offset < len(payload); offset += os.Getpagesize() {
		payload[offset] = byte(offset)
	}
	if err := os.WriteFile(os.Getenv("MCP_LSP_RSS_READY"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("write RSS ready marker: %v", err)
	}
	time.Sleep(30 * time.Second)
	runtime.KeepAlive(payload)
}

func waitForRSSChildReady(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for RSS helper allocation")
}

func directProcessRSSBytesForTest(pid int) (uint64, error) {
	return hiddenexec.ProcessRSSBytes(pid)
}
