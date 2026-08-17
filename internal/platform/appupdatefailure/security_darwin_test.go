//go:build darwin

package appupdatefailure

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

func TestSidecarFlockSerializesProcesses(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_SIDECAR_LOCK_CHILD") == "1" {
		if err := runSidecarLockChild(); err != nil {
			t.Fatal(err)
		}
		return
	}
	testSidecarFlockParent(t)
}

func testSidecarFlockParent(t *testing.T) {
	t.Helper()
	stageDir := privateStageDir(t)
	ready := filepath.Join(t.TempDir(), "ready")
	release := filepath.Join(t.TempDir(), "release")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSidecarFlockSerializesProcesses$")
	cmd.Env = append(os.Environ(),
		"SUPER_DOLPHIN_SIDECAR_LOCK_CHILD=1",
		"SUPER_DOLPHIN_SIDECAR_LOCK_STAGE="+stageDir,
		"SUPER_DOLPHIN_SIDECAR_LOCK_READY="+ready,
		"SUPER_DOLPHIN_SIDECAR_LOCK_RELEASE="+release,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSidecarTestFile(t, ready)
	result := make(chan error, 1)
	var group errgroup.Group
	group.Go(func() error {
		result <- Begin(stageDir, testGenerationOld)
		return nil
	})
	select {
	case err := <-result:
		t.Fatalf("Begin() returned before subprocess released flock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("lock holder subprocess failed: %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Begin() remained blocked after subprocess released flock")
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}

func runSidecarLockChild() error {
	stageDir := os.Getenv("SUPER_DOLPHIN_SIDECAR_LOCK_STAGE")
	ready := os.Getenv("SUPER_DOLPHIN_SIDECAR_LOCK_READY")
	release := os.Getenv("SUPER_DOLPHIN_SIDECAR_LOCK_RELEASE")
	return withLockedStageDir(stageDir, func(*lockedStageDir) error {
		if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
			return err
		}
		for {
			if _, err := os.Stat(release); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

func waitForSidecarTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestSidecarUsesPrivateFiles(t *testing.T) {
	stageDir := privateStageDir(t)
	if err := Begin(stageDir, testGenerationOld); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{Filename, LockFilename} {
		info, err := os.Lstat(filepath.Join(stageDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want regular 0600", name, info.Mode())
		}
	}
}

func TestSidecarRejectsUnsafeStageDirPermissions(t *testing.T) {
	stageDir := privateStageDir(t)
	if err := os.Chmod(stageDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := Begin(stageDir, testGenerationOld); err == nil {
		t.Fatal("Begin() error = nil, want unsafe StageDir mode rejection")
	}
}

func TestSidecarRejectsIntermediateAncestorSymlink(t *testing.T) {
	realDir := privateStageDir(t)
	child := filepath.Join(realDir, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(privateStageDir(t), "linked-parent")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if err := Begin(filepath.Join(link, "child"), testGenerationOld); err == nil {
		t.Fatal("Begin(ancestor symlink) error = nil")
	}
}

func TestRecordSymlinkIsAtomicallyReplaced(t *testing.T) {
	stageDir := privateStageDir(t)
	target := filepath.Join(privateStageDir(t), "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(stageDir, Filename)); err != nil {
		t.Fatal(err)
	}
	if err := Begin(stageDir, testGenerationOld); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "target" {
		t.Fatalf("symlink target = %q, %v", raw, err)
	}
	info, err := os.Lstat(filepath.Join(stageDir, Filename))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("published record mode = %v, %v", info, err)
	}
}

func TestSidecarRejectsLockSymlink(t *testing.T) {
	stageDir := privateStageDir(t)
	target := filepath.Join(privateStageDir(t), "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(stageDir, LockFilename)); err != nil {
		t.Fatal(err)
	}
	if err := Begin(stageDir, testGenerationOld); err == nil {
		t.Fatal("Begin(lock symlink) error = nil")
	}
}

func TestSidecarRejectsSpecialPermissionBits(t *testing.T) {
	t.Run("stage directory", func(t *testing.T) {
		stageDir := privateStageDir(t)
		if err := unix.Chmod(stageDir, 0o1700); err != nil {
			t.Fatal(err)
		}
		if err := Begin(stageDir, testGenerationOld); err == nil {
			t.Fatal("Begin(sticky StageDir) error = nil")
		}
	})
	for _, name := range []string{Filename, LockFilename} {
		t.Run(name, func(t *testing.T) {
			stageDir := privateStageDir(t)
			if err := Begin(stageDir, testGenerationOld); err != nil {
				t.Fatal(err)
			}
			if err := unix.Chmod(filepath.Join(stageDir, name), 0o4600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := ReadFailure(stageDir); err == nil {
				t.Fatalf("ReadFailure(%s special bits) error = nil", name)
			}
		})
	}
}

func TestOpenedStageDirRemainsBoundAfterAncestorSwap(t *testing.T) {
	root := privateStageDir(t)
	stageDir := filepath.Join(root, "stage")
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	attacker := filepath.Join(root, "attacker")
	if err := os.Mkdir(attacker, 0o700); err != nil {
		t.Fatal(err)
	}
	err := withLockedStageDir(stageDir, func(dir *lockedStageDir) error {
		moved := filepath.Join(root, "moved")
		if err := os.Rename(stageDir, moved); err != nil {
			return err
		}
		if err := os.Rename(attacker, stageDir); err != nil {
			return err
		}
		return dir.writeRecord(pendingRecord(testGenerationOld))
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "moved", Filename)); err != nil {
		t.Fatalf("opened directory did not receive record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, Filename)); !os.IsNotExist(err) {
		t.Fatalf("replacement directory received record: %v", err)
	}
}
