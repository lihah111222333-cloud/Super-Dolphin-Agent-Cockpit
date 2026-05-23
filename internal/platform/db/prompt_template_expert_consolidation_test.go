package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

const promptTemplateExpertConsolidationMigrationName = "0107_prompt_template_expert_consolidation.sql"

var duplicateDeveloperExpertKeys = []string{
	"main/code-generate",
	"main/code-refactor",
	"main/code-test",
	"main/code-explain",
}

func TestPromptTemplateExpertConsolidationMigrationListsExpectedTargetsAndGuards(t *testing.T) {
	t.Parallel()

	content := readMigrationFixture(t, promptTemplateExpertConsolidationMigrationName)
	for _, check := range []string{
		"prompt_template_expert_consolidation_0107_restore",
		"WITH duplicate_keys(prompt_key) AS",
		"p.created_by IN ('system.seed', 'seed')",
		"p.updated_by IN ('system.seed', 'seed', 'migration')",
		"p.updated_by LIKE 'system.%'",
		"p.updated_by LIKE 'migration:%'",
		"p.manually_edited = FALSE",
		"WHERE p.prompt_key = 'main/code-task'",
		"updated_by = 'migration:0107'",
	} {
		if !strings.Contains(content, check) {
			t.Fatalf("%s missing %q", promptTemplateExpertConsolidationMigrationName, check)
		}
	}
	for _, key := range duplicateDeveloperExpertKeys {
		if !strings.Contains(content, "('"+key+"')") {
			t.Fatalf("%s missing duplicate key %q", promptTemplateExpertConsolidationMigrationName, key)
		}
	}
}

func TestPromptTemplateExpertConsolidationDeletesDuplicatesAndUpdatesCodeTask(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	for _, key := range duplicateDeveloperExpertKeys {
		id := insertExpertConsolidationTemplate(t, ctx, conn, key, "system.seed", "migration:0099", false)
		insertPromptCleanupSection(t, ctx, conn, id, "legacy_section", "section for "+key)
	}
	insertExpertConsolidationTemplate(t, ctx, conn, "main/code-task", "system.seed", "system.seed", false)
	for _, key := range []string{"main/code-review", "main/code-debug", "main/planning"} {
		insertExpertConsolidationTemplate(t, ctx, conn, key, "system.seed", "system.seed", false)
	}

	applyPromptTemplateExpertConsolidation0107(t, ctx, conn, schema)

	for _, key := range duplicateDeveloperExpertKeys {
		requirePromptCleanupTemplateMissing(t, ctx, conn, key)
		requirePromptCleanupSectionBodyCount(t, ctx, conn, "section for "+key, 0)
	}
	requireExpertConsolidationWhenToUseContains(t, ctx, conn, "main/code-task", []string{"实现", "重构", "解释", "补测试", "日常开发任务"})
	for _, key := range []string{"main/code-review", "main/code-debug", "main/planning"} {
		requirePromptCleanupTemplateExists(t, ctx, conn, key)
		requireExpertConsolidationWhenToUseContains(t, ctx, conn, key, []string{"original when " + key})
	}
}

func TestPromptTemplateExpertConsolidationPreservesUserOwnedAndEditedRows(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	cases := []struct {
		name           string
		promptKey      string
		createdBy      string
		updatedBy      string
		manuallyEdited bool
	}{
		{name: "rpc_created_duplicate", promptKey: "main/code-generate", createdBy: "rpc.prompts", updatedBy: "rpc.prompts"},
		{name: "rpc_updated_duplicate", promptKey: "main/code-refactor", createdBy: "system.seed", updatedBy: "rpc.prompts"},
		{name: "manually_edited_duplicate", promptKey: "main/code-test", createdBy: "system.seed", updatedBy: "system.seed", manuallyEdited: true},
		{name: "rpc_created_code_task", promptKey: "main/code-task", createdBy: "rpc.prompts", updatedBy: "rpc.prompts"},
		{name: "rpc_updated_code_task", promptKey: "main/code-task", createdBy: "system.seed", updatedBy: "rpc.prompts"},
		{name: "manually_edited_code_task", promptKey: "main/code-task", createdBy: "system.seed", updatedBy: "system.seed", manuallyEdited: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			truncatePromptCleanupTemplates(t, ctx, conn)
			insertExpertConsolidationTemplate(t, ctx, conn, tc.promptKey, tc.createdBy, tc.updatedBy, tc.manuallyEdited)

			applyPromptTemplateExpertConsolidation0107(t, ctx, conn, schema)

			requirePromptCleanupTemplateState(t, ctx, conn, tc.promptKey, promptCleanupTemplateState{
				CreatedBy:      tc.createdBy,
				UpdatedBy:      tc.updatedBy,
				ManuallyEdited: tc.manuallyEdited,
			})
			requireExpertConsolidationWhenToUseContains(t, ctx, conn, tc.promptKey, []string{"original when " + tc.promptKey})
		})
	}
}

func TestPromptTemplateExpertConsolidationRollbackRestoresOnlySnapshotRows(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	insertExpertConsolidationTemplate(t, ctx, conn, "main/code-task", "system.seed", "system.seed", false)

	applyPromptTemplateExpertConsolidation0107(t, ctx, conn, schema)
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, extractRollbackSQLBlock(t, "0107 data restore"), "0107 data restore")

	for _, key := range duplicateDeveloperExpertKeys {
		requirePromptCleanupTemplateMissing(t, ctx, conn, key)
	}
	requireExpertConsolidationWhenToUseContains(t, ctx, conn, "main/code-task", []string{"original when main/code-task"})
}

func TestPromptTemplateExpertConsolidationRollbackRestoresDeletedSnapshotsWithoutOverwritingUsers(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	for _, key := range duplicateDeveloperExpertKeys {
		insertExpertConsolidationTemplate(t, ctx, conn, key, "system.seed", "migration:0099", false)
	}
	insertExpertConsolidationTemplate(t, ctx, conn, "main/code-task", "system.seed", "system.seed", false)
	applyPromptTemplateExpertConsolidation0107(t, ctx, conn, schema)
	insertExpertConsolidationTemplate(t, ctx, conn, "main/code-generate", "rpc.prompts", "rpc.prompts", true)

	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, extractRollbackSQLBlock(t, "0107 data restore"), "0107 data restore")

	requirePromptCleanupTemplateState(t, ctx, conn, "main/code-generate", promptCleanupTemplateState{
		CreatedBy:      "rpc.prompts",
		UpdatedBy:      "rpc.prompts",
		ManuallyEdited: true,
	})
	for _, key := range []string{"main/code-refactor", "main/code-test", "main/code-explain"} {
		requireExpertConsolidationWhenToUseContains(t, ctx, conn, key, []string{"original when " + key})
		requireExpertConsolidationTagsContain(t, ctx, conn, key, "legacy:"+key)
	}
	requireExpertConsolidationWhenToUseContains(t, ctx, conn, "main/code-task", []string{"original when main/code-task"})
}

func TestPromptTemplateExpertConsolidationWithNullColumns(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)

	// Insert templates with explicit NULL values for variables, tags, and match_when
	_, err := conn.Exec(ctx, `
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, prompt_text, variables, tags, match_when,
    enabled, manually_edited, created_by, updated_by
) VALUES ($1, $1, 'main', $2, NULL, NULL, NULL, TRUE, FALSE, $3, $4)
`, "main/code-generate", "prompt text code-generate", "system.seed", "system.seed")
	if err != nil {
		t.Fatalf("failed to insert template with NULLs: %v", err)
	}

	insertExpertConsolidationTemplate(t, ctx, conn, "main/code-task", "system.seed", "system.seed", false)

	// Applying the migration should not raise NOT NULL constraint violations now
	applyPromptTemplateExpertConsolidation0107(t, ctx, conn, schema)

	// Verify that the duplicate template was successfully processed and deleted
	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/code-generate")
}

func applyPromptTemplateExpertConsolidation0107(t *testing.T, ctx context.Context, conn *pgx.Conn, schema string) {
	t.Helper()

	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, readMigrationFixture(t, promptTemplateExpertConsolidationMigrationName), promptTemplateExpertConsolidationMigrationName)
}

func insertExpertConsolidationTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, createdBy, updatedBy string, manuallyEdited bool) int64 {
	t.Helper()

	var id int64
	err := conn.QueryRow(ctx, `
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, prompt_text, tags, description, when_to_use,
    enabled, manually_edited, created_by, updated_by
) VALUES ($1, $1, 'main', $2, $3::jsonb, $4, $5, TRUE, $6, $7, $8)
RETURNING id
`, promptKey, "prompt text "+promptKey, jsonTags(t, []string{"scope.global", "legacy:" + promptKey}),
		"original description "+promptKey, "original when "+promptKey, manuallyEdited, createdBy, updatedBy).Scan(&id)
	if err != nil {
		t.Fatalf("insert expert consolidation prompt %s: %v", promptKey, err)
	}
	return id
}

func requireExpertConsolidationWhenToUseContains(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string, wants []string) {
	t.Helper()

	var got string
	if err := conn.QueryRow(ctx, `SELECT when_to_use FROM prompt_templates WHERE prompt_key = $1`, promptKey).Scan(&got); err != nil {
		t.Fatalf("query %s when_to_use: %v", promptKey, err)
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("%s when_to_use = %q, want %q", promptKey, got, want)
		}
	}
}

func requireExpertConsolidationTagsContain(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, want string) {
	t.Helper()

	var got string
	if err := conn.QueryRow(ctx, `SELECT tags::text FROM prompt_templates WHERE prompt_key = $1`, promptKey).Scan(&got); err != nil {
		t.Fatalf("query %s tags: %v", promptKey, err)
	}
	if !jsonTextArrayContains(got, want) {
		t.Fatalf("%s tags = %s, want %q", promptKey, got, want)
	}
}
