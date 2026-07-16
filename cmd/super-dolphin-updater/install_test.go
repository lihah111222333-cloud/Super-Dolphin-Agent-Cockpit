package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

func TestValidateInstallRequestRejectsMissingDMG(t *testing.T) {
	target := filepath.Join(t.TempDir(), "Super Dolphin.app")

	err := validateInstallRequest(installRequest{
		TargetAppPath: target,
	})

	if err == nil || !strings.Contains(err.Error(), "dmg") {
		t.Fatalf("validateInstallRequest() error = %v, want missing dmg error", err)
	}
}

func TestValidateInstallRequestRejectsMissingTargetParent(t *testing.T) {
	dmg := filepath.Join(t.TempDir(), "Super Dolphin.dmg")
	if err := os.WriteFile(dmg, []byte("not a real dmg"), 0o600); err != nil {
		t.Fatalf("write dmg fixture: %v", err)
	}

	err := validateInstallRequest(installRequest{
		DMGPath:       dmg,
		TargetAppPath: filepath.Join(t.TempDir(), "missing", "Super Dolphin.app"),
	})

	if err == nil || !strings.Contains(err.Error(), "target parent") {
		t.Fatalf("validateInstallRequest() error = %v, want target parent error", err)
	}
}

func TestValidateMountedAppAcceptsExpectedBundleShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filesystems do not preserve macOS launcher execute bits in this fixture")
	}
	app := createAppBundle(t, filepath.Join(t.TempDir(), "Super Dolphin.app"))

	if err := validateMountedApp(app); err != nil {
		t.Fatalf("validateMountedApp() error = %v", err)
	}
}

func TestRunCommandTimesOutAndKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group kill fixture uses POSIX shell semantics")
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := `sleep 30 &
child=$!
echo "$child" > "$1"
wait "$child"`

	started := time.Now()
	result, err := runCommand(context.Background(), 150*time.Millisecond, "sh", "-c", script, "sh", pidFile)
	if err == nil {
		t.Fatalf("runCommand() error = nil, stdout = %q, stderr = %q, want timeout", result.stdout, result.stderr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runCommand() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("runCommand() elapsed = %s, want timeout to return promptly", elapsed)
	}

	childPID := readPIDFile(t, pidFile)
	waitForProcessGone(t, childPID)
}

func TestVerifyAppSignatureAllowsUnsignedWithoutTeamIDOrSpctl(t *testing.T) {
	oldRunCommand := runCommand
	defer func() {
		runCommand = oldRunCommand
	}()
	var commands []string
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return commandResult{}, nil
	}

	if err := verifyAppSignature("/Applications/Super Dolphin.app", "", true); err != nil {
		t.Fatalf("verifyAppSignature() error = %v", err)
	}
	if len(commands) != 1 || !strings.HasPrefix(commands[0], "codesign --verify") {
		t.Fatalf("commands = %v, want only codesign --verify", commands)
	}
	for _, command := range commands {
		if strings.HasPrefix(command, "spctl ") || strings.Contains(command, " -dv ") {
			t.Fatalf("unsigned verification ran production signing command: %v", commands)
		}
	}
}

func TestVerifyAppSignatureRequiresDeveloperIDByDefault(t *testing.T) {
	err := verifyAppSignature("/Applications/Super Dolphin.app", "", false)
	if err == nil || !strings.Contains(err.Error(), "expected Team ID") {
		t.Fatalf("verifyAppSignature() error = %v, want Team ID requirement", err)
	}
}

func TestClearQuarantineIgnoresPermissionDeniedWhenNoQuarantineRemains(t *testing.T) {
	oldRunCommand := runCommand
	defer func() {
		runCommand = oldRunCommand
	}()
	var commands []string
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if len(args) >= 3 && args[0] == "-dr" {
			return commandResult{
				stderr: "xattr: [Errno 13] Permission denied: '/tmp/Super Dolphin.app/Contents/Resources/lsp/bin/rust-analyzer'\n",
			}, errors.New("exit status 1")
		}
		if len(args) >= 2 && args[0] == "-lr" {
			return commandResult{
				stdout: "/tmp/Super Dolphin.app/Contents/Resources/lsp/bin/rust-analyzer: com.apple.provenance: \n",
			}, nil
		}
		return commandResult{}, nil
	}

	if err := clearQuarantine("/tmp/Super Dolphin.app"); err != nil {
		t.Fatalf("clearQuarantine() error = %v, want nil", err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %v, want clear and inspect commands", commands)
	}
}

func TestClearQuarantineFailsWhenQuarantineRemains(t *testing.T) {
	oldRunCommand := runCommand
	defer func() {
		runCommand = oldRunCommand
	}()
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		if len(args) >= 3 && args[0] == "-dr" {
			return commandResult{
				stderr: "xattr: [Errno 13] Permission denied: '/tmp/Super Dolphin.app/Contents/Resources/lsp/bin/rust-analyzer'\n",
			}, errors.New("exit status 1")
		}
		if len(args) >= 2 && args[0] == "-lr" {
			return commandResult{
				stdout: "/tmp/Super Dolphin.app/Contents/Resources/lsp/bin/rust-analyzer: com.apple.quarantine: 0081;00000000;Super Dolphin;\n",
			}, nil
		}
		return commandResult{}, nil
	}

	err := clearQuarantine("/tmp/Super Dolphin.app")
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("clearQuarantine() error = %v, want original xattr failure", err)
	}
}

func TestParseInstallRequestAcceptsLogPathAndWaitPID(t *testing.T) {
	req, err := parseInstallRequest([]string{
		"-dmg", "/tmp/update.dmg",
		"-target", "/Applications/Super Dolphin.app",
		"-restart",
		"-allow-unsigned",
		"-wait-pid", "123",
		"-log", "/tmp/updater.log",
	})
	if err != nil {
		t.Fatalf("parseInstallRequest() error = %v", err)
	}
	if req.WaitPID != 123 {
		t.Fatalf("WaitPID = %d, want 123", req.WaitPID)
	}
	if req.LogPath != "/tmp/updater.log" {
		t.Fatalf("LogPath = %q, want /tmp/updater.log", req.LogPath)
	}
	if !req.Restart || !req.AllowUnsigned {
		t.Fatalf("restart/allow unsigned not parsed: %#v", req)
	}
}

func TestRestartTargetAppForcesNewInstance(t *testing.T) {
	oldRestartCommand := runRestartCommand
	defer func() {
		runRestartCommand = oldRestartCommand
	}()
	var gotArgs []string
	runRestartCommand = func(args ...string) (commandResult, error) {
		gotArgs = append([]string(nil), args...)
		return commandResult{}, nil
	}

	if err := restartTargetApp("/Applications/Super Dolphin.app"); err != nil {
		t.Fatalf("restartTargetApp() error = %v", err)
	}
	wantArgs := []string{"-n", "/Applications/Super Dolphin.app"}
	if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("command args = %q, want %q", gotArgs, wantArgs)
	}
}

func TestRestartEnvironmentDropsStaleDevOverrides(t *testing.T) {
	env := sanitizedRestartEnv([]string{
		"PATH=/bin",
		"HOME=/Users/ai",
		"DATABASE_URL=postgres://super_dolphin@127.0.0.1:55435/super_dolphin?sslmode=disable",
		"POSTGRES_CONNECTION_STRING=postgres://super_dolphin@127.0.0.1:55435/super_dolphin?sslmode=disable",
		"SUPER_DOLPHIN_UPDATE_VERSION=1.0.2",
		"SUPER_DOLPHIN_LOCAL_POSTGRES_PORT=55435",
		"SUPER_DOLPHIN_HOME=/tmp/sd-update-manual/home",
		"GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8094",
		"VITE_DEV_URL=http://127.0.0.1:5177",
		"FRONTEND_DEVSERVER_URL=http://127.0.0.1:5177",
		"VERSION=1.0.2",
	})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, blocked := range []string{
		"\nDATABASE_URL=",
		"\nPOSTGRES_CONNECTION_STRING=",
		"\nSUPER_DOLPHIN_UPDATE_VERSION=",
		"\nSUPER_DOLPHIN_LOCAL_POSTGRES_PORT=",
		"\nSUPER_DOLPHIN_HOME=",
		"\nGO_AGENT_CTL_RPC_ADDR=",
		"\nVITE_DEV_URL=",
		"\nFRONTEND_DEVSERVER_URL=",
		"\nVERSION=",
	} {
		if strings.Contains(joined, blocked) {
			t.Fatalf("sanitized env still contains %q: %v", blocked, env)
		}
	}
	for _, kept := range []string{"PATH=/bin", "HOME=/Users/ai"} {
		if !strings.Contains(joined, "\n"+kept+"\n") {
			t.Fatalf("sanitized env missing %q: %v", kept, env)
		}
	}
}

func TestInstallFromMountWaitsForAppExitBeforeReplacing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filesystems do not preserve macOS launcher execute bits in this fixture")
	}
	oldRunCommand := runCommand
	oldWaitForProcessExit := waitForProcessExit
	defer func() {
		runCommand = oldRunCommand
		waitForProcessExit = oldWaitForProcessExit
	}()
	var events []string
	waitForProcessExit = func(pid int, timeout time.Duration) error {
		events = append(events, "wait")
		if pid != 12345 {
			t.Fatalf("wait pid = %d, want 12345", pid)
		}
		return nil
	}
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		if name == "ditto" {
			events = append(events, "copy")
			if len(args) != 2 {
				t.Fatalf("ditto args = %v, want source and target", args)
			}
			if err := os.Rename(args[0], args[1]); err != nil {
				t.Fatalf("fake ditto rename: %v", err)
			}
		}
		return commandResult{}, nil
	}
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := filepath.Join(t.TempDir(), "Super Dolphin.app")

	err := installFromMount(installRequest{
		TargetAppPath: target,
		WaitPID:       12345,
		AllowUnsigned: true,
	}, mountPoint)
	if err != nil {
		t.Fatalf("installFromMount() error = %v", err)
	}
	if strings.Join(events, ",") != "wait,copy" {
		t.Fatalf("events = %v, want wait before copy", events)
	}
}

func TestFirstInstallUsesAtomicPathWithoutRollbackTransaction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filesystems do not preserve macOS launcher execute bits in this fixture")
	}
	stubSuccessfulDitto(t)
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	parent := t.TempDir()
	target := filepath.Join(parent, "Super Dolphin.app")
	if err := installFromMount(installRequest{TargetAppPath: target, AllowUnsigned: true}, mountPoint); err != nil {
		t.Fatalf("installFromMount() error = %v", err)
	}
	if err := validateMountedApp(target); err != nil {
		t.Fatalf("first install target is invalid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, updateTransactionDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first install created rollback transaction root: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(parent, ".Super Dolphin.backup-*.app"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("first install created backups: %v", backups)
	}
}

func TestReplacementWithoutRestartHasNoSideEffects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filesystems do not preserve macOS launcher execute bits in this fixture")
	}
	oldRunCommand := runCommand
	t.Cleanup(func() { runCommand = oldRunCommand })
	commandCalls := 0
	runCommand = func(context.Context, time.Duration, string, ...string) (commandResult, error) {
		commandCalls++
		return commandResult{}, errors.New("unexpected updater command")
	}
	mountPoint := t.TempDir()
	staged := createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	parent := t.TempDir()
	target := createAppBundle(t, filepath.Join(parent, "Super Dolphin.app"))
	originalMarker := filepath.Join(target, "Contents", "Resources", "old-release.txt")
	if err := os.WriteFile(originalMarker, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := defaultUpdaterApp().replaceTargetAppTransaction(staged, target, "", true, false)
	if err == nil || !strings.Contains(err.Error(), "requires restart supervision") {
		t.Fatalf("replaceTargetAppTransaction(no restart replacement) error = %v", err)
	}
	if commandCalls != 0 {
		t.Fatalf("no-restart replacement command calls = %d, want zero", commandCalls)
	}
	assertPathsExist(t, staged, originalMarker)
	assertNoPathMatches(t,
		filepath.Join(parent, updateTransactionDirName),
		filepath.Join(parent, ".Super Dolphin.backup-*.app"),
		filepath.Join(parent, ".Super Dolphin.staging-*.app"),
	)
}

func assertPathsExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected path %s: %v", path, err)
		}
	}
}

func assertNoPathMatches(t *testing.T, patterns ...string) {
	t.Helper()
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("no-restart replacement created %s: %v", pattern, matches)
		}
	}
}

func TestSupervisedReplacementRetainsTransactionBackup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filesystems do not preserve macOS launcher execute bits in this fixture")
	}
	stubSuccessfulDitto(t)
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	parent := t.TempDir()
	target := createAppBundle(t, filepath.Join(parent, "Super Dolphin.app"))
	oldMarker := filepath.Join(target, "Contents", "Resources", "old-release.txt")
	if err := os.WriteFile(oldMarker, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertSupervisedReplacementStartsProbation(t, filepath.Join(mountPoint, "Super Dolphin.app"), target)
	backups, err := filepath.Glob(filepath.Join(parent, ".Super Dolphin.backup-*.app"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("retained transaction backups = %v, want exactly one", backups)
	}
	if _, err := os.Stat(filepath.Join(backups[0], "Contents", "Resources", "old-release.txt")); err != nil {
		t.Fatalf("retained backup does not contain old release: %v", err)
	}
	journals, err := filepath.Glob(filepath.Join(parent, updateTransactionDirName, "*", "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(journals) != 1 {
		t.Fatalf("durable transaction journals = %v, want exactly one", journals)
	}
}

func assertSupervisedReplacementStartsProbation(t *testing.T, staged string, target string) {
	t.Helper()
	transaction, transactional, err := defaultUpdaterApp().replaceTargetAppTransaction(staged, target, "", true, true)
	if err != nil {
		t.Fatalf("replaceTargetAppTransaction() error = %v", err)
	}
	if !transactional || transaction.State != recovery.StateProbation {
		t.Fatalf("replacement result = transactional %v state %q", transactional, transaction.State)
	}
}

func TestInstallKeepsTargetWhenDittoTimesOutBeforeTransaction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filesystems do not preserve macOS launcher execute bits in this fixture")
	}

	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := createAppBundle(t, filepath.Join(t.TempDir(), "Super Dolphin.app"))
	originalMarker := filepath.Join(target, "Contents", "Resources", "original-marker.txt")
	if err := os.WriteFile(originalMarker, []byte("original"), 0o644); err != nil {
		t.Fatalf("write original marker: %v", err)
	}
	dittoDest := stubRunCommandWithTimedOutDitto(t)

	err := installFromMount(installRequest{
		TargetAppPath: target,
		AllowUnsigned: true,
		Restart:       true,
	}, mountPoint)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("installFromMount() error = %v, want ditto timeout", err)
	}
	if *dittoDest == "" {
		t.Fatal("ditto was not called")
	}
	if *dittoDest == target {
		t.Fatalf("ditto destination = %q, want staged copy path before final replacement", *dittoDest)
	}
	if _, err := os.Stat(originalMarker); err != nil {
		t.Fatalf("original app changed after pre-transaction timeout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "Contents", "Resources", "partial-copy.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial copy leaked into restored target: %v", err)
	}
	if _, err := os.Stat(*dittoDest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged copy path still exists after failed preparation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), updateTransactionDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-transaction timeout created transaction root: %v", err)
	}
}

func createAppBundle(t *testing.T, appPath string) string {
	t.Helper()

	macos := filepath.Join(appPath, "Contents", "MacOS")
	resources := filepath.Join(appPath, "Contents", "Resources")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatalf("create MacOS dir: %v", err)
	}
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatalf("create Resources dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(macos, "agent-terminal"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resources, "runtime-manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appPath, "Contents", "Info.plist"), []byte(infoPlist("com.superdolphin.app")), 0o644); err != nil {
		t.Fatalf("write Info.plist: %v", err)
	}
	return appPath
}

func infoPlist(bundleID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>` + bundleID + `</string>
</dict>
</plist>
`
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read child pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	return pid
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exists, err := processExists(pid)
		if err != nil {
			t.Fatalf("inspect child process %d: %v", pid, err)
		}
		if !exists {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("child process %d still exists; timeout must kill the process group", pid)
}

func stubRunCommandWithTimedOutDitto(t *testing.T) *string {
	t.Helper()

	oldRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = oldRunCommand
	})
	dittoDest := ""
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		if name != "ditto" {
			return commandResult{}, nil
		}
		recordPartialDittoCopy(t, args, &dittoDest)
		return commandResult{stderr: "ditto timed out"}, context.DeadlineExceeded
	}
	return &dittoDest
}

func stubSuccessfulDitto(t *testing.T) {
	t.Helper()
	oldRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = oldRunCommand
	})
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		if name != "ditto" {
			return commandResult{}, nil
		}
		if err := os.Rename(args[0], args[1]); err != nil {
			return commandResult{}, err
		}
		return commandResult{}, nil
	}
}

func recordPartialDittoCopy(t *testing.T, args []string, dittoDest *string) {
	t.Helper()

	if len(args) != 2 {
		t.Fatalf("ditto args = %v, want source and target", args)
	}
	*dittoDest = args[1]
	partialDir := filepath.Join(*dittoDest, "Contents", "Resources")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("write partial copy dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "partial-copy.txt"), []byte("partial"), 0o644); err != nil {
		t.Fatalf("write partial copy marker: %v", err)
	}
}

func TestCandidateHandleReapsCrashedProcess(t *testing.T) {
	handle, identity := startCandidateHandleTestProcess(t, "exit")
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := handle.Wait(waitCtx)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("candidate Wait() error = %v, want ExitError", err)
	}
	alive, probeErr := handle.ProcessAlive(identity)
	if alive || !errors.As(probeErr, &exitErr) {
		t.Fatalf("ProcessAlive() = %v, %v, want false with Wait error", alive, probeErr)
	}
	if _, err := pidregistry.CaptureStableProcessIdentity(identity.PID); !errors.Is(err, pidregistry.ErrStableProcessIdentityRead) {
		t.Fatalf("candidate PID remains observable after Wait: %v", err)
	}
}

func TestCandidateHandleTerminatesAndReapsExactProcess(t *testing.T) {
	handle, identity := startCandidateHandleTestProcess(t, "block")
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := handle.Stop(stopCtx, identity); err != nil {
		t.Fatalf("candidate Stop() error = %v", err)
	}
	if _, err := pidregistry.CaptureStableProcessIdentity(identity.PID); !errors.Is(err, pidregistry.ErrStableProcessIdentityRead) {
		t.Fatalf("candidate PID remains observable after Stop: %v", err)
	}
}

func TestProbationCandidateProcess(t *testing.T) {
	mode := os.Getenv("SUPER_DOLPHIN_TEST_PROBATION_CANDIDATE")
	if mode == "" {
		return
	}
	switch mode {
	case "exit":
		time.Sleep(200 * time.Millisecond)
		os.Exit(17)
	case "block":
		select {}
	default:
		os.Exit(19)
	}
}

func startCandidateHandleTestProcess(t *testing.T, mode string) (*candidateHandle, recovery.ProcessIdentity) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestProbationCandidateProcess")
	cmd.Env = append(os.Environ(), "SUPER_DOLPHIN_TEST_PROBATION_CANDIDATE="+mode)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start candidate fixture: %v", err)
	}
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("CaptureStableProcessIdentity() error = %v", err)
	}
	identity := recovery.ProcessIdentity{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: stable.ExecutableIdentity, ExecutableSHA256: "test-digest",
	}
	return newCandidateHandle(cmd, identity), identity
}
