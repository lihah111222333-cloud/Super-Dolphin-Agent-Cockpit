package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPromptTemplateRuntimeMetadataMigrationLeavesAssetBackedDeveloperRosterOutOfDB(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	createPromptTemplateRecallTopicIndex(t, ctx, conn)

	applyPromptTemplateMigrationFixture(t, ctx, conn, schema, "0040_prompt_templates_production_v3.sql")
	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/git-ops")
	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/docs")
	requireRosterRepairPromptField(t, ctx, conn, "main/orchestrator", "when_to_use", "")

	seedPromptTemplateMigration0100Prerequisites(t, ctx, conn)
	applyPromptTemplateMigrationFixture(t, ctx, conn, schema, "0100_seed_recall_packs_and_when_to_use.sql")
	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/git-ops")
	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/docs")
	requireRosterRepairPromptField(t, ctx, conn, "main/orchestrator", "when_to_use", "")

	applyPromptTemplateCleanupMigration0105(t, ctx, conn, schema)
	applyPromptTemplateRuntimeMetadataMigration0106(t, ctx, conn, schema)

	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/git-ops")
	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/docs")
	requireRosterRepairRuntimeVisibleExpert(t, ctx, conn, "main/orchestrator")
	requireRosterRepairPromptFieldExcludes(t, ctx, conn, "main/orchestrator", "when_to_use", []string{
		"DAG", "cron", "节点依赖", "sharedfile", "流程编排",
	})
	requireRosterRepairPromptTagsExclude(t, ctx, conn, "main/orchestrator", []string{"workflow:dag"})
	requireDefaultDeveloperExpertSubsetSize(t, ctx, conn, 8)

	dataRestore := extractRollbackSQLBlock(t, "0106 data restore")
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0106 data restore")

	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/git-ops")
	requirePromptCleanupTemplateMissing(t, ctx, conn, "main/docs")
	requireRosterRepairPromptField(t, ctx, conn, "main/orchestrator", "description", "多 agent 编排")
	requireRosterRepairPromptField(t, ctx, conn, "main/orchestrator", "when_to_use", "")
	row := queryEnterprisePromptRow(t, ctx, conn, "main/orchestrator")
	requireEnterpriseTagsExact(t, "main/orchestrator", row.Tags, []string{
		"orchestrator", "orchestrate", "coordinate", "delegate", "multi-agent",
		"multi agent", "sub-agent", "sub agent", "plan and delegate", "decompose",
		"break down", "拆分任务", "多 agent 协作", "子 agent 协作", "编排", "协调多个",
	})
}

func TestPromptTemplateRuntimeMetadataRosterRepairPreservesUserOwnedOrEditedRows(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)

	cases := []struct {
		name           string
		promptKey      string
		createdBy      string
		updatedBy      string
		manuallyEdited bool
	}{
		{name: "rpc_created_docs", promptKey: "main/docs", createdBy: "rpc.prompts", updatedBy: "rpc.prompts", manuallyEdited: true},
		{name: "rpc_updated_git_ops", promptKey: "main/git-ops", createdBy: "system.seed", updatedBy: "rpc.prompts"},
		{name: "manually_edited_orchestrator", promptKey: "main/orchestrator", createdBy: "system.seed", updatedBy: "system.seed", manuallyEdited: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			truncatePromptCleanupTemplates(t, ctx, conn)
			insertRosterRepairTemplate(t, ctx, conn, tc.promptKey, tc.createdBy, tc.updatedBy, tc.manuallyEdited)

			applyPromptTemplateRuntimeMetadataMigration0106(t, ctx, conn, schema)
			dataRestore := extractRollbackSQLBlock(t, "0106 data restore")
			applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0106 data restore")

			requirePromptCleanupTemplateState(t, ctx, conn, tc.promptKey, promptCleanupTemplateState{
				CreatedBy:      tc.createdBy,
				UpdatedBy:      tc.updatedBy,
				ManuallyEdited: tc.manuallyEdited,
				PromptText:     "user-controlled " + tc.promptKey,
			})
		})
	}
}

func createPromptTemplateRecallTopicIndex(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	_, err := conn.Exec(ctx, `
CREATE UNIQUE INDEX prompt_template_sections_recall_topic_uidx
ON prompt_template_sections (recall_topic)
WHERE trigger_type = 'recall' AND recall_topic <> ''
`)
	if err != nil {
		t.Fatalf("create recall topic unique index: %v", err)
	}
}

func seedPromptTemplateMigration0100Prerequisites(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	templateID := insertPromptCleanupTemplate(t, ctx, conn, promptCleanupTemplate{
		PromptKey: "main/general-zh",
		CreatedBy: "system.seed",
		UpdatedBy: "system.seed",
	})
	insertPromptCleanupSection(t, ctx, conn, templateID, "lsp_basics", "LSP basics prerequisite")
	insertPromptCleanupSection(t, ctx, conn, templateID, "lsp_advanced", "LSP advanced prerequisite")
}

func applyPromptTemplateMigrationFixture(t *testing.T, ctx context.Context, conn *pgx.Conn, schema, name string) {
	t.Helper()

	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, readMigrationFixture(t, name), name)
}

func insertRosterRepairTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, createdBy, updatedBy string, manuallyEdited bool) {
	t.Helper()

	_, err := conn.Exec(ctx, `
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, prompt_text, tags, description, when_to_use,
    enabled, manually_edited, created_by, updated_by
) VALUES ($1, $1, 'main', $2, '["scope.global","user-owned"]'::jsonb,
          'user description', 'user when_to_use', TRUE, $3, $4, $5)
`, promptKey, "user-controlled "+promptKey, manuallyEdited, createdBy, updatedBy)
	if err != nil {
		t.Fatalf("insert roster repair prompt %s: %v", promptKey, err)
	}
}

func requireRosterRepairRuntimeVisibleExpert(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) {
	t.Helper()

	var enabled bool
	var whenToUse string
	var tags string
	var createdBy string
	var updatedBy string
	var manuallyEdited bool
	err := conn.QueryRow(ctx, `
SELECT enabled, when_to_use, tags::text, created_by, updated_by, manually_edited
FROM prompt_templates
WHERE prompt_key = $1
`, promptKey).Scan(&enabled, &whenToUse, &tags, &createdBy, &updatedBy, &manuallyEdited)
	if err != nil {
		t.Fatalf("query roster repair prompt %s: %v", promptKey, err)
	}
	if !enabled || whenToUse == "" || !isSystemOwnedPromptCreator(createdBy) || updatedBy != "migration:0106" || manuallyEdited {
		t.Fatalf("%s runtime state = enabled:%v when_to_use:%q created_by:%q updated_by:%q manually_edited:%v",
			promptKey, enabled, whenToUse, createdBy, updatedBy, manuallyEdited)
	}
	for _, tag := range []string{"scope.global", "intent:expert", "domain:developer"} {
		if !jsonTextArrayContains(tags, tag) {
			t.Fatalf("%s tags = %s, want %q", promptKey, tags, tag)
		}
	}
}

func isSystemOwnedPromptCreator(createdBy string) bool {
	return createdBy == "system.seed" || createdBy == "seed"
}

func requireRosterRepairPromptField(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, field, want string) {
	t.Helper()

	var got string
	err := conn.QueryRow(ctx, "SELECT "+field+" FROM prompt_templates WHERE prompt_key = $1", promptKey).Scan(&got)
	if err != nil {
		t.Fatalf("query %s %s: %v", promptKey, field, err)
	}
	if got != want {
		t.Fatalf("%s %s = %q, want %q", promptKey, field, got, want)
	}
}

func requireRosterRepairPromptFieldExcludes(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, field string, forbidden []string) {
	t.Helper()

	var got string
	err := conn.QueryRow(ctx, "SELECT "+field+" FROM prompt_templates WHERE prompt_key = $1", promptKey).Scan(&got)
	if err != nil {
		t.Fatalf("query %s %s: %v", promptKey, field, err)
	}
	for _, value := range forbidden {
		if strings.Contains(got, value) {
			t.Fatalf("%s %s = %q, must not contain %q", promptKey, field, got, value)
		}
	}
}

func requireRosterRepairPromptTagsExclude(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string, forbidden []string) {
	t.Helper()

	var tags string
	err := conn.QueryRow(ctx, "SELECT tags::text FROM prompt_templates WHERE prompt_key = $1", promptKey).Scan(&tags)
	if err != nil {
		t.Fatalf("query %s tags: %v", promptKey, err)
	}
	for _, tag := range forbidden {
		if jsonTextArrayContains(tags, tag) {
			t.Fatalf("%s tags = %s, must not contain %q", promptKey, tags, tag)
		}
	}
}

func requireDefaultDeveloperExpertSubsetSize(t *testing.T, ctx context.Context, conn *pgx.Conn, max int) {
	t.Helper()

	var count int
	err := conn.QueryRow(ctx, `
WITH developer_keys(prompt_key) AS (
    VALUES
        ('main/code-review'),
        ('main/code-debug'),
        ('main/code-task'),
        ('main/sql'),
        ('main/planning'),
        ('main/git-ops'),
        ('main/docs'),
        ('main/orchestrator')
)
SELECT COUNT(*)
FROM prompt_templates p
JOIN developer_keys d ON d.prompt_key = p.prompt_key
WHERE p.created_by IN ('system.seed', 'seed')
  AND p.enabled = TRUE
  AND BTRIM(p.when_to_use) <> ''
`).Scan(&count)
	if err != nil {
		t.Fatalf("count default developer experts: %v", err)
	}
	if count > max {
		t.Fatalf("default developer expert subset count = %d, want <= %d", count, max)
	}
}
