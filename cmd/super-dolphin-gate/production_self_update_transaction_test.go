package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestProductionSelfUpdateSwitchTransactionMatrix(t *testing.T) {
	t.Parallel()
	t.Run("success", testProductionSwitchSuccess)
	t.Run("state publish fails", testProductionSwitchStatePublishFailure)
	t.Run("current publish fails", testProductionSwitchCurrentPublishFailure)
	t.Run("current publish restores prior SQLite state", testProductionSwitchCurrentPublishRestoresPriorSQLiteState)
	t.Run("post-switch fsync fails", testProductionSwitchPostSyncFailure)
}

func TestProductionSelfUpdateStateMatrix(t *testing.T) {
	t.Parallel()
	t.Run("cache hit refreshes source object metadata", testProductionStateCacheHitRefresh)
	t.Run("source digest change requires rebuild", testProductionStateSourceChange)
	t.Run("first migration accepts only exact bootstrap current", testProductionStateFirstMigration)
	t.Run("unknown state field fails closed", testProductionStateUnknownField)
}

func testProductionSwitchSuccess(t *testing.T) {
	fixture := newProductionSwitchFixture(t)
	if err := switchProductionCurrentCLI(
		fixture.candidate,
		fixture.current,
		fixture.statePath,
		fixture.state,
		liveProductionSwitchOps(),
	); err != nil {
		t.Fatal(err)
	}
	fixture.assertOldCurrent(t, fixture.previous)
	fixture.assertNewCurrent(t)
	loaded, err := loadProductionSelfUpdateState(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != fixture.state {
		t.Fatalf("state = %#v, want %#v", loaded, fixture.state)
	}
}

func testProductionSwitchStatePublishFailure(t *testing.T) {
	fixture := newProductionSwitchFixture(t)
	fixture.state.SchemaVersion = 0
	if err := switchProductionCurrentCLI(
		fixture.candidate,
		fixture.current,
		fixture.statePath,
		fixture.state,
		liveProductionSwitchOps(),
	); err == nil {
		t.Fatal("invalid SQLite state publish succeeded")
	}
	fixture.assertOldCurrent(t, fixture.current)
	assertProductionStateNotFound(t, fixture.statePath)
}

func testProductionSwitchCurrentPublishFailure(t *testing.T) {
	fixture := newProductionSwitchFixture(t)
	ops := liveProductionSwitchOps()
	liveRename := ops.rename
	ops.rename = func(oldPath, newPath string) error {
		if oldPath == fixture.candidate && newPath == fixture.current {
			return errors.New("injected current rename failure")
		}
		return liveRename(oldPath, newPath)
	}
	if err := switchProductionCurrentCLI(
		fixture.candidate,
		fixture.current,
		fixture.statePath,
		fixture.state,
		ops,
	); err == nil {
		t.Fatal("current rename failure succeeded")
	}
	fixture.assertOldCurrent(t, fixture.current)
	assertProductionStateNotFound(t, fixture.statePath)
}

func testProductionSwitchCurrentPublishRestoresPriorSQLiteState(t *testing.T) {
	fixture := newProductionSwitchFixture(t)
	prior := fixture.state
	prior.BinaryDigest = prior.PreviousBinaryDigest
	prior.PreviousBinaryDigest = productionTestDigest("7")
	if err := writeProductionSelfUpdateState(fixture.statePath, prior); err != nil {
		t.Fatal(err)
	}
	ops := liveProductionSwitchOps()
	liveRename := ops.rename
	ops.rename = func(oldPath, newPath string) error {
		if oldPath == fixture.candidate && newPath == fixture.current {
			return errors.New("injected current rename failure")
		}
		return liveRename(oldPath, newPath)
	}
	if err := switchProductionCurrentCLI(
		fixture.candidate,
		fixture.current,
		fixture.statePath,
		fixture.state,
		ops,
	); err == nil {
		t.Fatal("current rename failure succeeded")
	}
	fixture.assertOldCurrent(t, fixture.current)
	loaded, err := loadProductionSelfUpdateState(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != prior {
		t.Fatalf("rollback state = %#v, want %#v", loaded, prior)
	}
}

func testProductionSwitchPostSyncFailure(t *testing.T) {
	fixture := newProductionSwitchFixture(t)
	ops := liveProductionSwitchOps()
	liveSyncDir := ops.syncDir
	syncCalls := 0
	ops.syncDir = func(path string) error {
		syncCalls++
		if syncCalls == 2 {
			return errors.New("injected post-switch fsync failure")
		}
		return liveSyncDir(path)
	}
	if err := switchProductionCurrentCLI(
		fixture.candidate,
		fixture.current,
		fixture.statePath,
		fixture.state,
		ops,
	); err == nil {
		t.Fatal("post-switch fsync failure succeeded")
	}
	fixture.assertOldCurrent(t, fixture.current)
	assertProductionPathAbsent(t, fixture.statePath)
}

func testProductionStateCacheHitRefresh(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	expected := fixture.state
	expected.Commit = strings.Repeat("a", 40)
	expected.Tree = strings.Repeat("b", 40)
	loaded, err := loadProductionSelfUpdateState(fixture.statePath)
	if err != nil || !productionUpdateStateMatchesExpected(loaded, expected) {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	persisted, err := loadProductionSelfUpdateState(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Commit == expected.Commit || persisted.Tree == expected.Tree {
		t.Fatal("cache-hit inspection rewrote source identity before ancestry authorization")
	}
	if err := refreshProductionCacheHitState(fixture.statePath, &loaded, expected); err != nil {
		t.Fatal(err)
	}
	refreshed, err := loadProductionSelfUpdateState(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Commit != expected.Commit || refreshed.Tree != expected.Tree ||
		refreshed.BinaryDigest != fixture.state.BinaryDigest {
		t.Fatalf("refreshed state = %#v", refreshed)
	}
}

func testProductionStateSourceChange(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	expected := fixture.state
	expected.SourceDigest = productionTestDigest("9")
	if productionUpdateStateMatchesExpected(fixture.state, expected) {
		t.Fatal("CLI source change was accepted as cache hit")
	}
}

func testProductionStateFirstMigration(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	if err := deleteProductionSelfUpdateState(fixture.statePath); err != nil {
		t.Fatal(err)
	}
	if err := copyProductionTestFile(fixture.bootstrap, fixture.current, 0o700); err != nil {
		t.Fatal(err)
	}
	currentDigest, err := productionBinaryDigest(fixture.current)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadProductionCurrentUpdateState(fixture.statePath, currentDigest, fixture.bootstrap)
	if err != nil || loaded != nil {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if err := os.WriteFile(fixture.current, []byte("unknown current"), 0o700); err != nil {
		t.Fatal(err)
	}
	currentDigest, err = productionBinaryDigest(fixture.current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadProductionCurrentUpdateState(fixture.statePath, currentDigest, fixture.bootstrap); err == nil {
		t.Fatal("missing state accepted a non-bootstrap current")
	}
}

func TestProductionCacheHitStateWriteFailureFailsClosed(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	expected := fixture.state
	expected.Commit = strings.Repeat("a", 40)
	if err := refreshProductionCacheHitState(filepath.Join(t.TempDir(), "missing", "state.json"), &fixture.state, expected); err == nil {
		t.Fatal("cache-hit state write failure was accepted")
	}
}

func TestProductionCurrentCacheHitRefreshesNonCLITreeWithoutBuild(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	expected := fixture.state
	expected.Commit = strings.Repeat("a", 40)
	expected.Tree = strings.Repeat("b", 40)
	matched, err := refreshProductionCurrentCacheHit(
		productionSelfUpdateSession{current: fixture.current, statePath: fixture.statePath}, &fixture.state, expected,
	)
	if err != nil || !matched {
		t.Fatalf("non-CLI tree cache hit matched=%t error=%v", matched, err)
	}
	refreshed, err := loadProductionSelfUpdateState(fixture.statePath)
	if err != nil || refreshed.Commit != expected.Commit || refreshed.Tree != expected.Tree ||
		refreshed.BinaryDigest != fixture.state.BinaryDigest {
		t.Fatalf("refreshed=%#v error=%v", refreshed, err)
	}
}

func TestProductionCurrentCacheHitRejectsCLIClosureChange(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	expected := fixture.state
	expected.SourceDigest = productionTestDigest("9")
	matched, err := refreshProductionCurrentCacheHit(
		productionSelfUpdateSession{current: fixture.current, statePath: fixture.statePath}, &fixture.state, expected,
	)
	if err != nil || matched {
		t.Fatalf("CLI closure change cache hit matched=%t error=%v", matched, err)
	}
}

func TestProductionCurrentCacheHitRejectsStateTOCTOU(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*productionSelfUpdateState, productionSelfUpdateState)
	}{
		{"remote", func(state *productionSelfUpdateState, _ productionSelfUpdateState) {
			state.Remote = "https://invalid.example/other.git"
		}},
		{"ref", func(state *productionSelfUpdateState, _ productionSelfUpdateState) {
			state.TrustedRef = "refs/heads/other"
		}},
		{"platform", func(state *productionSelfUpdateState, _ productionSelfUpdateState) { state.Platform = "other/platform" }},
		{"commit", func(state *productionSelfUpdateState, expected productionSelfUpdateState) {
			state.Commit = expected.Commit
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProductionUpdateStateFixture(t)
			snapshot := fixture.state
			expected := fixture.state
			expected.Commit = strings.Repeat("a", 40)
			persisted := fixture.state
			test.mutate(&persisted, expected)
			if err := writeProductionSelfUpdateState(fixture.statePath, persisted); err != nil {
				t.Fatal(err)
			}
			matched, err := refreshProductionCurrentCacheHit(
				productionSelfUpdateSession{current: fixture.current, statePath: fixture.statePath}, &snapshot, expected,
			)
			if err == nil || matched {
				t.Fatalf("replaced state matched=%t error=%v", matched, err)
			}
		})
	}
}

func testProductionStateUnknownField(t *testing.T) {
	fixture := newProductionUpdateStateFixture(t)
	database, installRoot, ownerUID, err := openProductionSelfUpdateStateStore(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(
		"UPDATE production_self_update_state SET state_json = ? WHERE install_root = ? AND owner_uid = ?",
		[]byte(`{"unknown":true}`),
		installRoot,
		ownerUID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := loadProductionSelfUpdateState(fixture.statePath); err == nil {
		t.Fatal("strict SQLite state decoder accepted an unknown field")
	}
}

func TestProductionSelfUpdateStateFieldRegistry(t *testing.T) {
	t.Parallel()
	registered := []string{
		"schema_version",
		"remote",
		"trusted_ref",
		"commit",
		"tree",
		"source_digest",
		"lock_digest",
		"toolchain_digest",
		"platform",
		"binary_digest",
		"previous_binary_digest",
		"previous_source_digest",
		"previous_toolchain_digest",
		"current",
		"previous",
	}
	var produced []string
	stateType := reflect.TypeFor[productionSelfUpdateState]()
	for field := range stateType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("state field %s has no JSON owner", field.Name)
		}
		produced = append(produced, name)
	}
	slices.Sort(produced)
	slices.Sort(registered)
	if !slices.Equal(produced, registered) {
		t.Fatalf("state fields=%q registered consumers=%q", produced, registered)
	}
}

type productionSwitchFixture struct {
	directory string
	current   string
	candidate string
	previous  string
	statePath string
	state     productionSelfUpdateState
}

func newProductionSwitchFixture(t *testing.T) productionSwitchFixture {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(directory, "staging")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := productionSwitchFixture{
		directory: directory,
		current:   filepath.Join(directory, productionCurrentGateCLI),
		candidate: filepath.Join(staging, productionCurrentGateCLI),
		previous:  filepath.Join(directory, productionPreviousGateCLI),
		statePath: filepath.Join(directory, productionSelfUpdateStateFile),
	}
	if err := os.WriteFile(fixture.current, []byte("old current"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.candidate, []byte("new current"), 0o700); err != nil {
		t.Fatal(err)
	}
	currentDigest, err := productionBinaryDigest(fixture.current)
	if err != nil {
		t.Fatal(err)
	}
	candidateDigest, err := productionBinaryDigest(fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	fixture.state = validProductionSelfUpdateState(candidateDigest, currentDigest)
	return fixture
}

func (fixture productionSwitchFixture) assertOldCurrent(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old current" {
		t.Fatalf("%s = %q, want old current", path, data)
	}
}

func (fixture productionSwitchFixture) assertNewCurrent(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(fixture.current)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new current" {
		t.Fatalf("current = %q, want new current", data)
	}
}

type productionUpdateStateFixture struct {
	current     string
	bootstrap   string
	statePath   string
	state       productionSelfUpdateState
	identityRun productionSelfUpdateDepsRun
}

func newProductionUpdateStateFixture(t *testing.T) productionUpdateStateFixture {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := productionUpdateStateFixture{
		current:   filepath.Join(directory, productionCurrentGateCLI),
		bootstrap: filepath.Join(directory, "bootstrap-controller"),
		statePath: filepath.Join(directory, productionSelfUpdateStateFile),
	}
	if err := os.WriteFile(fixture.current, []byte("current"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.bootstrap, []byte("bootstrap"), 0o700); err != nil {
		t.Fatal(err)
	}
	currentDigest, err := productionBinaryDigest(fixture.current)
	if err != nil {
		t.Fatal(err)
	}
	fixture.state = validProductionSelfUpdateState(currentDigest, productionTestDigest("8"))
	if err := writeProductionSelfUpdateState(fixture.statePath, fixture.state); err != nil {
		t.Fatal(err)
	}
	fixture.identityRun = func(
		_ context.Context,
		_ string,
		_ []string,
		_ string,
		_ []string,
	) ([]byte, error) {
		return []byte(
			"gate_source_sha256=" + fixture.state.SourceDigest + "\n" +
				"platform=" + runtime.GOOS + "/" + runtime.GOARCH + "\n" +
				"toolchain_digest=" + fixture.state.ToolchainDigest + "\n",
		), nil
	}
	return fixture
}

func validProductionSelfUpdateState(binaryDigest, previousBinaryDigest string) productionSelfUpdateState {
	return productionSelfUpdateState{
		SchemaVersion:        productionSelfUpdateStateV1,
		Remote:               "https://example.invalid/repository.git",
		TrustedRef:           "refs/heads/main",
		Commit:               strings.Repeat("1", 40),
		Tree:                 strings.Repeat("2", 40),
		SourceDigest:         productionTestDigest("3"),
		LockDigest:           productionTestDigest("4"),
		ToolchainDigest:      productionTestDigest("5"),
		Platform:             runtime.GOOS + "/" + runtime.GOARCH,
		BinaryDigest:         binaryDigest,
		PreviousBinaryDigest: previousBinaryDigest,
		Current:              productionCurrentGateCLI,
		Previous:             productionPreviousGateCLI,
	}
}

func productionTestDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func assertProductionPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %q remains: %v", path, err)
	}
}

func assertProductionStateNotFound(t *testing.T, path string) {
	t.Helper()
	if _, err := loadProductionSelfUpdateState(path); !errors.Is(err, errProductionSelfUpdateStateNotFound) {
		t.Fatalf("state error = %v, want not found", err)
	}
}

func copyProductionTestFile(source, destination string, mode os.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, mode)
}
