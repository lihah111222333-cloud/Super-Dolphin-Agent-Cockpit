package thread

import (
	"context"
	"encoding/json"
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
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/config/set": newThreadConfigSetHandler(svc),
		"thread/model/set":  newModelSetHandler(svc),
		"thread/clear":      newThreadCommandHandler(svc, "/clear"),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/personality/set": newThreadCommandHandler(svc, "/personality"),
		"thread/approvals/set":   newApprovalsSetHandler(svc),
		"thread/compact/start":   newCompactStartHandler(svc, capResolver),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/rollback": newThreadCommandHandler(svc, "/rollback"),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/undo": newThreadCommandHandler(svc, "/undo"),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/backgroundTerminals/clean": newThreadCommandHandler(svc, "/clean"),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/mcp/list": newThreadCommandHandler(svc, "/mcp"),
		// thread/skills/list: 走 thread 命令通道，语义上返回 thread 绑定的 active skills。
		// 与 skills/list 不同：后者扫描本地 skill 目录，返回所有已安装的 skill 元信息。
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/skills/list": newThreadCommandHandler(svc, "/skills"),

		// Phase 1.8d：fork 前预检（flush worker + stat handoff 文件存在）。
		// 失败时 error message 含 handoff_flush_failed / handoff_missing
		// 关键字，前端 classifyError 识别为 permanent 不重试。
		"ui/task/flush_and_verify": platformrpc.StrictHandler(func(ctx context.Context, p flushAndVerifyParams) (any, error) {
			if err := svc.FlushAndVerifyTaskHandoff(ctx, p.ThreadID, p.TaskID); err != nil {
				return nil, err
			}
			return flushAndVerifyResponse{OK: true}, nil
		}),

		// Phase 2.1: promote a normal thread to a task thread. Frontend
		// surfaces this from the thread config panel ("作为自动化任务运行")
		// and from the watchdog stuck-banner upgrade button. Idempotent on
		// the backend so repeated clicks return the existing taskId rather
		// than overwrite handoff state.
		"ui/thread/promote-task": platformrpc.StrictHandler(func(ctx context.Context, p promoteTaskParams) (any, error) {
			result, err := svc.PromoteTaskFromThread(ctx, p.ThreadID)
			if err != nil {
				return nil, err
			}
			return buildPromoteTaskResponse(result), nil
		}),

		// thread/debugMemory 当前返回 Go runtime.MemStats。
		// TODO(P7): V2 返回的是 agent 进程内存快照（通过 provider），不是宿主进程 stats。
		// P7 补齐 provider-level memory stats 后替换此实现。
		"thread/debugMemory": newThreadCall(func(context.Context, string) (any, error) {
			return runtimeMemoryStats(), nil
		}),

		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/realtime/start": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/start"),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/realtime/appendAudio": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/appendAudio"),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/realtime/appendText": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/appendText"),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/realtime/stop": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/stop"),
	}}
}

func newStartHandler(svc Service) handler.Func {
	return platformrpc.LoggedStrictHandler(contract.ThreadRPCStart, func(ctx context.Context, p startParams) (any, error) {
		logStartRPCReceived(p)
		result, err := svc.Start(ctx, buildStartRequestFromParams(p))
		if err != nil {
			return nil, err
		}
		return buildStartResponse(result), nil
	})
}

func logStartRPCReceived(p startParams) {
	cfg := decodeConfigMap(p.Config)
	// Observability: dump the params that thread/start actually received so
	// we can distinguish "frontend never sent agent_key" from "backend
	// dropped it" without running tcpdump. Values are scalar / boolean so
	// log volume stays tame.
	pkglogger.Info("thread/start: rpc received",
		"agent_id", p.AgentID,
		"agent_key", p.AgentKey,
		"prompt_key", p.PromptKey,
		"use_classifier", p.UseClassifier,
		"provider", p.Provider,
		"model_provider", p.ModelProvider,
		"model", p.Model,
		"effort", p.Effort,
		"cwd", p.CWD,
		"has_prompt", strings.TrimSpace(p.Prompt) != "",
		"has_base_instructions", strings.TrimSpace(p.BaseInstructions) != "",
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

func buildStartRequestFromParams(p startParams) StartRequest {
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
		Config:                decodeConfigMap(p.Config),

		// p20.3 §4.3：public payload 用 `selectedSkills` / `manualSkillSelection`，
		// 内部合同归一化为 `LaunchSkillNames` / `ForceLaunchSkills`。
		LaunchSkillNames:  append([]string(nil), p.SelectedSkills...),
		ForceLaunchSkills: p.ManualSkillSelection,
		AgentKey:          p.AgentKey,
		PromptKey:         p.PromptKey,
		UseClassifier:     p.UseClassifier,
		DeferSpawn:        p.DeferSpawn,
	}
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
		Effective: startEffectiveResponse{
			Model:          result.Model,
			Provider:       result.Provider,
			ModelProvider:  result.ModelProvider,
			CWD:            result.CWD,
			ApprovalPolicy: result.ApprovalPolicy,
		},
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
	if result.PendingLaunch {
		resp.PendingLaunch = &result.PendingLaunch
		resp.PendingLaunchC = &result.PendingLaunch
	}
	if result.TaskID != "" {
		resp.TaskID = &result.TaskID
		resp.TaskIDCamel = &result.TaskID
	}
	if result.HandoffFile != "" {
		resp.HandoffFile = &result.HandoffFile
		resp.HandoffFileCamel = &result.HandoffFile
	}
	return resp
}

func buildPromoteTaskResponse(result PromoteTaskResult) promoteTaskResponse {
	resp := promoteTaskResponse{
		ThreadIDSnake: result.ThreadID,
		ThreadIDCamel: result.ThreadID,
		TaskIDSnake:   result.TaskID,
		TaskIDCamel:   result.TaskID,
		AlreadyTask:   result.AlreadyTask,
		AlreadyTaskC:  result.AlreadyTask,
	}
	if result.TaskTitle != "" {
		resp.TaskTitle = &result.TaskTitle
		resp.TaskTitleCamel = &result.TaskTitle
	}
	if result.HandoffFile != "" {
		resp.HandoffFile = &result.HandoffFile
		resp.HandoffFileC = &result.HandoffFile
	}
	if result.HandoffShellWarning != "" {
		resp.HandoffShellWarn = &result.HandoffShellWarning
		resp.HandoffShellWarnC = &result.HandoffShellWarning
	}
	return resp
}

func decodeConfigMap(raw json.RawMessage) map[string]any {
	raw = trimRawJSON(raw)
	if len(raw) == 0 {
		return nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg) == 0 {
		return nil
	}
	return cfg
}

func configTraceString(cfg map[string]any, key string) string {
	if len(cfg) == 0 {
		return ""
	}
	value, ok := cfg[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
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
		return forkResponse{
			Thread: threadInfo{ID: result.NewThreadID, ForkedFrom: result.ForkedFrom},
		}, nil
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
		resp := handoffResponse{
			SourceThreadID:      result.SourceThreadID,
			SourceThreadIDCamel: result.SourceThreadID,
			NewThreadID:         result.NewThreadID,
			NewThreadIDCamel:    result.NewThreadID,
			Thread:              threadInfo{ID: result.NewThreadID, Status: result.Status},
			AgentID:             result.AgentID,
			AgentIDCamel:        result.AgentID,
			Status:              result.Status,
		}
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
		return recoverResponse{
			Thread:    threadInfo{ID: result.ThreadID, Status: result.Status},
			Recovered: result.Recovered,
			Mode:      result.Mode,
		}, nil
	})
}

// cmd 构造低频命令的 SendCommand handler。
// 高频命令已拆到 typed handler；剩余命令先保留 string args 壳，并在路由上显式标 TODO(P9)。
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
		return resumeResponse{
			Thread:         threadInfo{ID: result.ThreadID, Status: status},
			ThreadID:       result.ThreadID,
			ThreadIDSnake:  result.ThreadID,
			SessionID:      sessionID,
			SessionIDSnake: sessionID,
			Status:         status,
			Model:          result.Model,
			CWD:            result.CWD,
		}, nil
	})
}

func runtimeMemoryStats() runtime.MemStats {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}
