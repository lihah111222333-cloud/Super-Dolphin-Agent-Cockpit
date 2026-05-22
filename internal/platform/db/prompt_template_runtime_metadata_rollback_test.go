package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

const promptTemplateRuntimeMetadataMigrationName = "0106_prompt_template_runtime_metadata.sql"

type dagDesignerPromptState struct {
	Description    string
	WhenToUse      string
	PromptText     string
	ForbiddenText  []string
	Tags           []string
	Enabled        bool
	CreatedBy      string
	UpdatedBy      string
	ManuallyEdited *bool
}

type dagDesignerPromptRow struct {
	Description    string
	WhenToUse      string
	PromptText     string
	Tags           string
	Enabled        bool
	CreatedBy      string
	UpdatedBy      string
	ManuallyEdited bool
}

func TestPromptTemplateRuntimeMetadataMigrationUpdatesDAGDesignerAndRollbackRestores(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	insertDAGDesignerRuntimeTemplateUpdatedByMigration0090(t, ctx, conn, "main/dag_designer_zh", dagDesignerZHSeedText(), dagDesignerZHSeedDescription(), dagDesignerZHSeedTagsWithScope(), "")
	insertDAGDesignerRuntimeTemplateUpdatedByMigration0090(t, ctx, conn, "main/dag_designer_en", dagDesignerENSeedText(), dagDesignerENSeedDescription(), dagDesignerENSeedTagsWithScope(), "")

	applyPromptTemplateRuntimeMetadataMigration0106(t, ctx, conn, schema)

	requireDAGDesignerForwardRuntimeMetadata(t, ctx, conn)

	dataRestore := extractRollbackSQLBlock(t, "0106 data restore")
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0106 data restore")

	requireDAGDesignerRollbackRuntimeMetadata(t, ctx, conn)
}

func requireDAGDesignerForwardRuntimeMetadata(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	requireDAGDesignerPromptState(t, ctx, conn, "main/dag_designer_zh", dagDesignerPromptState{
		Description: "DAG 流程设计师：发现模型、提示词、命令卡和 sharedfile 资源，设计 cron、节点依赖、on_failure、to_node_result/to_sharedfile 输出边界，并写入 DAG。",
		WhenToUse:   "当用户要设计 DAG、定时任务、流程编排、节点依赖、cron 自动化或 sharedfile 输出边界时使用。",
		PromptText:  "transient / quota / validation / capability / hard / needs_human / infrastructure",
		ForbiddenText: []string{
			"timeout / cancelled / unknown / not_implemented",
			"`timeout` / `cancelled` / `unknown` / `not_implemented`",
			`"provider": "claude"`,
			`"model": "opus"`,
			`"model": "sonnet"`,
			`"escalation_chain": ["sonnet","opus"]`,
			`list_models(provider="claude")`,
			`model=sonnet`,
			`provider_from_list_models`,
			`model_from_list_models`,
			`list_models(provider="provider_from_list_models")`,
		},
		Tags:      []string{"scope.global", "intent:dag_designer", "workflow:dag", "workflow:enterprise", "io:sharedfile"},
		Enabled:   true,
		CreatedBy: "system.seed",
		UpdatedBy: "migration:0106",
	})
	requireDAGDesignerPromptState(t, ctx, conn, "main/dag_designer_en", dagDesignerPromptState{
		Description: "English DAG designer mirror. Disabled from default runtime discovery until language or mode filtering exists; keeps schema parity for explicit future use.",
		WhenToUse:   "",
		PromptText:  "transient / quota / validation / capability / hard / needs_human / infrastructure",
		ForbiddenText: []string{
			"timeout / cancelled / unknown / not_implemented",
			"`timeout` / `cancelled` / `unknown` / `not_implemented`",
			`"provider": "claude"`,
			`"model": "opus"`,
			`"model": "sonnet"`,
			`"escalation_chain": ["sonnet","opus"]`,
			`list_models(provider="claude")`,
			`model=sonnet`,
			`provider_from_list_models`,
			`model_from_list_models`,
			`list_models(provider="provider_from_list_models")`,
		},
		Tags:      []string{"intent:dag_designer", "workflow:dag"},
		Enabled:   false,
		CreatedBy: "system.seed",
		UpdatedBy: "migration:0106",
	})
	requireDAGDesignerTemplateHiddenFromDefaultRuntimeDiscovery(t, ctx, conn, "main/dag_designer_en")
	requireDAGDesignerPromptTextContains(t, ctx, conn, "main/dag_designer_zh", []string{
		`list_models()`,
		`model=<selected model from list_models()>`,
		`"verifier":   { "agent_key": "code-review" }`,
		`"escalation_chain": []`,
	})
	requireDAGDesignerPromptTextContains(t, ctx, conn, "main/dag_designer_en", []string{
		`list_models()`,
		`model=<selected model from list_models()>`,
		`"verifier":   { "agent_key": "code-review" }`,
		`"escalation_chain": []`,
	})
}

func requireDAGDesignerRollbackRuntimeMetadata(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()

	requireDAGDesignerPromptState(t, ctx, conn, "main/dag_designer_zh", dagDesignerPromptState{
		Description: dagDesignerZHSeedDescription(),
		WhenToUse:   "",
		PromptText:  "capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented",
		Tags:        []string{"scope.global", "设计 DAG", "流程编排", "定时任务"},
		Enabled:     true,
		CreatedBy:   "system.seed",
		UpdatedBy:   "migration:0090",
	})
	requireDAGDesignerPromptTextContains(t, ctx, conn, "main/dag_designer_zh", []string{
		`"provider": "claude"`,
		`list_models(provider="claude")`,
	})
	requireDAGDesignerPromptState(t, ctx, conn, "main/dag_designer_en", dagDesignerPromptState{
		Description: dagDesignerENSeedDescription(),
		WhenToUse:   "",
		PromptText:  "capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented",
		Tags:        []string{"scope.global", "AI design flow", "workflow design", "daily at 8"},
		Enabled:     true,
		CreatedBy:   "system.seed",
		UpdatedBy:   "migration:0090",
	})
	requireDAGDesignerPromptTextContains(t, ctx, conn, "main/dag_designer_en", []string{
		`"provider": "claude"`,
		`list_models(provider="claude")`,
	})
}

func TestPromptTemplateRuntimeMetadataMigrationPreservesUserOwnedOrEditedDAGDesigner(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)

	cases := []struct {
		name           string
		promptKey      string
		createdBy      string
		updatedBy      string
		manuallyEdited bool
	}{
		{name: "rpc_created_zh", promptKey: "main/dag_designer_zh", createdBy: "rpc.prompts", updatedBy: "rpc.prompts"},
		{name: "rpc_updated_zh", promptKey: "main/dag_designer_zh", createdBy: "system.seed", updatedBy: "rpc.prompts"},
		{name: "manually_edited_zh", promptKey: "main/dag_designer_zh", createdBy: "system.seed", updatedBy: "system.seed", manuallyEdited: true},
		{name: "rpc_created_en", promptKey: "main/dag_designer_en", createdBy: "rpc.prompts", updatedBy: "rpc.prompts"},
		{name: "rpc_updated_en", promptKey: "main/dag_designer_en", createdBy: "system.seed", updatedBy: "rpc.prompts"},
		{name: "manually_edited_en", promptKey: "main/dag_designer_en", createdBy: "system.seed", updatedBy: "system.seed", manuallyEdited: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			truncatePromptCleanupTemplates(t, ctx, conn)
			insertDAGDesignerRuntimeTemplateWithOwner(t, ctx, conn, tc.promptKey, tc.createdBy, tc.updatedBy, tc.manuallyEdited)

			applyPromptTemplateRuntimeMetadataMigration0106(t, ctx, conn, schema)
			dataRestore := extractRollbackSQLBlock(t, "0106 data restore")
			applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0106 data restore")

			requireDAGDesignerPromptState(t, ctx, conn, tc.promptKey, dagDesignerPromptState{
				Description:    "user controlled description",
				WhenToUse:      "user controlled when_to_use",
				PromptText:     "capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented",
				Tags:           []string{"scope.global", "user-owned"},
				Enabled:        true,
				CreatedBy:      tc.createdBy,
				UpdatedBy:      tc.updatedBy,
				ManuallyEdited: boolPtr(tc.manuallyEdited),
			})
		})
	}
}

func applyPromptTemplateRuntimeMetadataMigration0106(t *testing.T, ctx context.Context, conn *pgx.Conn, schema string) {
	t.Helper()

	body := readMigrationFixture(t, promptTemplateRuntimeMetadataMigrationName)
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, body, promptTemplateRuntimeMetadataMigrationName)
}

func insertDAGDesignerRuntimeTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, promptText, description string, tags []string, whenToUse string) {
	t.Helper()

	insertDAGDesignerRuntimeTemplateRaw(t, ctx, conn, promptKey, "system.seed", "system.seed", false, promptText, description, tags, whenToUse)
}

func insertDAGDesignerRuntimeTemplateUpdatedByMigration0090(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, promptText, description string, tags []string, whenToUse string) {
	t.Helper()

	insertDAGDesignerRuntimeTemplateRaw(t, ctx, conn, promptKey, "system.seed", "migration:0090", false, promptText, description, tags, whenToUse)
}

func insertDAGDesignerRuntimeTemplateWithOwner(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, createdBy, updatedBy string, manuallyEdited bool) {
	t.Helper()

	insertDAGDesignerRuntimeTemplateRaw(t, ctx, conn, promptKey, createdBy, updatedBy, manuallyEdited,
		"capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented",
		"user controlled description",
		[]string{"scope.global", "user-owned"},
		"user controlled when_to_use",
	)
}

func insertDAGDesignerRuntimeTemplateRaw(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, createdBy, updatedBy string, manuallyEdited bool, promptText, description string, tags []string, whenToUse string) {
	t.Helper()

	_, err := conn.Exec(ctx, `
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, prompt_text, tags, description, when_to_use,
    enabled, manually_edited, created_by, updated_by
) VALUES ($1, $1, 'dag_designer', $2, $3::jsonb, $4, $5, TRUE, $6, $7, $8)
`, promptKey, promptText, jsonTags(t, tags), description, whenToUse, manuallyEdited, createdBy, updatedBy)
	if err != nil {
		t.Fatalf("insert DAG designer runtime template %s: %v", promptKey, err)
	}
}

func requireDAGDesignerPromptState(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string, want dagDesignerPromptState) {
	t.Helper()

	var got dagDesignerPromptRow
	err := conn.QueryRow(ctx, `
SELECT description, when_to_use, prompt_text, tags::text, enabled, created_by, updated_by, manually_edited
FROM prompt_templates
WHERE prompt_key = $1
`, promptKey).Scan(&got.Description, &got.WhenToUse, &got.PromptText, &got.Tags, &got.Enabled, &got.CreatedBy, &got.UpdatedBy, &got.ManuallyEdited)
	if err != nil {
		t.Fatalf("query DAG designer prompt %s: %v", promptKey, err)
	}
	requireDAGDesignerPromptFields(t, promptKey, got, want)
	requireDAGDesignerPromptTags(t, promptKey, got.Tags, want.Tags)
}

func requireDAGDesignerPromptFields(t *testing.T, promptKey string, got dagDesignerPromptRow, want dagDesignerPromptState) {
	t.Helper()

	checks := []struct {
		field string
		got   string
		want  string
	}{
		{field: "description", got: got.Description, want: want.Description},
		{field: "when_to_use", got: got.WhenToUse, want: want.WhenToUse},
		{field: "updated_by", got: got.UpdatedBy, want: want.UpdatedBy},
	}
	if want.CreatedBy != "" {
		checks = append(checks, struct {
			field string
			got   string
			want  string
		}{field: "created_by", got: got.CreatedBy, want: want.CreatedBy})
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s %s = %q, want %q", promptKey, check.field, check.got, check.want)
		}
	}
	if !strings.Contains(got.PromptText, want.PromptText) {
		t.Fatalf("%s prompt_text = %q, want fragment %q", promptKey, got.PromptText, want.PromptText)
	}
	requireDAGDesignerPromptTextExcludes(t, promptKey, got.PromptText, want.ForbiddenText)
	requireDAGDesignerPromptBooleans(t, promptKey, got, want)
}

func requireDAGDesignerPromptBooleans(t *testing.T, promptKey string, got dagDesignerPromptRow, want dagDesignerPromptState) {
	t.Helper()

	if got.Enabled != want.Enabled {
		t.Fatalf("%s enabled = %v, want %v", promptKey, got.Enabled, want.Enabled)
	}
	if want.ManuallyEdited != nil && got.ManuallyEdited != *want.ManuallyEdited {
		t.Fatalf("%s manually_edited = %v, want %v", promptKey, got.ManuallyEdited, *want.ManuallyEdited)
	}
}

func requireDAGDesignerPromptTags(t *testing.T, promptKey, gotTags string, wantTags []string) {
	t.Helper()

	for _, tag := range wantTags {
		if !jsonTextArrayContains(gotTags, tag) {
			t.Fatalf("%s tags = %s, want tag %q", promptKey, gotTags, tag)
		}
	}
}

func requireDAGDesignerPromptTextExcludes(t *testing.T, promptKey, gotPromptText string, forbiddenText []string) {
	t.Helper()

	for _, forbidden := range forbiddenText {
		if strings.Contains(gotPromptText, forbidden) {
			t.Fatalf("%s prompt_text contains forbidden fragment %q: %q", promptKey, forbidden, gotPromptText)
		}
	}
}

func requireDAGDesignerPromptTextContains(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string, fragments []string) {
	t.Helper()

	var promptText string
	err := conn.QueryRow(ctx, `SELECT prompt_text FROM prompt_templates WHERE prompt_key = $1`, promptKey).Scan(&promptText)
	if err != nil {
		t.Fatalf("query DAG designer prompt text %s: %v", promptKey, err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(promptText, fragment) {
			t.Fatalf("%s prompt_text missing restored fragment %q: %q", promptKey, fragment, promptText)
		}
	}
}

func requireDAGDesignerTemplateHiddenFromDefaultRuntimeDiscovery(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) {
	t.Helper()

	var visible bool
	err := conn.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM prompt_templates
    WHERE prompt_key = $1
      AND enabled = TRUE
      AND (
          tags ? 'scope.global'
          OR tags ? 'scope.cwd:/repo/default'
          OR NOT EXISTS (
              SELECT 1
              FROM jsonb_array_elements_text(tags) tag(value)
              WHERE tag.value = 'scope.global'
                 OR tag.value LIKE 'scope.cwd:%'
          )
      )
)
`, promptKey).Scan(&visible)
	if err != nil {
		t.Fatalf("query default runtime visibility for %s: %v", promptKey, err)
	}
	if visible {
		t.Fatalf("%s remains visible to default runtime discovery", promptKey)
	}
}

func dagDesignerZHSeedText() string {
	return dagDesignerFixedProviderExample() +
		"on_failure.by_class: capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented\n" +
		"FailureClass detail: `capability`(模型力不够 → escalate_model) / `validation`(输出不合 schema → append_error 重试) / `infrastructure`(网络/DB → retry with backoff) / `timeout` / `cancelled` / `unknown` / `not_implemented`"
}

func dagDesignerENSeedText() string {
	return dagDesignerFixedProviderExample() +
		"on_failure.by_class dispatches by FailureClass: capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented\n" +
		"FailureClass detail: `capability` (model not strong enough → escalate_model) / `validation` (output fails schema → append_error and retry) / `infrastructure` (network/DB → retry with backoff) / `timeout` / `cancelled` / `unknown` / `not_implemented`"
}

func dagDesignerFixedProviderExample() string {
	return `{
  "exec": {
    "provider": "claude",
    "model": "opus",
    "on_failure": { "escalation_chain": ["sonnet","opus"] }
  },
  "exec_hybrid": {
    "verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review" }
  }
}
Call list_models(provider="claude") and use model=sonnet in the topology.
`
}

func dagDesignerZHSeedDescription() string {
	return "AI 流程设计师 (中文) — 把用户口语化的需求翻译成可执行 DAG，调 list_models / prompt_list / command_list / shared_file_list 摸清资源后用 task_create_dag / task_dag_apply_ops 落库。Seeded by migration 0084 (F7.1)。"
}

func dagDesignerENSeedDescription() string {
	return "AI Flow Designer (English) — turns natural-language workflow requests into executable DAGs, discovers resources with list_models / prompt_list / command_list / shared_file_list, then persists the design with task_create_dag / task_dag_apply_ops. Seeded by migration 0085 (F7.2)."
}

func dagDesignerZHSeedTagsWithScope() []string {
	return []string{"设计 DAG", "流程编排", "定时任务", "scope.global"}
}

func dagDesignerENSeedTagsWithScope() []string {
	return []string{"AI design flow", "workflow design", "daily at 8", "scope.global"}
}

func jsonTags(t *testing.T, tags []string) string {
	t.Helper()

	quoted := make([]string, 0, len(tags))
	for _, tag := range tags {
		quoted = append(quoted, `"`+tag+`"`)
	}
	return `[` + strings.Join(quoted, ",") + `]`
}

func jsonTextArrayContains(jsonText, value string) bool {
	return strings.Contains(jsonText, `"`+value+`"`)
}
