package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func namedContentHandler(fn func(context.Context, skillNamedContentParams) (any, error)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p skillNamedContentParams) (any, error) {
		return fn(ctx, p)
	})
}

func skillListPayload(skills []SkillInfo) skillListResult {
	items := make([]skillListItem, 0, len(skills))
	for _, info := range skills {
		items = append(items, skillListItem{
			Name:                   info.Name,
			DisplayName:            info.DisplayName,
			Scope:                  info.Scope,
			PersonalType:           info.PersonalType,
			Summary:                info.Summary,
			Description:            info.Description,
			Trust:                  info.Trust,
			ContentHash:            info.ContentHash,
			DisableModelInvocation: info.DisableModelInvocation,
		})
	}
	return skillListResult{Skills: items}
}

func skillsListPayload(skills []SkillInfo) skillListResult {
	result := skillListPayload(skills)
	for idx, info := range skills {
		result.Skills[idx].Dir = info.Dir
		result.Skills[idx].SkillFile = skillMainFilePath(info.Dir)
	}
	return result
}

func skillMainFilePath(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, skillMainFile)
}

func skillRPCError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return err
	}
	if mapped := skillRPCCommonError(err); mapped != nil {
		return mapped
	}
	if mapped := skillRPCApprovalError(err); mapped != nil {
		return mapped
	}
	return err
}

func skillRPCCommonError(err error) error {
	switch {
	case errors.Is(err, ErrMissingCWD):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, os.ErrNotExist):
		return platformrpc.ErrNotFound(err.Error())
	case errors.Is(err, ErrSkillSameNameConflict):
		return platformrpc.ErrConflict(err.Error())
	case errors.Is(err, ErrInvalidSkillName), errors.Is(err, ErrInvalidSkillScope), errors.Is(err, ErrSkillSystemScopeRemoved):
		return platformrpc.ErrInvalidParams(err.Error())
	default:
		return nil
	}
}

func skillRPCApprovalError(err error) error {
	switch {
	case errors.Is(err, ErrSkillSystemReviewRequired):
		return jrpc2.Errorf(jrpc2.InternalError, "%s", err.Error())
	default:
		return nil
	}
}

func requireRequestCWD(cwd string) error {
	if strings.TrimSpace(cwd) == "" {
		return skillRPCError(ErrMissingCWD)
	}
	return nil
}

// NewSkillHandlers 创建技能处理器。
func NewSkillHandlers(deps skillHandlerDeps) platformrpc.HandlerMapResult {
	return newSkillHandlers(deps.Service, deps.DreamExecutor)
}

func newSkillHandlers(svc Service, dreams ...contract.DreamExecutor) platformrpc.HandlerMapResult {
	var dream contract.DreamExecutor
	if len(dreams) > 0 {
		dream = dreams[0]
	}
	return platformrpc.HandlerMapResult{Handlers: mergeSkillHandlerMaps(
		skillCoreHandlers(svc),
		skillLocalHandlers(svc),
		skillRemoteHandlers(svc),
		skillPreviewHandlers(svc),
		skillResolutionHandlers(svc),
		skillSummarySuggestHandlers(dream),
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
		"command/exec": platformrpc.StrictHandler(func(ctx context.Context, p execParams) (any, error) {
			return svc.ExecCommand(ctx, p.Command, p.Args, p.CWD, p.Env)
		}),
		"skill/list":  platformrpc.StrictHandler(skillListHandler(svc)),
		"skills/list": platformrpc.StrictHandler(skillsListHandler(svc)),
	}
}

func skillLocalHandlers(svc Service) handler.Map {
	return handler.Map{
		"skills/local/read":      platformrpc.StrictHandler(skillLocalReadHandler(svc)),
		"skills/local/listFiles": platformrpc.StrictHandler(skillLocalListFilesHandler(svc)),
		"skills/local/write":     platformrpc.StrictHandler(skillLocalWriteHandler(svc)),
		"skills/local/importDir": platformrpc.StrictHandler(skillLocalImportDirHandler(svc)),
		"skills/local/delete":    platformrpc.StrictHandler(skillLocalDeleteHandler(svc)),
		"skills/create":          platformrpc.StrictHandler(skillCreateHandler(svc)),
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
		"skills/remote/list": platformrpc.StrictHandler(func(ctx context.Context, p skillRemoteReadParams) (any, error) {
			return svc.ReadRemote(ctx, p.URL)
		}),
		"skills/remote/export": namedContentHandler(func(ctx context.Context, p skillNamedContentParams) (any, error) {
			return svc.WriteRemote(ctx, p.Name, p.Content)
		}),
		"skills/remote/read": platformrpc.StrictHandler(func(ctx context.Context, p skillRemoteReadParams) (any, error) {
			return svc.ReadRemote(ctx, p.URL)
		}),
		"skills/remote/write": namedContentHandler(func(ctx context.Context, p skillNamedContentParams) (any, error) {
			return svc.WriteRemote(ctx, p.Name, p.Content)
		}),
		"skills/config/read": platformrpc.StrictHandler(func(ctx context.Context, p skillConfigReadParams) (any, error) {
			return svc.ReadConfig(ctx, p.AgentID)
		}),
		"skills/config/write": namedContentHandler(func(ctx context.Context, p skillNamedContentParams) (any, error) {
			return svc.WriteSkillContent(ctx, p.Name, p.Content)
		}),
		"skills/summary/write": platformrpc.StrictHandler(func(ctx context.Context, p skillSummaryWriteParams) (any, error) {
			return svc.WriteSummary(ctx, p.Name, p.Summary)
		}),
	}
}

func skillPreviewHandlers(svc Service) handler.Map {
	return handler.Map{
		"skills/match/preview": platformrpc.StrictHandler(skillMatchPreviewHandler(svc)),
	}
}

func skillSummarySuggestHandlers(dream contract.DreamExecutor) handler.Map {
	return handler.Map{
		"skills/summary/suggest": platformrpc.StrictHandler(skillSummarySuggestHandler(dream)),
	}
}

func skillSummarySuggestHandler(dream contract.DreamExecutor) func(context.Context, skillSummarySuggestParams) (skillSummarySuggestResult, error) {
	return func(ctx context.Context, p skillSummarySuggestParams) (skillSummarySuggestResult, error) {
		description, err := suggestSkillSummary(ctx, dream, p)
		if err != nil {
			return skillSummarySuggestResult{}, err
		}
		return skillSummarySuggestResult{Description: description}, nil
	}
}

func skillResolutionHandlers(svc Service) handler.Map {
	return handler.Map{
		"skills/resolution_list":    platformrpc.StrictHandler(skillResolutionListHandler(svc)),
		"skills/resolution_preview": platformrpc.StrictHandler(skillResolutionPreviewHandler(svc)),
		"skills/resolution_apply":   platformrpc.StrictHandler(skillResolutionApplyHandler(svc)),
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
		return skillsListPayload(list), nil
	}
}

func skillLocalReadHandler(svc Service) func(context.Context, pathParams) (any, error) {
	return func(ctx context.Context, p pathParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		result, err := svc.ReadLocal(scopedCtx, p.Path)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillLocalListFilesHandler(svc Service) func(context.Context, listSkillFilesParams) (any, error) {
	return func(ctx context.Context, p listSkillFilesParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		result, err := svc.ListLocalFiles(scopedCtx, p)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillLocalWriteHandler(svc Service) func(context.Context, contentParams) (any, error) {
	return func(ctx context.Context, p contentParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		result, err := svc.WriteLocal(scopedCtx, p.Path, p.Content, p.Scope, p.PersonalType)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillLocalImportDirHandler(svc Service) func(context.Context, importSkillDirParams) (any, error) {
	return func(ctx context.Context, p importSkillDirParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		result, err := svc.ImportLocalDir(scopedCtx, p)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillLocalDeleteHandler(svc Service) func(context.Context, deleteLocalSkillParams) (any, error) {
	return func(ctx context.Context, p deleteLocalSkillParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.Scope) == "" {
			return nil, skillRPCError(fmt.Errorf("%w: scope is required", ErrInvalidSkillScope))
		}
		result, err := svc.DeleteLocal(scopedCtx, DeleteSkillParams{
			Name:         p.Name,
			Scope:        p.Scope,
			PersonalType: p.PersonalType,
		})
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillMatchPreviewHandler(svc Service) func(context.Context, skillMatchPreviewParams) (any, error) {
	return func(ctx context.Context, p skillMatchPreviewParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		result, err := svc.MatchPreview(scopedCtx, p.AgentID, p.ThreadID, p.Text, p.Input)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillResolutionListHandler(svc Service) func(context.Context, skillResolutionListParams) (any, error) {
	return func(_ context.Context, p skillResolutionListParams) (any, error) {
		if err := requireRequestCWD(p.CWD); err != nil {
			return nil, err
		}
		impl, ok := svc.(*service)
		if !ok {
			return nil, skillRPCError(errors.New("skill resolution service is not configured"))
		}
		result, err := impl.listSkillResolutions(p.CWD)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillResolutionPreviewHandler(svc Service) func(context.Context, skillResolutionPreviewParams) (any, error) {
	return func(_ context.Context, p skillResolutionPreviewParams) (any, error) {
		if err := requireRequestCWD(p.CWD); err != nil {
			return nil, err
		}
		impl, ok := svc.(*service)
		if !ok {
			return nil, skillRPCError(errors.New("skill resolution service is not configured"))
		}
		result, err := impl.previewSkillResolution(p)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillResolutionApplyHandler(svc Service) func(context.Context, skillResolutionApplyParams) (any, error) {
	return func(ctx context.Context, p skillResolutionApplyParams) (any, error) {
		if err := requireRequestCWD(p.CWD); err != nil {
			return nil, err
		}
		impl, ok := svc.(*service)
		if !ok {
			return nil, skillRPCError(errors.New("skill resolution service is not configured"))
		}
		result, err := impl.applySkillResolution(WithCWD(ctx, p.CWD), p)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func scopedSkillContext(ctx context.Context, cwd string) (context.Context, error) {
	if err := requireRequestCWD(cwd); err != nil {
		return nil, err
	}
	return WithCWD(ctx, cwd), nil
}
