package thread

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	capabilityContextCompact = "context_compact"
	capabilityRealtime       = "realtime"
)

func NewThreadHandlers(svc Service, capResolver rpc.CapabilityResolver) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		contract.ThreadRPCStart:   newStartHandler(svc),
		contract.ThreadRPCStop:    newThreadEffect(svc.Stop),
		"thread/resume":           newResumeHandler(svc),
		"thread/fork":             newForkHandler(svc),
		"thread/recover":          newRecoverHandler(svc),
		"thread/handoff":          newHandoffHandler(svc),
		contract.ThreadRPCArchive: newTracedThreadEffect(contract.ThreadRPCArchive, svc.Archive),
		"thread/unarchive":        newThreadEffect(svc.Unarchive),
		"thread/delete":           newThreadEffect(svc.Delete),

		"thread/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.List(ctx)
		}),
		"thread/loaded/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.ListByStatus(ctx, statusCreated)
		}),
		// thread/read and thread/resolve intentionally remain separate RPC names.
		// `thread/read` keeps the V2 compatibility history wrapper, while
		// `thread/resolve` remains the runtime identity lookup.
		"thread/read": newThreadReadHandler(svc),
		"thread/resolve": newThreadCall(func(ctx context.Context, id string) (any, error) {
			return svc.Get(ctx, id)
		}),
		"thread/messages": rpc.ThreadHandler(func(ctx context.Context, p messagesParams) (any, error) {
			return svc.ReadMessages(ctx, rpc.ThreadIDFrom(ctx), p.Limit, p.Before)
		}),

		contract.ThreadRPCNameSet: rpc.ThreadHandler(func(ctx context.Context, p nameSetParams) (any, error) {
			return nil, svc.SetName(ctx, rpc.ThreadIDFrom(ctx), p.Name)
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
		"ui/task/flush_and_verify": rpc.StrictHandler(func(ctx context.Context, p flushAndVerifyParams) (any, error) {
			if err := svc.FlushAndVerifyTaskHandoff(ctx, p.ThreadID, p.TaskID); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),

		// Phase 2.1: promote a normal thread to a task thread. Frontend
		// surfaces this from the thread config panel ("作为自动化任务运行")
		// and from the watchdog stuck-banner upgrade button. Idempotent on
		// the backend so repeated clicks return the existing taskId rather
		// than overwrite handoff state.
		"ui/thread/promote-task": rpc.StrictHandler(func(ctx context.Context, p promoteTaskParams) (any, error) {
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
	return rpc.StrictHandler(func(ctx context.Context, p startParams) (any, error) {
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
		"cwd", p.CWD,
		"has_prompt", strings.TrimSpace(p.Prompt) != "",
		"has_base_instructions", strings.TrimSpace(p.BaseInstructions) != "",
		"defer_spawn", p.DeferSpawn,
		"selected_skills_n", len(p.SelectedSkills))
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

func buildStartEffective(result StartResult) map[string]any {
	return map[string]any{
		"model":          result.Model,
		"provider":       result.Provider,
		"modelProvider":  result.ModelProvider,
		"cwd":            result.CWD,
		"approvalPolicy": result.ApprovalPolicy,
	}
}

func buildStartResponse(result StartResult) map[string]any {
	status := shared.FirstNonEmpty(result.Status, "running")
	sessionID := shared.FirstNonEmpty(result.SessionID, result.ThreadID)
	response := map[string]any{
		"thread":         threadInfo{ID: result.ThreadID, Status: status},
		"threadId":       result.ThreadID,
		"thread_id":      result.ThreadID,
		"sessionId":      sessionID,
		"session_id":     sessionID,
		"status":         status,
		"agentId":        result.AgentID,
		"agent_id":       result.AgentID,
		"model":          result.Model,
		"provider":       result.Provider,
		"modelProvider":  result.ModelProvider,
		"cwd":            result.CWD,
		"approvalPolicy": result.ApprovalPolicy,
		"effective":      buildStartEffective(result),
	}
	if result.AgentKey != "" {
		response["agent_key"] = result.AgentKey
		response["agentKey"] = result.AgentKey
	}
	if result.AgentTitle != "" {
		response["agent_title"] = result.AgentTitle
		response["agentTitle"] = result.AgentTitle
	}
	if result.PromptKey != "" {
		response["prompt_key"] = result.PromptKey
		response["promptKey"] = result.PromptKey
	}
	if result.PromptVersionID != nil {
		response["prompt_version_id"] = *result.PromptVersionID
		response["promptVersionId"] = *result.PromptVersionID
	}
	if result.PendingLaunch {
		response["pending_launch"] = true
		response["pendingLaunch"] = true
	}
	if result.TaskID != "" {
		response["task_id"] = result.TaskID
		response["taskId"] = result.TaskID
	}
	if result.HandoffFile != "" {
		response["handoff_file"] = result.HandoffFile
		response["handoffFile"] = result.HandoffFile
	}
	return response
}

func buildPromoteTaskResponse(result PromoteTaskResult) map[string]any {
	response := map[string]any{
		"thread_id":    result.ThreadID,
		"threadId":     result.ThreadID,
		"task_id":      result.TaskID,
		"taskId":       result.TaskID,
		"already_task": result.AlreadyTask,
		"alreadyTask":  result.AlreadyTask,
	}
	if result.TaskTitle != "" {
		response["task_title"] = result.TaskTitle
		response["taskTitle"] = result.TaskTitle
	}
	if result.HandoffFile != "" {
		response["handoff_file"] = result.HandoffFile
		response["handoffFile"] = result.HandoffFile
	}
	if result.HandoffShellWarning != "" {
		response["handoff_shell_warning"] = result.HandoffShellWarning
		response["handoffShellWarning"] = result.HandoffShellWarning
	}
	return response
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
	return rpc.ThreadHandler(func(ctx context.Context, _ threadIDParams) (any, error) {
		return fn(ctx, rpc.ThreadIDFrom(ctx))
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
		return map[string]any{
			"thread": threadInfo{ID: result.NewThreadID, ForkedFrom: result.ForkedFrom},
		}, nil
	})
}

func newHandoffHandler(svc Service) handler.Func {
	return rpc.StrictHandler(func(ctx context.Context, p handoffParams) (any, error) {
		result, err := svc.Handoff(ctx, HandoffRequest{
			SourceThreadID: p.ThreadID,
			TargetAgentKey: p.AgentKey,
			InitialMessage: p.InitialMessage,
		})
		if err != nil {
			return nil, err
		}
		response := map[string]any{
			"source_thread_id": result.SourceThreadID,
			"sourceThreadId":   result.SourceThreadID,
			"new_thread_id":    result.NewThreadID,
			"newThreadId":      result.NewThreadID,
			"thread":           threadInfo{ID: result.NewThreadID, Status: result.Status},
			"agent_id":         result.AgentID,
			"agentId":          result.AgentID,
			"status":           result.Status,
		}
		if result.AgentKey != "" {
			response["agent_key"] = result.AgentKey
			response["agentKey"] = result.AgentKey
		}
		if result.PromptKey != "" {
			response["prompt_key"] = result.PromptKey
			response["promptKey"] = result.PromptKey
		}
		if result.PromptVersionID != nil {
			response["prompt_version_id"] = *result.PromptVersionID
			response["promptVersionId"] = *result.PromptVersionID
		}
		return response, nil
	})
}

func newRecoverHandler(svc Service) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		result, err := svc.Recover(ctx, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"thread":    threadInfo{ID: result.ThreadID, Status: result.Status},
			"recovered": result.Recovered,
			"mode":      result.Mode,
		}, nil
	})
}

// cmd 构造低频命令的 SendCommand handler。
// 高频命令已拆到 typed handler；剩余命令先保留 string args 壳，并在路由上显式标 TODO(P9)。
func newThreadCommandHandler(svc Service, command string) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p commandParams) (any, error) {
		return svc.SendCommand(ctx, rpc.ThreadIDFrom(ctx), command, p.Args)
	})
}

// V2 compatibility: accept both `policy` and `args`, while keeping V3's
// explicit thread-scoped routing requirement.
func newApprovalsSetHandler(svc Service) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p approvalsSetParams) (any, error) {
		args, err := resolveApprovalsSetArgs(p)
		if err != nil {
			return nil, err
		}
		return svc.SendCommand(ctx, rpc.ThreadIDFrom(ctx), "/approvals", args)
	})
}

func newThreadConfigGetHandler(svc Service) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, _ configGetParams) (any, error) {
		return svc.GetConfig(ctx, rpc.ThreadIDFrom(ctx))
	})
}

func newThreadConfigSetHandler(svc Service) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p configSetParams) (any, error) {
		return svc.SetConfig(ctx, rpc.ThreadIDFrom(ctx), dto.ThreadConfigPatch{
			Model:  p.Model,
			Effort: p.Effort,
		})
	})
}

func newCapabilityThreadCommandHandler(
	svc Service,
	capResolver rpc.CapabilityResolver,
	cap string,
	command string,
) handler.Func {
	return rpc.CapabilityThreadHandler(cap, capResolver, func(ctx context.Context, p commandParams) (any, error) {
		return svc.SendCommand(ctx, rpc.ThreadIDFrom(ctx), command, p.Args)
	})
}

func newModelSetHandler(svc Service) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p modelSetParams) (any, error) {
		args, err := resolveModelSetArgs(p)
		if err != nil {
			return nil, err
		}
		return svc.SetModel(ctx, rpc.ThreadIDFrom(ctx), args)
	})
}

func newCompactStartHandler(svc Service, capResolver rpc.CapabilityResolver) handler.Func {
	return rpc.CapabilityThreadHandler(capabilityContextCompact, capResolver, func(ctx context.Context, p compactStartParams) (any, error) {
		return svc.Compact(ctx, rpc.ThreadIDFrom(ctx), p.Args)
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

var errModelSetArgsConflict = errors.New("thread/model/set: model and args must match when both are provided")

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

var errApprovalsSetArgsConflict = errors.New("thread/approvals/set: policy and args must match when both are provided")

func newResumeHandler(svc Service) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p resumeParams) (any, error) {
		result, err := svc.Resume(ctx, ResumeRequest{
			ThreadID: shared.FirstNonEmpty(p.ThreadID, rpc.ThreadIDFrom(ctx)),
			Path:     p.Path,
			CWD:      p.CWD,
			Model:    p.Model,
			Provider: p.Provider,
		})
		if err != nil {
			return nil, err
		}
		status := shared.FirstNonEmpty(result.Status, "resumed")
		sessionID := shared.FirstNonEmpty(result.SessionID, result.ThreadID)
		return map[string]any{
			"thread":     threadInfo{ID: result.ThreadID, Status: status},
			"threadId":   result.ThreadID,
			"thread_id":  result.ThreadID,
			"sessionId":  sessionID,
			"session_id": sessionID,
			"status":     status,
			"model":      result.Model,
			"cwd":        result.CWD,
		}, nil
	})
}

func runtimeMemoryStats() runtime.MemStats {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}
