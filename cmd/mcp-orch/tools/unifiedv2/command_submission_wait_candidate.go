package unifiedv2

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	toolspkg "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
)

var (
	// ErrCommandSubmissionWaitDisabled 表示 command_submission wait 缺少完整权威投影。
	ErrCommandSubmissionWaitDisabled = errors.New("unified_v2 command_submission wait is disabled")
	// ErrCommandSubmissionWaitUnavailable 表示候选查询无法证明身份或 resolution。
	ErrCommandSubmissionWaitUnavailable = errors.New("unified_v2 command_submission wait is unavailable")
)

// CommandSubmissionWaitAdapter 是未注册的 command_submission wait/reconcile 候选。
// 当前 durable submission owner 已具备 caller/source/target fence、transaction-bound
// read seam 和 accepted/rejected command-fence projection，但尚未接入 wait verifier、
// authoritative recovery owner 或 registry；该候选只保留禁用 seam，绝不把
// submission metadata 当作成功。
type CommandSubmissionWaitAdapter struct{}

// commandSubmissionWaitReadTx 是未来实现必须满足的同事务只读 seam。
// submission 与 intent 必须在同一 DBTX 中重读，才能逐字段回验 command、version、
// namespace/key/request digest；任何 split-read 或仅凭 caller metadata 的实现均不合格。
type commandSubmissionWaitReadTx interface {
	GetSubmissionTx(context.Context, storedb.DBTX, orchestrationcommand.DurableSubmissionKey) (orchestrationcommand.DurableSubmission, error)
	GetIntentTx(context.Context, storedb.DBTX, string) (orchestrationcommand.Record, error)
}

// NewCommandSubmissionWaitAdapter 在完整的 read-only transaction projection
// 出现前保持禁用，不接受任意 owner 或 provider 作为替代实现。
func NewCommandSubmissionWaitAdapter(...any) (*CommandSubmissionWaitAdapter, error) {
	return nil, ErrCommandSubmissionWaitDisabled
}

// ToolHandler 不向 registry 暴露 command_submission wait；直接返回稳定禁用错误。
func (a *CommandSubmissionWaitAdapter) ToolHandler() toolspkg.ToolHandler {
	return func(context.Context, json.RawMessage) (any, error) {
		return nil, ErrCommandSubmissionWaitDisabled
	}
}

// Handle 保留未来候选调用形状，但在缺字段时 fail-closed，不读取或改变任何 owner。
func (a *CommandSubmissionWaitAdapter) Handle(context.Context, json.RawMessage) (any, error) {
	return nil, ErrCommandSubmissionWaitDisabled
}
