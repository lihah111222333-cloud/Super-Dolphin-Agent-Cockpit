// Package reconcilecoordinator 提供 RecoveryCustody service-owned reconcile 的本地候选。
//
// 该包只负责 durable job/journal、owner fencing、启动扫描和有界 runner；它不接入
// mcp-orch public API/Fx，也不把 pending/indeterminate 推进为 accepted/dispatch。
package reconcilecoordinator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

// SchemaVersion 标识 coordinator 自有数据库 schema 版本。
const SchemaVersion int64 = 1

var (
	// ErrInvalidConfig 表示配置缺失或超出有界约束。
	ErrInvalidConfig = errors.New("recovery custody reconcile coordinator configuration is invalid")
	// ErrSchemaMismatch 表示数据库缺失、漂移或结构损坏。
	ErrSchemaMismatch = errors.New("recovery custody reconcile coordinator schema does not match")
	// ErrOwnerFenced 表示旧 owner epoch/token 已被撤权。
	ErrOwnerFenced = errors.New("recovery custody reconcile coordinator owner is fenced")
	// ErrJobConflict 表示 operation 已绑定不同句柄。
	ErrJobConflict = errors.New("recovery custody reconcile coordinator job conflicts with existing operation")
	// ErrJobNotFound 表示已 claim 的 durable job 缺失或损坏。
	ErrJobNotFound = errors.New("recovery custody reconcile coordinator job was not found")
	// ErrCommitOutcomeUnknown 表示 SQLite 提交结果不确定，禁止猜测。
	ErrCommitOutcomeUnknown = errors.New("recovery custody reconcile coordinator commit outcome is unknown")
)

// Authority 是 coordinator 所需的最小 authority 端口。sqliteauthority.Authority
// 通过 ListActiveResolutionHandles/ReconcileResolution 实现该接口。
type Authority interface {
	ListActiveResolutionHandles(context.Context) ([]recoverycustody.SubmissionHandle, error)
	ReconcileResolution(context.Context, recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error)
}

// Config 要求所有运行参数显式提供，不使用隐式数据库、owner 或重试默认值。
type Config struct {
	Path         string
	OwnerID      string
	OwnerEpoch   int64
	OwnerToken   string
	ClaimTTL     time.Duration
	RetryBase    time.Duration
	RetryMax     time.Duration
	MaxAttempts  int
	RunnerPeriod time.Duration
	Now          func() time.Time
}

// Validate 拒绝隐式路径、owner 身份和无界 runner 参数。
func (c Config) Validate() error {
	if err := validateConfigPath(c.Path); err != nil {
		return err
	}
	if err := validateConfigOwner(c.OwnerID, c.OwnerEpoch, c.OwnerToken); err != nil {
		return err
	}
	return validateConfigBounds(c.ClaimTTL, c.RetryBase, c.RetryMax, c.MaxAttempts, c.RunnerPeriod)
}

func validateConfigPath(path string) error {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%w: absolute path is required", ErrInvalidConfig)
	}
	return nil
}

func validateConfigOwner(id string, epoch int64, token string) error {
	if strings.TrimSpace(id) == "" || len(id) > 256 || strings.TrimSpace(id) != id {
		return fmt.Errorf("%w: owner id", ErrInvalidConfig)
	}
	if epoch <= 0 || len(token) != 64 || strings.TrimSpace(token) != token || !isLowerHex(token) {
		return fmt.Errorf("%w: owner fence", ErrInvalidConfig)
	}
	return nil
}

func validateConfigBounds(claimTTL, retryBase, retryMax time.Duration, maxAttempts int, runnerPeriod time.Duration) error {
	if claimTTL <= 0 || retryBase <= 0 || retryMax < retryBase || maxAttempts <= 0 || runnerPeriod <= 0 {
		return fmt.Errorf("%w: bounded lease/retry parameters", ErrInvalidConfig)
	}
	return nil
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// JobState 是 coordinator 自己的执行状态，不是 authority terminal outcome。
type JobState string

const (
	// StateQueued 表示 durable job 等待首次 claim。
	StateQueued JobState = "queued"
	// StateRunning 表示 job 已绑定单一 owner claim。
	StateRunning JobState = "running"
	// StateRetryWait 表示等待有界重试。
	StateRetryWait JobState = "retry_wait"
	// StateSucceeded 表示 authority 已验证 terminal outcome。
	StateSucceeded JobState = "succeeded"
	// StateIndeterminate 表示 fail-closed 耗尽或证据异常。
	StateIndeterminate JobState = "indeterminate"
)

// Job 是 durable reconcile job 的只读投影。
type Job struct {
	JobID          string
	OperationID    string
	Handle         recoverycustody.SubmissionHandle
	State          JobState
	Attempts       int
	NextRunAt      time.Time
	OwnerID        string
	OwnerEpoch     int64
	ClaimToken     string
	LeaseExpiresAt time.Time
	LastStatus     string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AuditRecord 是恢复动作的 append-only 审计投影。
type AuditRecord struct {
	Sequence   int64
	JobID      string
	Event      string
	State      JobState
	Status     string
	Detail     string
	OwnerID    string
	OwnerEpoch int64
	CreatedAt  time.Time
}

// JournalRecord 是 reconcile 状态转移的 append-only 投影。
type JournalRecord struct {
	Sequence   int64
	JobID      string
	Event      string
	State      JobState
	ClaimToken string
	Detail     string
	CreatedAt  time.Time
}
