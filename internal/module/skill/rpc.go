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
	case errors.Is(err, ErrInvalidSkillName), errors.Is(err, errInvalidSkillExpandParam), errors.Is(err, ErrInvalidSkillScope):
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
	case errors.Is(err, errSkillApprovalRequired):
		return jrpc2.Errorf(-31002, "%s", err.Error())
	case errors.Is(err, ErrSkillSystemReviewRequired), errors.Is(err, errSkillApprovalDenied), errors.Is(err, errSkillApprovalRequesterUnavailable), errors.Is(err, errSkillApprovalProjectCacheMissing):
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
	return rpc.HandlerMapResult{Handlers: mergeSkillHandlerMaps(
		skillCoreHandlers(svc),
		skillLocalHandlers(svc),
		skillRemoteHandlers(svc),
		skillPreviewHandlers(svc),
	)}
}

func mergeSkillHandlerMaps(parts ...handler.Map) handler.Map {
	merged := handler.Map{}
	for _, part := range parts {
		for name, fn := range part {
			merged[name] = fn
		}
	}
	return merged
}

func skillCoreHandlers(svc Service) handler.Map {
	return handler.Map{
		"command/exec": rpc.StrictHandler(func(ctx context.Context, p execParams) (any, error) {
			return svc.ExecCommand(ctx, p.Command, p.Args, p.CWD, p.Env)
		}),
		"skill/list":   rpc.StrictHandler(skillListHandler(svc)),
		"skill/expand": rpc.StrictHandler(skillExpandHandler(svc)),
		"skills/list":  rpc.StrictHandler(skillsListHandler(svc)),
	}
}

func skillLocalHandlers(svc Service) handler.Map {
	return handler.Map{
		"skills/local/read":      rpc.StrictHandler(skillLocalReadHandler(svc)),
		"skills/local/listFiles": rpc.StrictHandler(skillLocalListFilesHandler(svc)),
		"skills/local/write":     rpc.StrictHandler(skillLocalWriteHandler(svc)),
		"skills/local/importDir": rpc.StrictHandler(skillLocalImportDirHandler(svc)),
		"skills/local/delete":    rpc.StrictHandler(skillLocalDeleteHandler(svc)),
		"skills/create":          rpc.StrictHandler(skillCreateHandler(svc)),
	}
}

// skillCreateHandler is the host/UI RPC wrapper for project-scope skill
// creation. It enforces cwd before scoping and then delegates to CreateSkill
// (which routes through WriteLocal(..., scope=project)). See P21 P0a.
func skillCreateHandler(svc Service) func(context.Context, createSkillParams) (any, error) {
	return func(ctx context.Context, p createSkillParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		result, err := svc.CreateSkill(scopedCtx, p)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillRemoteHandlers(svc Service) handler.Map {
	return handler.Map{
		"skills/remote/list": rpc.StrictHandler(func(ctx context.Context, p skillRemoteReadParams) (any, error) {
			return svc.ReadRemote(ctx, p.URL)
		}),
		"skills/remote/export": namedContentHandler(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteRemote(ctx, name, content)
		}),
		"skills/remote/read": rpc.StrictHandler(func(ctx context.Context, p skillRemoteReadParams) (any, error) {
			return svc.ReadRemote(ctx, p.URL)
		}),
		"skills/remote/write": namedContentHandler(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteRemote(ctx, name, content)
		}),
		"skills/config/read": rpc.StrictHandler(func(ctx context.Context, p skillConfigReadParams) (any, error) {
			return svc.ReadConfig(ctx, p.AgentID)
		}),
		"skills/config/write": namedContentHandler(func(ctx context.Context, name, content string) (any, error) {
			return svc.WriteSkillContent(ctx, name, content)
		}),
		"skills/summary/write": rpc.StrictHandler(func(ctx context.Context, p skillSummaryWriteParams) (any, error) {
			return svc.WriteSummary(ctx, p.Name, p.Summary)
		}),
	}
}

func skillPreviewHandlers(svc Service) handler.Map {
	return handler.Map{
		"skills/match/preview": rpc.StrictHandler(skillMatchPreviewHandler(svc)),
		"skills/expandBody":    rpc.StrictHandler(skillExpandBodyHandler(svc)),
		"skills/readResource":  rpc.StrictHandler(skillReadResourceHandler(svc)),
	}
}

func skillListHandler(svc Service) func(context.Context, skillListParams) (skillListResult, error) {
	return func(ctx context.Context, p skillListParams) (skillListResult, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return skillListResult{}, err
		}
		list, err := svc.ListSkills(scopedCtx)
		if err != nil {
			return skillListResult{}, skillRPCError(err)
		}
		return skillListPayload(list), nil
	}
}

func skillsListHandler(svc Service) func(context.Context, skillListParams) (any, error) {
	return func(ctx context.Context, p skillListParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		list, err := svc.ListSkills(scopedCtx)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return map[string]any{"skills": list}, nil
	}
}

func skillExpandHandler(svc Service) func(context.Context, skillExpandParams) (skillExpandResult, error) {
	return func(ctx context.Context, p skillExpandParams) (skillExpandResult, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return skillExpandResult{}, err
		}
		result, err := expandSkillWithApproval(scopedCtx, svc, p)
		if err != nil {
			return skillExpandResult{}, skillRPCError(err)
		}
		return result, nil
	}
}

func skillLocalReadHandler(svc Service) func(context.Context, pathParams) (any, error) {
	return func(ctx context.Context, p pathParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		return svc.ReadLocal(scopedCtx, p.Path)
	}
}

func skillLocalListFilesHandler(svc Service) func(context.Context, listSkillFilesParams) (any, error) {
	return func(ctx context.Context, p listSkillFilesParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		return svc.ListLocalFiles(scopedCtx, p)
	}
}

func skillLocalWriteHandler(svc Service) func(context.Context, contentParams) (any, error) {
	return func(ctx context.Context, p contentParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		return svc.WriteLocal(scopedCtx, p.Path, p.Content, p.Scope)
	}
}

func skillLocalImportDirHandler(svc Service) func(context.Context, importSkillDirParams) (any, error) {
	return func(ctx context.Context, p importSkillDirParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		return svc.ImportLocalDir(scopedCtx, p)
	}
}

func skillLocalDeleteHandler(svc Service) func(context.Context, deleteLocalSkillParams) (any, error) {
	return func(ctx context.Context, p deleteLocalSkillParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		return svc.DeleteLocal(scopedCtx, p.Name)
	}
}

func skillMatchPreviewHandler(svc Service) func(context.Context, skillMatchPreviewParams) (any, error) {
	return func(ctx context.Context, p skillMatchPreviewParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		return svc.MatchPreview(scopedCtx, p.AgentID, p.ThreadID, p.Text, p.Input)
	}
}

func skillExpandBodyHandler(svc Service) func(context.Context, ExpandBodyParams) (any, error) {
	return func(ctx context.Context, p ExpandBodyParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		return svc.ExpandBody(scopedCtx, p)
	}
}

func skillReadResourceHandler(svc Service) func(context.Context, ReadResourceParams) (any, error) {
	return func(ctx context.Context, p ReadResourceParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		return svc.ReadResource(scopedCtx, p)
	}
}

func scopedSkillContext(ctx context.Context, cwd string) (context.Context, error) {
	if err := requireRequestCWD(cwd); err != nil {
		return nil, err
	}
	return WithCWD(ctx, cwd), nil
}

func expandSkillWithApproval(ctx context.Context, svc Service, p skillExpandParams) (skillExpandResult, error) {
	if impl, ok := svc.(*service); ok {
		return impl.expandWithApproval(ctx, p)
	}
	return svc.Expand(ctx, p)
}
