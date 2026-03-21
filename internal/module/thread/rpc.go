package thread

import (
	"context"
	"errors"
	"runtime"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const (
	capabilityContextCompact = "context_compact"
	capabilityModelSwitch    = "model_switch"
	capabilityRealtime       = "realtime"
)

func NewThreadHandlers(svc Service, capResolver rpc.CapabilityResolver) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"thread/start":  newStartHandler(svc),
		"thread/stop":   newThreadEffect(svc.Stop),
		"thread/resume": newResumeHandler(svc),
		"thread/fork": newThreadCall(func(ctx context.Context, id string) (any, error) {
			return svc.Fork(ctx, id)
		}),
		"thread/recover":   newThreadEffect(svc.Recover),
		"thread/archive":   newThreadEffect(svc.Archive),
		"thread/unarchive": newThreadEffect(svc.Unarchive),
		"thread/delete":    newThreadEffect(svc.Delete),

		"thread/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.List(ctx)
		}),
		"thread/loaded/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.ListByStatus(ctx, statusCreated)
		}),
		"thread/read": newThreadGetHandler(svc),
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
		"thread/config/set": newThreadCommandHandler(svc, "config/set"),
		"thread/model/set":  newModelSetHandler(svc, capResolver),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/personality/set": newThreadCommandHandler(svc, "/personality"),
		// TODO(P9): 补真实参数校验和结构化返回。当前仍走通用 SendCommand 壳。
		"thread/approvals/set": newThreadCommandHandler(svc, "/approvals"),
		"thread/compact/start": newCompactStartHandler(svc, capResolver),
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
		return svc.Start(ctx, StartRequest{
			Provider:              p.Provider,
			CWD:                   p.CWD,
			Model:                 p.Model,
			ModelProvider:         p.ModelProvider,
			Prompt:                p.Prompt,
			BaseInstructions:      p.BaseInstructions,
			DeveloperInstructions: p.DeveloperInstructions,
			ApprovalPolicy:        p.ApprovalPolicy,
			Sandbox:               p.Sandbox,
			Summary:               p.Summary,
			Effort:                p.Effort,
			Personality:           p.Personality,
		})
	})
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

// cmd 构造低频命令的 SendCommand handler。
// 高频命令已拆到 typed handler；剩余命令先保留 string args 壳，并在路由上显式标 TODO(P9)。
func newThreadCommandHandler(svc Service, command string) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p commandParams) (any, error) {
		return svc.SendCommand(ctx, rpc.ThreadIDFrom(ctx), command, p.Args)
	})
}

func newThreadConfigGetHandler(svc Service) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, _ configGetParams) (any, error) {
		return svc.GetConfig(ctx, rpc.ThreadIDFrom(ctx))
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

func newModelSetHandler(svc Service, capResolver rpc.CapabilityResolver) handler.Func {
	return rpc.CapabilityThreadHandler(capabilityModelSwitch, capResolver, func(ctx context.Context, p modelSetParams) (any, error) {
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

func newResumeHandler(svc Service) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p resumeParams) (any, error) {
		result, err := svc.Resume(ctx, ResumeRequest{
			ThreadID: rpc.ThreadIDFrom(ctx),
			Path:     p.Path,
			CWD:      p.CWD,
			Model:    p.Model,
			Provider: p.Provider,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"thread": threadInfo{ID: result.ThreadID, Status: result.Status},
			"model":  result.Model,
		}, nil
	})
}

func newThreadGetHandler(svc Service) handler.Func {
	return newThreadCall(func(ctx context.Context, id string) (any, error) {
		return svc.Get(ctx, id)
	})
}

func runtimeMemoryStats() runtime.MemStats {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}
