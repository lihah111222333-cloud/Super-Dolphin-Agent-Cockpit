package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateInstallRequestRejectsMissingDMG(t *testing.T) {
	target := createAppBundle(t, filepath.Join(t.TempDir(), "Super Dolphin.app"))

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

func TestVerifyAppSignatureAllowsUnsignedWithoutTeamIDOrSpctl(t *testing.T) {
	oldRunCommand := runCommand
	defer func() {
		runCommand = oldRunCommand
	}()
	var commands []string
	runCommand = func(name string, args ...string) (commandResult, error) {
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
	runCommand = func(name string, args ...string) (commandResult, error) {
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
	runCommand = func(name string, args ...string) (commandResult, error) {
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
	runCommand = func(name string, args ...string) (commandResult, error) {
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
	target := createAppBundle(t, filepath.Join(t.TempDir(), "Super Dolphin.app"))

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
