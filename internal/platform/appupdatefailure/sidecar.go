package appupdatefailure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	Filename = "pre-journal-failure.json"
	Version  = 1
	maxSize  = 4096
)

type record struct {
	Version       int    `json:"version"`
	Code          string `json:"code"`
	Retryable     bool   `json:"retryable"`
	Action        string `json:"action"`
	TransactionID string `json:"transaction_id"`
}

// Error 仅携带 registry 校验后的恢复元数据跨越 appupdate RPC 边界。
type Error struct{ failure contract.RecoveryFailure }

// Error 返回固定分类文本，避免泄露 updater 原始输出。
func (Error) Error() string { return "app update pre-journal recovery action is required" }

// RecoveryFailure 返回最小恢复元数据。
func (err Error) RecoveryFailure() contract.RecoveryFailure { return err.failure }

// NewError 从合法 sidecar 元数据构造恢复错误。
func NewError(failure contract.RecoveryFailure) (Error, error) {
	if err := validateFailure(failure); err != nil {
		return Error{}, err
	}
	return Error{failure: failure}, nil
}

// CanonicalPath 只接受干净绝对 StageDir 并派生固定 sidecar 路径。
func CanonicalPath(stageDir string) (string, error) {
	if stageDir == "" || !filepath.IsAbs(stageDir) || filepath.Clean(stageDir) != stageDir {
		return "", errors.New("app update stage dir must be a clean absolute path")
	}
	return filepath.Join(stageDir, Filename), nil
}

// Write 以临时文件、fsync 和原子 rename 发布最小恢复记录。
func Write(stageDir string, failure contract.RecoveryFailure) error {
	if err := validateFailure(failure); err != nil {
		return err
	}
	path, err := validateExistingStageDir(stageDir)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(record{Version: Version, Code: failure.Code, Retryable: failure.Retryable, Action: string(failure.Action), TransactionID: failure.TransactionID})
	if err != nil {
		return fmt.Errorf("encode app update failure sidecar: %w", err)
	}
	temp, err := os.CreateTemp(stageDir, ".pre-journal-failure-*")
	if err != nil {
		return fmt.Errorf("create app update failure sidecar temp: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure app update failure sidecar temp: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return fmt.Errorf("write app update failure sidecar temp: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync app update failure sidecar temp: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close app update failure sidecar temp: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish app update failure sidecar: %w", err)
	}
	return syncDirectory(stageDir)
}

// WriteCode 从唯一 registry 构造无 transaction 恢复元数据后写入 sidecar。
func WriteCode(stageDir string, code string) error {
	failure, ok := contract.RecoveryFailureForCode(code, "")
	if !ok {
		return errors.New("app update pre-journal recovery code is not registered")
	}
	return Write(stageDir, failure)
}

// Read 严格读取并校验固定版本、schema、权限和恢复 registry。
func Read(stageDir string) (contract.RecoveryFailure, bool, error) {
	path, err := CanonicalPath(stageDir)
	if err != nil {
		return contract.RecoveryFailure{}, false, err
	}
	if _, err := validateStageDirIfPresent(stageDir); err != nil {
		return contract.RecoveryFailure{}, false, err
	}
	raw, exists, err := readSafeSidecar(path)
	if err != nil || !exists {
		return contract.RecoveryFailure{}, exists, err
	}
	value, err := decodeRecord(raw)
	if err != nil {
		return contract.RecoveryFailure{}, false, err
	}
	failure := contract.RecoveryFailure{Code: value.Code, Retryable: value.Retryable, Action: contract.RecoveryAction(value.Action), TransactionID: value.TransactionID}
	if err := validateFailure(failure); err != nil {
		return contract.RecoveryFailure{}, false, err
	}
	return failure, true, nil
}

// readSafeSidecar 在读取前校验 sidecar 文件类型、权限和大小。
func readSafeSidecar(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect app update failure sidecar: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > maxSize {
		return nil, false, errors.New("app update failure sidecar has unsafe metadata")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read app update failure sidecar: %w", err)
	}
	return raw, true, nil
}

// decodeRecord 拒绝额外字段、尾随 JSON 与未知 sidecar 版本。
func decodeRecord(raw []byte) (record, error) {
	var value record
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return record{}, errors.New("app update failure sidecar is malformed")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return record{}, err
	}
	if value.Version != Version {
		return record{}, errors.New("app update failure sidecar version is unsupported")
	}
	return value, nil
}

// Clear 幂等删除固定 sidecar，并同步 StageDir 目录项。
func Clear(stageDir string) error {
	path, err := validateExistingStageDir(stageDir)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear app update failure sidecar: %w", err)
	}
	return syncDirectory(stageDir)
}

// validateFailure 仅允许无 transaction 的签名或完整性固定恢复动作。
func validateFailure(failure contract.RecoveryFailure) error {
	if failure.TransactionID != "" || (failure.Code != "UPDATE_SIGNATURE_INVALID" && failure.Code != "UPDATE_INTEGRITY_INVALID") {
		return errors.New("app update failure sidecar recovery metadata is invalid")
	}
	want, ok := contract.RecoveryFailureForCode(failure.Code, "")
	if !ok || want != failure {
		return errors.New("app update failure sidecar recovery metadata is inconsistent")
	}
	return nil
}

// validateExistingStageDir 要求 StageDir 已存在且为非符号链接目录。
func validateExistingStageDir(stageDir string) (string, error) {
	path, err := CanonicalPath(stageDir)
	if err != nil {
		return "", err
	}
	if _, err := validateStageDirIfPresent(stageDir); err != nil {
		return "", err
	}
	info, err := os.Lstat(stageDir)
	if err != nil {
		return "", fmt.Errorf("inspect app update stage dir: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("app update stage dir is not a regular directory")
	}
	resolved, err := filepath.EvalSymlinks(stageDir)
	if err != nil {
		return "", fmt.Errorf("resolve app update stage dir: %w", err)
	}
	if resolved != stageDir {
		return "", errors.New("app update stage dir contains a symbolic-link component")
	}
	return path, nil
}

// validateStageDirIfPresent 允许首次运行时目录缺席，否则严格校验目录类型。
func validateStageDirIfPresent(stageDir string) (string, error) {
	path, err := CanonicalPath(stageDir)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(stageDir)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect app update stage dir: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("app update stage dir is not a regular directory")
	}
	resolved, err := filepath.EvalSymlinks(stageDir)
	if err != nil {
		return "", fmt.Errorf("resolve app update stage dir: %w", err)
	}
	if resolved != stageDir {
		return "", errors.New("app update stage dir contains a symbolic-link component")
	}
	return path, nil
}

// requireJSONEOF 确保 sidecar 仅包含一个 JSON 值。
func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("app update failure sidecar contains trailing data")
	}
	return nil
}
