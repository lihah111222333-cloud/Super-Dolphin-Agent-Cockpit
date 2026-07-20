package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

// clearPreparedPreJournalFailure 在 Store.Create 前清理 sidecar，失败时同时清理 candidate。
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

// clearPreJournalFailure 在签名成功后和 journal 创建边界前确认 sidecar 缺席。
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
