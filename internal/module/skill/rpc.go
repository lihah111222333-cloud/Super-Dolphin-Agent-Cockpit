package skill

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func namedContentHandler(fn func(context.Context, string, string) (any, error)) handler.Func {
	return rpc.StrictHandler(func(ctx context.Context, p skillNamedContentParams) (any, error) {
		return fn(ctx, p.Name, p.Content)
	})
}

func NewSkillHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"command/exec": rpc.StrictHandler(func(ctx context.Context, p execParams) (any, error) {
			return svc.ExecCommand(ctx, p.Command, p.Args, p.CWD, p.Env)
		}),
		// skills/list: 扫描本地 skill 目录，返回所有已安装的 skill 元信息。
		// 与 thread/skills/list 不同：后者走 thread 命令通道，返回 thread 绑定的 active skills。
		"skills/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			list, err := svc.ListSkills(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"skills": list}, nil
		}),
		"skills/local/read":      rpc.StrictHandler(func(ctx context.Context, p pathParams) (any, error) { return svc.ReadLocal(ctx, p.Path) }),
		"skills/local/listFiles": rpc.StrictHandler(func(ctx context.Context, p listSkillFilesParams) (any, error) { return svc.ListLocalFiles(ctx, p) }),
		"skills/local/write":     rpc.StrictHandler(func(ctx context.Context, p contentParams) (any, error) { return svc.WriteLocal(ctx, p.Path, p.Content) }),
		"skills/local/importDir": rpc.StrictHandler(func(ctx context.Context, p importSkillDirParams) (any, error) { return svc.ImportLocalDir(ctx, p) }),
		"skills/local/delete":    rpc.StrictHandler(func(ctx context.Context, p deleteLocalSkillParams) (any, error) { return svc.DeleteLocal(ctx, p.Name) }),
		"skills/remote/list":     rpc.StrictHandler(func(ctx context.Context, p skillRemoteReadParams) (any, error) { return svc.ReadRemote(ctx, p.URL) }),
		"skills/remote/export": namedContentHandler(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteRemote(ctx, name, content)
		}),
		"skills/remote/read": rpc.StrictHandler(func(ctx context.Context, p skillRemoteReadParams) (any, error) { return svc.ReadRemote(ctx, p.URL) }),
		"skills/remote/write": namedContentHandler(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteRemote(ctx, name, content)
		}),
		"skills/config/read": rpc.StrictHandler(func(ctx context.Context, p skillConfigReadParams) (any, error) { return svc.ReadConfig(ctx, p.AgentID) }),
		// Legacy RPC key: V2 uses skills/config/write for saving the main skill file content.
		"skills/config/write": namedContentHandler(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteSkillContent(ctx, name, content)
		}),
		"skills/summary/write": rpc.StrictHandler(func(ctx context.Context, p skillSummaryWriteParams) (any, error) {
			return svc.WriteSummary(ctx, p.Name, p.Summary)
		}),
		"skills/match/preview": rpc.StrictHandler(func(ctx context.Context, p skillMatchPreviewParams) (any, error) {
			return svc.MatchPreview(ctx, p.AgentID, p.ThreadID, p.Text, p.Input)
		}),
	}}
}
