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

func NewThreadHandlers(svc Service, capResolver contract.CapabilityResolver) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		contract.ThreadRPCStart:   newStartHandler(svc),
		contract.ThreadRPCStop:    newThreadEffect(svc.Stop),
		"thread/resume":           newResumeHandler(svc),
		"thread/fork":             newForkHandler(svc),
		"thread/recover":          newRecoverHandler(svc),
		"thread/handoff":          newHandoffHandler(svc),
		contract.ThreadRPCArchive: newTracedThreadEffect(contract.ThreadRPCArchive, svc.Archive),
		"thread/unarchive":        newThreadEffect(svc.Unarchive),
		"thread/delete":           newThreadEffect(svc.Delete),

		"thread/list": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.List(ctx)
		}),
		"thread/loaded/list": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.ListByStatus(ctx, statusCreated)
		}),
		// thread/read and thread/resolve intentionally remain separate RPC names.
		// `thread/read` keeps the V2 compatibility history wrapper, while
		// `thread/resolve` remains the runtime identity lookup.
		"thread/read": newThreadReadHandler(svc),
		"thread/resolve": newThreadCall(func(ctx context.Context, id string) (any, error) {
			return svc.Get(ctx, id)
		}),
		"thread/messages": platformrpc.ThreadHandler(func(ctx context.Context, p messagesParams) (any, error) {
			return svc.ReadMessages(ctx, contract.ThreadIDFrom(ctx), p.Limit, p.Before)
		}),

		contract.ThreadRPCNameSet: platformrpc.ThreadHandler(func(ctx context.Context, p nameSetParams) (any, error) {
			return nil, svc.SetName(ctx, contract.ThreadIDFrom(ctx), p.Name)
		}),
		"thread/config/get": newThreadConfigGetHandler(svc),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/config/set": newThreadConfigSetHandler(svc),
		"thread/model/set":  newModelSetHandler(svc),
		"thread/clear":      newThreadCommandHandler(svc, "/clear"),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/personality/set": newThreadCommandHandler(svc, "/personality"),
		"thread/approvals/set":   newApprovalsSetHandler(svc),
		"thread/compact/start":   newCompactStartHandler(svc, capResolver),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/rollback": newThreadCommandHandler(svc, "/rollback"),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/undo": newThreadCommandHandler(svc, "/undo"),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/backgroundTerminals/clean": newThreadCommandHandler(svc, "/clean"),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/mcp/list": newThreadCommandHandler(svc, "/mcp"),
		// thread/skills/list: 走 thread 命令通道，语义上返回 thread 绑定的 active skills。
		// 与 skills/list 不同：后者扫描本地 skill 目录，返回所有已安装的 skill 元信息。
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/skills/list": newThreadCommandHandler(svc, "/skills"),

		// thread/debugMemory 当前返回 Go runtime.MemStats。
		// P7 backlog: V2 返回 agent 进程内存快照（通过 provider），不是宿主进程 stats。
		// P7 补齐 provider-level memory stats 后替换此实现。
		"thread/debugMemory": newThreadCall(func(context.Context, string) (any, error) {
			return runtimeMemoryStats(), nil
		}),

		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/realtime/start": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/start"),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/realtime/appendAudio": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/appendAudio"),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/realtime/appendText": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/appendText"),
		// P9 backlog: 当前保留低频 SendCommand 兼容壳，typed contract 另行落地。
		"thread/realtime/stop": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/stop"),
	}}
}

func newStartHandler(svc Service) handler.Func {
	return platformrpc.LoggedStrictHandler(contract.ThreadRPCStart, func(ctx context.Context, p startParams) (any, error) {
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

func logStartRPCReceived(p startParams, cfg map[string]any) {
	// Observability: dump the params that thread/start actually received so
	// we can distinguish "frontend never sent agent_key" from "backend
	// dropped it" without running tcpdump. Values are scalar / boolean so
	// log volume stays tame.
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

		// p20.3 §4.3：public payload 用 `selectedSkills` / `manualSkillSelection`，
		// 内部合同归一化为 launch skill carriers；refs 保留 scope/path 以区分同名。
		LaunchSkillNames:  append([]string(nil), p.SelectedSkills...),
		LaunchSkillRefs:   threadSkillRefsFromParams(p.SelectedSkillRefs, p.ManualSkillSelection),
		ForceLaunchSkills: p.ManualSkillSelection,
		AgentKey:          p.AgentKey,
		PromptKey:         p.PromptKey,
		DeferSpawn:        p.DeferSpawn,
		LaunchIntentID:    p.LaunchIntentID,
	}
}

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

func newForkHandler(svc Service) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		result, err := svc.Fork(ctx, id)
		if err != nil {
			return nil, err
		}
		return forkResponse{Thread: threadInfo{ID: result.NewThreadID, ForkedFrom: result.ForkedFrom}}, nil
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

// cmd 构造低频命令的 SendCommand handler。
// 高频命令已拆到 typed handler；剩余命令先保留 string args 兼容壳，P9 再补 typed contract。
func newThreadCommandHandler(svc Service, command string) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p commandParams) (any, error) {
		return svc.SendCommand(ctx, contract.ThreadIDFrom(ctx), command, p.Args)
	})
}

// V2 compatibility: accept both `policy` and `args`, while keeping V3's
// explicit thread-scoped routing requirement.
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

func newResumeHandler(svc Service) handler.Func {
	return platformrpc.ThreadHandler(func(ctx context.Context, p resumeParams) (any, error) {
		result, err := svc.Resume(ctx, ResumeRequest{
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
