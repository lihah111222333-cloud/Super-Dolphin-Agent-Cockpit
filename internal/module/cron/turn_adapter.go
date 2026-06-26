package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// TurnServiceAdapter 基于 contract.CronTurnExecutor 实现 TurnSubmitter。
// 它只桥接 cron 的提交/查重/观察接口到 turn 层，run/job 持久化、重试和 observe_lost 仍由 Scheduler 统一处理。
// StartTurn 负责 ResolveSession -> CronPrepareTurn -> CronStartTurn；Lookup 只查本进程 tracker；Observe 只接管已提交 turn。
type TurnServiceAdapter struct {
	svc      contract.CronTurnExecutor
	resolver contract.SessionResolver
	logger   *slog.Logger
	// bootstrapper 只在 job.thread_id 为空的首次触发路径使用；nil 时返回显式错误，避免落到默认线程。
	bootstrapper ThreadBootstrapper
}

var _ TurnSubmitter = (*TurnServiceAdapter)(nil)

// NewTurnServiceAdapter 创建 cron 到 turn service 的适配器。
// svc 和 resolver 是跨模块提交链路的必填依赖，调用方应在构造前完成未接线处理。
func NewTurnServiceAdapter(logger *slog.Logger, svc contract.CronTurnExecutor, resolver contract.SessionResolver) *TurnServiceAdapter {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &TurnServiceAdapter{svc: svc, resolver: resolver, logger: logger}
}

// WithBootstrapper 设置 turn 适配器使用的线程引导器。
// 该方法由 fx factory 补接，保持构造函数签名稳定；测试可直接注入。
func (a *TurnServiceAdapter) WithBootstrapper(b ThreadBootstrapper) *TurnServiceAdapter {
	if a == nil {
		return nil
	}
	a.bootstrapper = b
	return a
}

// ErrJobNotBootstrapped 表示 job 尚未绑定 thread 且没有可用 bootstrapper。
// scheduler 会把本次 run 标记失败并按重试预算处理，而不是创建隐式默认线程。
var ErrJobNotBootstrapped = errors.New("cron: job thread_id is empty (agent/thread bootstrap not yet supported)")

// StartTurn 只负责准备并启动 turn，返回本地 turn_id。
// 它不写 cron_runs，也不处理终态事件。
func (a *TurnServiceAdapter) StartTurn(ctx context.Context, req StartTurnRequest) (StartTurnResult, error) {
	if a == nil || a.svc == nil || a.resolver == nil {
		return StartTurnResult{}, errors.New("cron: turn adapter not wired")
	}
	session, agentID, err := a.resolveThreadAgent(ctx, req)
	if err != nil {
		return StartTurnResult{}, err
	}
	turnID, err := a.executeTurn(ctx, session, req)
	if err != nil {
		return StartTurnResult{}, err
	}
	return StartTurnResult{
		TurnID:   turnID,
		ThreadID: strings.TrimSpace(session.ThreadID()),
		AgentID:  agentID,
	}, nil
}

// resolveThreadAgent 解析 ThreadID/AgentID，首次触发时调用 bootstrapper 创建线程。
func (a *TurnServiceAdapter) resolveThreadAgent(ctx context.Context, req StartTurnRequest) (contract.Session, string, error) {
	threadID := strings.TrimSpace(req.ThreadID)
	agentID := strings.TrimSpace(req.AgentID)
	if threadID == "" {
		// 首次触发只让 bootstrapper 生成线程/agent；返回 ID 的持久化仍等 StartTurn 成功后由 scheduler 完成。
		bootstrapped, err := a.bootstrapFirstRun(ctx, req)
		if err != nil {
			return nil, "", err
		}
		threadID = strings.TrimSpace(bootstrapped.ThreadID)
		if bootstrappedAgentID := strings.TrimSpace(bootstrapped.AgentID); bootstrappedAgentID != "" {
			agentID = bootstrappedAgentID
		}
	}
	session, err := a.resolver.ResolveSession(ctx, threadID)
	if err != nil {
		return nil, "", fmt.Errorf("cron: resolve session: %w", err)
	}
	// agent_id 可来自 job 或 bootstrap；真正落库的 thread_id 以
	// session.ThreadID() 为准。
	return session, agentID, nil
}

// executeTurn 调用 CronPrepareTurn 和 CronStartTurn，返回本地 turn_id。
func (a *TurnServiceAdapter) executeTurn(ctx context.Context, session contract.Session, req StartTurnRequest) (string, error) {
	input, err := a.buildPrepareInput(req)
	if err != nil {
		return "", err
	}
	prepared, err := a.svc.CronPrepareTurn(ctx, session, input)
	if err != nil {
		return "", fmt.Errorf("cron: prepare turn: %w", err)
	}
	handle, err := a.svc.CronStartTurn(ctx, session, prepared)
	if err != nil {
		return "", fmt.Errorf("cron: start turn: %w", err)
	}
	turnID := strings.TrimSpace(handle.LocalID())
	if turnID == "" {
		// 这里必须返回错误，不能造临时 id；否则恢复时无法重新 Observe。
		return "", errors.New("cron: CronStartTurn returned empty local id")
	}
	return turnID, nil
}

// bootstrapFirstRun 只用于首次触发且 job.thread_id 为空。
// 返回的 ThreadID 必须非空；否则无法 ResolveSession，也不能继续 CronPrepareTurn/CronStartTurn。
// 返回的 ID 只有在 StartTurn 成功后才会被保存。
func (a *TurnServiceAdapter) bootstrapFirstRun(ctx context.Context, req StartTurnRequest) (BootstrapResult, error) {
	if a == nil || a.bootstrapper == nil {
		return BootstrapResult{}, ErrJobNotBootstrapped
	}
	res, err := a.bootstrapper.BootstrapThread(ctx, BootstrapRequest{
		JobID:    strings.TrimSpace(req.JobID),
		Provider: strings.TrimSpace(req.Provider),
		Model:    strings.TrimSpace(req.Model),
		CWD:      strings.TrimSpace(req.CWD),
		Name:     strings.TrimSpace(req.JobID),
		Config:   req.Config,
	})
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("cron: bootstrap thread: %w", err)
	}
	if strings.TrimSpace(res.ThreadID) == "" {
		return BootstrapResult{}, errors.New("cron: bootstrap returned empty thread id")
	}
	return res, nil
}

// LookupByDedupeKey 只给恢复查重用，不能启动 provider。
// 空 dedupeKey 直接返回 Found=false；本进程 tracker 查不到时，是否失败由 Scheduler 决定。
func (a *TurnServiceAdapter) LookupByDedupeKey(ctx context.Context, dedupeKey string) (ObservedTurn, error) {
	if a == nil || a.svc == nil {
		return ObservedTurn{Found: false}, errors.New("cron: turn adapter not wired")
	}
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return ObservedTurn{Found: false}, nil
	}
	status, found, err := a.svc.CronLookupByDedupeKey(ctx, key)
	if err != nil {
		return ObservedTurn{Found: false}, fmt.Errorf("cron: lookup by dedupe key: %w", err)
	}
	if !found {
		return ObservedTurn{Found: false}, nil
	}
	// 优先使用本地 tracker id，与 StartTurn 返回并落库的 turn_id 保持一致。
	turnID := strings.TrimSpace(status.LocalID)
	if turnID == "" {
		turnID = strings.TrimSpace(status.ProviderID)
	}
	return ObservedTurn{Found: true, TurnID: turnID}, nil
}

// Observe 只接管已提交的 turn。
// 找不到 turn 时映射为 ErrTurnNotFound；其他错误保留上下文交给 Scheduler 记录和分类。
func (a *TurnServiceAdapter) Observe(ctx context.Context, turnID string) error {
	if a == nil || a.svc == nil {
		return errors.New("cron: turn adapter not wired")
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return errors.New("cron: Observe requires a turn id")
	}
	if _, err := a.svc.CronTrackTurn(ctx, turnID); err != nil {
		// CronTrackTurn 当前把 not found 包成普通 error，先用文本匹配维持 observe_lost 分类。
		if strings.Contains(strings.ToLower(err.Error()), "turn not found") {
			return ErrTurnNotFound
		}
		return fmt.Errorf("cron: track turn: %w", err)
	}
	return nil
}

// buildPrepareInput 将 cron 侧 StartTurnRequest 投影成 contract.CronPrepareInput。
// cron row 没有的 Files/Images/CandidateSkills/MCPSnapshot 等字段保持零值，由 turn 准备层决定如何解释。
// 这里只做字段投影，不添加 cron 专属默认值。坏配置应该在 Create/Update 时被挡住。
func (a *TurnServiceAdapter) buildPrepareInput(req StartTurnRequest) (contract.CronPrepareInput, error) {
	skills := make([]providerdto.SkillRef, 0, len(req.Skills))
	for _, s := range req.Skills {
		if name := strings.TrimSpace(s); name != "" {
			skills = append(skills, providerdto.SkillRef{Name: name})
		}
	}
	runtimeCfg, err := decodeRuntimeConfig(req.Config)
	if err != nil {
		return contract.CronPrepareInput{}, err
	}
	return contract.CronPrepareInput{
		Prompt:              req.Prompt,
		Skills:              skills,
		Provider:            strings.TrimSpace(req.Provider),
		Model:               strings.TrimSpace(req.Model),
		AgentID:             strings.TrimSpace(req.AgentID),
		CWD:                 strings.TrimSpace(req.CWD),
		ThreadRuntimeConfig: runtimeCfg,
		DedupeKey:           strings.TrimSpace(req.DedupeKey),
	}, nil
}

// decodeRuntimeConfig 将已入库的 runtime config 投影为 turn 层需要的 map。
// 历史坏 JSON 必须返回错误，避免 turn 层误当作“无覆盖配置”继续运行。
func decodeRuntimeConfig(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("cron: runtime config: %w", err)
	}
	return out, nil
}
