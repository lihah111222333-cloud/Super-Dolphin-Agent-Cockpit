package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	capabilityContextCompact = "context_compact"
	capabilityRealtime       = "realtime"
)

// NewThreadHandlers 注册 thread 模块暴露给 JSON-RPC 的所有方法。
// 高频入口走 typed handler；少量低频命令仍经 SendCommand 兼容壳转发，并在 provider 不支持时返回能力错误。
func NewThreadHandlers(svc Service, capResolver contract.CapabilityResolver) platformrpc.HandlerMapResult {
	return newThreadHandlers(svc, NewSessionPorts(svc), capResolver)
}

// newThreadHandlers 组装 thread RPC handler map，并允许测试替换 session port。
// 生产入口由 NewThreadHandlers 传入 thread.Service 的 session adapter，避免 handler 直接散落完整 service 依赖。
func newThreadHandlers(svc Service, sessionPorts contract.SessionPorts, capResolver contract.CapabilityResolver) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		contract.ThreadRPCStart:   newStartHandler(svc),
		contract.ThreadRPCFork:    newForkHandler(sessionPorts),
		contract.ThreadRPCStop:    newThreadEffect(svc.Stop),
		"thread/resume":           newResumeHandler(sessionPorts),
		"thread/recover":          newRecoverHandler(svc),
		"thread/handoff":          newHandoffHandler(svc),
		contract.ThreadRPCArchive: newTracedThreadEffect(contract.ThreadRPCArchive, svc.Archive),
		"thread/unarchive":        newThreadEffect(svc.Unarchive),
		"thread/delete":           newThreadEffect(svc.Delete),

		"thread/list": newThreadListHandler(sessionPorts),
		"thread/loaded/list": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.ListByStatus(ctx, statusCreated)
		}),
		// thread/read 与 thread/resolve 保持两个 RPC 名称。
		// 前者保留 UI 历史兼容包装，后者只做运行时身份解析，避免调用方混用返回形状。
		"thread/read": newThreadReadHandler(svc),
		"thread/resolve": newThreadCall(func(ctx context.Context, id string) (any, error) {
			return svc.Get(ctx, id)
		}),
		"thread/messages": newThreadMessagesHandler(sessionPorts),

		contract.ThreadRPCNameSet: platformrpc.ThreadHandler(func(ctx context.Context, p nameSetParams) (any, error) {
			return nil, svc.SetName(ctx, contract.ThreadIDFrom(ctx), p.Name)
		}),
		"thread/config/get": newThreadConfigGetHandler(svc),
		// thread/config/set 已有 typed 参数，内部仍复用配置更新主流程并保留旧命令兼容。
		"thread/config/set": newThreadConfigSetHandler(svc),
		"thread/model/set":  newModelSetHandler(svc),
		"thread/clear":      newThreadCommandHandler(svc, "/clear"),
		// personality 仍通过 SendCommand 兼容壳触达 provider Configure。
		"thread/personality/set": newThreadCommandHandler(svc, "/personality"),
		"thread/approvals/set":   newApprovalsSetHandler(svc),
		"thread/compact/start":   newCompactStartHandler(svc, capResolver),
		// 以下低频 provider 命令保留 RPC 名称，但能力未接入时会明确返回 unsupported。
		"thread/rollback":                  newThreadCommandHandler(svc, "/rollback"),
		"thread/undo":                      newThreadCommandHandler(svc, "/undo"),
		"thread/backgroundTerminals/clean": newThreadCommandHandler(svc, "/clean"),
		"thread/mcp/list":                  newThreadCommandHandler(svc, "/mcp"),
		// thread/skills/list: 走 thread 命令通道，语义上返回 thread 绑定的 active skills。
		// 与 skills/list 不同：后者扫描本地 skill 目录，返回所有已安装的 skill 元信息。
		"thread/skills/list": newThreadCommandHandler(svc, "/skills"),

		// thread/debugMemory 当前返回 Go runtime.MemStats。
		// 这是宿主进程调试快照，不代表 provider 子进程内存；调用方不能据此做 agent 资源判定。
		"thread/debugMemory": newThreadCall(func(context.Context, string) (any, error) {
			return runtimeMemoryStats(), nil
		}),

		// realtime 系列先做能力门控，再进入兼容命令壳；未声明能力时返回明确错误。
		"thread/realtime/start":       newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/start"),
		"thread/realtime/appendAudio": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/appendAudio"),
		"thread/realtime/appendText":  newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/appendText"),
		"thread/realtime/stop":        newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/stop"),
	}}
}

// newThreadListHandler 让生产 thread/list RPC 通过 session status port 读取摘要。
// 返回字段沿用 thread.Ref 的 JSON 形状，避免 list 迁移改变前端可见响应。
func newThreadListHandler(sessionPorts contract.SessionPorts) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
		if sessionPorts == nil {
			return nil, fmt.Errorf("thread/list: session ports are required")
		}
		return sessionPorts.ListSessions(ctx)
	})
}

// newThreadMessagesHandler 让生产 thread/messages RPC 通过 session port 读取消息。
// 这条只读入口保持分页 DTO 不变，避免一次性迁移 thread/start 主流程。
func newThreadMessagesHandler(sessionPorts contract.SessionPorts) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p messagesParams) (any, error) {
		if sessionPorts == nil {
			return nil, fmt.Errorf("thread/messages: session ports are required")
		}
		return sessionPorts.ReadMessages(ctx, contract.ThreadIDFrom(ctx), p.Limit, p.Before)
	})
}

func newStartHandler(svc Service) handler.Func {
	return platformrpc.LoggedStrictHandler(contract.ThreadRPCStart, func(ctx context.Context, p startParams) (any, error) {
		if err := validateStartParams(p); err != nil {
			return nil, err
		}
		cfg, err := decodeConfigMap(p.Config)
		if err != nil {
			return nil, err
		}
		logStartRPCReceived(p, cfg)
		result, err := svc.Start(ctx, buildStartRequestFromParams(p, cfg))
		if err != nil {
			return nil, err
		}
		return buildStartResponse(result), nil
	})
}

func validateStartParams(p startParams) error {
	if strings.TrimSpace(p.CWD) == "" {
		return fmt.Errorf("%s: cwd is required", contract.ThreadRPCStart)
	}
	if strings.TrimSpace(util.FirstNonEmpty(p.Provider, p.ModelProvider)) == "" {
		return fmt.Errorf("%s: provider is required", contract.ThreadRPCStart)
	}
	return nil
}

// logStartRPCReceived 记录 thread/start 实际收到的路由和 provider 字段。
// 日志只写标量和布尔值，用于区分前端漏传、后端解析和 config 覆盖问题。
func logStartRPCReceived(p startParams, cfg map[string]any) {
	// 只记录标量和布尔字段，既能定位 agent_key 丢失边界，也避免把大块配置写进日志。
	pkglogger.Info("thread/start: rpc received",
		"agent_id", p.AgentID,
		"agent_key", p.AgentKey,
		"prompt_key", p.PromptKey,
		"provider", p.Provider,
		"model_provider", p.ModelProvider,
		"model", p.Model,
		"effort", p.Effort,
		"cwd", p.CWD,
		"has_prompt", strings.TrimSpace(p.Prompt) != "",
		"has_base_instructions", strings.TrimSpace(p.BaseInstructions) != "",
		"tool_surface_mode", p.ToolSurfaceMode,
		"defer_spawn", p.DeferSpawn,
		"selected_skills_n", len(p.SelectedSkills),
		"config_provider", configTraceString(cfg, "provider"),
		"config_model_provider", configTraceString(cfg, "modelProvider"),
		"config_codex_model_provider", configTraceString(cfg, "codexModelProvider"),
		"config_model", configTraceString(cfg, "model"),
		"config_effort", configTraceString(cfg, "effort"),
		"has_config", len(cfg) > 0)
	pkglogger.Debug("thread/start: config trace",
		"agent_id", p.AgentID,
		"provider", p.Provider,
		"model_provider", p.ModelProvider,
		"model", p.Model,
		"effort", p.Effort,
		"config_model", configTraceString(cfg, "model"),
		"config_effort", configTraceString(cfg, "effort"),
		"has_config", len(cfg) > 0,
	)
	if shouldWarnStartProviderIdentity(p, cfg) {
		pkglogger.Warn("thread/start: provider identity trace",
			"agent_id", p.AgentID,
			"provider", p.Provider,
			"model_provider", p.ModelProvider,
			"model", p.Model,
			"effort", p.Effort,
			"config_provider", configTraceString(cfg, "provider"),
			"config_model_provider", configTraceString(cfg, "modelProvider"),
			"config_codex_model_provider", configTraceString(cfg, "codexModelProvider"),
			"config_model", configTraceString(cfg, "model"),
			"config_effort", configTraceString(cfg, "effort"),
			"has_config", len(cfg) > 0,
		)
	}
}

func shouldWarnStartProviderIdentity(p startParams, cfg map[string]any) bool {
	if strings.EqualFold(strings.TrimSpace(p.Provider), "codex") {
		return true
	}
	if strings.TrimSpace(p.ModelProvider) != "" {
		return true
	}
	return configTraceString(cfg, "provider") != "" ||
		configTraceString(cfg, "modelProvider") != "" ||
		configTraceString(cfg, "codexModelProvider") != ""
}

// buildStartRequestFromParams 将 wire 参数转换为 service.Start 使用的 StartRequest。
// selected skill 字段在这里折叠为内部 launch skill carrier，保留 scope/path 以区分同名 skill。
func buildStartRequestFromParams(p startParams, cfg map[string]any) StartRequest {
	return StartRequest{
		AgentID:               p.AgentID,
		Provider:              p.Provider,
		CWD:                   p.CWD,
		Model:                 p.Model,
		ModelProvider:         p.ModelProvider,
		ParentAgentID:         p.ParentAgentID,
		AgentType:             p.AgentType,
		AgentMemoryScope:      p.AgentMemoryScope,
		Name:                  p.Name,
		Prompt:                p.Prompt,
		BaseInstructions:      p.BaseInstructions,
		DeveloperInstructions: p.DeveloperInstructions,
		ApprovalPolicy:        p.ApprovalPolicy,
		Sandbox:               p.Sandbox,
		Summary:               p.Summary,
		Effort:                p.Effort,
		Personality:           p.Personality,
		Language:              p.Language,
		ToolSurfaceMode:       p.ToolSurfaceMode,
		Config:                cfg,

		// public payload 使用 selectedSkills / manualSkillSelection，
		// 内部归一为 launch skill carriers；refs 保留 scope/path 以区分同名。
		LaunchSkillNames:  append([]string(nil), p.SelectedSkills...),
		LaunchSkillRefs:   threadSkillRefsFromParams(p.SelectedSkillRefs, p.ManualSkillSelection),
		ForceLaunchSkills: p.ManualSkillSelection,
		AgentKey:          p.AgentKey,
		PromptKey:         p.PromptKey,
		DeferSpawn:        p.DeferSpawn,
		LaunchIntentID:    p.LaunchIntentID,
	}
}

// threadSkillRefsFromParams 把 RPC skill ref 参数整理为 provider DTO。
// 手动选择会标记 source；调用方显式传入的 source 更精确时优先生效，空名称会被丢弃。
func threadSkillRefsFromParams(params []skillRefParams, manual bool) []dto.SkillRef {
	if len(params) == 0 {
		return nil
	}
	source := dto.SkillSourceUnspecified
	if manual {
		source = dto.SkillSourceManual
	}
	var refs []dto.SkillRef
	for _, p := range params {
		refSource := source
		if rawSource := dto.SkillSource(strings.TrimSpace(p.Source)); rawSource.Valid() && rawSource != dto.SkillSourceUnspecified {
			refSource = rawSource
		}
		ref := dto.SkillRef{
			Key:          strings.TrimSpace(p.Key),
			Name:         strings.TrimSpace(p.Name),
			Scope:        strings.TrimSpace(p.Scope),
			PersonalType: strings.TrimSpace(p.PersonalType),
			Path:         strings.TrimSpace(p.Path),
			Source:       refSource,
		}
		if ref.Name == "" {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

// buildStartResponse 构造 thread/start 的 wire 响应。
// snake_case 和 camelCase 字段同时保留，避免旧 UI 与新 contract 在升级期间读不到关键身份。
func buildStartResponse(result StartResult) startResponse {
	status := util.FirstNonEmpty(result.Status, "running")
	sessionID := util.FirstNonEmpty(result.SessionID, result.ThreadID)
	resp := startResponse{
		Thread:         threadInfo{ID: result.ThreadID, Status: status},
		ThreadID:       result.ThreadID,
		ThreadIDSnake:  result.ThreadID,
		SessionID:      sessionID,
		SessionIDSnake: sessionID,
		Status:         status,
		AgentID:        result.AgentID,
		AgentIDSnake:   result.AgentID,
		Model:          result.Model,
		Provider:       result.Provider,
		ModelProvider:  result.ModelProvider,
		CWD:            result.CWD,
		ApprovalPolicy: result.ApprovalPolicy,
		Effective:      startEffectiveResponse{Model: result.Model, Provider: result.Provider, ModelProvider: result.ModelProvider, CWD: result.CWD, ApprovalPolicy: result.ApprovalPolicy},
	}
	if result.AgentKey != "" {
		resp.AgentKey = &result.AgentKey
		resp.AgentKeyCamel = &result.AgentKey
	}
	if result.AgentTitle != "" {
		resp.AgentTitle = &result.AgentTitle
		resp.AgentTitleCamel = &result.AgentTitle
	}
	if result.PromptKey != "" {
		resp.PromptKey = &result.PromptKey
		resp.PromptKeyCamel = &result.PromptKey
	}
	if result.PromptVersionID != nil {
		resp.PromptVersionID = result.PromptVersionID
		resp.PromptVersionIDC = result.PromptVersionID
	}
	attachPromptKeyStale(&resp, result.PromptKeyStale)
	if result.PendingLaunch {
		resp.PendingLaunch = &result.PendingLaunch
		resp.PendingLaunchC = &result.PendingLaunch
	}
	return resp
}

func decodeConfigMap(raw json.RawMessage) (map[string]any, error) {
	raw = trimRawJSON(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("thread/start config must be an object: %w", err)
	}
	return cfg, nil
}

func configTraceString(cfg map[string]any, key string) string {
	if len(cfg) == 0 {
		return ""
	}
	text, ok := cfg[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func newThreadCall(fn func(context.Context, string) (any, error)) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, _ threadIDParams) (any, error) {
		return fn(ctx, contract.ThreadIDFrom(ctx))
	})
}

func newThreadEffect(fn func(context.Context, string) error) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		return nil, fn(ctx, id)
	})
}

func newTracedThreadEffect(method string, fn func(context.Context, string) error) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		pkglogger.Info("thread: RPC effect INVOKED",
			"method", method,
			"thread_id", id,
		)
		err := fn(ctx, id)
		if err != nil {
			pkglogger.Warn("thread: RPC effect FAILED",
				"method", method,
				"thread_id", id,
				"error", err,
			)
		}
		return nil, err
	})
}

func newForkHandler(sessionPorts contract.SessionPorts) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		if sessionPorts == nil {
			return nil, fmt.Errorf("thread/fork: session ports are required")
		}
		result, err := sessionPorts.ForkSession(ctx, id)
		if err != nil {
			return nil, err
		}
		return forkResponse{Thread: threadInfo{ID: result.NewThreadID, ForkedFrom: result.ForkedFrom}, KickoffState: result.KickoffState, KickoffStateCamel: result.KickoffState}, nil
	})
}

func newHandoffHandler(svc Service) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p handoffParams) (any, error) {
		result, err := svc.Handoff(ctx, HandoffRequest{
			SourceThreadID: p.ThreadID,
			TargetAgentKey: p.AgentKey,
			InitialMessage: p.InitialMessage,
		})
		if err != nil {
			return nil, err
		}
		resp := handoffResponse{SourceThreadID: result.SourceThreadID, SourceThreadIDCamel: result.SourceThreadID, NewThreadID: result.NewThreadID, NewThreadIDCamel: result.NewThreadID, Thread: threadInfo{ID: result.NewThreadID, Status: result.Status}, AgentID: result.AgentID, AgentIDCamel: result.AgentID, Status: result.Status}
		if result.AgentKey != "" {
			resp.AgentKey = &result.AgentKey
			resp.AgentKeyCamel = &result.AgentKey
		}
		if result.PromptKey != "" {
			resp.PromptKey = &result.PromptKey
			resp.PromptKeyCamel = &result.PromptKey
		}
		if result.PromptVersionID != nil {
			resp.PromptVersionID = result.PromptVersionID
			resp.PromptVersionIDC = result.PromptVersionID
		}
		return resp, nil
	})
}

func newRecoverHandler(svc Service) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		result, err := svc.Recover(ctx, id)
		if err != nil {
			return nil, err
		}
		return recoverResponse{Thread: threadInfo{ID: result.ThreadID, Status: result.Status}, Recovered: result.Recovered, Mode: result.Mode}, nil
	})
}

// newThreadCommandHandler 构造低频命令的 SendCommand handler。
// 高频命令已拆到 typed handler；剩余命令保留 string args 兼容壳，并由 SendCommand 明确拒绝未支持命令。
func newThreadCommandHandler(svc Service, command string) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p commandParams) (any, error) {
		return svc.SendCommand(ctx, contract.ThreadIDFrom(ctx), command, p.Args)
	})
}

// newApprovalsSetHandler 同时接受 policy 和 args 两种 wire 字段。
// thread id 仍从 thread-scoped RPC context 读取，避免请求体绕过路由校验。
func newApprovalsSetHandler(svc Service) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p approvalsSetParams) (any, error) {
		args, err := resolveApprovalsSetArgs(p)
		if err != nil {
			return nil, err
		}
		return svc.SendCommand(ctx, contract.ThreadIDFrom(ctx), "/approvals", args)
	})
}

func newThreadConfigGetHandler(svc Service) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, _ configGetParams) (any, error) {
		return svc.GetConfig(ctx, contract.ThreadIDFrom(ctx))
	})
}

func newThreadConfigSetHandler(svc Service) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p configSetParams) (any, error) {
		return svc.SetConfig(ctx, contract.ThreadIDFrom(ctx), dto.ThreadConfigPatch{
			Model:  p.Model,
			Effort: p.Effort,
		})
	})
}

func newCapabilityThreadCommandHandler(
	svc Service,
	capResolver contract.CapabilityResolver,
	cap string,
	command string,
) handler.Func {
	return platformrpc.CapabilityThreadHandler(cap, capResolver, func(ctx context.Context, p commandParams) (any, error) {
		return svc.SendCommand(ctx, contract.ThreadIDFrom(ctx), command, p.Args)
	})
}

func newModelSetHandler(svc Service) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p modelSetParams) (any, error) {
		args, err := resolveModelSetArgs(p)
		if err != nil {
			return nil, err
		}
		return svc.SetModel(ctx, contract.ThreadIDFrom(ctx), args)
	})
}

func newCompactStartHandler(svc Service, capResolver contract.CapabilityResolver) handler.Func {
	return platformrpc.CapabilityThreadHandler(capabilityContextCompact, capResolver, func(ctx context.Context, p compactStartParams) (any, error) {
		return svc.Compact(ctx, contract.ThreadIDFrom(ctx), p.Args)
	})
}

func resolveModelSetArgs(p modelSetParams) (string, error) {
	model := p.Model
	args := p.Args
	if model != "" && args != "" && model != args {
		return "", errModelSetArgsConflict
	}
	if model != "" {
		return model, nil
	}
	return args, nil
}

var errModelSetArgsConflict = platformrpc.ErrInvalidParams("thread/model/set: model and args must match when both are provided")

func resolveApprovalsSetArgs(p approvalsSetParams) (string, error) {
	policy := strings.TrimSpace(p.Policy)
	args := strings.TrimSpace(p.Args)
	if policy != "" && args != "" && policy != args {
		return "", errApprovalsSetArgsConflict
	}
	if policy != "" {
		return policy, nil
	}
	return args, nil
}

var errApprovalsSetArgsConflict = platformrpc.ErrInvalidParams("thread/approvals/set: policy and args must match when both are provided")

func newResumeHandler(sessionPorts contract.SessionPorts) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p resumeParams) (any, error) {
		if sessionPorts == nil {
			return nil, fmt.Errorf("thread/resume: session ports are required")
		}
		result, err := sessionPorts.ResumeSession(ctx, contract.SessionResumeRequest{
			ThreadID: util.FirstNonEmpty(p.ThreadID, contract.ThreadIDFrom(ctx)),
			Path:     p.Path,
			CWD:      p.CWD,
			Model:    p.Model,
			Provider: p.Provider,
		})
		if err != nil {
			return nil, err
		}
		status := util.FirstNonEmpty(result.Status, "resumed")
		sessionID := util.FirstNonEmpty(result.SessionID, result.ThreadID)
		return resumeResponse{Thread: threadInfo{ID: result.ThreadID, Status: status}, ThreadID: result.ThreadID, ThreadIDSnake: result.ThreadID, SessionID: sessionID, SessionIDSnake: sessionID, Status: status, Model: result.Model, CWD: result.CWD}, nil
	})
}

func runtimeMemoryStats() runtime.MemStats {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}
