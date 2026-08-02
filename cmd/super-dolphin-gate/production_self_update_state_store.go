package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const productionSelfUpdateStateStoreSchema = `
CREATE TABLE IF NOT EXISTS production_self_update_state (
	install_root TEXT NOT NULL,
	owner_uid INTEGER NOT NULL,
	state_json BLOB NOT NULL,
	PRIMARY KEY (install_root, owner_uid)
);`

var errProductionSelfUpdateStateNotFound = errors.New("production self-update state is not initialized")

// productionSelfUpdateDatabasePath 校验安装根专属 SQLite 权威账本路径。
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
	return filepath.Join(installRoot, productionSelfUpdateStateFile), nil
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
	return loadProductionSelfUpdateStateFromStore(database, installRoot, ownerUID)
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

// writeProductionSelfUpdateState 只写 SQLite 权威账本。
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
	if _, err := database.ExecContext(
		context.Background(),
		"DELETE FROM production_self_update_state WHERE install_root = ? AND owner_uid = ?",
		installRoot,
		ownerUID,
	); err != nil {
		database.Close()
		return fmt.Errorf("delete production self-update SQLite state: %w", err)
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("close production self-update SQLite state before removal: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove empty production self-update SQLite state: %w", err)
	}
	return nil
}
