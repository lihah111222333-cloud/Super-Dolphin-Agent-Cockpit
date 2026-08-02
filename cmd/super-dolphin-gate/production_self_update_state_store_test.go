package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProductionSelfUpdateStateMissingSQLiteIsNoState(t *testing.T) {
	if _, err := loadProductionSelfUpdateState(newPrivateProductionStatePath(t)); !errors.Is(err, errProductionSelfUpdateStateNotFound) {
		t.Fatalf("missing SQLite state error = %v, want state not found", err)
	}
}

func TestProductionSelfUpdateStateStoreIsolatedByInstallRoot(t *testing.T) {
	firstPath := newPrivateProductionStatePath(t)
	secondPath := newPrivateProductionStatePath(t)
	first := validProductionSelfUpdateState(productionTestDigest("c"), productionTestDigest("d"))
	if err := writeProductionSelfUpdateState(firstPath, first); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProductionSelfUpdateState(secondPath); !errors.Is(err, errProductionSelfUpdateStateNotFound) {
		t.Fatalf("second install root error = %v, want state not found", err)
	}
}

func TestProductionSelfUpdateStateStoreIsolatedByOwner(t *testing.T) {
	statePath := newPrivateProductionStatePath(t)
	database, installRoot, ownerUID, err := openProductionSelfUpdateStateStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state := validProductionSelfUpdateState(productionTestDigest("1"), productionTestDigest("2"))
	if err := storeProductionSelfUpdateStateInStore(database, installRoot, ownerUID+1, state, true); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProductionSelfUpdateState(statePath); !errors.Is(err, errProductionSelfUpdateStateNotFound) {
		t.Fatalf("current owner read another owner state: %v", err)
	}
}

func TestProductionSelfUpdateStateWriteCreatesOwnerOnlySQLite(t *testing.T) {
	statePath := newPrivateProductionStatePath(t)
	state := validProductionSelfUpdateState(productionTestDigest("e"), productionTestDigest("f"))
	if err := writeProductionSelfUpdateState(statePath, state); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !productionProvisionOwnedByCurrentUser(info) {
		t.Fatalf("SQLite state permissions = %v, want owner-only regular file", info.Mode())
	}
}

func TestProductionSelfUpdateStateRejectsUnsafeInstallRoot(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := writeProductionSelfUpdateState(filepath.Join(directory, productionSelfUpdateStateFile), validProductionSelfUpdateState(productionTestDigest("a"), productionTestDigest("b"))); err == nil {
		t.Fatal("world-writable install root was accepted")
	}
}

func TestProductionSelfUpdateStateRejectsPreexistingDatabaseSymlink(t *testing.T) {
	statePath := newPrivateProductionStatePath(t)
	databasePath, err := productionSelfUpdateDatabasePath(statePath)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.sqlite")
	if err := os.WriteFile(target, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, databasePath); err != nil {
		t.Fatal(err)
	}
	if err := writeProductionSelfUpdateState(statePath, validProductionSelfUpdateState(productionTestDigest("c"), productionTestDigest("d"))); err == nil {
		t.Fatal("preexisting SQLite symlink was accepted")
	}
}

func TestProductionSelfUpdateStateDetectsDatabaseReplacementCompetition(t *testing.T) {
	statePath := newPrivateProductionStatePath(t)
	databasePath, err := productionSelfUpdateDatabasePath(statePath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := openProductionSelfUpdateDatabaseFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	target := filepath.Join(t.TempDir(), "replacement.sqlite")
	if err := os.WriteFile(target, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(databasePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, databasePath); err != nil {
		t.Fatal(err)
	}
	if err := verifyProductionSelfUpdateDatabaseFile(file, databasePath); err == nil {
		t.Fatal("SQLite replacement competition was accepted")
	}
}

func newPrivateProductionStatePath(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(directory, productionSelfUpdateStateFile)
}
