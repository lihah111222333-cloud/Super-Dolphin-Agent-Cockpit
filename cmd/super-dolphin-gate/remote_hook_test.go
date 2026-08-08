package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestParseRemotePrePushOptions(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(cicontract.AgentTokenEnvironment, token)
	digest, err := requireRemoteCIAgentTokenDigest([]string{"remote", "hook", "pre-push"}, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	options, remoteName, remoteURL, err := parseRemotePrePushOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--ledger", "/tmp/remote-ci.baseline-state.sqlite",
		"--repository", "/tmp/repository",
		"--force",
		"origin",
		"ssh://git@example.invalid/repository.git",
	}, digest)
	if err != nil {
		t.Fatal(err)
	}
	assertParsedRemotePrePushOptions(t, options, digest, remoteName, remoteURL)
}

func assertParsedRemotePrePushOptions(t *testing.T, options remoteRunOptions, digest, remoteName, remoteURL string) {
	t.Helper()
	if options.ConfigPath != "/tmp/remote-ci.json" ||
		options.LedgerPath != "/tmp/remote-ci.baseline-state.sqlite" ||
		options.RepositoryRoot != "/tmp/repository" ||
		options.AgentTokenDigest != digest ||
		remoteName != "origin" ||
		remoteURL != "ssh://git@example.invalid/repository.git" {
		t.Fatalf("options=%#v remote=%q url=%q", options, remoteName, remoteURL)
	}
	if !options.Force {
		t.Fatal("parseRemotePrePushOptions(--force) did not set force mode")
	}
}

func TestParseRemotePrePushOptionsDefaultsToPassReuse(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	options, _, _, err := parseRemotePrePushOptions([]string{
		"--config", "/tmp/remote-ci.json", "origin", "ssh://git@example.invalid/repository.git",
	}, digest)
	if err != nil {
		t.Fatal(err)
	}
	if options.Force {
		t.Fatal("pre-push hook without --force disabled default PASS reuse")
	}
}

func TestParseRemotePrePushOptionsRejectsAgentTokenFlagAndMissingDigest(t *testing.T) {
	token, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := cicontract.AgentTokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	baseArgs := []string{"--config", "/tmp/remote-ci.json", "origin", "ssh://git@example.invalid/repository.git"}
	flagArgs := []string{"--config", "/tmp/remote-ci.json", cicontract.AgentTokenFlag, token, "origin", "ssh://git@example.invalid/repository.git"}
	if _, _, _, err := parseRemotePrePushOptions(flagArgs, digest); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("pre-push parser accepted unreachable agent-token flag: %v", err)
	}
	if _, _, _, err := parseRemotePrePushOptions(baseArgs, ""); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("pre-push parser accepted missing validated digest: %v", err)
	}
}

func TestRemotePushRunOptionsUsesCanonicalNormalizedRange(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	head := remoteRunGitOutput(t, repository, "rev-parse", "HEAD")
	base := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^")
	requests, err := gatehook.NormalizePrePush(
		context.Background(),
		repository,
		"delivery:v1:"+strings.Repeat("a", 64),
		strings.NewReader(
			"refs/heads/main "+head+" refs/heads/main "+base+"\n",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].Submit == nil {
		t.Fatalf("requests = %#v", requests)
	}
	options, err := remotePushRunOptions(
		remoteRunOptions{
			ConfigPath: "/tmp/remote-ci.json", LedgerPath: "/tmp/ledger.json",
			RemoteName: "origin", RemoteURL: "ssh://git@example.invalid/repository.git",
		},
		*requests[0].Submit,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	assertRemotePushRunOptions(t, options, resolvedRepository, head, base)
}

func TestValidateAuthoritativeRemoteHookResultRejectsManualOrDirtyCleanup(t *testing.T) {
	tree := strings.Repeat("a", 40)
	result := remoteci.RunResult{
		Entrypoint: gatecontract.CIEntrypointGitPreCommit,
		Profile:    gatecontract.ProfileLocalFast, SourceTreeSHA: tree,
		Status: gatecontract.ResultStatusPassed, Authoritative: true, CleanupComplete: true,
	}
	if err := validateAuthoritativeRemoteHookResult(
		result,
		gatecontract.CIEntrypointGitPreCommit,
		gatecontract.ProfileLocalFast,
		tree,
		"",
		"",
		"",
	); err != nil {
		t.Fatal(err)
	}
	result.AgentTokenDigest = "sha256:" + strings.Repeat("c", 64)
	if err := validateAuthoritativeRemoteHookResult(
		result,
		gatecontract.CIEntrypointGitPreCommit,
		gatecontract.ProfileLocalFast,
		tree,
		"",
		"",
		"",
	); err == nil {
		t.Fatal("result for a different requester unexpectedly accepted")
	}
	result.AgentTokenDigest = ""
	result.Authoritative = false
	if err := validateAuthoritativeRemoteHookResult(
		result,
		gatecontract.CIEntrypointGitPreCommit,
		gatecontract.ProfileLocalFast,
		tree,
		"",
		"",
		"",
	); err == nil {
		t.Fatal("manual result unexpectedly accepted")
	}
	result.Authoritative = true
	result.CleanupComplete = false
	if err := validateAuthoritativeRemoteHookResult(
		result,
		gatecontract.CIEntrypointGitPreCommit,
		gatecontract.ProfileLocalFast,
		tree,
		"",
		"",
		"",
	); err == nil {
		t.Fatal("incomplete cleanup unexpectedly accepted")
	}
}

func TestCanonicalRemoteIdentityRejectsUnsafeURL(t *testing.T) {
	for _, remoteURL := range []string{
		"https://token@example.invalid/repository.git",
		"https://example.invalid/repository.git#fragment",
		"https://example.invalid/repository.git?access_token=secret",
	} {
		if _, _, err := canonicalRemoteIdentity("origin", remoteURL); err == nil {
			t.Fatalf("canonicalRemoteIdentity(%q) unexpectedly passed", remoteURL)
		}
	}
	name, remoteURL, err := canonicalRemoteIdentity("origin", "SSH://git@EXAMPLE.invalid/repository.git")
	if err != nil || name != "origin" || remoteURL != "ssh://git@example.invalid/repository.git" {
		t.Fatalf("canonical identity = %q, %q, %v", name, remoteURL, err)
	}
}

func TestValidateAuthoritativeRemoteHookResultRejectsDifferentRemote(t *testing.T) {
	tree := strings.Repeat("a", 40)
	result := remoteci.RunResult{
		Entrypoint: gatecontract.CIEntrypointGitPrePush,
		Profile:    gatecontract.ProfilePush, SourceTreeSHA: tree,
		RemoteName: "origin", RemoteURL: "ssh://git@example.invalid/repository.git",
		Status: gatecontract.ResultStatusPassed, Authoritative: true, CleanupComplete: true,
	}
	if err := validateAuthoritativeRemoteHookResult(result, gatecontract.CIEntrypointGitPrePush, gatecontract.ProfilePush, tree, "mirror", "ssh://git@example.invalid/repository.git", ""); err == nil {
		t.Fatal("authority result for a different remote name unexpectedly accepted")
	}
	if err := validateAuthoritativeRemoteHookResult(result, gatecontract.CIEntrypointGitPrePush, gatecontract.ProfilePush, tree, "origin", "ssh://git@other.invalid/repository.git", ""); err == nil {
		t.Fatal("authority result for a different remote URL unexpectedly accepted")
	}
}
