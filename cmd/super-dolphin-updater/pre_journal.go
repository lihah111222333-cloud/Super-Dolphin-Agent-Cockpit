package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

// clearPreparedPreJournalFailure 在首次安装发布 target 前清理 sidecar，失败时同时清理 candidate。
func clearPreparedPreJournalFailure(stageDir string, generation string, stagingPath string) error {
	if stageDir == "" {
		return nil
	}
	if err := clearPreJournalFailure(stageDir, generation); err != nil {
		return removePreparedCandidate(stagingPath, err)
	}
	return nil
}

// updaterSidecarStageDir 从已验证 DMG 的父目录派生唯一 package-owned StageDir。
func updaterSidecarStageDir(req installRequest) (string, error) {
	stageDir := filepath.Dir(strings.TrimSpace(req.DMGPath))
	if _, err := appupdatefailure.CanonicalPath(stageDir); err != nil {
		return "", fmt.Errorf("derive app update pre-journal sidecar: %w", err)
	}
	return stageDir, nil
}

// clearPreJournalFailure 仅删除 matching generation；调用方决定首次安装或 journal 发布后的安全边界。
func clearPreJournalFailure(stageDir string, generation string) error {
	if stageDir == "" {
		return nil
	}
	if err := appupdatefailure.Clear(stageDir, generation); err != nil {
		return fmt.Errorf("clear app update pre-journal failure: %w", err)
	}
	return nil
}

// recordPreJournalFailure 仅为明确的签名/完整性失败写入最小 sidecar。
func recordPreJournalFailure(stageDir string, generation string, cause error) error {
	code := ""
	switch {
	case errors.Is(cause, recovery.ErrUpdateSignatureInvalid):
		code = "UPDATE_SIGNATURE_INVALID"
	case errors.Is(cause, recovery.ErrUpdateIntegrityInvalid):
		code = "UPDATE_INTEGRITY_INVALID"
	default:
		return cause
	}
	if err := appupdatefailure.FailCode(stageDir, generation, code); err != nil {
		return errors.Join(cause, fmt.Errorf("write app update pre-journal failure: %w", err))
	}
	return cause
}

// recordStoreCreateFailure 将 journal 发布前的 Store 故障收敛为 matching-generation 安全恢复信号。
// UPDATE_INTEGRITY_INVALID 在此表示保守阻断，而不是断言包摘要已被判定不一致；sidecar 协议只允许两类无事务安全码。
func recordStoreCreateFailure(stageDir string, generation string, cause error) error {
	if stageDir == "" {
		return cause
	}
	if err := appupdatefailure.FailCode(stageDir, generation, "UPDATE_INTEGRITY_INVALID"); err != nil {
		return errors.Join(cause, fmt.Errorf("write app update pre-journal failure: %w", err))
	}
	return cause
}
