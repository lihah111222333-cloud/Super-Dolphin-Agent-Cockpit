package main

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/trustedlauncher"
)

// TestLauncherLinkedPayloadRoundTrip 验证 payload 全字段和独立参数摘要可无损还原。
func TestLauncherLinkedPayloadRoundTrip(t *testing.T) {
	want := validLauncherLinkedIdentity()
	linkedPayload, argumentDigest, err := trustedlauncher.BuildLinkedIdentityValues(want)
	if err != nil {
		t.Fatalf("build launcher linker values: %v", err)
	}
	withLinkedLauncherIdentity(t, linkedPayload, argumentDigest)
	got, err := linkedLauncherIdentity()
	if err != nil {
		t.Fatalf("decode linked launcher identity: %v", err)
	}
	want.BuildArgumentsSHA256 = argumentDigest
	if got != want {
		t.Fatalf("linked identity = %+v, want %+v", got, want)
	}
}

// TestLauncherBuildArgumentsDigestRejectsDrift 验证参数摘要既不自引用也不能被替换。
func TestLauncherBuildArgumentsDigestRejectsDrift(t *testing.T) {
	linkedPayload, digest, err := trustedlauncher.BuildLinkedIdentityValues(validLauncherLinkedIdentity())
	if err != nil {
		t.Fatalf("build launcher linker values: %v", err)
	}
	withLinkedLauncherIdentity(t, linkedPayload, digest)
	if _, err := linkedLauncherIdentity(); err != nil {
		t.Fatalf("decode canonical launcher identity: %v", err)
	}
	withLinkedLauncherIdentity(t, linkedPayload, "sha256:"+strings.Repeat("f", 64))
	if _, err := linkedLauncherIdentity(); err == nil {
		t.Fatal("drifted launcher build arguments digest was accepted")
	}
	withLinkedLauncherIdentity(t, linkedPayload+"A", digest)
	if _, err := linkedLauncherIdentity(); err == nil {
		t.Fatal("launcher payload drift did not invalidate build arguments digest")
	}
}

// TestParseLauncherCommandFailFast 验证 launcher 命令面拒绝缺失和多余参数。
func TestParseLauncherCommandFailFast(t *testing.T) {
	tests := map[string][]string{
		"missing action":            nil,
		"unknown action":            {"unknown"},
		"verify missing repository": {"verify", "--tree", "tree", "--receipt", "receipt"},
		"retired artifact command":  {"verify-artifact", "--receipt", "receipt"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseLauncherCommand(args); err == nil {
				t.Fatalf("invalid launcher command %v was accepted", args)
			}
		})
	}
}

func validLauncherLinkedIdentity() trustedlauncher.LinkedIdentity {
	return trustedlauncher.LinkedIdentity{
		Tree:                  strings.Repeat("a", 40),
		SourceSHA256:          "sha256:" + strings.Repeat("b", 64),
		ToolchainSHA256:       "sha256:" + strings.Repeat("c", 64),
		CompilerSHA256:        "sha256:" + strings.Repeat("d", 64),
		CompilerClosureSHA256: "sha256:" + strings.Repeat("e", 64),
	}
}

func withLinkedLauncherIdentity(t *testing.T, sourceDigest, toolchainDigest string) {
	t.Helper()
	previousSource, previousToolchain := gateSourceDigest, gateToolchainDigest
	t.Cleanup(func() {
		gateSourceDigest, gateToolchainDigest = previousSource, previousToolchain
	})
	gateSourceDigest, gateToolchainDigest = sourceDigest, toolchainDigest
}
