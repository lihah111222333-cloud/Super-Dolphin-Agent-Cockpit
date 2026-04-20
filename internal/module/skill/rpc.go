package skill

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func namedContentHandler(fn func(context.Context, string, string) (any, error)) handler.Func {
	return rpc.StrictHandler(func(ctx context.Context, p skillNamedContentParams) (any, error) {
		return fn(ctx, p.Name, p.Content)
	})
}

func skillListPayload(skills []SkillInfo) skillListResult {
	items := make([]skillListItem, 0, len(skills))
	for _, info := range skills {
		items = append(items, skillListItem{
			Name:                   info.Name,
			Summary:                info.Summary,
			Description:            info.Description,
			Trust:                  info.Trust,
			ContentHash:            info.ContentHash,
			DisableModelInvocation: info.DisableModelInvocation,
		})
	}
	return skillListResult{Skills: items}
}

func skillRPCError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return err
	}
	switch {
	case errors.Is(err, ErrMissingCWD):
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
	case errors.Is(err, os.ErrNotExist):
		return rpc.ErrNotFound(err.Error())
	case errors.Is(err, ErrInvalidSkillName), errors.Is(err, errInvalidSkillExpandParam):
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
	case errors.Is(err, errSkillApprovalDenied), errors.Is(err, errSkillApprovalRequesterUnavailable), errors.Is(err, errSkillApprovalProjectCacheMissing):
		return jrpc2.Errorf(jrpc2.InternalError, "%s", err.Error())
	default:
		return err
	}
}

func requireRequestCWD(cwd string) error {
	if strings.TrimSpace(cwd) == "" {
		return skillRPCError(ErrMissingCWD)
	}
	return nil
}

func NewSkillHandlers(svc Service, requester contract.ApprovalRequester) rpc.HandlerMapResult {
	return newSkillHandlers(svc, requester)
}

func newSkillHandlers(svc Service, requester contract.ApprovalRequester) rpc.HandlerMapResult {
	if impl, ok := svc.(*service); ok {
		impl.approvalRequester = requester
	}
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"command/exec": rpc.StrictHandler(func(ctx context.Context, p execParams) (any, error) {
			return svc.ExecCommand(ctx, p.Command, p.Args, p.CWD, p.Env)
		}),
		"skill/list": rpc.StrictHandler(func(ctx context.Context, p skillListParams) (skillListResult, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return skillListResult{}, err
			}
			list, err := svc.ListSkills(WithCWD(ctx, p.CWD))
			if err != nil {
				return skillListResult{}, skillRPCError(err)
			}
			return skillListPayload(list), nil
		}),
		"skill/expand": rpc.StrictHandler(func(ctx context.Context, p skillExpandParams) (skillExpandResult, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return skillExpandResult{}, err
			}
			result, err := expandSkillWithApproval(WithCWD(ctx, p.CWD), svc, p)
			if err != nil {
				return skillExpandResult{}, skillRPCError(err)
			}
			return result, nil
		}),
		// skills/list: 扫描本地 skill 目录，返回所有已安装的 skill 元信息。
		// 与 thread/skills/list 不同：后者走 thread 命令通道，返回 thread 绑定的 active skills。
		"skills/list": rpc.StrictHandler(func(ctx context.Context, p skillListParams) (any, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			list, err := svc.ListSkills(WithCWD(ctx, p.CWD))
			if err != nil {
				return nil, skillRPCError(err)
			}
			return map[string]any{"skills": list}, nil
		}),
		"skills/local/read": rpc.StrictHandler(func(ctx context.Context, p pathParams) (any, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			return svc.ReadLocal(WithCWD(ctx, p.CWD), p.Path)
		}),
		"skills/local/listFiles": rpc.StrictHandler(func(ctx context.Context, p listSkillFilesParams) (any, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			return svc.ListLocalFiles(WithCWD(ctx, p.CWD), p)
		}),
		"skills/local/write": rpc.StrictHandler(func(ctx context.Context, p contentParams) (any, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			return svc.WriteLocal(WithCWD(ctx, p.CWD), p.Path, p.Content)
		}),
		"skills/local/importDir": rpc.StrictHandler(func(ctx context.Context, p importSkillDirParams) (any, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			return svc.ImportLocalDir(WithCWD(ctx, p.CWD), p)
		}),
		"skills/local/delete": rpc.StrictHandler(func(ctx context.Context, p deleteLocalSkillParams) (any, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			return svc.DeleteLocal(WithCWD(ctx, p.CWD), p.Name)
		}),
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
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			return svc.MatchPreview(WithCWD(ctx, p.CWD), p.AgentID, p.ThreadID, p.Text, p.Input)
		}),
		// P20.1 Phase 6：按名读 SKILL.md body（可选 Markdown 锚点切片）。
		// 对外暴露为 MCP 工具 `skill_expand_body` 时，由 mcp-orch 将本方法签名
		// 映射为符合 P20.1 §3.1 的工具 schema。
		"skills/expandBody": rpc.StrictHandler(func(ctx context.Context, p ExpandBodyParams) (any, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			return svc.ExpandBody(WithCWD(ctx, p.CWD), p)
		}),
		// P20.1 Phase 6：按名 + 相对路径读取 skill 目录内资源文件。
		// 对外暴露为 MCP 工具 `skill_read_resource`。
		"skills/readResource": rpc.StrictHandler(func(ctx context.Context, p ReadResourceParams) (any, error) {
			if err := requireRequestCWD(p.CWD); err != nil {
				return nil, err
			}
			return svc.ReadResource(WithCWD(ctx, p.CWD), p)
		}),
	}}
}

func expandSkillWithApproval(ctx context.Context, svc Service, p skillExpandParams) (skillExpandResult, error) {
	if impl, ok := svc.(*service); ok {
		return impl.expandWithApproval(ctx, p)
	}
	return svc.Expand(ctx, p)
}
