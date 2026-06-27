package prompt

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	platformsqlite "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestPromptIntentCommitExpertWritesTemplateAndSections(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyExpertIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/expert/1"] = promptIntentDraftForTest("intent/expert/1", "/repo/a", "expert", "ready_to_save", cardJSON, nil)
	rec := &recordingSectionInvalidator{}

	got, err := promptintent.HandleCommit(context.Background(), store, rec, nil, promptintent.CommitParams{
		DraftKey: "intent/expert/1",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "expert", result.Kind)
	require.NotEmpty(t, result.PromptKey)
	require.Equal(t, 1, store.txCalls)
	require.Equal(t, "enabled", store.drafts["intent/expert/1"].Status)
	saved := store.templates[result.PromptKey]
	require.Equal(t, "SQLC Reviewer", saved.Title)
	require.Equal(t, "main", saved.AgentKey)
	require.JSONEq(t, `["intent:expert","scope.cwd:/repo/a"]`, string(saved.Tags))
	require.False(t, promptstore.IsRuntimeAssetTemplate(saved))
	require.Len(t, store.sections[saved.ID], 4)
	require.Equal(t, contract.InvalidateClear, rec.reason)
	require.Equal(t, []string{contract.DynamicSectionAvailableExperts}, rec.names)
}

func TestPromptIntentCommitExternalPromptWritesDBUserAssetNotBuiltin(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyExpertIntentCard()
	card.Title = "SQLC Reviewer"
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/expert/1"] = promptIntentDraftForTest("intent/expert/1", "/repo/a", "expert", "ready_to_save", cardJSON, nil)
	builtin := fakeBuiltinRegistryWithKeys("main/expert/sqlc-reviewer")

	got, err := promptintent.HandleCommit(context.Background(), store, nil, builtin, promptintent.CommitParams{
		DraftKey:    "intent/expert/1",
		Cwd:         "/repo/a",
		ConfirmRisk: true,
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.NotEmpty(t, result.PromptKey)
	require.NotEqual(t, "main/expert/sqlc-reviewer", result.PromptKey)
	require.Equal(t, promptUpdatedBy, store.templates[result.PromptKey].CreatedBy)
	require.NotContains(t, store.templates, "main/expert/sqlc-reviewer")
}

func TestPromptIntentCommitRecallWritesScopedRecallSection(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyRecallIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/recall/1"] = promptIntentDraftForTest("intent/recall/1", "/repo/a", "recall", "ready_to_save", cardJSON, nil)
	rec := &recordingSectionInvalidator{}

	got, err := promptintent.HandleCommit(context.Background(), store, rec, nil, promptintent.CommitParams{
		DraftKey: "intent/recall/1",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	saved := store.templates[result.PromptKey]
	require.JSONEq(t, `["intent:recall","scope.cwd:/repo/a"]`, string(saved.Tags))
	require.True(t, promptstore.IsRuntimeAssetTemplate(saved))
	require.Len(t, store.sections[saved.ID], 1)
	section := store.sections[saved.ID]["recall_sqlc_workflow"]
	require.Equal(t, "recall", section.TriggerType)
	require.Equal(t, "sqlc-workflow", section.RecallTopic)
	require.Equal(t, []struct {
		cwd   string
		topic string
	}{{cwd: "/repo/a", topic: "sqlc-workflow"}}, store.lockRecallCalls)
	require.Equal(t, []string{contract.DynamicSectionRecallCatalog}, rec.names)
}

func TestPromptIntentCommitRecallSQLiteSavesOldDraftWithoutEnableWhen(t *testing.T) {
	t.Parallel()

	db := openPromptIntentCommitSQLite(t)
	store := promptstore.NewStore(sqlc.New(db))
	card := readyRecallIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	_, err = store.UpsertIntentDraft(context.Background(), promptstore.PromptIntentDraft{
		DraftKey:      "intent/recall/sqlite",
		CWD:           "/repo/a",
		Kind:          "recall",
		RawInput:      "Remember this sqlc workflow as project knowledge.",
		GeneratedCard: cardJSON,
		Confidence:    0.85,
		Status:        "ready_to_save",
		Scope:         "project",
		Issues:        json.RawMessage(`[]`),
	})
	require.NoError(t, err)

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/recall/sqlite",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	var enableWhen string
	err = db.QueryRowContext(context.Background(), `SELECT enable_when FROM prompt_template_sections WHERE section_key = ?`, "recall_sqlc_workflow").Scan(&enableWhen)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, enableWhen)
}

func TestPromptIntentCommitRecallNormalizesUnderscoreTopicBeforeLock(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyRecallIntentCard()
	card.Title = "SQLite Prompt Intent Draft Acceptance Token"
	card.Summary = "SQLite prompt intent acceptance token notes."
	card.RecallTopic = "sqlite_prompt_intent_draft_acceptance_token"
	card.RecallBody = "The acceptance token must survive recall draft save."
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/recall/1"] = promptIntentDraftForTest("intent/recall/1", "/repo/a", "recall", "ready_to_save", cardJSON, nil)

	got, err := promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/recall/1",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "main/knowledge/sqlite-prompt-intent-draft-acceptance-token", result.PromptKey)
	saved := store.templates[result.PromptKey]
	section := store.sections[saved.ID]["recall_sqlite_prompt_intent_draft_acceptance_token"]
	require.Equal(t, "sqlite-prompt-intent-draft-acceptance-token", section.RecallTopic)
	require.Equal(t, []struct {
		cwd   string
		topic string
	}{{cwd: "/repo/a", topic: "sqlite-prompt-intent-draft-acceptance-token"}}, store.lockRecallCalls)
	require.Equal(t, []struct {
		cwd        string
		topic      string
		templateID int64
		sectionKey string
	}{{
		cwd:        "/repo/a",
		topic:      "sqlite-prompt-intent-draft-acceptance-token",
		templateID: saved.ID,
		sectionKey: "recall_sqlite_prompt_intent_draft_acceptance_token",
	}}, store.upsertRecallTargetCalls)
}

func TestPromptIntentCommitRejectsSiblingReadyDrafts(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	rawInput := "external prompt raw input"
	originHash := "same-origin-hash"
	defaultRuleJSON, err := json.Marshal(readyDefaultRuleIntentCard())
	require.NoError(t, err)
	expertJSON, err := json.Marshal(readyExpertIntentCard())
	require.NoError(t, err)
	recallJSON, err := json.Marshal(readyRecallIntentCard())
	require.NoError(t, err)

	selected := promptIntentDraftForTest("intent/default_rule/selected", "/repo/a", "default_rule", "ready_to_save", defaultRuleJSON, nil)
	selected.RawInput = rawInput
	selected.OriginHash = originHash
	siblingExpert := promptIntentDraftForTest("intent/expert/sibling", "/repo/a", "expert", "ready_to_save", expertJSON, nil)
	siblingExpert.RawInput = rawInput
	siblingExpert.OriginHash = originHash
	siblingRecall := promptIntentDraftForTest("intent/recall/sibling", "/repo/a", "recall", "ready_to_save", recallJSON, nil)
	siblingRecall.RawInput = rawInput
	siblingRecall.OriginHash = originHash
	otherInput := promptIntentDraftForTest("intent/expert/other", "/repo/a", "expert", "ready_to_save", expertJSON, nil)
	otherInput.RawInput = "different raw input"
	otherInput.OriginHash = "different-origin-hash"
	for _, draft := range []promptstore.PromptIntentDraft{selected, siblingExpert, siblingRecall, otherInput} {
		store.drafts[draft.DraftKey] = draft
	}

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/default_rule/selected",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	require.Equal(t, "enabled", store.drafts["intent/default_rule/selected"].Status)
	require.Equal(t, "rejected", store.drafts["intent/expert/sibling"].Status)
	require.Equal(t, "rejected", store.drafts["intent/recall/sibling"].Status)
	require.Equal(t, "ready_to_save", store.drafts["intent/expert/other"].Status)
}

func TestPromptIntentCommitRecallAllowsProjectOverrideOfGlobalTopic(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	global := scopedPromptTemplate("main/knowledge/sqlc-global", "")
	global.ID = 21
	global.Title = "Global SQLC Knowledge"
	global.Tags = json.RawMessage(`["intent:recall","scope.global"]`)
	store.templates[global.PromptKey] = global
	store.sections[global.ID] = map[string]promptstore.PromptTemplateSection{
		"recall_sqlc_workflow": {
			TemplateID:  global.ID,
			SectionKey:  "recall_sqlc_workflow",
			Enabled:     true,
			TriggerType: "recall",
			RecallTopic: "sqlc-workflow",
			Body:        "Global SQLC workflow guidance.",
		},
	}
	card := readyRecallIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/recall/1"] = promptIntentDraftForTest("intent/recall/1", "/repo/a", "recall", "ready_to_save", cardJSON, nil)

	got, err := promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/recall/1",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	saved := store.templates[result.PromptKey]
	require.JSONEq(t, `["intent:recall","scope.cwd:/repo/a"]`, string(saved.Tags))
	require.Equal(t, "Read source SQL before generated code.", store.sections[saved.ID]["recall_sqlc_workflow"].Body)
}

func TestPromptIntentCommitGlobalRecallAllowsProjectTopicOverride(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	project := scopedPromptTemplate("main/knowledge/sqlc-project", "/repo/a")
	project.ID = 22
	project.Title = "Project SQLC Knowledge"
	project.Tags = json.RawMessage(`["intent:recall","scope.cwd:/repo/a"]`)
	store.templates[project.PromptKey] = project
	store.sections[project.ID] = map[string]promptstore.PromptTemplateSection{
		"recall_sqlc_workflow": {
			TemplateID:  project.ID,
			SectionKey:  "recall_sqlc_workflow",
			Enabled:     true,
			TriggerType: "recall",
			RecallTopic: "sqlc-workflow",
			Body:        "Project SQLC workflow guidance.",
		},
	}
	card := readyRecallIntentCard()
	card.Title = "Global SQLC Workflow"
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	draft := promptIntentDraftForTest("intent/recall/1", "/repo/a", "recall", "ready_to_save", cardJSON, nil)
	draft.Scope = "global"
	store.drafts["intent/recall/1"] = draft

	got, err := promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey:      "intent/recall/1",
		Cwd:           "/repo/a",
		EnableGlobal:  true,
		ConfirmGlobal: true,
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	saved := store.templates[result.PromptKey]
	require.JSONEq(t, `["intent:recall","scope.global"]`, string(saved.Tags))
	require.Equal(t, "Read source SQL before generated code.", store.sections[saved.ID]["recall_sqlc_workflow"].Body)
}

func TestPromptIntentCommitDefaultRuleWritesProjectRuleSection(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyDefaultRuleIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/default_rule/1"] = promptIntentDraftForTest("intent/default_rule/1", "/repo/a", "default_rule", "ready_to_save", cardJSON, nil)
	rec := &recordingSectionInvalidator{}

	got, err := promptintent.HandleCommit(context.Background(), store, rec, nil, promptintent.CommitParams{
		DraftKey: "intent/default_rule/1",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	saved := store.templates[result.PromptKey]
	require.Equal(t, "default_rule", saved.AgentKey)
	require.JSONEq(t, `["intent:default_rule","scope.cwd:/repo/a"]`, string(saved.Tags))
	require.True(t, promptstore.IsRuntimeAssetTemplate(saved))
	require.Equal(t, "Run focused tests before reporting completion.", store.sections[saved.ID]["project_rule"].Body)
	require.Equal(t, []string{contract.DynamicSectionProjectDefaultRules}, rec.names)
}

func TestPromptIntentCommitUsesDraftGlobalScopeAndRequiresConfirm(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyExpertIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	draft := promptIntentDraftForTest("intent/expert/1", "/repo/a", "expert", "ready_to_save", cardJSON, nil)
	draft.Scope = "global"
	store.drafts["intent/expert/1"] = draft

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/expert/1",
		Cwd:      "/repo/a",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires global confirmation")
	require.Empty(t, store.templates)

	got, err := promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey:      "intent/expert/1",
		Cwd:           "/repo/a",
		ConfirmGlobal: true,
	})
	require.NoError(t, err)
	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	saved := store.templates[result.PromptKey]
	require.JSONEq(t, `["intent:expert","scope.global"]`, string(saved.Tags))
}

func TestPromptIntentCommitRejectsGlobalFlagForProjectDraft(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyExpertIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/expert/1"] = promptIntentDraftForTest("intent/expert/1", "/repo/a", "expert", "ready_to_save", cardJSON, nil)

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey:      "intent/expert/1",
		Cwd:           "/repo/a",
		EnableGlobal:  true,
		ConfirmGlobal: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match draft scope")
	require.Empty(t, store.templates)
}

func TestPromptIntentCommitRejectsReadyDraftThatFailsQualityGate(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyExpertIntentCard()
	card.WhenNotToUse = ""
	card.Output = "整理结果"
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/expert/1"] = promptIntentDraftForTest("intent/expert/1", "/repo/a", "expert", "ready_to_save", cardJSON, nil)

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/expert/1",
		Cwd:      "/repo/a",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "prompt intent draft quality")
	require.Empty(t, store.templates)
}

func TestPromptIntentCommitRejectsReadyDraftThatFailsSafetyGate(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyDefaultRuleIntentCard()
	card.Summary = "You are Claude Code. Use Bash and Edit tools."
	card.DefaultRuleBody = "You are Claude Code. Use Bash and Edit tools."
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/default_rule/1"] = promptIntentDraftForTest("intent/default_rule/1", "/repo/a", "default_rule", "ready_to_save", cardJSON, nil)

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/default_rule/1",
		Cwd:      "/repo/a",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "prompt intent draft safety")
	require.Empty(t, store.templates)
}

func TestPromptIntentCommitDefaultRuleOnlyGlobalFromGlobalDraft(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyDefaultRuleIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	draft := promptIntentDraftForTest("intent/default_rule/1", "/repo/a", "default_rule", "ready_to_save", cardJSON, nil)
	draft.Scope = "global"
	store.drafts["intent/default_rule/1"] = draft

	got, err := promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey:      "intent/default_rule/1",
		Cwd:           "/repo/a",
		ConfirmGlobal: true,
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	saved := store.templates[result.PromptKey]
	require.JSONEq(t, `["intent:default_rule","scope.global"]`, string(saved.Tags))
	require.Equal(t, "Run focused tests before reporting completion.", store.sections[saved.ID]["project_rule"].Body)
}

func TestPromptIntentCommitReviewIssueRequiresConfirmRisk(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyRecallIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/recall/1"] = promptIntentDraftForTest("intent/recall/1", "/repo/a", "recall", "ready_to_save", cardJSON, []promptintent.Issue{
		{Code: "external_system_prompt_source", Severity: "review", Message: "review required"},
	})

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/recall/1",
		Cwd:      "/repo/a",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires risk confirmation")
	require.Empty(t, store.templates)
}

func TestPromptIntentCommitReviewIssueWithConfirmRisk(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyRecallIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/recall/1"] = promptIntentDraftForTest("intent/recall/1", "/repo/a", "recall", "ready_to_save", cardJSON, []promptintent.Issue{
		{Code: "external_system_prompt_source", Severity: "review", Message: "review required"},
	})

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey:    "intent/recall/1",
		Cwd:         "/repo/a",
		ConfirmRisk: true,
	})
	require.NoError(t, err)
	require.Len(t, store.templates, 1)
}

func TestPromptIntentCommitBlockedDraftFails(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyExpertIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/expert/1"] = promptIntentDraftForTest("intent/expert/1", "/repo/a", "expert", "draft", cardJSON, nil)

	_, err = promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/expert/1",
		Cwd:      "/repo/a",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not ready to save")
	require.Empty(t, store.templates)
}

func TestPromptIntentDiscardReadyDraft(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyExpertIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/expert/1"] = promptIntentDraftForTest("intent/expert/1", "/repo/a", "expert", "ready_to_save", cardJSON, nil)

	got, err := promptintent.HandleDiscard(context.Background(), store, promptintent.DiscardParams{
		DraftKey: "intent/expert/1",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.DiscardResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "intent/expert/1", result.DraftKey)
	require.Equal(t, "rejected", result.Status)
	require.Equal(t, "rejected", store.drafts["intent/expert/1"].Status)
	require.Empty(t, store.templates)
}

func TestPromptIntentCommitDoesNotOverwriteExistingPromptTemplate(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	existing := scopedPromptTemplate("main/expert/sqlc-reviewer", "/repo/a")
	existing.ID = 77
	existing.Title = "Existing"
	store.templates[existing.PromptKey] = existing
	card := readyExpertIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/expert/1"] = promptIntentDraftForTest("intent/expert/1", "/repo/a", "expert", "ready_to_save", cardJSON, nil)

	got, err := promptintent.HandleCommit(context.Background(), store, nil, nil, promptintent.CommitParams{
		DraftKey: "intent/expert/1",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.CommitResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.NotEqual(t, existing.PromptKey, result.PromptKey)
	require.Equal(t, "Existing", store.templates[existing.PromptKey].Title)
	require.Equal(t, 0, store.upsertCalls)
}

func readyExpertIntentCard() promptintent.Card {
	return promptintent.Card{
		Kind:         "expert",
		Title:        "SQLC Reviewer",
		Summary:      "Review sqlc query and generated-code drift.",
		WhenToUse:    "Use when reviewing sqlc query changes.",
		WhenNotToUse: "Do not use for frontend-only work.",
		Workflow:     []string{"Read query SQL", "Compare generated code"},
		Constraints:  []string{"Do not edit generated code first"},
		Output:       "Findings with file references.",
		HitExamples:  []string{"Review a sqlc query migration"},
		MissExamples: []string{"Polish a CSS button"},
	}
}

func readyDefaultRuleIntentCard() promptintent.Card {
	return promptintent.Card{
		Kind:            "default_rule",
		Title:           "Focused Tests",
		Summary:         "Run focused tests before reporting completion.",
		DefaultRuleBody: "Run focused tests before reporting completion.",
		HitExamples:     []string{"Finish backend prompt changes"},
		MissExamples:    []string{"Brainstorm product ideas"},
	}
}

func openPromptIntentCommitSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	migrationsDir := filepath.Join(promptIntentCommitRepoRoot(t), "internal", "platform", "db", "sqlite", "migrations")
	require.NoError(t, platformsqlite.RunMigrations(context.Background(), db, migrationsDir))
	return db
}

// TestCommitPromptIntentDraft_ConcurrentSubmit 验证两个 goroutine 同时提交同批次的两个草稿时，
// 最终只有一个草稿变为 enabled，不会出现两个都被拒绝的竞态。
// 依赖 SQLite IMMEDIATE 事务串行化：WithTx 使用 BEGIN IMMEDIATE，写锁在事务开始时即获取，
// 保证并发提交者只有一个能持有锁执行完整的"设为 enabled + 拒绝兄弟"序列。
func TestCommitPromptIntentDraft_ConcurrentSubmit(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "concurrent-commit.db")
	dbA := openConcurrentCommitDB(t, ctx, dbPath, true)
	dbB := openConcurrentCommitDB(t, ctx, dbPath, false)
	storeA := promptstore.NewStoreWithDB(dbA, sqlc.New(dbA))
	storeB := promptstore.NewStoreWithDB(dbB, sqlc.New(dbB))

	rawInput := "concurrent-submit-raw-input"
	originHash := "concurrent-submit-origin-hash"
	expertCard := readyExpertIntentCard()
	expertCard.Title = "Concurrent Expert A"
	cardA, err := json.Marshal(expertCard)
	require.NoError(t, err)
	expertCard.Title = "Concurrent Expert B"
	expertCard.Workflow = []string{"Step one B", "Step two B"}
	cardB, err := json.Marshal(expertCard)
	require.NoError(t, err)

	draftA := promptIntentDraftForTest("intent/expert/concurrent-a", "/repo/concurrent", "expert", "ready_to_save", cardA, nil)
	draftA.RawInput, draftA.OriginHash = rawInput, originHash
	draftB := promptIntentDraftForTest("intent/expert/concurrent-b", "/repo/concurrent", "expert", "ready_to_save", cardB, nil)
	draftB.RawInput, draftB.OriginHash = rawInput, originHash
	_, err = storeA.UpsertIntentDraft(ctx, draftA)
	require.NoError(t, err)
	_, err = storeA.UpsertIntentDraft(ctx, draftB)
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errs[0] = promptintent.HandleCommit(ctx, storeA, nil, nil, promptintent.CommitParams{DraftKey: "intent/expert/concurrent-a", Cwd: "/repo/concurrent"})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errs[1] = promptintent.HandleCommit(ctx, storeB, nil, nil, promptintent.CommitParams{DraftKey: "intent/expert/concurrent-b", Cwd: "/repo/concurrent"})
	}()
	close(start)
	wg.Wait()

	successCount := 0
	for _, e := range errs {
		if e == nil {
			successCount++
		}
	}
	// 至少一个提交必须成功；IMMEDIATE 事务串行化保证另一个要么先成功要么看到兄弟草稿已被拒绝。
	require.GreaterOrEqual(t, successCount, 1, "errs: %v | %v", errs[0], errs[1])

	// 注：ListIntentDrafts SQL 用 (?2 IS NULL OR status=?2)，空字符串不是 NULL，必须显式过滤。
	drafts, err := storeA.ListIntentDrafts(ctx, promptstore.PromptIntentDraftListFilter{CWD: "/repo/concurrent", Status: "enabled", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, len(drafts), "同批次并发提交后应只有 1 个 enabled 草稿，drafts: %+v", drafts)
}

func openConcurrentCommitDB(t *testing.T, ctx context.Context, path string, migrate bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	for _, p := range []string{"PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 5000", "PRAGMA synchronous = FULL"} {
		_, err := db.ExecContext(ctx, p)
		require.NoError(t, err)
	}
	if migrate {
		migrationsDir := filepath.Join(promptIntentCommitRepoRoot(t), "internal", "platform", "db", "sqlite", "migrations")
		require.NoError(t, platformsqlite.RunMigrations(ctx, db, migrationsDir))
	}
	return db
}

func promptIntentCommitRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from %s", dir)
		}
		dir = parent
	}
}
