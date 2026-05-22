package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPromptListRuntimeDiscoveryBoundaryAfterBuiltinPromptOptimizationMigrations(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	seedPromptListRuntimeDiscoveryBoundaryFixtures(t, ctx, conn)

	applyPromptTemplateCleanupMigration0105(t, ctx, conn, schema)
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema,
		readMigrationFixture(t, promptTemplateRuntimeMetadataMigrationName),
		promptTemplateRuntimeMetadataMigrationName)
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema,
		readMigrationFixture(t, promptTemplateExpertConsolidationMigrationName),
		promptTemplateExpertConsolidationMigrationName)

	keys := promptListRuntimeVisibleKeys(t, ctx, conn, "/repo/a")
	for _, want := range []string{
		"main/dag_designer_zh",
		"main/morning_briefer",
		"main/code-generate",
	} {
		if !keys[want] {
			t.Fatalf("runtime-visible prompt_list keys missing %q: %#v", want, keys)
		}
	}
	for _, absent := range []string{
		"main/general-en",
		"main/claude-style",
		"main/claude-style-zh",
		"main/writing",
		"main/code-refactor",
		"main/code-test",
		"main/code-explain",
		"sql/expert",
		"main/dag_designer_en",
	} {
		if keys[absent] {
			t.Fatalf("runtime-visible prompt_list keys unexpectedly include %q: %#v", absent, keys)
		}
	}
}

func seedPromptListRuntimeDiscoveryBoundaryFixtures(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	for _, template := range []promptCleanupTemplate{
		{PromptKey: "main/dag_designer_zh", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/dag_designer_en", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/morning_briefer", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/general-en", CreatedBy: "test-seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/claude-style", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/claude-style-zh", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/writing", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/code-refactor", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/code-test", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/code-explain", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "sql/expert", CreatedBy: "system.seed", UpdatedBy: "system.seed"},
		{PromptKey: "main/code-generate", CreatedBy: "rpc.prompts", UpdatedBy: "rpc.prompts"},
	} {
		insertPromptCleanupTemplate(t, ctx, conn, template)
	}
}

func promptListRuntimeVisibleKeys(t *testing.T, ctx context.Context, conn *pgx.Conn, cwd string) map[string]bool {
	t.Helper()

	rows, err := conn.Query(ctx, `
SELECT prompt_key
FROM prompt_templates
WHERE enabled = TRUE
  AND EXISTS (
      SELECT 1
        FROM jsonb_array_elements_text(COALESCE(tags, '[]'::jsonb)) tag(value)
       WHERE tag.value = 'scope.global'
          OR tag.value = 'scope.cwd:' || $1::text
  )
ORDER BY prompt_key
`, cwd)
	if err != nil {
		t.Fatalf("query prompt_list runtime-visible keys: %v", err)
	}
	defer rows.Close()

	keys := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan prompt_list runtime-visible key: %v", err)
		}
		keys[key] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate prompt_list runtime-visible keys: %v", err)
	}
	return keys
}
