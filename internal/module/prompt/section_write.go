package prompt

import (
	"context"
	"strings"

	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// writePromptSectionInTx 在单个事务中校验父 prompt scope、写入 section，并维护 recall topic 索引。
// recall 写入会先做同 cwd 去重锁，防止并发请求生成重复 topic。
func writePromptSectionInTx(
	ctx context.Context,
	store promptstore.Store,
	requestScope, promptKey string,
	req PromptSectionWriteRequest,
) (*promptstore.PromptTemplateSection, error) {
	var saved *promptstore.PromptTemplateSection
	err := store.WithTx(ctx, func(txStore promptstore.Store) error {
		template, gerr := txStore.Get(ctx, promptKey)
		if gerr != nil {
			return gerr
		}
		if err := validatePromptMutationScope(template, requestScope, req.Scope, req.ScopeSet); err != nil {
			return err
		}
		recallWrite := strings.TrimSpace(strings.ToLower(req.TriggerType)) == "recall"
		if recallWrite {
			targetScope := promptRecallDuplicateTargetScope(template, requestScope, req.Scope, req.ScopeSet)
			if err := rejectDuplicateRecallTopicInCWD(ctx, txStore, requestScope, req.RecallTopic, targetScope, template.ID, req.SectionKey); err != nil {
				return err
			}
		}
		section, uerr := txStore.UpsertSection(ctx, promptstore.PromptTemplateSection{
			TemplateID:  template.ID,
			SectionKey:  req.SectionKey,
			Region:      req.Region,
			Ordinal:     req.Ordinal,
			Body:        req.Body,
			EnableWhen:  req.EnableWhen,
			Enabled:     req.Enabled,
			TriggerType: req.TriggerType,
			RecallTopic: req.RecallTopic,
		})
		if uerr != nil {
			return uerr
		}
		if recallWrite {
			if err := txStore.UpsertRecallTopicTargetInCWD(ctx, requestScope, section.RecallTopic, section.TemplateID, section.SectionKey); err != nil {
				return err
			}
		}
		saved = section
		return nil
	})
	return saved, err
}
