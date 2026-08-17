//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestSQLiteReleaseGatePackageSmokeCommandUsesWorkerXvfb(t *testing.T) {
	fixture := newSQLitePackageSmokeCommandFixture(t)
	assertSQLitePackageSmokeCommand(t, fixture)
	assertSQLitePackageSmokeRuntimePath(t, fixture.gitPath, fixture.xvfbRun)
	assertSQLitePackageSmokeMemoryOverride(t, fixture.stage)
}

type sqlitePackageSmokeCommandFixture struct {
	stage   sqliteReleaseGateUnsignedPackage
	command *exec.Cmd
	gitPath string
	xvfbRun string
}

func newSQLitePackageSmokeCommandFixture(t *testing.T) sqlitePackageSmokeCommandFixture {
	t.Helper()
	xvfbRun := filepath.Join(t.TempDir(), "xvfb-run")
	if err := os.WriteFile(xvfbRun, []byte("#!/bin/sh\n:\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\n:\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPER_DOLPHIN_TEST_BACKEND", "remote-worker")
	t.Setenv("SUPER_DOLPHIN_GATE_GIT", gitPath)
	t.Setenv("SUPER_DOLPHIN_GATE_XVFB_RUN", xvfbRun)
	stage := sqliteReleaseGateUnsignedPackage{
		root:       t.TempDir(),
		entrypoint: "/stage/bin/agent-terminal",
		launchers:  []string{"/stage/run.sh"},
	}
	return sqlitePackageSmokeCommandFixture{
		stage:   stage,
		command: sqliteReleaseGatePackageSmokeCommand(t, stage),
		gitPath: gitPath,
		xvfbRun: xvfbRun,
	}
}

func assertSQLitePackageSmokeRuntimePath(t *testing.T, gitPath, xvfbRun string) {
	t.Helper()
	path := sqlitePackageSmokeRuntimePath(t)
	for _, directory := range []string{filepath.Dir(gitPath), filepath.Dir(xvfbRun)} {
		if !slices.Contains(filepath.SplitList(path), directory) {
			t.Fatalf("remote worker runtime PATH = %q, missing %q", path, directory)
		}
	}
}

func assertSQLitePackageSmokeMemoryOverride(t *testing.T, stage sqliteReleaseGateUnsignedPackage) {
	t.Helper()
	home := t.TempDir()
	inheritedOverride := filepath.Join(t.TempDir(), "inherited-memory")
	t.Setenv("MULTI_AGENT_MEMORY_PATH_OVERRIDE", inheritedOverride)
	env := sqliteReleaseGatePackageSmokeEnv(t, stage, home, t.TempDir())
	wantOverride := "MULTI_AGENT_MEMORY_PATH_OVERRIDE=" + filepath.Join(home, "memory")
	if !slices.Contains(env, wantOverride) {
		t.Fatalf("remote worker package smoke env missing isolated memory override %q", wantOverride)
	}
	if slices.Contains(env, "MULTI_AGENT_MEMORY_PATH_OVERRIDE="+inheritedOverride) {
		t.Fatalf("remote worker package smoke env retained inherited memory override %q", inheritedOverride)
	}
}
