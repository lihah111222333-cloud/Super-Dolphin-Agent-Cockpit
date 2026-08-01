package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionSelfUpdateProgressRejectsRollbackAndSibling(t *testing.T) {
	repository, commits := newProductionSelfUpdateGitFixture(t)
	session := productionSelfUpdateSession{
		repository: repository,
		git:        mustResolveProductionGitExecutable(t),
		root:       productionBootstrapRoot{BaselineCommit: commits["base"]},
	}
	run := runProductionSelfUpdateProgram
	if err := requireProductionSelfUpdateAncestor(
		context.Background(), session, run, commits["base"], commits["main"],
	); err != nil {
		t.Fatalf("fast-forward candidate rejected: %v", err)
	}
	previous := &productionSelfUpdateState{Commit: commits["main"]}
	for name, candidate := range map[string]string{
		"rollback": commits["base"],
		"sibling":  commits["sibling"],
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateProductionSelfUpdateProgress(
				context.Background(), session, candidate, previous, run,
			); err == nil {
				t.Fatalf("%s candidate was accepted", name)
			}
		})
	}
}

func TestFetchProductionSelfUpdateCommitDoesNotUseFETCHHEAD(t *testing.T) {
	source, commits := newProductionSelfUpdateGitFixture(t)
	authority := filepath.Join(t.TempDir(), "authority.git")
	runProductionSelfUpdateGitTest(t, "", "clone", "--bare", "--", source, authority)
	malicious := []byte(commits["base"] + "\t\tbranch 'malicious' of invalid\n")
	if err := os.WriteFile(filepath.Join(authority, "FETCH_HEAD"), malicious, 0o600); err != nil {
		t.Fatal(err)
	}
	session := productionSelfUpdateSession{
		repository: authority,
		git:        mustResolveProductionGitExecutable(t),
		config:     productionCoordinatorConfig{TrustedRef: "refs/heads/main"},
		root:       productionBootstrapRoot{BaselineCommit: commits["base"]},
	}
	commit, release, err := fetchProductionSelfUpdateCommit(
		context.Background(), session, runProductionSelfUpdateProgram,
	)
	if err != nil {
		t.Fatal(err)
	}
	if commit != commits["main"] {
		t.Fatalf("fetched commit = %s, want %s", commit, commits["main"])
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(authority, "FETCH_HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, malicious) {
		t.Fatal("process-unique fetch rewrote shared FETCH_HEAD")
	}
	refs := productionSelfUpdateGitOutput(t, authority, "for-each-ref", "--format=%(refname)", "refs/super-dolphin/self-update/")
	if refs != "" {
		t.Fatalf("temporary self-update refs leaked: %q", refs)
	}
}

func TestProductionProvisionCloneIgnoresCallerGitEnvironment(t *testing.T) {
	source, commits := newProductionSelfUpdateGitFixture(t)
	gitExecutable := mustResolveProductionGitExecutable(t)
	tree := productionSelfUpdateGitOutput(t, source, "rev-parse", commits["main"]+"^{tree}")
	attackerRoot := t.TempDir()
	marker := filepath.Join(attackerRoot, "fake-git-ran")
	fakeGit := filepath.Join(attackerRoot, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\n: >\""+marker+"\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(home, ".gitconfig"),
		[]byte("[url \"file:///invalid/\"]\n\tinsteadOf = "+source+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", attackerRoot)
	t.Setenv("HOME", home)
	destination := filepath.Join(t.TempDir(), "trusted.git")
	root := productionBootstrapRoot{
		RemoteURL: source, TrustedRef: "refs/heads/main",
		BaselineCommit: commits["main"], BaselineTree: tree,
	}
	if err := cloneProductionProvisionTrustedRepository(
		context.Background(), gitExecutable, root, destination,
	); err != nil {
		t.Fatalf("trusted clone inherited caller Git environment: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("caller PATH Git executed, marker error = %v", err)
	}
}

func TestLoadProductionSelfUpdateInputsSkipsGoForUnchangedTrustedHead(t *testing.T) {
	source, commits := newProductionSelfUpdateGitFixture(t)
	authority := filepath.Join(t.TempDir(), "authority.git")
	runProductionSelfUpdateGitTest(t, "", "clone", "--bare", "--", source, authority)
	tree := productionSelfUpdateGitOutput(t, authority, "rev-parse", commits["main"]+"^{tree}")
	installRoot := t.TempDir()
	current := filepath.Join(installRoot, productionCurrentGateCLI)
	if err := os.WriteFile(current, []byte("installed gate"), 0o700); err != nil {
		t.Fatal(err)
	}
	binaryDigest, err := productionBinaryDigest(current)
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := "sha256:" + strings.Repeat("a", 64)
	toolchainDigest := "sha256:" + strings.Repeat("b", 64)
	statePath := filepath.Join(installRoot, productionSelfUpdateStateFile)
	state := productionSelfUpdateState{
		SchemaVersion:        productionSelfUpdateStateV1,
		Remote:               "https://example.invalid/super-dolphin.git",
		TrustedRef:           "refs/heads/main",
		Commit:               commits["main"],
		Tree:                 tree,
		SourceDigest:         sourceDigest,
		LockDigest:           "sha256:" + strings.Repeat("c", 64),
		ToolchainDigest:      toolchainDigest,
		Platform:             runtimePlatform(),
		BinaryDigest:         binaryDigest,
		PreviousBinaryDigest: binaryDigest,
		Current:              productionCurrentGateCLI,
		Previous:             productionPreviousGateCLI,
	}
	if err := writeProductionSelfUpdateState(statePath, state); err != nil {
		t.Fatal(err)
	}
	session := productionSelfUpdateSession{
		repository: authority,
		current:    current,
		statePath:  statePath,
		git:        mustResolveProductionGitExecutable(t),
		config: productionCoordinatorConfig{
			TrustedRef: "refs/heads/main",
		},
		root: productionBootstrapRoot{
			RemoteURL:      state.Remote,
			TrustedRef:     state.TrustedRef,
			BaselineCommit: commits["base"],
		},
	}
	run := func(ctx context.Context, program string, args []string, directory string, environment []string) ([]byte, error) {
		if program == current {
			return []byte(
				"gate_source_sha256=" + sourceDigest + "\n" +
					"platform=" + runtimePlatform() + "\n" +
					"toolchain_digest=" + toolchainDigest + "\n",
			), nil
		}
		return runProductionSelfUpdateProgram(ctx, program, args, directory, environment)
	}
	_, matched, err := loadProductionSelfUpdateInputs(
		context.Background(), session, productionSelfUpdateDeps{run: run},
	)
	if err != nil || !matched {
		t.Fatalf("unchanged trusted head matched=%t error=%v", matched, err)
	}
}

func TestRunProductionSelfUpdateProgramHonorsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runProductionSelfUpdateProgram(
		ctx, "/bin/sh", []string{"-c", "sleep 5"}, "", []string{"PATH=/bin:/usr/bin"},
	)
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("cancelled child err=%v elapsed=%s", err, time.Since(started))
	}
}

func newProductionSelfUpdateGitFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	repository := t.TempDir()
	runProductionSelfUpdateGitTest(t, repository, "init", "--quiet", "-b", "main")
	runProductionSelfUpdateGitTest(t, repository, "config", "user.name", "Self Update Test")
	runProductionSelfUpdateGitTest(t, repository, "config", "user.email", "self-update@example.invalid")
	writeProductionSelfUpdateGitFile(t, repository, "base\n")
	runProductionSelfUpdateGitTest(t, repository, "add", "fixture.txt")
	runProductionSelfUpdateGitTest(t, repository, "commit", "--quiet", "-m", "基线")
	base := productionSelfUpdateGitOutput(t, repository, "rev-parse", "HEAD")
	writeProductionSelfUpdateGitFile(t, repository, "main\n")
	runProductionSelfUpdateGitTest(t, repository, "commit", "--quiet", "-am", "主线")
	main := productionSelfUpdateGitOutput(t, repository, "rev-parse", "HEAD")
	runProductionSelfUpdateGitTest(t, repository, "checkout", "--quiet", "-b", "sibling", base)
	writeProductionSelfUpdateGitFile(t, repository, "sibling\n")
	runProductionSelfUpdateGitTest(t, repository, "commit", "--quiet", "-am", "兄弟")
	sibling := productionSelfUpdateGitOutput(t, repository, "rev-parse", "HEAD")
	runProductionSelfUpdateGitTest(t, repository, "checkout", "--quiet", "main")
	return repository, map[string]string{"base": base, "main": main, "sibling": sibling}
}

func writeProductionSelfUpdateGitFile(t *testing.T, repository string, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, "fixture.txt"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runProductionSelfUpdateGitTest(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command(mustResolveProductionGitExecutable(t), args...)
	command.Dir = directory
	command.Env = controlledProductionGitEnvironment(mustResolveProductionGitExecutable(t))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func productionSelfUpdateGitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command(mustResolveProductionGitExecutable(t), args...)
	command.Dir = directory
	command.Env = controlledProductionGitEnvironment(mustResolveProductionGitExecutable(t))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output))
}
