package prompt

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func writePromptSectionInTx(
	ctx context.Context,
	store contract.PromptStore,
	requestScope, promptKey string,
	req PromptSectionWriteRequest,
) (*contract.PromptTemplateSection, error) {
	var saved *contract.PromptTemplateSection
	err := store.WithTx(ctx, func(txStore contract.PromptStore) error {
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
		section, uerr := txStore.UpsertSection(ctx, contract.PromptTemplateSection{
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
