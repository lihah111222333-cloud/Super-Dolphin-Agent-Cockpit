//go:build windows && e2e

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	windowsGoplsProvisionalHostEnv    = "MCP_LSP_TEST_PROVISIONAL_HOST"
	windowsGoplsProvisionalReportEnv  = "MCP_LSP_TEST_PROVISIONAL_REPORT"
	windowsGoplsProvisionalGoplsEnv   = "MCP_LSP_TEST_PROVISIONAL_GOPLS"
	windowsGoplsProvisionalWorkDirEnv = "MCP_LSP_TEST_PROVISIONAL_WORK_DIR"
	windowsGoplsProvisionalTriggerEnv = "MCP_LSP_TEST_PROVISIONAL_TRIGGER"
)

type windowsGoplsProvisionalIdentity struct {
	PID           int    `json:"pid"`
	StartIdentity string `json:"start_identity"`
}

type windowsGoplsProvisionalReport struct {
	Broker windowsGoplsProvisionalIdentity `json:"broker"`
	Daemon windowsGoplsProvisionalIdentity `json:"daemon"`
}

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsGoplsProvisionalParentExitE2E(t *testing.T) {
	install := buildWindowsGoplsTestInstall(t)
	installWindowsGoplsTestHost(t, install.Binary)
	fixtureDir := t.TempDir()
	cacheRoot := filepath.Join(fixtureDir, "cache")
	reportPath := filepath.Join(fixtureDir, "provisional.json")
	triggerPath := filepath.Join(fixtureDir, "start.trigger")
	argsLog := filepath.Join(fixtureDir, "gopls-args.log")
	stderrPath := filepath.Join(fixtureDir, "host-stderr.log")
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open provisional gopls parent stderr: %v", err)
	}
	t.Cleanup(func() {
		if err := stderrFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("close provisional gopls parent stderr: %v", err)
		}
	})
	host := exec.Command(install.Binary, "-test.run=^TestMcpLSPBinaryWindowsGoplsProvisionalParentExitHelper$")
	host.Env = append(os.Environ(),
		windowsGoplsProvisionalHostEnv+"=1",
		windowsGoplsProvisionalReportEnv+"="+reportPath,
		windowsGoplsProvisionalGoplsEnv+"="+install.Gopls,
		windowsGoplsProvisionalWorkDirEnv+"="+fixtureDir,
		windowsGoplsProvisionalTriggerEnv+"="+triggerPath,
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR="+install.Bundle,
		"SUPER_DOLPHIN_LSP_MANIFEST="+install.Manifest,
		fakeGoplsArgsLogEnv+"="+argsLog,
		"AGENT_LSP_SHARED_CACHE_DIR="+cacheRoot,
	)
	host.Stderr = stderrFile
	startWindowsGoplsTestHostInJob(t, host)
	hostIdentity := requireWindowsGoplsStartIdentity(t, host.Process.Pid)
	t.Cleanup(func() { cleanupWindowsGoplsExactIdentity(t, hostIdentity) })
	if err := os.WriteFile(triggerPath, []byte("start"), 0o600); err != nil {
		t.Fatalf("release provisional gopls parent: %v", err)
	}
	report := waitForWindowsGoplsProvisionalReport(t, reportPath, stderrPath)
	t.Cleanup(func() {
		cleanupWindowsGoplsExactIdentity(t, report.Broker)
		cleanupWindowsGoplsExactIdentity(t, report.Daemon)
	})
	waitErr := host.Wait()
	closeErr := stderrFile.Close()
	if waitErr != nil || closeErr != nil {
		stderr, _ := os.ReadFile(stderrPath)
		t.Fatalf("provisional gopls parent hard exit: %v; stderr=%s", errors.Join(waitErr, closeErr), stderr)
	}
	paths, err := windowsGoplsBrokerRecordPaths(cacheRoot)
	if err != nil || len(paths) != 0 {
		t.Fatalf("daemon.json before commit = %v, err=%v; want none", paths, err)
	}
	requireWindowsGoplsExactIdentitiesGone(t, 4*time.Second, report.Broker, report.Daemon)
}

// super-dolphin-ci: helper
func TestMcpLSPBinaryWindowsGoplsProvisionalParentExitHelper(t *testing.T) {
	if os.Getenv(windowsGoplsProvisionalHostEnv) != "1" {
		return
	}
	if err := runWindowsGoplsProvisionalParent(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func runWindowsGoplsProvisionalParent() error {
	reportPath := os.Getenv(windowsGoplsProvisionalReportEnv)
	goplsPath := os.Getenv(windowsGoplsProvisionalGoplsEnv)
	workDir := os.Getenv(windowsGoplsProvisionalWorkDirEnv)
	if reportPath == "" || goplsPath == "" || !filepath.IsAbs(workDir) {
		return errors.New("provisional gopls parent fixture is incomplete")
	}
	if err := waitForWindowsGoplsProvisionalTrigger(os.Getenv(windowsGoplsProvisionalTriggerEnv)); err != nil {
		return err
	}
	idle := 15 * time.Minute
	endpoint, err := runtimeServerReserveWindowsGoplsDaemonEndpoint()
	if err != nil {
		return err
	}
	process, err := runtimeServerLaunchWindowsGoplsDaemon(runtimeServerWindowsGoplsDaemonStartSpec{
		Directory: workDir, Binary: goplsPath, Endpoint: endpoint, ConfigDigest: "provisional-parent-exit-e2e",
		IdleTimeoutNanos: idle.Nanoseconds(), Env: os.Environ(),
		Args: []string{"serve", "-listen=" + endpoint, "-listen.timeout=" + idle.String()},
	})
	if err != nil {
		return err
	}
	report := windowsGoplsProvisionalReport{
		Broker: windowsGoplsProvisionalIdentity{PID: process.OwnerPID, StartIdentity: process.OwnerStartIdentity},
		Daemon: windowsGoplsProvisionalIdentity{PID: process.DaemonPID, StartIdentity: process.DaemonStartIdentity},
	}
	payload, err := json.Marshal(report)
	if err == nil {
		err = os.WriteFile(reportPath, payload, 0o600)
	}
	if err != nil {
		return errors.Join(err, process.KillAndWait())
	}
	return nil
}

func waitForWindowsGoplsProvisionalTrigger(path string) error {
	deadline := time.Now().Add(5 * time.Second)
	for path != "" {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("provisional gopls parent trigger timed out")
}

func waitForWindowsGoplsProvisionalReport(t *testing.T, path, stderrPath string) windowsGoplsProvisionalReport {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		payload, err := os.ReadFile(path)
		if err == nil {
			var report windowsGoplsProvisionalReport
			if err := json.Unmarshal(payload, &report); err != nil {
				t.Fatalf("decode provisional gopls report: %v", err)
			}
			if report.Broker.PID <= 1 || report.Broker.StartIdentity == "" || report.Daemon.PID <= 1 || report.Daemon.StartIdentity == "" {
				t.Fatalf("provisional gopls identities are incomplete: %+v", report)
			}
			return report
		}
		if !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
			stderr, _ := os.ReadFile(stderrPath)
			t.Fatalf("wait provisional gopls report: %v; stderr=%s", err, stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
