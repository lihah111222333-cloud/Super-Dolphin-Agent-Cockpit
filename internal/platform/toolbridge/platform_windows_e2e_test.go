//go:build windows && e2e

package toolbridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unsafe"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"golang.org/x/sys/windows"
)

const (
	// Windows Job Object LimitFlags：关闭 Job 时终止进程树，并按策略允许子进程脱离。
	testJobKillOnClose = 0x2000
	testJobBreakaway   = 0x0800
)

func TestStdioCreateKillOnCloseJobBreakawayPolicy(t *testing.T) {
	tests := []struct {
		name           string
		allowBreakaway bool
		wantFlags      uint32
	}{
		{
			name:           "managed_lsp",
			allowBreakaway: true,
			wantFlags:      testJobKillOnClose | testJobBreakaway,
		},
		{
			name:           "external_mcp",
			allowBreakaway: false,
			wantFlags:      testJobKillOnClose,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := stdioCreateKillOnCloseJobWithOps(tt.allowBreakaway, newStdioWindowsOps())
			if err != nil {
				t.Fatalf("stdioCreateKillOnCloseJob() error = %v", err)
			}
			t.Cleanup(func() {
				if err := windows.CloseHandle(job); err != nil {
					t.Errorf("CloseHandle() error = %v", err)
				}
			})

			var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
			if err := windows.QueryInformationJobObject(
				job,
				windows.JobObjectExtendedLimitInformation,
				uintptr(unsafe.Pointer(&info)),
				uint32(unsafe.Sizeof(info)),
				nil,
			); err != nil {
				t.Fatalf("QueryInformationJobObject() error = %v", err)
			}
			if got := info.BasicLimitInformation.LimitFlags; got != tt.wantFlags {
				t.Fatalf("LimitFlags = %#x, want %#x", got, tt.wantFlags)
			}
		})
	}
}

func TestStdioWindowsJobGrandchildHelper(t *testing.T) {
	if os.Getenv("STDIO_JOB_GRANDCHILD_HELPER") != "1" {
		return
	}
	time.Sleep(10 * time.Minute)
}

func TestStdioWindowsJobDescendantHelper(t *testing.T) {
	if os.Getenv("STDIO_JOB_DESCENDANT_HELPER") != "1" {
		return
	}
	grandchildPIDPath := os.Getenv("STDIO_JOB_GRANDCHILD_PID_PATH")
	if grandchildPIDPath == "" {
		os.Exit(2)
	}
	grandchild := exec.Command(os.Args[0], "-test.run=^TestStdioWindowsJobGrandchildHelper$")
	grandchild.Env = append(os.Environ(), "STDIO_JOB_GRANDCHILD_HELPER=1")
	if err := grandchild.Start(); err != nil {
		os.Exit(3)
	}
	tmpPIDPath := grandchildPIDPath + ".tmp"
	if err := os.WriteFile(tmpPIDPath, []byte(strconv.Itoa(grandchild.Process.Pid)), 0600); err != nil {
		_ = grandchild.Process.Kill()
		_, _ = grandchild.Process.Wait()
		os.Exit(4)
	}
	if err := os.Rename(tmpPIDPath, grandchildPIDPath); err != nil {
		_ = grandchild.Process.Kill()
		_, _ = grandchild.Process.Wait()
		os.Exit(5)
	}
	serveMinimalStdioMCP()
}

func TestStdioWindowsJobKillsDescendantsOnClientClose(t *testing.T) {
	grandchildPIDPath := filepath.Join(t.TempDir(), "grandchild.pid")
	client, err := newStdioMCPClientForValidatedBinary(context.Background(), providerdto.MCPBinary{
		Name:            "helper",
		TrustedServerID: "helper",
		Command:         []string{os.Args[0], "-test.run=^TestStdioWindowsJobDescendantHelper$"},
		Env: map[string]string{
			"STDIO_JOB_DESCENDANT_HELPER":   "1",
			"STDIO_JOB_GRANDCHILD_PID_PATH": grandchildPIDPath,
		},
	})
	if err != nil {
		t.Fatalf("newStdioMCPClientForValidatedBinary() error = %v", err)
	}

	var grandchildPID int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, readErr := os.ReadFile(grandchildPIDPath)
		if readErr == nil {
			grandchildPID, err = strconv.Atoi(string(raw))
			if err != nil {
				t.Fatalf("parse grandchild PID: %v", err)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if grandchildPID <= 1 {
		t.Fatalf("grandchild PID = %d, want a running descendant", grandchildPID)
	}
	if !stdioTestProcessAlive(grandchildPID) {
		t.Fatalf("grandchild PID %d exited before client close", grandchildPID)
	}

	if closeErr := client.Close(); closeErr != nil {
		t.Logf("stdio client close returned expected process termination error: %v", closeErr)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && stdioTestProcessAlive(grandchildPID) {
		time.Sleep(50 * time.Millisecond)
	}
	if stdioTestProcessAlive(grandchildPID) {
		t.Fatalf("grandchild PID %d is still alive after client close", grandchildPID)
	}
}
