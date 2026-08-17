//go:build windows && e2e

package main

import (
	"context"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsGoplsTestJobSilentBreakawayOK             = 0x00001000
	windowsExternalStdioHostJobLimitFlags            = windowsGoplsTestJobBreakawayOK | windowsGoplsTestJobSilentBreakawayOK
	windowsExternalStdioHostKillOnCloseJobLimitFlags = windowsGoplsTestJobKillOnClose
)

// super-dolphin-ci: compile-group-exclusive
func TestWindowsExternalStdioMCPHostWithoutKillOnCloseJobStartsSharedGoplsE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	argsLog := filepath.Join(t.TempDir(), "gopls-args.log")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	install := buildWindowsGoplsShortIdlePrecheckTestInstall(t)
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

// super-dolphin-ci: compile-group-exclusive
func TestWindowsExternalStdioMCPHostKillOnCloseJobWithoutBreakawayRejectsSharedGoplsE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	argsLog := filepath.Join(t.TempDir(), "gopls-args.log")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	install := buildWindowsGoplsShortIdlePrecheckTestInstall(t)
	env := windowsGoplsSidecarEnv(t, install,
		fakeGoplsArgsLogEnv+"="+argsLog,
		"AGENT_LSP_SHARED_CACHE_DIR="+cacheRoot,
		"PATH="+filepath.Join(build.Default.GOROOT, "bin"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := startWindowsGoplsExternalStdioHostInKillOnCloseJobWithoutBreakaway(t, ctx, install.Binary, roots[0], env)
	t.Cleanup(func() { client.close(t) })

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	result := client.callTool(t, "completion", map[string]any{"pos": targets[0] + ":3:1"})
	if !result.Result.IsError {
		t.Fatalf("external Windows stdio host shared gopls startup unexpectedly succeeded: structured=%s text=%q", result.Result.StructuredContent, result.Result.ContentText())
	}
	requireToolResultContains(t, result, "approved mcp-lsp broker breakaway", "external Windows stdio host breakaway rejection")
	requireWindowsGoplsNoFakeInvocations(t, argsLog, "before KILL_ON_CLOSE Job close")
	requireWindowsGoplsNoLifecycleArtifacts(t, cacheRoot, "before KILL_ON_CLOSE Job close")

	if client == nil || client.cmd == nil || client.closeHook == nil {
		t.Fatal("external Windows stdio host KILL_ON_CLOSE lifecycle handles are nil")
	}
	sidecarPID := client.cmd.Process.Pid
	if err := client.closeHook(); err != nil {
		t.Fatalf("close external Windows stdio host KILL_ON_CLOSE Job: %v", err)
	}
	requireWindowsProcessExit(t, sidecarPID, "mcp-lsp sidecar after KILL_ON_CLOSE Job close")
	client.close(t)
	requireWindowsGoplsNoFakeInvocations(t, argsLog, "after KILL_ON_CLOSE Job close")
	requireWindowsGoplsNoLifecycleArtifacts(t, cacheRoot, "after KILL_ON_CLOSE Job close")
}

func startWindowsGoplsExternalStdioHostInNonKillingJob(t *testing.T, ctx context.Context, binary, root string, env []string) *mcpLSPBinaryClient {
	return startWindowsGoplsExternalStdioHostInJob(t, ctx, binary, root, env, windowsExternalStdioHostJobLimitFlags, "non-killing")
}

func startWindowsGoplsExternalStdioHostInKillOnCloseJobWithoutBreakaway(t *testing.T, ctx context.Context, binary, root string, env []string) *mcpLSPBinaryClient {
	return startWindowsGoplsExternalStdioHostInJob(t, ctx, binary, root, env, windowsExternalStdioHostKillOnCloseJobLimitFlags, "KILL_ON_CLOSE without breakaway")
}

func startWindowsGoplsExternalStdioHostInJob(t *testing.T, ctx context.Context, binary, root string, env []string, jobFlags uint32, jobLabel string) *mcpLSPBinaryClient {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatalf("create %s Windows external stdio host Job: %v", jobLabel, err)
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = windows.CloseHandle(job)
		}
	}()
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: jobFlags,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatalf("configure %s Windows external stdio host Job: %v", jobLabel, err)
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
			return func() error {
				if job == 0 {
					return nil
				}
				err := windows.CloseHandle(job)
				job = 0
				return err
			}, nil
		},
	)
}

func requireWindowsGoplsNoFakeInvocations(t *testing.T, path, phase string) {
	t.Helper()
	invocations, payload, err := readFakeGoplsInvocations(path, 1)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read fake gopls args log %s: %v", phase, err)
	}
	if len(invocations) != 0 || strings.TrimSpace(string(payload)) != "" {
		t.Fatalf("fake gopls invocations %s = %v (%q), want none", phase, invocations, payload)
	}
}

func requireWindowsGoplsNoLifecycleArtifacts(t *testing.T, cacheRoot, phase string) {
	t.Helper()
	var artifacts []string
	err := filepath.WalkDir(cacheRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if name == "daemon.json" || name == "resource.json" ||
			(strings.HasPrefix(name, "lease-") && strings.HasSuffix(name, ".json")) {
			artifacts = append(artifacts, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("find Windows gopls lifecycle artifacts %s: %v", phase, err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("Windows gopls lifecycle artifacts %s = %v, want none", phase, artifacts)
	}
}
