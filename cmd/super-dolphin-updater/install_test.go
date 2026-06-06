package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
