package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

const promptBuiltinRegistryMigrationName = "0104_disable_registry_backed_system_seed_prompts.sql"

func TestPromptBuiltinRegistryMigrationDisablesOnlyRegistryBackedSeeds(t *testing.T) {
	t.Parallel()

	content := readMigrationFixture(t, promptBuiltinRegistryMigrationName)
	checks := []string{
		"WITH registry_backed(prompt_key) AS",
		"('main/default')",
		"('main/general-zh')",
		"UPDATE public.prompt_templates t",
		"SET enabled = FALSE",
		"updated_by = 'system.registry-migration'",
		"WHERE t.prompt_key = r.prompt_key",
		"AND t.enabled = TRUE",
		"AND t.created_by IN ('system.seed', 'seed')",
		"AND (t.updated_by IN ('system.seed', 'seed', 'migration') OR t.updated_by LIKE 'system.%')",
		"AND t.manually_edited = FALSE",
	}
	for _, needle := range checks {
		if !strings.Contains(content, needle) {
			t.Fatalf("%s missing %q", promptBuiltinRegistryMigrationName, needle)
		}
	}
}

func TestPromptBuiltinRegistryMigrationDoesNotDeleteOrTouchUserAssets(t *testing.T) {
	t.Parallel()

	content := readMigrationFixture(t, promptBuiltinRegistryMigrationName)
	upper := strings.ToUpper(content)
	if strings.Contains(upper, "DELETE FROM PUBLIC.PROMPT_TEMPLATES") || strings.Contains(upper, "DELETE FROM PROMPT_TEMPLATES") {
		t.Fatalf("%s must not delete prompt_templates rows", promptBuiltinRegistryMigrationName)
	}
	if strings.Contains(content, "rpc.prompts") {
		t.Fatalf("%s must not target rpc.prompts user assets", promptBuiltinRegistryMigrationName)
	}
	if strings.Count(content, "main/default") != 1 {
		t.Fatalf("%s must whitelist main/default exactly once", promptBuiltinRegistryMigrationName)
	}
	if strings.Count(content, "main/general-zh") != 1 {
		t.Fatalf("%s must whitelist main/general-zh exactly once", promptBuiltinRegistryMigrationName)
	}
}

func TestPromptBuiltinRegistryMigrationExecutesConservativeUpdate(t *testing.T) {
	t.Parallel()

	ctx, conn, schema := setupPromptIntentMigrationTest(t)
	insertPromptBuiltinRegistryTemplate(t, ctx, conn, promptBuiltinRegistryTemplate{
		PromptKey: "main/default",
		Enabled:   true,
		CreatedBy: "system.seed",
		UpdatedBy: "system.seed",
	})
	insertPromptBuiltinRegistryTemplate(t, ctx, conn, promptBuiltinRegistryTemplate{
		PromptKey: "main/general-zh",
		Enabled:   true,
		CreatedBy: "seed",
		UpdatedBy: "migration",
	})
	insertPromptBuiltinRegistryTemplate(t, ctx, conn, promptBuiltinRegistryTemplate{
		PromptKey: "main/not-registry-backed",
		Enabled:   true,
		CreatedBy: "system.seed",
		UpdatedBy: "system.seed",
	})
	insertPromptBuiltinRegistryTemplate(t, ctx, conn, promptBuiltinRegistryTemplate{
		PromptKey: "user/imported",
		Enabled:   true,
		CreatedBy: "rpc.prompts",
		UpdatedBy: "rpc.prompts",
	})

	applyPromptBuiltinRegistryMigration0104(t, ctx, conn, schema)

	requirePromptBuiltinRegistryTemplateState(t, ctx, conn, "main/default", false, "system.registry-migration")
	requirePromptBuiltinRegistryTemplateState(t, ctx, conn, "main/general-zh", false, "system.registry-migration")
	requirePromptBuiltinRegistryTemplateState(t, ctx, conn, "main/not-registry-backed", true, "system.seed")
	requirePromptBuiltinRegistryTemplateState(t, ctx, conn, "user/imported", true, "rpc.prompts")
	requirePromptBuiltinRegistryTemplateCount(t, ctx, conn, 4)
}

func TestPromptBuiltinRegistryMigrationPreservesWhitelistedUserOwnedRows(t *testing.T) {
	t.Parallel()

	ctx, conn, schema := setupPromptIntentMigrationTest(t)
	insertPromptBuiltinRegistryTemplate(t, ctx, conn, promptBuiltinRegistryTemplate{
		PromptKey: "main/default",
		Enabled:   true,
		CreatedBy: "rpc.prompts",
		UpdatedBy: "rpc.prompts",
	})
	insertPromptBuiltinRegistryTemplate(t, ctx, conn, promptBuiltinRegistryTemplate{
		PromptKey: "main/general-zh",
		Enabled:   true,
		CreatedBy: "system.seed",
		UpdatedBy: "rpc.prompts",
	})

	applyPromptBuiltinRegistryMigration0104(t, ctx, conn, schema)

	requirePromptBuiltinRegistryTemplateState(t, ctx, conn, "main/default", true, "rpc.prompts")
	requirePromptBuiltinRegistryTemplateState(t, ctx, conn, "main/general-zh", true, "rpc.prompts")
}

func TestPromptBuiltinRegistryMigrationPreservesManuallyEditedWhitelistedSeed(t *testing.T) {
	t.Parallel()

	ctx, conn, schema := setupPromptIntentMigrationTest(t)
	insertPromptBuiltinRegistryTemplate(t, ctx, conn, promptBuiltinRegistryTemplate{
		PromptKey:      "main/default",
		Enabled:        true,
		ManuallyEdited: true,
		CreatedBy:      "system.seed",
		UpdatedBy:      "system.seed",
	})

	applyPromptBuiltinRegistryMigration0104(t, ctx, conn, schema)

	requirePromptBuiltinRegistryTemplateState(t, ctx, conn, "main/default", true, "system.seed")
}

type promptBuiltinRegistryTemplate struct {
	PromptKey      string
	Enabled        bool
	ManuallyEdited bool
	CreatedBy      string
	UpdatedBy      string
}

func insertPromptBuiltinRegistryTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, template promptBuiltinRegistryTemplate) {
	t.Helper()

	_, err := conn.Exec(ctx, `
INSERT INTO prompt_templates (prompt_key, title, enabled, manually_edited, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, $6)
`, template.PromptKey, template.PromptKey, template.Enabled, template.ManuallyEdited, template.CreatedBy, template.UpdatedBy)
	if err != nil {
		t.Fatalf("insert prompt template %s: %v", template.PromptKey, err)
	}
}

func applyPromptBuiltinRegistryMigration0104(t *testing.T, ctx context.Context, conn *pgx.Conn, schema string) {
	t.Helper()

	body := readMigrationFixture(t, promptBuiltinRegistryMigrationName)
	schemaPromptTemplates := pgx.Identifier{schema}.Sanitize() + ".prompt_templates"
	body = strings.ReplaceAll(body, "public.prompt_templates", schemaPromptTemplates)
	if _, err := conn.Exec(ctx, body); err != nil {
		t.Fatalf("apply %s: %v", promptBuiltinRegistryMigrationName, err)
	}
}

func requirePromptBuiltinRegistryTemplateState(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string, wantEnabled bool, wantUpdatedBy string) {
	t.Helper()

	var enabled bool
	var updatedBy string
	err := conn.QueryRow(ctx, `
SELECT enabled, updated_by
FROM prompt_templates
WHERE prompt_key = $1
`, promptKey).Scan(&enabled, &updatedBy)
	if err != nil {
		t.Fatalf("query prompt template %s: %v", promptKey, err)
	}
	if enabled != wantEnabled || updatedBy != wantUpdatedBy {
		t.Fatalf("prompt template %s state = enabled:%v updated_by:%q, want enabled:%v updated_by:%q",
			promptKey, enabled, updatedBy, wantEnabled, wantUpdatedBy)
	}
}

func requirePromptBuiltinRegistryTemplateCount(t *testing.T, ctx context.Context, conn *pgx.Conn, want int) {
	t.Helper()

	var got int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM prompt_templates`).Scan(&got); err != nil {
		t.Fatalf("count prompt_templates: %v", err)
	}
	if got != want {
		t.Fatalf("prompt_templates count = %d, want %d", got, want)
	}
}
