package turn

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

// Service 定义 turn 生命周期的核心接口：准备、提交、转向、中断、强制完成和状态追踪。
type Service interface {
	PrepareTurn(ctx context.Context, session contract.Session, input PrepareInput) (dto.TurnRequest, error)
	StartTurn(ctx context.Context, session contract.Session, req dto.TurnRequest) (contract.TurnHandle, error)
	SteerTurn(ctx context.Context, session contract.Session, expectedTurnID string, input PrepareInput) (contract.TurnHandle, error)
	InterruptTurn(ctx context.Context, session contract.Session, source string) (TurnStatus, error)
	InterruptActiveTurn(ctx context.Context, session contract.Session, source string) error
	ForceCompleteTurn(ctx context.Context, session contract.Session) error
	CleanupThread(ctx context.Context, threadID, reason string) error
	TrackTurn(ctx context.Context, localID string) (TurnStatus, error)
	// LookupByDedupeKey 返回当前进程内已登记的非终态 turn 状态。
	// ok=false 只表示本进程未见过该 dedupe key；去重登记表不跨进程持久化，重启后调用方必须按未提交处理。
	LookupByDedupeKey(ctx context.Context, dedupeKey string) (TurnStatus, bool, error)
}

// SessionProvider 按 agentID 获取会话，供 orchestration starter 使用。
type SessionProvider interface {
	GetSession(agentID string) (contract.Session, error)
}

type ThreadStateConfigReader = contract.ThreadStateConfigReader

type InputItem = shareddto.InputItem

// PrepareInput 包含一次 turn 准备所需的全部参数，包括输入内容、技能引用、MCP 快照和运行时配置。
type PrepareInput struct {
	Inputs                       []InputItem
	Prompt                       string
	Images                       []string
	Files                        []string
	Skills                       []dto.SkillRef
	CandidateSkills              []dto.SkillRef
	ManualSkillSelection         bool
	Provider                     string
	Model                        string
	Effort                       string
	OutputSchema                 json.RawMessage
	PromptKey                    string
	AgentID                      string
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	MCPSnapshot                  contract.MCPSnapshot
	SessionFlags                 map[string]bool
	Summary                      string
	OutputStyleConfig            *contract.OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *contract.FRCConfig
	RuntimeUserContext           map[string]string
	ThreadRuntimeConfig          map[string]any
	ThreadCaps                   dto.CapabilitySet
	BinaryDir                    string
	// DedupeKey 是可选的单次提交幂等键。
	// cron 用它在 StartTurn 已返回但任务状态尚未写成 submitted 的窗口内反查本进程 turn，空值表示不登记。
	DedupeKey string
}

// TurnStatus 表示一次 turn 的当前状态快照，包含本地 ID、provider ID 和状态字符串。
type TurnStatus struct {
	LocalID    string `json:"localId"`
	ProviderID string `json:"providerId"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	interrupt  turnInterruptEnvelope
}

// CronExecutorAdapter 将完整 turn.Service 收窄为 cron 模块需要的执行接口。
// 它是跨模块边界，避免 cron 直接依赖 turn 的全部交互能力。
type CronExecutorAdapter struct {
	svc Service
}

// NewCronExecutorAdapter 构造 cron turn adapter。
// svc 必须非 nil；装配错误应在调用方暴露，而不是在 adapter 内静默吞掉。
func NewCronExecutorAdapter(svc Service) *CronExecutorAdapter {
	return &CronExecutorAdapter{svc: svc}
}

var _ contract.CronTurnExecutor = (*CronExecutorAdapter)(nil)

// CronPrepareTurn 将 cron 的窄输入转换为 PrepareInput。
// 这里只透传 cron 需要的字段，避免把 UI-only 状态带入定时任务 turn。
func (a *CronExecutorAdapter) CronPrepareTurn(ctx context.Context, session contract.Session, input contract.CronPrepareInput) (dto.TurnRequest, error) {
	return a.svc.PrepareTurn(ctx, session, PrepareInput{
		Prompt:              input.Prompt,
		Skills:              input.Skills,
		Provider:            input.Provider,
		Model:               input.Model,
		AgentID:             input.AgentID,
		CWD:                 input.CWD,
		ThreadRuntimeConfig: input.ThreadRuntimeConfig,
		DedupeKey:           input.DedupeKey,
	})
}

// CronStartTurn 透传已准备好的 provider turn 请求。
// 该阶段不再改写请求内容，失败由 turn.Service 保持原始错误边界。
func (a *CronExecutorAdapter) CronStartTurn(ctx context.Context, session contract.Session, req dto.TurnRequest) (contract.TurnHandle, error) {
	return a.svc.StartTurn(ctx, session, req)
}

// CronTrackTurn 将 turn.Service 的状态投影成 cron 可持久化的窄 DTO。
func (a *CronExecutorAdapter) CronTrackTurn(ctx context.Context, localID string) (contract.CronTurnStatus, error) {
	st, err := a.svc.TrackTurn(ctx, localID)
	if err != nil {
		return contract.CronTurnStatus{}, err
	}
	return contract.CronTurnStatus{
		LocalID:    st.LocalID,
		ProviderID: st.ProviderID,
		State:      st.State,
	}, nil
}

// CronLookupByDedupeKey 按幂等键查询当前进程内 turn 状态。
// 只返回 cron 关心的状态字段，found=false 时调用方可继续执行恢复分支。
func (a *CronExecutorAdapter) CronLookupByDedupeKey(ctx context.Context, dedupeKey string) (contract.CronTurnStatus, bool, error) {
	st, found, err := a.svc.LookupByDedupeKey(ctx, dedupeKey)
	if err != nil {
		return contract.CronTurnStatus{}, false, err
	}
	return contract.CronTurnStatus{
		LocalID:    st.LocalID,
		ProviderID: st.ProviderID,
		State:      st.State,
	}, found, nil
}
