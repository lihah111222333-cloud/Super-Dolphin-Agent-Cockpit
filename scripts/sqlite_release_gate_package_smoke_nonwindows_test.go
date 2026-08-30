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
	assertSQLitePackageSmokeEnvIsolation(t, fixture.stage)
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

func assertSQLitePackageSmokeEnvIsolation(t *testing.T, stage sqliteReleaseGateUnsignedPackage) {
	t.Helper()
	home := t.TempDir()
	inheritedPath := filepath.Join(t.TempDir(), "inherited-path")
	t.Setenv(sqlitePackageSmokeInheritedEnvKey, inheritedPath)
	env := sqliteReleaseGatePackageSmokeEnv(t, stage, home, t.TempDir())
	wantPath := sqlitePackageSmokeInheritedEnvKey + "=" + filepath.Join(home, "isolated")
	if !slices.Contains(env, wantPath) {
		t.Fatalf("remote worker package smoke env missing isolated inherited-path value %q", wantPath)
	}
	if slices.Contains(env, sqlitePackageSmokeInheritedEnvKey+"="+inheritedPath) {
		t.Fatalf("remote worker package smoke env retained inherited path %q", inheritedPath)
	}
}
