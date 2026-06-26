package prompt

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/creachadair/jrpc2/handler"
)

// SectionInvalidator 是 prompt section 缓存失效能力的本包兼容别名。
type SectionInvalidator = contract.SectionInvalidator

// AsSectionInvalidator 将聚合 Service 暴露为 section 失效契约。
func AsSectionInvalidator(svc Service) SectionInvalidator {
	return svc
}

// InvalidateSections 清空指定动态 section 缓存，并通知支持失效回调的 provider。
func (s *service) InvalidateSections(reason InvalidateReason, names ...string) uint64 {
	generation := s.cache.InvalidateSections(names...)
	s.notifySectionInvalidationProviders(reason, names)
	if s.logger != nil {
		s.logger.Debug("prompt sections invalidated", "reason", reason, "sections", compactSectionNames(names), "generation", generation)
	}
	return generation
}

// notifySectionInvalidationProviders 对去重后的 section 名称触发 provider 失效回调。
func (s *service) notifySectionInvalidationProviders(reason InvalidateReason, names []string) {
	if len(names) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		s.dynamicMu.RLock()
		provider := s.dynamic[key]
		s.dynamicMu.RUnlock()
		aware, ok := provider.(InvalidationAwareProvider)
		if ok {
			aware.OnPromptInvalidate(reason)
		}
	}
}

// compactSectionNames 清理并去重 section 名称，供日志输出使用。
func compactSectionNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// invalidateAvailableExperts 清空专家推荐动态 section 缓存。
func (s *promptService) invalidateAvailableExperts() {
	s.invalidatePromptSections(contract.DynamicSectionAvailableExperts)
}

// invalidateRecallCatalog 清空 recall catalog 动态 section 缓存。
func (s *promptService) invalidateRecallCatalog() {
	s.invalidatePromptSections(contract.DynamicSectionRecallCatalog)
}

// invalidateProjectDefaultRules 清空项目默认规则动态 section 缓存。
func (s *promptService) invalidateProjectDefaultRules() {
	s.invalidatePromptSections(contract.DynamicSectionProjectDefaultRules)
}

// invalidateSectionAssetCatalogs 清空依赖 prompt sections 的资产 catalog 缓存。
func (s *promptService) invalidateSectionAssetCatalogs() {
	s.invalidatePromptSections(contract.DynamicSectionRecallCatalog, contract.DynamicSectionProjectDefaultRules)
}

// invalidatePromptTemplateCatalogs 清空依赖 prompt 模板列表的动态 catalog 缓存。
func (s *promptService) invalidatePromptTemplateCatalogs() {
	s.invalidatePromptSections(
		contract.DynamicSectionAvailableExperts,
		contract.DynamicSectionRecallCatalog,
		contract.DynamicSectionProjectDefaultRules,
	)
}

// invalidatePromptSections 通过注入的 SectionInvalidator 发出清缓存请求；未注入时保持无操作。
func (s *promptService) invalidatePromptSections(names ...string) {
	if s == nil || s.sections == nil {
		return
	}
	s.sections.InvalidateSections(contract.InvalidateClear, names...)
}

// publishUIPromptsChanged 在写操作成功后发布 UI 刷新事件。
// err 非空时不发送事件，避免前端对失败写入做乐观刷新。
func publishUIPromptsChanged(
	emit func(uidto.UIPromptsChanged),
	cwd string,
	promptKey string,
	draftKey string,
	action string,
	err error,
) {
	if emit == nil || err != nil {
		return
	}
	emit(uidto.UIPromptsChanged{
		Cwd:       strings.TrimSpace(cwd),
		PromptKey: strings.TrimSpace(promptKey),
		DraftKey:  strings.TrimSpace(draftKey),
		Action:    strings.TrimSpace(action),
	})
}

// publishUIPromptIntentChanged 将 prompt intent 结果转换为 UI 刷新事件。
// draft set 会把多条 draft key 合并，方便前端按同一批次刷新。
func publishUIPromptIntentChanged(
	emit func(uidto.UIPromptsChanged),
	cwd string,
	result any,
	action string,
	err error,
) {
	if err != nil {
		return
	}
	switch value := result.(type) {
	case promptintent.DraftResult:
		publishUIPromptsChanged(emit, cwd, "", value.DraftKey, action, nil)
	case promptintent.DraftSetResult:
		publishUIPromptsChanged(emit, cwd, "", promptDraftSetEventKey(value), action, nil)
	case promptintent.CommitResult:
		publishUIPromptsChanged(emit, cwd, value.PromptKey, value.DraftKey, action, nil)
	case promptintent.DiscardResult:
		publishUIPromptsChanged(emit, cwd, "", value.DraftKey, action, nil)
	}
}

// promptDraftSetEventKey 合并批量草稿的 draft key，用于 UI 事件定位同一批创建结果。
func promptDraftSetEventKey(result promptintent.DraftSetResult) string {
	keys := make([]string, 0, len(result.Drafts))
	for _, draft := range result.Drafts {
		if key := strings.TrimSpace(draft.DraftKey); key != "" {
			keys = append(keys, key)
		}
	}
	return strings.Join(keys, ",")
}

// promptWriteRPCHandler 包装 prompts/write，并在成功后通知前端刷新 prompt 列表。
func promptWriteRPCHandler(promptSvc PromptService, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptWriteParams) (any, error) {
		result, err := handlePromptWrite(ctx, promptSvc, p)
		publishUIPromptsChanged(emit, p.Cwd, p.ID, "", "write", err)
		return result, err
	})
}

// promptDeleteRPCHandler 包装 prompts/delete，并在成功后通知前端移除对应 prompt。
func promptDeleteRPCHandler(promptSvc PromptService, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptDeleteParams) (any, error) {
		result, err := handlePromptDelete(ctx, promptSvc, p)
		publishUIPromptsChanged(emit, p.Cwd, p.ID, "", "delete", err)
		return result, err
	})
}

// promptSectionWriteRPCHandler 包装 prompt-sections/write，并在成功后通知前端刷新父 prompt。
func promptSectionWriteRPCHandler(promptSvc PromptService, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptSectionWriteParams) (any, error) {
		result, err := handlePromptSectionWrite(ctx, promptSvc, p)
		publishUIPromptsChanged(emit, p.Cwd, p.PromptID, "", "section-write", err)
		return result, err
	})
}

// promptSectionDeleteRPCHandler 包装 prompt-sections/delete，并在成功后通知前端刷新父 prompt。
func promptSectionDeleteRPCHandler(promptSvc PromptService, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptSectionDeleteParams) (any, error) {
		result, err := handlePromptSectionDelete(ctx, promptSvc, p)
		publishUIPromptsChanged(emit, p.Cwd, p.PromptID, "", "section-delete", err)
		return result, err
	})
}

// promptIntentDraftRPCHandler 包装 prompt-intents/draft。
// 创建成功后发布 draft 事件；失败时保留原错误返回给 StrictHandler。
func promptIntentDraftRPCHandler(
	store promptstore.Store,
	dream contract.DreamExecutor,
	builtin contract.BuiltinPromptRegistry,
	emit func(uidto.UIPromptsChanged),
) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptintent.DraftParams) (any, error) {
		result, err := promptintent.HandleDraft(ctx, store, dream, builtin, p)
		publishUIPromptIntentChanged(emit, p.Cwd, result, "draft", err)
		return result, err
	})
}

// promptIntentCommitRPCHandler 包装 prompt-intents/commit。
// commit 成功会触发 section cache 失效，并通过 UI 事件刷新正式 prompt 与草稿状态。
func promptIntentCommitRPCHandler(
	store promptstore.Store,
	sectionInvalidator contract.SectionInvalidator,
	builtin contract.BuiltinPromptRegistry,
	emit func(uidto.UIPromptsChanged),
) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptintent.CommitParams) (any, error) {
		result, err := promptintent.HandleCommit(ctx, store, sectionInvalidator, builtin, p)
		publishUIPromptIntentChanged(emit, p.Cwd, result, "commit", err)
		return result, err
	})
}

// promptIntentDiscardRPCHandler 包装 prompt-intents/discard，并在成功后刷新草稿状态。
func promptIntentDiscardRPCHandler(store promptstore.Store, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptintent.DiscardParams) (any, error) {
		result, err := promptintent.HandleDiscard(ctx, store, p)
		publishUIPromptIntentChanged(emit, p.Cwd, result, "discard", err)
		return result, err
	})
}
