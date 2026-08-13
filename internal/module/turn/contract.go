package turn

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

const turnStartDiagnosticDedupeProviderIDBindFailed = "TURN_DEDUPE_PROVIDER_ID_BIND_FAILED"

// ErrDedupeNotFound 表示没有可复用的 live 去重记录。
var ErrDedupeNotFound = errors.New("turn dedupe: no live registry row")

// DedupeStore 是 turn 服务跨进程去重恢复所需的最小持久化端口。
type DedupeStore interface {
	Upsert(ctx context.Context, params DedupeUpsertParams) error
	BindProviderTurnID(ctx context.Context, params DedupeBindProviderTurnIDParams) error
	MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error
	GetLive(ctx context.Context, dedupeKey string) (DedupeEntry, error)
}

// DedupeEntry 是 registry live 行的领域投影。
type DedupeEntry struct {
	DedupeKey      string
	LocalTurnID    string
	ProviderTurnID string
	ThreadID       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	TerminalAt     time.Time
}

// DedupeUpsertParams 承载 dedupe key 与本地 turn ID 的写入参数。
type DedupeUpsertParams struct {
	DedupeKey   string
	LocalTurnID string
	ThreadID    string
	Now         time.Time
}

// DedupeBindProviderTurnIDParams 承载 provider turn ID 回写参数。
type DedupeBindProviderTurnIDParams struct {
	DedupeKey      string
	ProviderTurnID string
	Now            time.Time
}

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
	// LookupByDedupeKey 先查询当前进程 tracker，再查询可选持久 registry 中的非终态 turn 状态。
	// ok=false 表示两者均无 live 记录，或当前进程未注入持久端口且 tracker 未命中。
	LookupByDedupeKey(ctx context.Context, dedupeKey string) (TurnStatus, bool, error)
}

// SessionProvider 按 agentID 获取会话，供 orchestration starter 使用。
type SessionProvider interface {
	GetSession(agentID string) (contract.Session, error)
}

// ThreadStateConfigReader 是 turn 准备阶段读取 thread runtime 配置的契约别名。
type ThreadStateConfigReader = contract.ThreadStateConfigReader

// InputItem 复用 shared DTO 中可发送给 provider 的输入片段。
type InputItem = shareddto.InputItem

// PrepareInput 包含一次 turn 准备所需的全部参数，包括输入内容、技能引用、MCP 快照和运行时配置。
type PrepareInput struct {
	LocalTurnID                  string
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
	LocalID                string `json:"localId"`
	ProviderID             string `json:"providerId"`
	State                  string `json:"state"`
	Error                  string `json:"error,omitempty"`
	InterruptRetryable     bool   `json:"interruptRetryable,omitempty"`
	InterruptRetryableCode string `json:"interruptRetryableCode,omitempty"`
	StartDiagnosticCode    string `json:"startDiagnosticCode,omitempty"`
	interrupt              turnInterruptEnvelope
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

// CronInterruptActiveTurn 只暴露 cron 失租收口需要的 active turn 中断能力。
func (a *CronExecutorAdapter) CronInterruptActiveTurn(ctx context.Context, session contract.Session, source string) error {
	if a == nil || a.svc == nil {
		return errors.New("turn: cron interrupt adapter is not wired")
	}
	return a.svc.InterruptActiveTurn(ctx, session, source)
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
