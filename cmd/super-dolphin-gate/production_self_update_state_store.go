package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const productionSelfUpdateStateStoreSchema = `
CREATE TABLE IF NOT EXISTS production_self_update_state (
	install_root TEXT NOT NULL,
	owner_uid INTEGER NOT NULL,
	state_json BLOB NOT NULL,
	PRIMARY KEY (install_root, owner_uid)
);
CREATE TABLE IF NOT EXISTS production_self_update_legacy_import (
	install_root TEXT NOT NULL,
	owner_uid INTEGER NOT NULL,
	retired INTEGER NOT NULL CHECK (retired IN (0, 1)),
	PRIMARY KEY (install_root, owner_uid)
);`

var errProductionSelfUpdateStateNotFound = errors.New("production self-update state is not initialized")

// productionSelfUpdateDatabasePath 将旧 JSON 路径映射到安装根专属 SQLite 权威账本。
func productionSelfUpdateDatabasePath(path string) (string, error) {
	if filepath.Base(path) != productionSelfUpdateStateFile || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("production self-update state path must be canonical and absolute")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("resolve production self-update install root: %w", err)
	}
	installRoot, err := gateprivate.CanonicalOwnerDirectory(resolved)
	if err != nil {
		return "", fmt.Errorf("validate production self-update install root: %w", err)
	}
	info, err := os.Stat(installRoot)
	if err != nil || !productionProvisionOwnedByCurrentUser(info) {
		return "", errors.Join(errors.New("production self-update install root is not owned by the current user"), err)
	}
	return filepath.Join(installRoot, strings.TrimSuffix(productionSelfUpdateStateFile, ".json")+".sqlite"), nil
}

func openProductionSelfUpdateStateStore(path string) (*sql.DB, string, int, error) {
	databasePath, err := productionSelfUpdateDatabasePath(path)
	if err != nil {
		return nil, "", 0, err
	}
	databaseFile, err := openProductionSelfUpdateDatabaseFile(databasePath)
	if err != nil {
		return nil, "", 0, err
	}
	database, err := gateprivate.OpenSQLite(databasePath)
	if err != nil {
		_ = databaseFile.Close()
		return nil, "", 0, fmt.Errorf("open production self-update SQLite: %w", err)
	}
	closeWithError := func(cause error) (*sql.DB, string, int, error) {
		return nil, "", 0, errors.Join(cause, database.Close(), databaseFile.Close())
	}
	if _, err := database.ExecContext(context.Background(), productionSelfUpdateStateStoreSchema); err != nil {
		return closeWithError(fmt.Errorf("initialize production self-update SQLite: %w", err))
	}
	if err := gateprivate.RestrictOwnerFile(databasePath); err != nil {
		return closeWithError(fmt.Errorf("protect production self-update SQLite: %w", err))
	}
	if err := verifyProductionSelfUpdateDatabaseFile(databaseFile, databasePath); err != nil {
		return closeWithError(err)
	}
	if err := databaseFile.Close(); err != nil {
		return closeWithError(fmt.Errorf("close production self-update SQLite identity handle: %w", err))
	}
	return database, filepath.Dir(databasePath), os.Getuid(), nil
}

func openProductionSelfUpdateDatabaseFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		return file, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create production self-update SQLite: %w", err)
	}
	if _, err := gateprivate.CanonicalOwnerFile(path); err != nil {
		return nil, fmt.Errorf("validate existing production self-update SQLite: %w", err)
	}
	file, err = os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open existing production self-update SQLite: %w", err)
	}
	if err := verifyProductionSelfUpdateDatabaseFile(file, path); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func verifyProductionSelfUpdateDatabaseFile(file *os.File, path string) error {
	opened, statErr := file.Stat()
	pathInfo, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !os.SameFile(opened, pathInfo) || !opened.Mode().IsRegular() ||
		opened.Mode().Perm() != 0o600 || !productionProvisionOwnedByCurrentUser(opened) {
		return errors.Join(errors.New("production self-update SQLite changed or is not owner-only"), statErr, lstatErr)
	}
	return nil
}

func loadProductionSelfUpdateState(path string) (productionSelfUpdateState, error) {
	database, installRoot, ownerUID, err := openProductionSelfUpdateStateStore(path)
	if err != nil {
		return productionSelfUpdateState{}, err
	}
	defer database.Close()
	state, err := loadProductionSelfUpdateStateFromStore(database, installRoot, ownerUID)
	if err == nil {
		if err := requireProductionSelfUpdateLegacyRetired(database, installRoot, ownerUID, path); err != nil {
			return productionSelfUpdateState{}, err
		}
		return state, nil
	}
	if errors.Is(err, errProductionSelfUpdateStateNotFound) {
		return importProductionSelfUpdateLegacyJSON(database, installRoot, ownerUID, path)
	}
	return state, err
}

func loadProductionSelfUpdateStateFromStore(database *sql.DB, installRoot string, ownerUID int) (productionSelfUpdateState, error) {
	var data []byte
	err := database.QueryRowContext(
		context.Background(),
		"SELECT state_json FROM production_self_update_state WHERE install_root = ? AND owner_uid = ?",
		installRoot,
		ownerUID,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return productionSelfUpdateState{}, errProductionSelfUpdateStateNotFound
	}
	if err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("read production self-update SQLite: %w", err)
	}
	var state productionSelfUpdateState
	if err := gatecontract.DecodeStrictJSON(data, &state); err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("decode production self-update SQLite state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("validate production self-update SQLite state: %w", err)
	}
	return state, nil
}

// importProductionSelfUpdateLegacyJSON 只在当前安装根和 owner 尚无 SQLite 状态时导入一次旧 JSON。
func importProductionSelfUpdateLegacyJSON(database *sql.DB, installRoot string, ownerUID int, legacyPath string) (productionSelfUpdateState, error) {
	retired, err := productionSelfUpdateLegacyRetired(database, installRoot, ownerUID)
	if err != nil {
		return productionSelfUpdateState{}, err
	}
	if retired {
		return productionSelfUpdateState{}, errProductionSelfUpdateStateNotFound
	}
	state, err := loadProductionSelfUpdateLegacyJSON(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return productionSelfUpdateState{}, errProductionSelfUpdateStateNotFound
	}
	if err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("load legacy production self-update JSON: %w", err)
	}
	if err := storeProductionSelfUpdateLegacyImport(database, installRoot, ownerUID, state); err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("import legacy production self-update JSON into SQLite: %w", err)
	}
	if err := retireProductionSelfUpdateLegacyJSON(legacyPath); err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("retire legacy production self-update JSON: %w", err)
	}
	if _, err := database.ExecContext(context.Background(), "UPDATE production_self_update_legacy_import SET retired = 1 WHERE install_root = ? AND owner_uid = ?", installRoot, ownerUID); err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("mark legacy production self-update JSON retired: %w", err)
	}
	return state, nil
}

func storeProductionSelfUpdateLegacyImport(database *sql.DB, installRoot string, ownerUID int, state productionSelfUpdateState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode legacy production self-update SQLite state: %w", err)
	}
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin legacy production self-update import: %w", err)
	}
	if _, err := transaction.ExecContext(context.Background(), "INSERT INTO production_self_update_state(install_root, owner_uid, state_json) VALUES (?, ?, ?)", installRoot, ownerUID, data); err != nil {
		return errors.Join(fmt.Errorf("write legacy production self-update state: %w", err), transaction.Rollback())
	}
	if _, err := transaction.ExecContext(context.Background(), "INSERT INTO production_self_update_legacy_import(install_root, owner_uid, retired) VALUES (?, ?, 0)", installRoot, ownerUID); err != nil {
		return errors.Join(fmt.Errorf("write legacy production self-update marker: %w", err), transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit legacy production self-update import: %w", err)
	}
	return nil
}

func productionSelfUpdateLegacyRetired(database *sql.DB, installRoot string, ownerUID int) (bool, error) {
	var retired int
	err := database.QueryRowContext(context.Background(), "SELECT retired FROM production_self_update_legacy_import WHERE install_root = ? AND owner_uid = ?", installRoot, ownerUID).Scan(&retired)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || (retired != 0 && retired != 1) {
		return false, errors.Join(errors.New("production self-update legacy marker is invalid"), err)
	}
	return retired == 1, nil
}

func requireProductionSelfUpdateLegacyRetired(database *sql.DB, installRoot string, ownerUID int, legacyPath string) error {
	var retired int
	err := database.QueryRowContext(context.Background(), "SELECT retired FROM production_self_update_legacy_import WHERE install_root = ? AND owner_uid = ?", installRoot, ownerUID).Scan(&retired)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || retired != 1 {
		return errors.Join(errors.New("production self-update legacy retirement is incomplete"), err)
	}
	if _, err := os.Lstat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(errors.New("retired production self-update legacy JSON reappeared"), err)
	}
	return nil
}

func retireProductionSelfUpdateLegacyJSON(path string) error {
	if _, err := loadProductionSelfUpdateLegacyJSON(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(errors.New("legacy production self-update JSON remains after retirement"), err)
	}
	return nil
}

func loadProductionSelfUpdateLegacyJSON(path string) (productionSelfUpdateState, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return productionSelfUpdateState{}, os.ErrNotExist
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !productionProvisionOwnedByCurrentUser(info) {
		return productionSelfUpdateState{}, errors.Join(errors.New("legacy production self-update JSON is not an owner-only regular file"), err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("resolve legacy production self-update JSON: %w", err)
	}
	data, err := gateprivate.ReadOwnerFile(resolved, 64<<10)
	if err != nil {
		return productionSelfUpdateState{}, fmt.Errorf("read legacy production self-update JSON: %w", err)
	}
	var state productionSelfUpdateState
	if err := gatecontract.DecodeStrictJSON(data, &state); err != nil {
		return productionSelfUpdateState{}, err
	}
	return state, state.Validate()
}

// writeProductionSelfUpdateState 只写 SQLite 权威账本；不会重新发布旧 JSON。
func writeProductionSelfUpdateState(path string, state productionSelfUpdateState) error {
	database, installRoot, ownerUID, err := openProductionSelfUpdateStateStore(path)
	if err != nil {
		return err
	}
	defer database.Close()
	return storeProductionSelfUpdateStateInStore(database, installRoot, ownerUID, state, false)
}

func storeProductionSelfUpdateStateInStore(database *sql.DB, installRoot string, ownerUID int, state productionSelfUpdateState, insertOnly bool) error {
	if err := state.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode production self-update SQLite state: %w", err)
	}
	query := `INSERT INTO production_self_update_state(install_root, owner_uid, state_json) VALUES (?, ?, ?)`
	if !insertOnly {
		query += ` ON CONFLICT(install_root, owner_uid) DO UPDATE SET state_json = excluded.state_json`
	}
	if _, err := database.ExecContext(context.Background(), query, installRoot, ownerUID, data); err != nil {
		return fmt.Errorf("write production self-update SQLite: %w", err)
	}
	return nil
}

func restoreProductionSelfUpdateState(path string, state productionSelfUpdateState, existed bool) error {
	if !existed {
		return deleteProductionSelfUpdateState(path)
	}
	return writeProductionSelfUpdateState(path, state)
}

func deleteProductionSelfUpdateState(path string) error {
	database, installRoot, ownerUID, err := openProductionSelfUpdateStateStore(path)
	if err != nil {
		return err
	}
	defer database.Close()
	if _, err := database.ExecContext(
		context.Background(),
		"DELETE FROM production_self_update_state WHERE install_root = ? AND owner_uid = ?",
		installRoot,
		ownerUID,
	); err != nil {
		return fmt.Errorf("delete production self-update SQLite state: %w", err)
	}
	return nil
}
