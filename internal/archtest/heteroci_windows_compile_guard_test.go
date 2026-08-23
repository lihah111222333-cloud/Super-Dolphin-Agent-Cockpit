package archtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeterociWindowsBatchBoundaryCrossCompilesWithoutPhysicalExecutor(t *testing.T) {
	root := findRepoRoot(t)
	boundaryPath := filepath.Join(root, "internal", "devtools", "heterocinode", "batch_process_windows_unsupported.go")
	source := readRemoteCIContractGuardFile(t, boundaryPath)
	for _, marker := range []string{"//go:build windows", "errUnsupportedPhysicalWindows", "func startBatchProcess", "func isBatchProcessGone"} {
		if !strings.Contains(source, marker) {
			t.Errorf("Windows batch boundary is missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{"exec.Command", ".Start()", ".Wait()", "CreateProcess", "PowerShell", "powershell"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("Windows batch boundary retains workload-launch marker %q", forbidden)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "devtools", "heterocinode", "batch_process_windows.go")); err == nil {
		t.Fatal("legacy Windows process executor is still present")
	}

	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("resolve go executable: %v", err)
	}
	output := filepath.Join(t.TempDir(), "heteroci-node.test.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, goExecutable, "test", "-c", "-o", output, "./internal/devtools/heterocinode")
	command.Dir = root
	command.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0", "GOWORK=off", "GOTOOLCHAIN=local")
	outputBytes, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("Windows heterocinode cross-compile timed out: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("Windows heterocinode cross-compile failed: %v\n%s", err, outputBytes)
	}
	info, err := os.Stat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("Windows heterocinode cross-compile output is invalid: info=%v err=%v", info, err)
	}
}
