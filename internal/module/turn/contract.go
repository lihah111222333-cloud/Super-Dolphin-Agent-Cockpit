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
	// LookupByDedupeKey returns the tracked status of a non-terminal turn
	// that previously registered the given dedupeKey via
	// PrepareTurn/StartTurn. ok=false means "never submitted (in this
	// process)" — callers such as cron crash-recovery must treat that as
	// an absent submission per the P21 P1b plan. The tracker is
	// in-memory, so a process restart erases registrations; a follow-up
	// PR will persist dedupe_key to SQL for cross-process recovery.
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
	// DedupeKey is an optional per-submission idempotency token. Cron
	// sets it to sha256(job_id||scheduled_at||idempotency_key) so a
	// crash between "StartTurn returned" and "run.status advanced to
	// submitted" can still resolve the turn via
	// Service.LookupByDedupeKey instead of double-submitting. Empty
	// means "no dedupe tracking" (the default; non-cron callers).
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

// ---------------------------------------------------------------------------
// CronExecutorAdapter (was cron_adapter.go)
// ---------------------------------------------------------------------------

// CronExecutorAdapter wraps the full turn.Service into the narrow
// contract.CronTurnExecutor interface consumed by the cron module.
type CronExecutorAdapter struct {
	svc Service
}

// NewCronExecutorAdapter creates an adapter. svc must not be nil.
// NewCronExecutorAdapter 创建cronexecutor适配器。
func NewCronExecutorAdapter(svc Service) *CronExecutorAdapter {
	return &CronExecutorAdapter{svc: svc}
}

var _ contract.CronTurnExecutor = (*CronExecutorAdapter)(nil)

// CronPrepareTurn 处理cronprepareturn。
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

// CronStartTurn 处理cron起点turn。
func (a *CronExecutorAdapter) CronStartTurn(ctx context.Context, session contract.Session, req dto.TurnRequest) (contract.TurnHandle, error) {
	return a.svc.StartTurn(ctx, session, req)
}

// CronTrackTurn 处理crontrackturn。
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

// CronLookupByDedupeKey 按去重键处理cronlookup。
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
