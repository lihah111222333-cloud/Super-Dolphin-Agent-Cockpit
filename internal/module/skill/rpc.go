package skill

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/repofingerprint"
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
		rpcErr := jrpc2.Errorf(-31002, "%s", err.Error())
		var required SkillApprovalRequiredError
		if errors.As(err, &required) {
			return rpcErr.WithData(required.Request)
		}
		return rpcErr
	case errors.Is(err, ErrSkillSystemReviewRequired), errors.Is(err, errSkillApprovalDenied), errors.Is(err, errSkillApprovalRequesterUnavailable), errors.Is(err, errSkillApprovalProjectCacheMissing):
		return jrpc2.Errorf(jrpc2.InternalError, "%s", err.Error())
	case errors.Is(err, ErrCandidateApprovedByRequired),
		errors.Is(err, ErrCandidateMissingFingerprint),
		errors.Is(err, ErrCallerFingerprintRequired),
		errors.Is(err, ErrRepoFingerprintMismatch),
		errors.Is(err, ErrCandidateNotPending):
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
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
		// P0b Step 5: candidate review gate. List + approve + reject share
		// the local-skills namespace because approvals always promote into a
		// project-scope SKILL.md via CreateSkill.
		"skills/candidate/list/pending": rpc.StrictHandler(skillCandidateListPendingHandler(svc)),
		"skills/candidate/get":          rpc.StrictHandler(skillCandidateGetHandler(svc)),
		"skills/candidate/approve":      rpc.StrictHandler(skillCandidateApproveHandler(svc)),
		"skills/candidate/reject":       rpc.StrictHandler(skillCandidateRejectHandler(svc)),
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

// skillCandidateApproveHandler delegates to ApproveCandidate after
// scoping cwd onto the request context. CreateSkill (called by
// ApproveCandidate) enforces ErrMissingCWD when cwd is absent, so the
// scopedSkillContext call here is the explicit happy-path injection.
func skillCandidateApproveHandler(svc Service) func(context.Context, approveCandidateRPCParams) (any, error) {
	return func(ctx context.Context, p approveCandidateRPCParams) (any, error) {
		scopedCtx, err := scopedSkillContext(ctx, p.CWD)
		if err != nil {
			return nil, err
		}
		callerFP, err := repofingerprint.Compute(p.CWD)
		if err != nil {
			return nil, skillRPCError(ErrCallerFingerprintRequired)
		}
		result, err := svc.ApproveCandidate(scopedCtx, ApproveCandidateParams{
			CandidateID:           p.CandidateID,
			ApprovedBy:            p.ApprovedBy,
			Reason:                p.Reason,
			CallerRepoFingerprint: callerFP,
		})
		if err != nil {
			return nil, skillRPCError(err)
		}
		return result, nil
	}
}

func skillCandidateRejectHandler(svc Service) func(context.Context, rejectCandidateRPCParams) (any, error) {
	return func(ctx context.Context, p rejectCandidateRPCParams) (any, error) {
		if err := requireRequestCWD(p.CWD); err != nil {
			return nil, err
		}
		callerFP, err := repofingerprint.Compute(p.CWD)
		if err != nil {
			return nil, skillRPCError(ErrCallerFingerprintRequired)
		}
		if err := svc.RejectCandidate(ctx, RejectCandidateParams{CandidateID: p.CandidateID, Reason: p.Reason, CallerRepoFingerprint: callerFP}); err != nil {
			return nil, skillRPCError(err)
		}
		return map[string]bool{"ok": true}, nil
	}
}

func skillCandidateGetHandler(svc Service) func(context.Context, getCandidateRPCParams) (any, error) {
	return func(ctx context.Context, p getCandidateRPCParams) (any, error) {
		row, err := svc.GetCandidateByID(ctx, p.CandidateID)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return row, nil
	}
}

// skillCandidateListPendingHandler is a read-only paginated list. The
// returned CandidateListItem values exclude SkillMD and RedactedSample.
func skillCandidateListPendingHandler(svc Service) func(context.Context, listPendingCandidatesRPCParams) (any, error) {
	return func(ctx context.Context, p listPendingCandidatesRPCParams) (any, error) {
		if err := requireRequestCWD(p.CWD); err != nil {
			return nil, err
		}
		callerFP, err := repofingerprint.Compute(p.CWD)
		if err != nil {
			return nil, skillRPCError(ErrCallerFingerprintRequired)
		}
		rows, err := svc.ListPendingCandidates(ctx, callerFP, p.Limit, p.Offset)
		if err != nil {
			return nil, skillRPCError(err)
		}
		return listPendingCandidatesRPCResult{Candidates: rows}, nil
	}
}
