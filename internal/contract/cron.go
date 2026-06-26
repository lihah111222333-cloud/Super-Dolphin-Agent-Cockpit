package contract

import (
	"context"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// CronThreadStarter 是 cron 首次触发任务时启动 provider thread 的窄边界。
// cron 只依赖该接口，不直接依赖完整 thread.Service；生产适配器负责从 thread.Service 拷贝所需字段。
type CronThreadStarter interface {
	CronStartThread(ctx context.Context, req CronStartThreadRequest) (CronStartThreadResult, error)
}

// CronStartThreadRequest 是 cron 首次启动 thread 时使用的最小 wire 入参。
// 字段名保持与 thread start 输入一致，便于适配器做无业务逻辑的字段拷贝。
type CronStartThreadRequest struct {
	Provider string
	CWD      string
	Model    string
	Name     string
	Config   map[string]any
}

// CronStartThreadResult 返回 cron 需要持久化回 job 行的 thread 和 agent 标识。
type CronStartThreadResult struct {
	ThreadID string
	AgentID  string
}

// CronTurnExecutor 是 cron 准备、提交、跟踪和去重 turn 的窄边界。
// turn 模块实现具体生命周期，cron 只通过这里观察 local/provider 状态和 dedupe 结果。
type CronTurnExecutor interface {
	CronPrepareTurn(ctx context.Context, session Session, input CronPrepareInput) (dto.TurnRequest, error)
	CronStartTurn(ctx context.Context, session Session, req dto.TurnRequest) (TurnHandle, error)
	CronTrackTurn(ctx context.Context, localID string) (CronTurnStatus, error)
	CronLookupByDedupeKey(ctx context.Context, dedupeKey string) (CronTurnStatus, bool, error)
}

// CronPrepareInput 是 cron 准备 turn 时传入 turn 模块的最小字段集。
// DedupeKey 跨 prepare/start/lookup 复用，保证重复调度不会静默创建多次 turn。
type CronPrepareInput struct {
	Prompt              string
	Skills              []dto.SkillRef
	Provider            string
	Model               string
	AgentID             string
	CWD                 string
	ThreadRuntimeConfig map[string]any
	DedupeKey           string
}

// CronTurnStatus 返回 cron 决策所需的 turn 跟踪字段。
type CronTurnStatus struct {
	LocalID    string
	ProviderID string
	State      string
}
