package prompt

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"
)

// SectionInvalidator is the prompt section cache invalidation port.
type SectionInvalidator = contract.SectionInvalidator

// AsSectionInvalidator 把prompt处理为sectioninvalidator。
func AsSectionInvalidator(svc Service) SectionInvalidator {
	return svc
}

// InvalidateSections 处理invalidatesections。
func (s *service) InvalidateSections(reason InvalidateReason, names ...string) uint64 {
	generation := s.cache.InvalidateSections(names...)
	s.notifySectionInvalidationProviders(reason, names)
	if s.logger != nil {
		s.logger.Debug("prompt sections invalidated", "reason", reason, "sections", compactSectionNames(names), "generation", generation)
	}
	return generation
}

// notifySectionInvalidationProviders 处理notifysectioninvalidationproviders。
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

// compactSectionNames 处理紧凑列表section名称。
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

func (s *promptService) invalidateSectionAssetCatalogs() {
	s.invalidatePromptSections(contract.DynamicSectionRecallCatalog, contract.DynamicSectionProjectDefaultRules)
}

func (s *promptService) invalidatePromptTemplateCatalogs() {
	s.invalidatePromptSections(
		contract.DynamicSectionAvailableExperts,
		contract.DynamicSectionRecallCatalog,
		contract.DynamicSectionProjectDefaultRules,
	)
}

func (s *promptService) invalidatePromptSections(names ...string) {
	if s == nil || s.sections == nil {
		return
	}
	s.sections.InvalidateSections(contract.InvalidateClear, names...)
}

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

// publishUIPromptIntentChanged 发布UIpromptintentchanged。
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

func promptDraftSetEventKey(result promptintent.DraftSetResult) string {
	keys := make([]string, 0, len(result.Drafts))
	for _, draft := range result.Drafts {
		if key := strings.TrimSpace(draft.DraftKey); key != "" {
			keys = append(keys, key)
		}
	}
	return strings.Join(keys, ",")
}

func promptWriteRPCHandler(promptSvc PromptService, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptWriteParams) (any, error) {
		result, err := handlePromptWrite(ctx, promptSvc, p)
		publishUIPromptsChanged(emit, p.Cwd, p.ID, "", "write", err)
		return result, err
	})
}

func promptDeleteRPCHandler(promptSvc PromptService, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptDeleteParams) (any, error) {
		result, err := handlePromptDelete(ctx, promptSvc, p)
		publishUIPromptsChanged(emit, p.Cwd, p.ID, "", "delete", err)
		return result, err
	})
}

func promptSectionWriteRPCHandler(promptSvc PromptService, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptSectionWriteParams) (any, error) {
		result, err := handlePromptSectionWrite(ctx, promptSvc, p)
		publishUIPromptsChanged(emit, p.Cwd, p.PromptID, "", "section-write", err)
		return result, err
	})
}

func promptSectionDeleteRPCHandler(promptSvc PromptService, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptSectionDeleteParams) (any, error) {
		result, err := handlePromptSectionDelete(ctx, promptSvc, p)
		publishUIPromptsChanged(emit, p.Cwd, p.PromptID, "", "section-delete", err)
		return result, err
	})
}

func promptIntentDraftRPCHandler(
	store contract.PromptStore,
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

func promptIntentCommitRPCHandler(
	store contract.PromptStore,
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

func promptIntentDiscardRPCHandler(store contract.PromptStore, emit func(uidto.UIPromptsChanged)) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p promptintent.DiscardParams) (any, error) {
		result, err := promptintent.HandleDiscard(ctx, store, p)
		publishUIPromptIntentChanged(emit, p.Cwd, result, "discard", err)
		return result, err
	})
}
