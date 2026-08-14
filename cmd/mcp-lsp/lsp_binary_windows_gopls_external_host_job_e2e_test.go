//go:build windows && e2e

package main

import (
	"context"
	"go/build"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsGoplsTestJobSilentBreakawayOK  = 0x00001000
	windowsExternalStdioHostJobLimitFlags = windowsGoplsTestJobBreakawayOK | windowsGoplsTestJobSilentBreakawayOK
)

// super-dolphin-ci: compile-group-exclusive
func TestWindowsExternalStdioMCPHostWithoutKillOnCloseJobStartsSharedGoplsE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	argsLog := filepath.Join(t.TempDir(), "gopls-args.log")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	install := buildWindowsGoplsTestInstall(t)
	env := windowsGoplsSidecarEnv(t, install,
		fakeGoplsArgsLogEnv+"="+argsLog,
		"AGENT_LSP_SHARED_CACHE_DIR="+cacheRoot,
		"PATH="+filepath.Join(build.Default.GOROOT, "bin"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := startWindowsGoplsExternalStdioHostInNonKillingJob(t, ctx, install.Binary, roots[0], env)
	t.Cleanup(func() { client.close(t) })

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	result := client.callTool(t, "completion", map[string]any{"pos": targets[0] + ":3:1"})
	requireMCPToolSuccess(t, client, result, "external Windows stdio host shared gopls startup")

	invocations := waitForFakeGoplsInvocations(t, argsLog, 2)
	daemon, forwarders := splitWindowsGoplsInvocations(invocations)
	endpoint, daemonPID := requireWindowsGoplsDaemonInvocation(t, daemon, install.Gopls)
	if len(forwarders) != 1 {
		t.Fatalf("gopls forwarder invocations = %v, want one", forwarders)
	}
	requireWindowsGoplsInvocationExecutable(t, forwarders[0], install.Gopls, "forwarder")
	if got := windowsGoplsArg(forwarders[0], "-remote="); got != endpoint {
		t.Fatalf("gopls forwarder endpoint = %q, want %q; args=%v", got, endpoint, forwarders[0])
	}
	record := waitForWindowsGoplsBrokerRecord(t, cacheRoot)
	requireWindowsGoplsBrokerRecord(t, record, install, endpoint, daemonPID)

	client.close(t)
	requireWindowsProcessExit(t, daemonPID, "gopls daemon")
	requireWindowsProcessExit(t, record.OwnerPID, "gopls broker")
}

func startWindowsGoplsExternalStdioHostInNonKillingJob(t *testing.T, ctx context.Context, binary, root string, env []string) *mcpLSPBinaryClient {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatalf("create non-killing Windows external stdio host Job: %v", err)
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = windows.CloseHandle(job)
		}
	}()
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windowsExternalStdioHostJobLimitFlags,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatalf("configure non-killing Windows external stdio host Job: %v", err)
	}
	return startMcpLSPBinaryForTestWithEnvConfigured(
		t, ctx, binary, root, t.TempDir(), env,
		func(command *exec.Cmd) {
			command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windowsGoplsTestCreateNoWindow, HideWindow: true}
		},
		func(command *exec.Cmd) (func() error, error) {
			if err := assignWindowsGoplsTestSidecarJob(job, command); err != nil {
				return nil, err
			}
			transferred = true
			return func() error { return windows.CloseHandle(job) }, nil
		},
	)
}
