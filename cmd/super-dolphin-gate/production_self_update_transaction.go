package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type productionSwitchOps struct {
	link     func(string, string) error
	rename   func(string, string) error
	remove   func(string) error
	syncFile func(string) error
	syncDir  func(string) error
}

type productionSwitchTransaction struct {
	candidate    string
	current      string
	statePath    string
	previous     string
	previousTemp string
	directory    string
	state        productionSelfUpdateState
	oldState     productionSelfUpdateState
	stateExisted bool
	ops          productionSwitchOps
}

// liveProductionSwitchOps 提供生产切换所需的真实文件系统原语。
func liveProductionSwitchOps() productionSwitchOps {
	return productionSwitchOps{
		link:     os.Link,
		rename:   os.Rename,
		remove:   os.Remove,
		syncFile: syncProductionFile,
		syncDir:  syncProductionDirectory,
	}
}

// switchProductionCurrentCLI 验证输入、暂存事务并原子发布新的 current。
func switchProductionCurrentCLI(
	candidate string,
	current string,
	statePath string,
	state productionSelfUpdateState,
	ops productionSwitchOps,
) (resultErr error) {
	if err := validateProductionSwitchOps(ops); err != nil {
		return err
	}
	directory, previous, err := verifyProductionSwitchInputs(candidate, current, statePath, state)
	if err != nil {
		return err
	}
	transaction, err := prepareProductionSwitchTransaction(
		candidate,
		current,
		statePath,
		previous,
		directory,
		state,
		ops,
	)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, transaction.cleanup())
	}()
	return transaction.publish()
}

// validateProductionSwitchOps 确保测试或生产调用没有缺失事务原语。
func validateProductionSwitchOps(ops productionSwitchOps) error {
	if ops.link == nil ||
		ops.rename == nil ||
		ops.remove == nil ||
		ops.syncFile == nil ||
		ops.syncDir == nil {
		return errors.New("production switch operations are required")
	}
	return nil
}

// verifyProductionSwitchInputs 绑定固定路径并校验切换前后的二进制身份。
func verifyProductionSwitchInputs(
	candidate string,
	current string,
	statePath string,
	state productionSelfUpdateState,
) (string, string, error) {
	directory, err := validateProductionSwitchPaths(candidate, current, statePath)
	if err != nil {
		return "", "", err
	}
	if err := state.Validate(); err != nil {
		return "", "", err
	}
	if err := verifyProductionCurrentCLI(current); err != nil {
		return "", "", err
	}
	if err := verifyProductionCurrentCLI(candidate); err != nil {
		return "", "", fmt.Errorf("verify production switch candidate: %w", err)
	}
	if err := verifyProductionSwitchDigest(candidate, state.BinaryDigest, "candidate"); err != nil {
		return "", "", err
	}
	if err := verifyProductionSwitchDigest(current, state.PreviousBinaryDigest, "current"); err != nil {
		return "", "", err
	}
	previous := filepath.Join(directory, productionPreviousGateCLI)
	if err := verifyOptionalProductionPrevious(previous); err != nil {
		return "", "", err
	}
	return directory, previous, nil
}

// validateProductionSwitchPaths 限定切换事务只能操作同目录固定文件名。
func validateProductionSwitchPaths(candidate, current, statePath string) (string, error) {
	directory := filepath.Dir(current)
	if filepath.Dir(candidate) == "" ||
		directory != filepath.Dir(statePath) ||
		filepath.Base(current) != productionCurrentGateCLI ||
		filepath.Base(statePath) != productionSelfUpdateStateFile {
		return "", errors.New("production switch paths are inconsistent")
	}
	return directory, nil
}

// verifyProductionSwitchDigest 校验事务输入与已签名状态绑定的二进制摘要。
func verifyProductionSwitchDigest(path, want, label string) error {
	got, err := productionBinaryDigest(path)
	if err != nil || got != want {
		return errors.Join(fmt.Errorf("production switch %s digest mismatch", label), err)
	}
	return nil
}

// prepareProductionSwitchTransaction 保存 SQLite 旧状态并暂存 previous。
func prepareProductionSwitchTransaction(
	candidate string,
	current string,
	statePath string,
	previous string,
	directory string,
	state productionSelfUpdateState,
	ops productionSwitchOps,
) (productionSwitchTransaction, error) {
	oldState, err := loadProductionSelfUpdateState(statePath)
	stateExisted := err == nil
	if err != nil && !errors.Is(err, errProductionSelfUpdateStateNotFound) {
		return productionSwitchTransaction{}, err
	}
	previousTemp, err := stageProductionPreviousLink(current, directory, ops)
	if err != nil {
		return productionSwitchTransaction{}, err
	}
	return productionSwitchTransaction{
		candidate:    candidate,
		current:      current,
		statePath:    statePath,
		previous:     previous,
		previousTemp: previousTemp,
		directory:    directory,
		state:        state,
		oldState:     oldState,
		stateExisted: stateExisted,
		ops:          ops,
	}, nil
}

// cleanup 删除尚未被 rename 消费的事务临时文件。
func (transaction productionSwitchTransaction) cleanup() error {
	return removeProductionTemp(transaction.ops.remove, transaction.previousTemp)
}

// publish 按 state、previous、current 顺序发布并在失败时回滚。
func (transaction productionSwitchTransaction) publish() error {
	if err := transaction.ops.syncFile(transaction.candidate); err != nil {
		return err
	}
	if err := writeProductionSelfUpdateState(transaction.statePath, transaction.state); err != nil {
		return fmt.Errorf("publish production update SQLite state before switch: %w", err)
	}
	if err := transaction.ops.syncDir(transaction.directory); err != nil {
		return transaction.rollback(fmt.Errorf("sync production state before switch: %w", err), false)
	}
	if err := transaction.ops.rename(transaction.previousTemp, transaction.previous); err != nil {
		return transaction.rollback(fmt.Errorf("publish previous production gate CLI: %w", err), false)
	}
	if err := transaction.ops.rename(transaction.candidate, transaction.current); err != nil {
		return transaction.rollback(fmt.Errorf("publish current production gate CLI: %w", err), false)
	}
	if err := transaction.ops.syncDir(transaction.directory); err != nil {
		return transaction.rollback(fmt.Errorf("sync switched production gate CLI: %w", err), true)
	}
	return nil
}

// rollback 恢复切换前的 current 和 SQLite 状态，并同步目录元数据。
func (transaction productionSwitchTransaction) rollback(cause error, currentSwitched bool) error {
	var rollbackErr error
	if currentSwitched {
		rollbackErr = restoreProductionCurrent(
			transaction.previous,
			transaction.current,
			transaction.directory,
			transaction.ops,
		)
	}
	rollbackErr = errors.Join(
		rollbackErr,
		restoreProductionSelfUpdateState(transaction.statePath, transaction.oldState, transaction.stateExisted),
		transaction.ops.syncDir(transaction.directory),
	)
	return errors.Join(cause, rollbackErr)
}

// verifyOptionalProductionPrevious 拒绝不受当前用户控制的 previous 残留。
func verifyOptionalProductionPrevious(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 ||
		!productionProvisionOwnedByCurrentUser(info) {
		return errors.Join(errors.New("production previous CLI is not an owner-only regular executable"), err)
	}
	return nil
}

// stageProductionPreviousLink 为当前二进制创建同目录硬链接快照。
func stageProductionPreviousLink(
	current string,
	directory string,
	ops productionSwitchOps,
) (string, error) {
	placeholder, err := os.CreateTemp(directory, ".super-dolphin-gate-previous-")
	if err != nil {
		return "", err
	}
	path := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		return "", errors.Join(err, removeProductionTemp(os.Remove, path))
	}
	if err := ops.remove(path); err != nil {
		return "", err
	}
	if err := ops.link(current, path); err != nil {
		return "", fmt.Errorf("stage previous production gate CLI: %w", err)
	}
	return path, nil
}

// restoreProductionCurrent 从 previous 快照恢复固定 current。
func restoreProductionCurrent(
	previous string,
	current string,
	directory string,
	ops productionSwitchOps,
) (resultErr error) {
	temp, err := stageProductionPreviousLink(previous, directory, ops)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, removeProductionTemp(ops.remove, temp))
	}()
	if err := ops.rename(temp, current); err != nil {
		return err
	}
	return ops.syncDir(directory)
}

// removeProductionTemp 幂等删除事务临时文件。
func removeProductionTemp(remove func(string) error, path string) error {
	if path == "" {
		return nil
	}
	err := remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// syncProductionFile 将候选二进制内容刷入稳定存储。
func syncProductionFile(path string) (resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	return file.Sync()
}

// syncProductionDirectory 将 rename 等目录项变更刷入稳定存储。
func syncProductionDirectory(path string) (resultErr error) {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, directory.Close())
	}()
	return directory.Sync()
}

// productionBinaryDigest 流式计算二进制 SHA-256，避免整文件载入内存。
func productionBinaryDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
