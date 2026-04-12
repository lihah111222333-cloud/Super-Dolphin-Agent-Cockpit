package thread

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	capabilityContextCompact = "context_compact"
	capabilityRealtime       = "realtime"
)

func NewThreadHandlers(svc Service, capResolver rpc.CapabilityResolver) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"thread/start":     newStartHandler(svc),
		"thread/stop":      newThreadEffect(svc.Stop),
		"thread/resume":    newResumeHandler(svc),
		"thread/fork":      newForkHandler(svc),
		"thread/recover":   newRecoverHandler(svc),
		"thread/archive":   newThreadEffect(svc.Archive),
		"thread/unarchive": newThreadEffect(svc.Unarchive),
		"thread/delete":    newThreadEffect(svc.Delete),

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

		"thread/name/set": rpc.ThreadHandler(func(ctx context.Context, p nameSetParams) (any, error) {
			return nil, svc.SetName(ctx, rpc.ThreadIDFrom(ctx), p.Name)
		}),
		"thread/config/get": newThreadConfigGetHandler(svc),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/config/set": newThreadConfigSetHandler(svc),
		"thread/model/set":  newModelSetHandler(svc),
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
		result, err := svc.Start(ctx, StartRequest{
			Provider:              p.Provider,
			CWD:                   p.CWD,
			Model:                 p.Model,
			ModelProvider:         p.ModelProvider,
			Name:                  p.Name,
			Prompt:                p.Prompt,
			BaseInstructions:      p.BaseInstructions,
			DeveloperInstructions: p.DeveloperInstructions,
			ApprovalPolicy:        p.ApprovalPolicy,
			Sandbox:               p.Sandbox,
			Summary:               p.Summary,
			Effort:                p.Effort,
			Personality:           p.Personality,
			Config:                decodeConfigMap(p.Config),
		})
		if err != nil {
			return nil, err
		}
		status := shared.FirstNonEmpty(result.Status, "running")
		sessionID := shared.FirstNonEmpty(result.SessionID, result.ThreadID)
		effective := map[string]any{
			"model":          result.Model,
			"provider":       result.Provider,
			"modelProvider":  result.ModelProvider,
			"cwd":            result.CWD,
			"approvalPolicy": result.ApprovalPolicy,
		}
		return map[string]any{
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
			"effective":      effective,
		}, nil
	})
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
