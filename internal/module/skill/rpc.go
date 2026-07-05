package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/toolstore"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/httpegress"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// namedContentHandler 将 name/content 形态的 skill RPC 包装成严格参数 handler。
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

// ReadRemote 从公开 URL 读取远程 skill 文本。
// URL 必须通过 egress 白名单校验，响应体会多读 1 字节用于识别超限并显式报错。
func (s *service) ReadRemote(ctx context.Context, url string) (any, error) {
	targetURL, err := httpegress.ValidatePublicURL(url)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch remote skill failed status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSkillFileBytes {
		return nil, fmt.Errorf("remote skill too large: exceeds %d bytes", maxSkillFileBytes)
	}
	return map[string]any{"skill": map[string]any{"url": targetURL, "content": string(body)}}, nil
}

// WriteRemote 保留旧 RPC 入口但拒绝远程写入。
// 当前实现不允许 system scope 通过该路径落盘，调用方会收到显式错误。
func (s *service) WriteRemote(ctx context.Context, name, content string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	return nil, ErrSkillSystemScopeRemoved
}

// ReadConfig 读取 agent 级 skill 配置。
// 当前 agent 级持久化契约尚未配置，因此返回显式的空绑定状态而不是伪造技能列表。
func (s *service) ReadConfig(_ context.Context, agentID string) (any, error) {
	// agent 级 skill 绑定还没有持久化存储契约，调用方必须看到未绑定状态。
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("agent_id is required")
	}
	return map[string]any{
		"agent_id":       agentID,
		"skills":         []string{},
		"session_bound":  false,
		"configured":     false,
		"binding_count":  0,
		"binding_source": "stub",
	}, nil
}

// WriteSkillContent 保留旧 RPC 名称但拒绝 system scope 内容写入。
// 真实写入必须走 WriteLocal/CreateSkill，并携带当前 scope 的路径和审批边界。
func (s *service) WriteSkillContent(ctx context.Context, name, content string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	return nil, ErrSkillSystemScopeRemoved
}

// WriteSummary 保留旧摘要写入入口但不再直接改 skill。
// 摘要治理必须走当前 skill 元数据路径，避免绕过审批和 mirror 同步。
func (s *service) WriteSummary(ctx context.Context, name, summary string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	return nil, ErrSkillSystemScopeRemoved
}

// skillRPCError 将 skill 模块内部错误统一映射为前端可识别的 JSON-RPC 错误。
// 已经带 jrpc2 code 的错误保持原样，避免重复包裹后丢失调用方需要展示的 code。
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

// skillRPCCommonError 把审批上下文外即可判定的通用错误转成 JSON-RPC 错误。
// 缺少 cwd、文件不存在、同名冲突、参数非法和 toolstore 未配置在这里映射；未命中的错误返回 nil，继续交给审批映射或由调用方保留原始错误。
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
	case toolstore.InvalidParamsError(err):
		return platformrpc.ErrInvalidParams(err.Error())
	case errors.Is(err, toolstore.ErrStoreNotConfigured):
		return platformrpc.ErrInvalidState(err.Error())
	case errors.Is(err, toolstore.ErrNotFound):
		return platformrpc.ErrNotFound(err.Error())
	default:
		return nil
	}
}

// skillRPCApprovalError 将需要人工审批的 skill 错误保留为内部 RPC 错误。
// 这类错误由前端按内容触发审批交互，不能被普通 invalid params 覆盖。
func skillRPCApprovalError(err error) error {
	switch {
	case errors.Is(err, ErrSkillSystemReviewRequired):
		return jrpc2.Errorf(jrpc2.InternalError, "%s", err.Error())
	default:
		return nil
	}
}

// requireRequestCWD 校验 RPC 请求必须携带 cwd。
// skill 读写都依赖项目根推导，缺失 cwd 时直接阻断，避免落到错误的默认目录。
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

// NewHandlersForService 为测试和轻量装配暴露 skill handler 注册表。
// 生产路径仍通过 NewSkillHandlers 接收 fx 依赖。
func NewHandlersForService(svc Service, dreams ...contract.DreamExecutor) platformrpc.HandlerMapResult {
	return newSkillHandlers(svc, dreams...)
}

// newSkillHandlers 汇总 skill 模块暴露给 host 的全部 RPC handler。
// 这里仅做注册表组装，具体错误映射保留在各 handler 内，方便按能力边界返回 code。
func newSkillHandlers(svc Service, dreams ...contract.DreamExecutor) platformrpc.HandlerMapResult {
	var dream contract.DreamExecutor
	if len(dreams) > 0 {
		dream = dreams[0]
	}
	return platformrpc.HandlerMapResult{Handlers: mergeSkillHandlerMaps(
		skillCoreHandlers(svc),
		skillLocalHandlers(svc),
		skillRemoteHandlers(svc),
		skillToolHandlers(svc),
		skillPreviewHandlers(svc),
		skillResolutionHandlers(svc),
		skillSummarySuggestHandlers(dream),
	)}
}

func mergeSkillHandlerMaps(parts ...handler.Map) handler.Map {
	merged := handler.Map{}
	for _, part := range parts {
		maps.Copy(merged, part)
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

func skillToolHandlers(svc Service) handler.Map {
	impl, ok := svc.(*service)
	if !ok || impl == nil {
		return toolstore.Handlers(nil, nil, skillRPCError)
	}
	return toolstore.Handlers(impl.skillTools, impl.resolveSkillToolCWD, skillRPCError)
}

// skillCreateHandler 是 host/UI 创建项目 skill 的 RPC 包装。
// 它先校验 cwd 并写入上下文，再交给 CreateSkill 走 project scope 的 WriteLocal 路径。
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

// skillResolutionApplyHandler 执行用户确认过的 mirror 修复动作。
// cwd 会在这里写入上下文，后续 preview hash 校验失败时直接返回 RPC 错误。
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

// scopedSkillContext 校验 cwd 后把项目路径写入 context。
// service 层只从 context 读取工作目录，因此这里是 RPC 边界的 fail-fast 入口。
func scopedSkillContext(ctx context.Context, cwd string) (context.Context, error) {
	if err := requireRequestCWD(cwd); err != nil {
		return nil, err
	}
	return WithCWD(ctx, cwd), nil
}
