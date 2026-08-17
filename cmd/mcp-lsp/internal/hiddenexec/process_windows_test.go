//go:build windows

package hiddenexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigureCommandHidesWindowsConsole(t *testing.T) {
	cmd := exec.Command("gopls.exe")
	configureCommand(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow is false")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("CREATE_NO_WINDOW flag is missing")
	}
	if cmd.SysProcAttr.CreationFlags&createNewProcessGroup == 0 {
		t.Fatal("CREATE_NEW_PROCESS_GROUP flag is missing")
	}
}

// TestCommandContextRunsWindowsCommandScriptHidden 证明生产安装器使用的 .cmd
// 入口在隐藏窗口和独立进程组标志下仍会真实执行，并把 stdout 返回调用方。
func TestCommandContextRunsWindowsCommandScriptHidden(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "hiddenexec-proof.cmd")
	if err := os.WriteFile(script, []byte("@echo off\r\necho hiddenexec-cmd-ok\r\n"), 0o600); err != nil {
		t.Fatalf("write Windows command-script fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := CommandContext(ctx, script).CombinedOutput()
	if err != nil {
		t.Fatalf("run hidden Windows command script: %v; output=%q", err, output)
	}
	if strings.TrimSpace(string(output)) != "hiddenexec-cmd-ok" {
		t.Fatalf("hidden Windows command-script output=%q, want hiddenexec-cmd-ok", output)
	}
}
