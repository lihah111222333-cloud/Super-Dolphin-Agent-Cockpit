package thread

import (
	"context"
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
		"thread/start": rpc.StrictHandler(func(ctx context.Context, p startParams) (any, error) {
			return svc.Start(ctx, StartRequest{
				Provider:       p.Provider,
				CWD:            p.CWD,
				Model:          p.Model,
				Prompt:         p.Prompt,
				ApprovalPolicy: p.ApprovalPolicy,
				Instructions:   p.Instructions,
				Effort:         p.Effort,
				Personality:    p.Personality,
			})
		}),
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
		"thread/config/get":                newThreadCommandHandler(svc, "config/get"),
		"thread/config/set":                newThreadCommandHandler(svc, "config/set"),
		"thread/model/set":                 newCapabilityThreadCommandHandler(svc, capResolver, capabilityModelSwitch, "/model"),
		"thread/personality/set":           newThreadCommandHandler(svc, "/personality"),
		"thread/approvals/set":             newThreadCommandHandler(svc, "/approvals"),
		"thread/compact/start":             newCapabilityThreadCommandHandler(svc, capResolver, capabilityContextCompact, "/compact"),
		"thread/rollback":                  newThreadCommandHandler(svc, "/rollback"),
		"thread/undo":                      newThreadCommandHandler(svc, "/undo"),
		"thread/backgroundTerminals/clean": newThreadCommandHandler(svc, "/clean"),
		"thread/mcp/list":                  newThreadCommandHandler(svc, "/mcp"),
		// thread/skills/list: 走 thread 命令通道，语义上返回 thread 绑定的 active skills。
		// 与 skills/list 不同：后者扫描本地 skill 目录，返回所有已安装的 skill 元信息。
		"thread/skills/list": newThreadCommandHandler(svc, "/skills"),

		// thread/debugMemory 当前返回 Go runtime.MemStats。
		// TODO(P7): V2 返回的是 agent 进程内存快照（通过 provider），不是宿主进程 stats。
		// P7 补齐 provider-level memory stats 后替换此实现。
		"thread/debugMemory": newThreadCall(func(context.Context, string) (any, error) {
			return runtimeMemoryStats(), nil
		}),

		"thread/realtime/start":       newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/start"),
		"thread/realtime/appendAudio": newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/appendAudio"),
		"thread/realtime/appendText":  newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/appendText"),
		"thread/realtime/stop":        newCapabilityThreadCommandHandler(svc, capResolver, capabilityRealtime, "realtime/stop"),
	}}
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

// cmd 构造 SendCommand handler。当前所有命令统一用 string args。
// TODO(P7): 按命令类型定义具体 param struct，消除 Args 压平问题。
// 当前只有 /model, /personality, /approvals 真正闭环，其余走骨架通道。
func newThreadCommandHandler(svc Service, command string) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p commandParams) (any, error) {
		return svc.SendCommand(ctx, rpc.ThreadIDFrom(ctx), command, p.Args)
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

func newResumeHandler(svc Service) handler.Func {
	return rpc.ThreadHandler(func(ctx context.Context, p resumeParams) (any, error) {
		threadID := rpc.ThreadIDFrom(ctx)
		ref, err := svc.Get(ctx, threadID)
		if err != nil {
			return nil, err
		}
		return nil, svc.Resume(ctx, ResumeRequest{
			ThreadID: threadID,
			Provider: p.Provider,
			AgentID:  ref.AgentID,
		})
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
