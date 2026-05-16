package skill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/util/repofingerprint"
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
			Scope:                  info.Scope,
			PersonalType:           info.PersonalType,
			Summary:                info.Summary,
			Description:            info.Description,
			Trust:                  info.Trust,
			ContentHash:            info.ContentHash,
			DisableModelInvocation: info.DisableModelInvocation,
			DisclosureTier:         info.DisclosureTier,
		})
	}
	return skillListResult{Skills: items}
}

func skillsWithDisclosureTiers(skills []SkillInfo, source contract.SkillDisclosureTierSource) []SkillInfo {
	tiers := disclosureTierSnapshot(skills, source, time.Now())
	out := make([]SkillInfo, 0, len(skills))
	for _, info := range skills {
		info.DisclosureTier = tiers[info.Name]
		out = append(out, info)
	}
	return out
}

func disclosureTierSnapshot(skills []SkillInfo, source contract.SkillDisclosureTierSource, now time.Time) map[string]string {
	out := make(map[string]string, len(skills))
	if len(skills) == 0 || source == nil || !source.Enabled() {
		return out
	}
	snapshot := source.DisclosureSnapshot()
	for _, info := range skills {
		name := strings.TrimSpace(info.Name)
		if name == "" {
			continue
		}
		score := skillDisclosureEffectiveScore(snapshot.Workspace[name], snapshot.Global[name], now, snapshot.Config)
		out[info.Name] = skillDisclosureTierForScore(score)
	}
	return out
}

func skillDisclosureEffectiveScore(ws, gl *contract.SkillDisclosureSkillStats, now time.Time, cfg contract.SkillDisclosureConfig) float64 {
	if ws != nil && len(ws.Calls) >= cfg.WSMinCalls {
		return skillDisclosureScore(ws, now, cfg.HalfLife, cfg.FrozenDuration)
	}
	if gl == nil {
		if ws == nil {
			return 0
		}
		return cfg.WSWeight * skillDisclosureScore(ws, now, cfg.HalfLife, cfg.FrozenDuration)
	}
	if ws == nil {
		return skillDisclosureScore(gl, now, cfg.HalfLife, cfg.FrozenDuration)
	}
	return cfg.WSWeight*skillDisclosureScore(ws, now, cfg.HalfLife, cfg.FrozenDuration) +
		(1-cfg.WSWeight)*skillDisclosureScore(gl, now, cfg.HalfLife, cfg.FrozenDuration)
}

func skillDisclosureScore(stats *contract.SkillDisclosureSkillStats, now time.Time, halfLife, frozen time.Duration) float64 {
	if stats == nil || len(stats.Calls) == 0 {
		return 0
	}
	hlSec := halfLife.Seconds()
	if hlSec <= 0 {
		return 0
	}
	cutoff := now.Add(-frozen)
	var score float64
	for _, calledAt := range stats.Calls {
		if calledAt.Before(cutoff) {
			continue
		}
		score += math.Pow(2, -now.Sub(calledAt).Seconds()/hlSec)
	}
	return score
}

func skillDisclosureTierForScore(score float64) string {
	switch {
	case score >= 3:
		return "hot"
	case score >= 1:
		return "warm"
	case score > 0:
		return "cold"
	default:
		return "frozen"
	}
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
	if skillRPCCandidateParamError(err) {
		return platformrpc.ErrInvalidParams(err.Error())
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
	case errors.Is(err, ErrInvalidSkillName), errors.Is(err, errInvalidSkillExpandParam), errors.Is(err, ErrInvalidSkillScope), errors.Is(err, ErrSkillSystemScopeRemoved):
		return platformrpc.ErrInvalidParams(err.Error())
	default:
		return nil
	}
}

func skillRPCApprovalError(err error) error {
	switch {
	case errors.Is(err, errSkillApprovalRequired):
		rpcErr := jrpc2.Errorf(platformrpc.CodeInvalidState, "%s", err.Error())
		var required SkillApprovalRequiredError
		if errors.As(err, &required) {
			return rpcErr.WithData(required.Request)
		}
		return rpcErr
	case errors.Is(err, ErrSkillSystemReviewRequired), errors.Is(err, errSkillApprovalDenied), errors.Is(err, errSkillApprovalRequesterUnavailable), errors.Is(err, errSkillApprovalProjectCacheMissing):
		return jrpc2.Errorf(jrpc2.InternalError, "%s", err.Error())
	default:
		return nil
	}
}

func skillRPCCandidateParamError(err error) bool {
	return errors.Is(err, ErrCandidateApprovedByRequired) ||
		errors.Is(err, ErrCandidateMissingFingerprint) ||
		errors.Is(err, ErrCallerFingerprintRequired) ||
		errors.Is(err, ErrRepoFingerprintMismatch) ||
		errors.Is(err, ErrCandidateNotPending)
}

func requireRequestCWD(cwd string) error {
	if strings.TrimSpace(cwd) == "" {
		return skillRPCError(ErrMissingCWD)
	}
	return nil
}

func NewSkillHandlers(svc Service, requester contract.ApprovalRequester) platformrpc.HandlerMapResult {
	return newSkillHandlers(svc, requester)
}

func newSkillHandlers(svc Service, requester contract.ApprovalRequester) platformrpc.HandlerMapResult {
	if impl, ok := svc.(*service); ok {
		impl.approvalRequester = requester
	}
	return platformrpc.HandlerMapResult{Handlers: mergeSkillHandlerMaps(
		skillCoreHandlers(svc),
		skillLocalHandlers(svc),
		skillRemoteHandlers(svc),
		skillPreviewHandlers(svc),
		skillResolutionHandlers(svc),
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
		"skill/list":   platformrpc.StrictHandler(skillListHandler(svc)),
		"skill/expand": platformrpc.StrictHandler(skillExpandHandler(svc)),
		"skills/list":  platformrpc.StrictHandler(skillsListHandler(svc)),
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
		// P0b Step 5: candidate review gate. List + approve + reject share
		// the local-skills namespace because approvals always promote into a
		// project-scope SKILL.md via CreateSkill.
		"skills/candidate/list/pending": platformrpc.StrictHandler(skillCandidateListPendingHandler(svc)),
		"skills/candidate/get":          platformrpc.StrictHandler(skillCandidateGetHandler(svc)),
		"skills/candidate/approve":      platformrpc.StrictHandler(skillCandidateApproveHandler(svc)),
		"skills/candidate/reject":       platformrpc.StrictHandler(skillCandidateRejectHandler(svc)),
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

func skillResolutionHandlers(svc Service) handler.Map {
	return handler.Map{
		"skills/resolution_list":    platformrpc.StrictHandler(skillResolutionListHandler(svc)),
		"skills/resolution_preview": platformrpc.StrictHandler(skillResolutionPreviewHandler(svc)),
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
