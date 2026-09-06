// Package wakeupcommandcandidate 提供 B08 的 transport-neutral 唤醒命令候选。
// 它只验证本地合同，不接入生产 dispatcher、provider、OUT 或 RecoveryCustody。
package wakeupcommandcandidate

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

var (
	ErrInvalidCommand    = errors.New("wakeup command is invalid")
	ErrNotFound          = errors.New("wakeup command not found")
	ErrDigestConflict    = errors.New("wakeup command digest conflict")
	ErrReplayConflict    = errors.New("wakeup command replay is not exact")
	ErrCommandConflict   = errors.New("wakeup command id conflict")
	ErrStaleFence        = errors.New("wakeup fence is stale")
	ErrAttemptUnresolved = errors.New("wakeup attempt outcome is unresolved")
	ErrAttemptTerminal   = errors.New("wakeup attempt is terminal")
	ErrPermitMismatch    = errors.New("wakeup dispatch permit mismatch")
	ErrOutcomeConflict   = errors.New("wakeup provider outcome conflict")
	ErrInvalidOutcome    = errors.New("wakeup provider outcome is invalid")
)

// ProviderOutcome 是 provider 对同一 attempt 的唯一结果分类。
type ProviderOutcome string

const (
	OutcomePending           ProviderOutcome = "pending"
	OutcomeAccepted          ProviderOutcome = "accepted"
	OutcomeUnknown           ProviderOutcome = "unknown"
	OutcomeDefinitiveFailure ProviderOutcome = "definitive_failure"
)

// AttemptPhase 描述 record-before-effect 与 mark-after-effect 的本地窗口。
type AttemptPhase string

const (
	PhaseEffectPending    AttemptPhase = "effect_pending"
	PhaseOutcomeUnknown   AttemptPhase = "outcome_unknown"
	PhaseProviderAccepted AttemptPhase = "provider_accepted"
	PhaseProviderFailed   AttemptPhase = "provider_definitive_failure"
)

// Intent 是同一命令的稳定 key/digest 对；digest 不代表签名或授权。
type Intent struct {
	Key    string `json:"key"`
	Digest string `json:"digest"`
}

// CommandIntent 是 Intent 的语义别名，便于调用方明确这是 command owner 的意图。
type CommandIntent = Intent

// WakeupCommand 把 wakeup、fence、意图和首个 attempt 绑定成不可变记录。
type WakeupCommand struct {
	CommandID     string `json:"command_id"`
	WakeupID      string `json:"wakeup_id"`
	WakeupFence   string `json:"wakeup_fence"`
	Intent        Intent `json:"intent"`
	AttemptNumber int    `json:"attempt_number"`
}

// Attempt 保存 provider 副作用前后必须复用的 receipt、permit、nonce 和结果。
type Attempt struct {
	CommandID       string          `json:"command_id"`
	WakeupID        string          `json:"wakeup_id"`
	WakeupFence     string          `json:"wakeup_fence"`
	IntentKey       string          `json:"intent_key"`
	IntentDigest    string          `json:"intent_digest"`
	AttemptNumber   int             `json:"attempt_number"`
	DispatchReceipt string          `json:"dispatch_receipt"`
	Permit          string          `json:"permit"`
	Nonce           string          `json:"nonce"`
	Phase           AttemptPhase    `json:"phase"`
	Outcome         ProviderOutcome `json:"outcome"`
}

// DispatchPermit 是 record-before-effect 后交给 provider 的不可变调用材料。
type DispatchPermit struct {
	CommandID       string
	WakeupID        string
	WakeupFence     string
	AttemptNumber   int
	DispatchReceipt string
	Permit          string
	Nonce           string
}

// OutcomeRequest 以完整 fence/permit/nonce 标识 mark-after-effect 或 reconcile。
type OutcomeRequest struct {
	CommandID       string
	WakeupID        string
	WakeupFence     string
	AttemptNumber   int
	DispatchReceipt string
	Permit          string
	Nonce           string
	Outcome         ProviderOutcome
}

// Owner 是单一 canonical WakeupCommand/Attempt owner；状态只在内存中保存。
type Owner struct {
	mu       sync.Mutex
	byKey    map[string]WakeupCommand
	byID     map[string]WakeupCommand
	attempts map[string]Attempt
}

// NewOwner 创建隔离候选 owner；它不打开数据库，也不启动后台任务。
func NewOwner() *Owner {
	return &Owner{
		byKey:    make(map[string]WakeupCommand),
		byID:     make(map[string]WakeupCommand),
		attempts: make(map[string]Attempt),
	}
}

// validate 检查 intent key 和摘要的规范形状，不代替上游授权验证。
func (i Intent) validate() error {
	if err := boundedText("intent key", i.Key, 256); err != nil {
		return err
	}
	if len(i.Digest) != 64 || strings.ToLower(i.Digest) != i.Digest {
		return fmt.Errorf("%w: intent digest must be lowercase sha-256 hex", ErrInvalidCommand)
	}
	if _, err := hex.DecodeString(i.Digest); err != nil {
		return fmt.Errorf("%w: intent digest is not hexadecimal", ErrInvalidCommand)
	}
	return nil
}

// validate 检查命令身份、wakeup fence 和候选首 attempt 的完整性。
func (c WakeupCommand) validate() error {
	for name, value := range map[string]string{"command_id": c.CommandID, "wakeup_id": c.WakeupID, "wakeup_fence": c.WakeupFence} {
		if err := boundedText(name, value, 256); err != nil {
			return err
		}
	}
	if err := c.Intent.validate(); err != nil {
		return err
	}
	if c.AttemptNumber != 1 {
		return fmt.Errorf("%w: first candidate attempt must be number 1", ErrInvalidCommand)
	}
	return nil
}

// boundedText 拒绝空白别名、非法编码、NUL 和超限文本，保持绑定值不可变。
func boundedText(name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrInvalidCommand, name)
	}
	return nil
}
