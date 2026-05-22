package db

import (
	"strings"
	"testing"
)

const promptTemplateDeleteUnusedBuiltinSeedsMigrationName = "0105_delete_unused_builtin_prompt_seeds.sql"

type promptTemplateCleanupCase struct {
	name      string
	promptKey string
	createdBy string
	updatedBy string
}

var promptTemplateCleanupDeleteCases = []promptTemplateCleanupCase{
	{name: "test/%", promptKey: "test/greeting", createdBy: "test-seed", updatedBy: "test-seed"},
	{name: "examples/sections-demo", promptKey: "examples/sections-demo", createdBy: "system", updatedBy: "system"},
	{name: "main/3", promptKey: "main/3", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/prompt", promptKey: "main/prompt", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/debug", promptKey: "main/debug", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/claude-style", promptKey: "main/claude-style", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/claude-style-zh", promptKey: "main/claude-style-zh", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/general-en", promptKey: "main/general-en", createdBy: "test-seed", updatedBy: "system.seed"},
	{name: "sql/expert", promptKey: "sql/expert", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/writing", promptKey: "main/writing", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/translate", promptKey: "main/translate", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/research", promptKey: "main/research", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/brainstorm", promptKey: "main/brainstorm", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/paper_summarizer", promptKey: "main/paper_summarizer", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/topic_curator", promptKey: "main/topic_curator", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/learning_card", promptKey: "main/learning_card", createdBy: "system.seed", updatedBy: "system.seed"},
	{name: "main/trip_briefer", promptKey: "main/trip_briefer", createdBy: "system.seed", updatedBy: "system.seed"},
}

var promptTemplateCleanupExactKeys = []string{
	"main/3",
	"main/prompt",
	"main/debug",
	"main/claude-style",
	"main/claude-style-zh",
	"main/general-en",
	"sql/expert",
	"main/writing",
	"main/translate",
	"main/research",
	"main/brainstorm",
	"main/paper_summarizer",
	"main/topic_curator",
	"main/learning_card",
	"main/trip_briefer",
}

var promptTemplateCleanupObsoleteRoutingRows = []promptRoutingFixture{
	{input: "帮我写一封辞职邮件", expectedPromptKey: "main/writing", note: "写邮件"},
	{input: "润色一下这段文案", expectedPromptKey: "main/writing", note: "润色"},
	{input: "draft a release announcement", expectedPromptKey: "main/writing", note: "EN draft"},
	{input: "把这段翻译成英文", expectedPromptKey: "main/translate", note: "翻译"},
	{input: "translate to Simplified Chinese", expectedPromptKey: "main/translate", note: "EN 翻译"},
	{input: "帮我翻译一下这个术语", expectedPromptKey: "main/translate", note: "帮我翻译"},
	{input: "什么是事件溯源", expectedPromptKey: "main/research", note: "什么是"},
	{input: "总结一下这篇论文的要点", expectedPromptKey: "main/research", note: "总结"},
	{input: "对比一下 PostgreSQL 和 MySQL", expectedPromptKey: "main/research", note: "对比"},
	{input: "给我的猫起个名字", expectedPromptKey: "main/brainstorm", note: "起名"},
	{input: "brainstorm 几个营销方案", expectedPromptKey: "main/brainstorm", note: "brainstorm"},
	{input: "想几个有创意的标题", expectedPromptKey: "main/brainstorm", note: "想个标题"},
}

var promptTemplateCleanupRetainedRoutingRows = []promptRoutingFixture{
	{input: "帮我 review 这段代码", expectedPromptKey: "main/code-review", note: "review 显式"},
	{input: "为什么这里报错了", expectedPromptKey: "main/code-debug", note: "为什么报错"},
	{input: "帮我写 SQL 查询订单表", expectedPromptKey: "main/sql", note: "写 SQL"},
	{input: "制定一个三步走的实施计划", expectedPromptKey: "main/planning", note: "制定计划"},
	{input: "拆分任务并分配给多个 agent", expectedPromptKey: "main/orchestrator", note: "拆分任务"},
	{input: "今天天气真好", expectedPromptKey: "main/default", note: "闲聊"},
}

func TestPromptTemplateDeleteUnusedBuiltinSeedsMigrationListsExpectedTargetsAndGuards(t *testing.T) {
	t.Parallel()

	content := readMigrationFixture(t, promptTemplateDeleteUnusedBuiltinSeedsMigrationName)
	checks := []string{
		"WITH delete_keys(prompt_key) AS",
		"p.prompt_key LIKE 'test/%'",
		"p.prompt_key = 'examples/sections-demo'",
		"p.created_by IN ('system.seed', 'seed', 'test-seed')",
		"p.created_by IN ('system.seed', 'seed', 'system', 'test-seed')",
		"p.updated_by IN ('system.seed', 'seed', 'test-seed', 'migration')",
		"p.updated_by IN ('system.seed', 'seed', 'system', 'test-seed', 'migration')",
		"p.updated_by LIKE 'system.%'",
		"p.updated_by LIKE 'migration:%'",
		"p.manually_edited = FALSE",
		"obsolete_routing_tests(input, expected_prompt_key) AS",
		"DELETE FROM public.prompt_routing_tests r",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Fatalf("%s missing %q", promptTemplateDeleteUnusedBuiltinSeedsMigrationName, check)
		}
	}
	for _, key := range promptTemplateCleanupExactKeys {
		if !strings.Contains(content, "('"+key+"')") {
			t.Fatalf("%s missing cleanup key %q", promptTemplateDeleteUnusedBuiltinSeedsMigrationName, key)
		}
	}
	for _, row := range promptTemplateCleanupObsoleteRoutingRows {
		if !strings.Contains(content, "('"+row.input+"', '"+row.expectedPromptKey+"')") {
			t.Fatalf("%s missing routing cleanup row %q -> %q", promptTemplateDeleteUnusedBuiltinSeedsMigrationName, row.input, row.expectedPromptKey)
		}
	}
}

func TestPromptTemplateDeleteUnusedBuiltinSeedsMigrationDeletesSystemSeedsAndSections(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)

	for _, tc := range promptTemplateCleanupDeleteCases {
		templateID := insertPromptCleanupTemplate(t, ctx, conn, promptCleanupTemplate{
			PromptKey: tc.promptKey,
			CreatedBy: tc.createdBy,
			UpdatedBy: tc.updatedBy,
		})
		insertPromptCleanupSection(t, ctx, conn, templateID, "owned_section", "section for "+tc.promptKey)
	}

	applyPromptTemplateCleanupMigration0105(t, ctx, conn, schema)

	for _, tc := range promptTemplateCleanupDeleteCases {
		requirePromptCleanupTemplateMissing(t, ctx, conn, tc.promptKey)
		requirePromptCleanupSectionBodyCount(t, ctx, conn, "section for "+tc.promptKey, 0)
	}
}

func TestPromptTemplateDeleteUnusedBuiltinSeedsMigrationDeletesMigrationMaintainedSeeds(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)

	for _, tc := range promptTemplateCleanupDeleteCases {
		templateID := insertPromptCleanupTemplate(t, ctx, conn, promptCleanupTemplate{
			PromptKey: tc.promptKey,
			CreatedBy: tc.createdBy,
			UpdatedBy: "migration:0099",
		})
		insertPromptCleanupSection(t, ctx, conn, templateID, "owned_section", "migration section for "+tc.promptKey)
	}

	applyPromptTemplateCleanupMigration0105(t, ctx, conn, schema)

	for _, tc := range promptTemplateCleanupDeleteCases {
		requirePromptCleanupTemplateMissing(t, ctx, conn, tc.promptKey)
		requirePromptCleanupSectionBodyCount(t, ctx, conn, "migration section for "+tc.promptKey, 0)
	}
}

func TestPromptTemplateDeleteUnusedBuiltinSeedsMigrationPreservesUserOwnedAndEditedRows(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	guards := []struct {
		name           string
		createdBy      string
		updatedBy      string
		manuallyEdited bool
	}{
		{name: "rpc_created_asset", createdBy: "rpc.prompts", updatedBy: "rpc.prompts"},
		{name: "rpc_updated_seed", updatedBy: "rpc.prompts"},
		{name: "manually_edited_seed", updatedBy: "system.seed", manuallyEdited: true},
	}

	for _, tc := range promptTemplateCleanupDeleteCases {
		for _, guard := range guards {
			t.Run(tc.name+"/"+guard.name, func(t *testing.T) {
				truncatePromptCleanupTemplates(t, ctx, conn)

				createdBy := guard.createdBy
				if createdBy == "" {
					createdBy = protectedPromptCleanupCreatedBy(tc.promptKey)
				}
				templateID := insertPromptCleanupTemplate(t, ctx, conn, promptCleanupTemplate{
					PromptKey:      tc.promptKey,
					CreatedBy:      createdBy,
					UpdatedBy:      guard.updatedBy,
					ManuallyEdited: guard.manuallyEdited,
				})
				insertPromptCleanupSection(t, ctx, conn, templateID, "protected_section", "protected section for "+tc.promptKey)

				applyPromptTemplateCleanupMigration0105(t, ctx, conn, schema)

				requirePromptCleanupTemplateState(t, ctx, conn, tc.promptKey, promptCleanupTemplateState{
					CreatedBy:      createdBy,
					UpdatedBy:      guard.updatedBy,
					ManuallyEdited: guard.manuallyEdited,
				})
				requirePromptCleanupSectionBodyCount(t, ctx, conn, "protected section for "+tc.promptKey, 1)
			})
		}
	}
}

func TestPromptTemplateDeleteUnusedBuiltinSeedsMigrationCleansObsoleteRoutingRows(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)

	for _, key := range []string{
		"main/code-review",
		"main/code-debug",
		"main/sql",
		"main/planning",
		"main/orchestrator",
		"main/default",
	} {
		insertRuntimeVisiblePromptCleanupTemplate(t, ctx, conn, key)
	}
	for _, row := range append(promptTemplateCleanupRetainedRoutingRows, promptTemplateCleanupObsoleteRoutingRows...) {
		insertPromptRoutingFixture(t, ctx, conn, row)
	}

	applyPromptTemplateCleanupMigration0105(t, ctx, conn, schema)

	for _, key := range []string{"main/writing", "main/translate", "main/research", "main/brainstorm"} {
		requireNoEnabledRoutingRowsForPromptKey(t, ctx, conn, key)
	}
	for _, row := range promptTemplateCleanupObsoleteRoutingRows {
		requirePromptRoutingMissing(t, ctx, conn, row.input)
	}
	for _, row := range promptTemplateCleanupRetainedRoutingRows {
		requirePromptRoutingState(t, ctx, conn, row.input, row.expectedPromptKey, true)
	}
	requireEnabledRoutingRowsPointToRuntimeVisibleTemplates(t, ctx, conn)
}

func TestPromptTemplateDeleteUnusedBuiltinSeedsRollbackRestoresDataWithoutOverwritingUsers(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	userTemplateID := insertPromptCleanupTemplate(t, ctx, conn, promptCleanupTemplate{
		PromptKey:      "main/translate",
		CreatedBy:      "rpc.prompts",
		UpdatedBy:      "rpc.prompts",
		ManuallyEdited: true,
		PromptText:     "user translation asset",
	})

	dataRestore := extractRollbackSQLBlock(t, "0105 data restore")
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0105 data restore")

	requirePromptCleanupTemplateExists(t, ctx, conn, "main/writing")
	requirePromptCleanupTemplateState(t, ctx, conn, "main/translate", promptCleanupTemplateState{
		CreatedBy:      "rpc.prompts",
		UpdatedBy:      "rpc.prompts",
		ManuallyEdited: true,
		PromptText:     "user translation asset",
	})
	requirePromptCleanupTemplateIDSectionCount(t, ctx, conn, userTemplateID, 0)
	requirePromptCleanupTemplateSection(t, ctx, conn, "examples/sections-demo", "identity", "sectioned prompt layout")

	for _, key := range []string{"main/claude-style", "main/claude-style-zh", "main/general-en", "sql/expert"} {
		requirePromptCleanupTemplateState(t, ctx, conn, key, promptCleanupTemplateState{
			Enabled: boolPtr(false),
		})
		requirePromptCleanupTemplateMissingRuntimeScope(t, ctx, conn, key)
	}
}

func TestPromptTemplateDeleteUnusedBuiltinSeedsRollbackRestoresRoutingWithoutOverwritingLocalRows(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)

	for _, row := range promptTemplateCleanupObsoleteRoutingRows {
		insertPromptRoutingFixture(t, ctx, conn, row)
	}
	applyPromptTemplateCleanupMigration0105(t, ctx, conn, schema)
	insertPromptRoutingFixture(t, ctx, conn, promptRoutingFixture{
		input:             "帮我写一封辞职邮件",
		expectedPromptKey: "user/custom-email",
		note:              "local override",
		enabled:           boolPtr(false),
	})

	routingRestore := extractRollbackSQLBlock(t, "0105 routing restore")
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, routingRestore, "0105 routing restore")

	requirePromptRoutingState(t, ctx, conn, "帮我写一封辞职邮件", "user/custom-email", false)
	requirePromptRoutingState(t, ctx, conn, "润色一下这段文案", "main/writing", true)
	requirePromptRoutingState(t, ctx, conn, "把这段翻译成英文", "main/translate", true)
}
