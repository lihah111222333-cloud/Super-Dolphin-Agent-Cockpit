package contract

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrSessionNotFound = errors.New("session not found")
)

var (
	// ErrUpdateTransactionAmbiguous 表示更新 journal 的意图无法与文件系统状态唯一收敛。
	ErrUpdateTransactionAmbiguous = errors.New("update transaction state is ambiguous")
	// ErrUpdateSignatureInvalid 表示更新签名验证失败，必须保留现场并阻断继续执行。
	ErrUpdateSignatureInvalid = errors.New("update signature is invalid")
	// ErrUpdateIntegrityInvalid 表示发布物或 helper 摘要与已绑定身份不一致。
	ErrUpdateIntegrityInvalid = errors.New("update integrity is invalid")
)

// ErrSkillMissingCWD 是 skill 模块 cwd 缺失错误的跨层哨兵。
// platform/toolbridge 等低层消费者用 errors.Is 匹配它，不需要导入 skill 模块。
var ErrSkillMissingCWD = errors.New("cwd is required")

// ErrSkillSameNameConflict 表示多个 canonical skill 同名且没有显式策略选择单一来源。
var ErrSkillSameNameConflict = errors.New("skill same-name conflict")

// ErrSkillRefIdentityMismatch 表示结构化 skill 引用无法精确匹配 canonical inventory。
// 调用方不得将该错误降级为 name-only 查找，否则可能切换到不同 scope 或 owner。
var ErrSkillRefIdentityMismatch = errors.New("skill ref identity mismatch")

// SkillApprovalRequiredError 表示 skill artifact 执行前需要用户审批。
// Request 保留审批载荷，调用方据此展示审批 UI 并阻断执行。
type SkillApprovalRequiredError struct {
	Request ApprovalRequest
}

var errSkillApprovalRequired = errors.New("skill artifact approval required")

// Error 返回稳定的 skill 审批错误文本。
func (e SkillApprovalRequiredError) Error() string {
	return errSkillApprovalRequired.Error()
}

// Unwrap 暴露审批哨兵，允许 errors.Is 识别该错误类别。
func (e SkillApprovalRequiredError) Unwrap() error { return errSkillApprovalRequired }

// 存储层哨兵错误由 contract 暴露给 module 层，避免 module 直接依赖 platform/db。
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
)

// IsNotFound 判断错误链是否匹配存储层 not-found 哨兵。
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

// ErrThreadRuntimeRequired 表示 toolbridge 调用无法解析 thread runtime。
// 需要 thread 身份才能应用 per-thread 策略的工具必须返回该哨兵而不是静默降级。
var ErrThreadRuntimeRequired = errors.New("toolbridge: thread runtime is required")

// ErrPersistentSubagentRuntimeRequired 表示 thread 存在但缺少 runtime config。
// 这种情况下 persistent_subagent_default 无法求值，调用方必须阻断策略判断。
var ErrPersistentSubagentRuntimeRequired = errors.New("toolbridge: persistent subagent runtime is required")

// ErrPersistentSubagentFlagRequired 表示 runtime config 未显式携带 persistent-subagent 标志。
// spawn_agent 策略检查用它区分“缺少配置”与“显式关闭”。
var ErrPersistentSubagentFlagRequired = errors.New("toolbridge: persistent subagent flag is required")

// CacheKeepaliveBinding 是 keepalive 判断 agent 是否仍可 ping 的最小绑定快照。
type CacheKeepaliveBinding struct {
	AgentID  string
	Archived bool
}

// CacheKeepaliveThreadRef 是启动事件缺少 agentID 时用于回查的最小线程引用。
type CacheKeepaliveThreadRef struct {
	ThreadID string
	AgentID  string
}

// CacheKeepaliveBindingLookup 隔离 cache keepalive 对 binding store 的只读需求。
type CacheKeepaliveBindingLookup interface {
	GetCacheKeepaliveBindingByAgentID(ctx context.Context, agentID string) (*CacheKeepaliveBinding, error)
}

// CacheKeepaliveThreadLookup 隔离 cache keepalive 对 thread store 的只读需求。
type CacheKeepaliveThreadLookup interface {
	GetCacheKeepaliveThreadByID(ctx context.Context, threadID string) (*CacheKeepaliveThreadRef, error)
}

// RecoveryAction 是失败后允许展示给用户的显式安全动作。
type RecoveryAction string

const (
	// RecoveryActionWaitThenRetry 要求等待当前操作收敛后再显式重试。
	RecoveryActionWaitThenRetry RecoveryAction = "wait_then_retry"
	// RecoveryActionRestartApplication 要求用户显式退出并重新启动应用。
	RecoveryActionRestartApplication RecoveryAction = "restart_application"
	// RecoveryActionPreserveStateExportDiagnostics 要求保留状态并显式导出诊断。
	RecoveryActionPreserveStateExportDiagnostics RecoveryAction = "preserve_state_export_diagnostics"
)

// RecoveryFailure 是跨后端与恢复 UI 的最小安全失败元数据。
// 字段必须保持精确，禁止加入原始错误、路径、凭据或 helper 输出。
type RecoveryFailure struct {
	Code          string         `json:"code"`
	Retryable     bool           `json:"retryable"`
	Action        RecoveryAction `json:"action"`
	TransactionID string         `json:"transaction_id"`
}

// RecoveryFailureCarrier 由安全错误实现，用结构化元数据跨越错误边界，禁止调用方解析错误字符串。
type RecoveryFailureCarrier interface {
	RecoveryFailure() RecoveryFailure
}

type recoveryFailureSpec struct {
	retryable bool
	action    RecoveryAction
}

var recoveryFailureRegistry = map[string]recoveryFailureSpec{
	"UPDATE_TRANSACTION_AMBIGUOUS":  {action: RecoveryActionPreserveStateExportDiagnostics},
	"UPDATE_SIGNATURE_INVALID":      {action: RecoveryActionPreserveStateExportDiagnostics},
	"UPDATE_INTEGRITY_INVALID":      {action: RecoveryActionPreserveStateExportDiagnostics},
	"MCP_SCHEMA_CAPACITY_EXHAUSTED": {retryable: true, action: RecoveryActionWaitThenRetry},
	"MCP_SCHEMA_REAP_FAILED":        {action: RecoveryActionRestartApplication},
	"MCP_SCHEMA_DIGEST_MISMATCH":    {action: RecoveryActionPreserveStateExportDiagnostics},
	"MCP_SCHEMA_PROTOCOL_VIOLATION": {action: RecoveryActionPreserveStateExportDiagnostics},
}

// RecoveryFailureForCode 从唯一稳定 registry 构造四字段恢复元数据。
func RecoveryFailureForCode(code, transactionID string) (RecoveryFailure, bool) {
	spec, ok := recoveryFailureRegistry[code]
	if !ok {
		return RecoveryFailure{}, false
	}
	return RecoveryFailure{Code: code, Retryable: spec.retryable, Action: spec.action, TransactionID: transactionID}, true
}

// ValidateRecoveryFailure 拒绝 code、retryable 与 action 之间的冲突语义。
func ValidateRecoveryFailure(failure RecoveryFailure) error {
	if failure == (RecoveryFailure{}) {
		return nil
	}
	want, ok := RecoveryFailureForCode(failure.Code, failure.TransactionID)
	if !ok {
		return fmt.Errorf("unknown recovery failure code %q", failure.Code)
	}
	if failure != want {
		return fmt.Errorf("recovery failure semantics conflict for code %q", failure.Code)
	}
	return nil
}

// RecoveryFailureFromError 从错误链中的显式 carrier 提取并校验恢复元数据。
func RecoveryFailureFromError(err error) (RecoveryFailure, bool) {
	var carrier RecoveryFailureCarrier
	if !errors.As(err, &carrier) || carrier == nil {
		return RecoveryFailure{}, false
	}
	failure := carrier.RecoveryFailure()
	if failure == (RecoveryFailure{}) || ValidateRecoveryFailure(failure) != nil {
		return RecoveryFailure{}, false
	}
	return failure, true
}
