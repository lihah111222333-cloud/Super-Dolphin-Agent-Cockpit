package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

func TestProductionSelfUpdateWinnerAcquiresUpdateLock(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, productionCurrentGateCLI)
	lock, acquired, err := tryAcquireProductionSelfUpdateLock(current)
	if err != nil || !acquired {
		t.Fatalf("winner lock acquired=%t error=%v", acquired, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionSelfUpdateContenderReusesVerifiedCurrentWithoutWaiting(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	session := productionUpdateReuseSession(fixture)
	lockPath := filepath.Join(filepath.Dir(fixture.current), ".super-dolphin-gate-update.lock")
	stopHolder := startProductionSelfUpdateLockHolder(t, lockPath)
	defer stopHolder()

	result := make(chan struct {
		lock     *gateprivate.ExclusiveFileLock
		acquired bool
		err      error
	}, 1)
	go func() {
		lock, acquired, err := tryAcquireProductionSelfUpdateLock(fixture.current)
		result <- struct {
			lock     *gateprivate.ExclusiveFileLock
			acquired bool
			err      error
		}{lock, acquired, err}
	}()
	var contender struct {
		lock     *gateprivate.ExclusiveFileLock
		acquired bool
		err      error
	}
	select {
	case contender = <-result:
	case <-time.After(time.Second):
		t.Fatal("contender did not return while a separate process held the update lock")
	}
	if contender.err != nil || contender.acquired || contender.lock != nil {
		t.Fatalf("contender lock=%v acquired=%t error=%v", contender.lock, contender.acquired, contender.err)
	}
	if err := reuseProductionCurrentDuringUpdate(context.Background(), session, fixture.identityRun); err != nil {
		t.Fatalf("verified contender current was not reused: %v", err)
	}
}

func TestProductionSelfUpdateLockHolderProcess(t *testing.T) {
	lockPath := os.Getenv("SUPER_DOLPHIN_TEST_UPDATE_LOCK_PATH")
	readyPath := os.Getenv("SUPER_DOLPHIN_TEST_UPDATE_LOCK_READY")
	if lockPath == "" || readyPath == "" {
		return
	}
	lock, err := gateprivate.AcquireExclusiveFileLock(context.Background(), lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
}

func startProductionSelfUpdateLockHolder(t *testing.T, lockPath string) func() {
	t.Helper()
	readyPath := filepath.Join(filepath.Dir(lockPath), "lock-holder-ready")
	command := exec.Command(os.Args[0], "-test.run=^TestProductionSelfUpdateLockHolderProcess$", "-test.count=1")
	command.Env = append(os.Environ(),
		"SUPER_DOLPHIN_TEST_UPDATE_LOCK_PATH="+lockPath,
		"SUPER_DOLPHIN_TEST_UPDATE_LOCK_READY="+readyPath,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		} else if !os.IsNotExist(err) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal("timed out waiting for lock holder process")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}
}

func TestProductionSelfUpdateContenderRejectsCorruptState(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	database, installRoot, ownerUID, err := openProductionSelfUpdateStateStore(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(
		"UPDATE production_self_update_state SET state_json = ? WHERE install_root = ? AND owner_uid = ?",
		[]byte("{not-json"),
		installRoot,
		ownerUID,
	); err != nil {
		t.Fatal(err)
	}
	owner, err := gateprivate.AcquireExclusiveFileLock(
		context.Background(), filepath.Join(filepath.Dir(fixture.current), ".super-dolphin-gate-update.lock"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Release() }()
	if _, acquired, err := tryAcquireProductionSelfUpdateLock(fixture.current); err != nil || acquired {
		t.Fatalf("contender lock acquired=%t error=%v", acquired, err)
	}
	if err := reuseProductionCurrentDuringUpdate(context.Background(), productionUpdateReuseSession(fixture), fixture.identityRun); err == nil {
		t.Fatal("corrupt state was accepted while another process updates")
	}
}

func productionUpdateReuseSession(fixture productionUpdateStateFixture) productionSelfUpdateSession {
	return productionSelfUpdateSession{
		current: fixture.current, statePath: fixture.statePath,
		config: productionCoordinatorConfig{BootstrapControllerFile: fixture.bootstrap},
		root:   productionBootstrapRoot{RemoteURL: fixture.state.Remote, TrustedRef: fixture.state.TrustedRef},
	}
}
