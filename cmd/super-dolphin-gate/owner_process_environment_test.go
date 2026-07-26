package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoordinatorOwnerCommandSanitizesInheritedGitRepositoryEnvironment(t *testing.T) {
	t.Setenv("GIT_DIR", "/untrusted/git-dir")
	t.Setenv("GIT_WORK_TREE", "/untrusted/work-tree")
	t.Setenv("GIT_COMMON_DIR", "/untrusted/common-dir")
	t.Setenv("GIT_INDEX_FILE", "/untrusted/alternate-index")
	t.Setenv("GIT_OBJECT_DIRECTORY", "/untrusted/objects")
	t.Setenv("PATH", "/untrusted/bin")

	command := newCoordinatorOwnerCommand("coordinator-owner")
	if !coordinatorOwnerCommandDetached(command) {
		t.Fatal("owner command remains attached to the caller terminal session")
	}
	for _, name := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	} {
		if environmentContains(command.Env, name) {
			t.Fatalf("owner command inherited %s: %q", name, command.Env)
		}
	}
	if environmentContains(command.Env, "PATH") && !strings.Contains(strings.Join(command.Env, "\n"), "PATH="+coordinatorOwnerToolchainPath) {
		t.Fatalf("owner command did not replace caller PATH: %q", command.Env)
	}
	if strings.Contains(strings.Join(command.Env, "\n"), "/untrusted/bin") {
		t.Fatalf("owner command inherited caller PATH: %q", command.Env)
	}
}

func TestConnectCoordinatorDeadlineCoversOwnerStarter(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	_, err := connectCoordinator(ctx, checkpoint, blockingOwnerStarter{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connectCoordinator() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("connectCoordinator() elapsed = %v, starter escaped deadline", elapsed)
	}
}

func TestCoordinatorOwnerStartupBudgetExceedsDialBudget(t *testing.T) {
	if coordinatorOwnerStartupTimeout <= coordinatorConnectTimeout {
		t.Fatalf(
			"owner startup timeout %v must exceed dial timeout %v",
			coordinatorOwnerStartupTimeout,
			coordinatorConnectTimeout,
		)
	}
}

func TestOwnerHandshakeDeadlineKillsAndReapsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCoordinatorProcessHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "SD_COORDINATOR_HELPER=hang-handshake")
	startedAt := time.Now()
	err := startCoordinatorOwnerCommand(ctx, command)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startCoordinatorOwnerCommand() error = %v, want deadline exceeded", err)
	}
	if command.ProcessState == nil {
		t.Fatal("timed-out owner child was not waited and reaped")
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("owner handshake cancellation elapsed = %v", elapsed)
	}
}

func TestOwnerSurvivesHandshakeContextCancellation(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "owner-survived")
	ctx, cancel := context.WithCancel(context.Background())
	command := newCoordinatorOwnerCommand(
		os.Args[0], "-test.run=^TestCoordinatorProcessHelper$", "-test.count=1",
	)
	command.Env = append(os.Environ(),
		"SD_COORDINATOR_HELPER=ready-after-handshake",
		"SD_COORDINATOR_SENTINEL="+sentinel,
	)
	if err := startCoordinatorOwnerCommand(ctx, command); err != nil {
		cancel()
		t.Fatalf("startCoordinatorOwnerCommand() error = %v", err)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sentinel); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("owner child stopped when handshake context was cancelled")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func environmentContains(environment []string, name string) bool {
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found && key == name {
			return true
		}
	}
	return false
}
