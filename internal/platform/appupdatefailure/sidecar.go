package appupdatefailure

import (
	"errors"
	"path/filepath"
	"regexp"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	Filename     = "pre-journal-failure.json"
	LockFilename = ".pre-journal-failure.lock"
	Version      = 2
)

var (
	ErrUnsupported    = errors.New("app update pre-journal sidecar is unsupported on this platform")
	generationPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

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
	if stageDir == "" || stageDir == "/" || !filepath.IsAbs(stageDir) || filepath.Clean(stageDir) != stageDir {
		return "", errors.New("app update stage dir must be a clean absolute non-root path")
	}
	return filepath.Join(stageDir, Filename), nil
}

func validateGeneration(generation string) error {
	if !generationPattern.MatchString(generation) {
		return errors.New("app update pre-journal generation is invalid")
	}
	return nil
}

// ValidateGeneration 校验跨进程传递的代际标识格式，不访问 sidecar。
func ValidateGeneration(generation string) error {
	return validateGeneration(generation)
}

// FailCode 从唯一恢复 registry 构造安全字段后执行 matching-generation 失败迁移。
func FailCode(stageDir string, generation string, code string) error {
	failure, ok := contract.RecoveryFailureForCode(code, "")
	if !ok {
		return errors.New("app update pre-journal recovery code is not registered")
	}
	return Fail(stageDir, generation, failure)
}

// validateFailure 仅接受 registry 中两类无事务恢复信号。
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
