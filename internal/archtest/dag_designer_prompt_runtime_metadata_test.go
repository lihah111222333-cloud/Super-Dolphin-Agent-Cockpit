package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDAGDesignerPromptRuntimeMetadataMigration0106(t *testing.T) {
	content := readDAGDesignerRuntimeMetadataMigration(t)
	block := dagDesignerRuntimeMetadataBlock(t, content)

	requireRuntimeMetadataContainsAll(t, block, "0106 DAG designer metadata migration", []string{
		"main/dag_designer_zh",
		"main/dag_designer_en",
		"when_to_use",
		"description",
		"tags",
		"scope.global",
		"scope.cwd:%",
		"REPLACE(",
		"enabled = FALSE",
		"updated_by = 'migration:0106'",
		"created_by IN ('system.seed', 'seed')",
		"updated_by IN ('system.seed', 'seed', 'migration')",
		"updated_by LIKE 'system.%'",
		"updated_by LIKE 'migration:%'",
		"manually_edited = FALSE",
	})

	requireRuntimeMetadataContainsAll(t, block, "0106 DAG designer migration current FailureClass", currentFailureClassLiterals())
	requireRuntimeMetadataContainsAll(t, block, "0106 obsolete FailureClass replacement", obsoleteFailureClassLiterals())
	assertDAGDesigner0106DoesNotInlinePromptBodies(t, block)
	if strings.Contains(block, "main/dag_designer_en'\n  AND when_to_use") ||
		strings.Contains(block, "main/dag_designer_en',\n    when_to_use") {
		t.Fatal("0106 must not set when_to_use for main/dag_designer_en")
	}
}

func TestDAGDesignerPromptRuntimeMetadataMigration0106CleansRealSeedPromptText(t *testing.T) {
	migration := readDAGDesignerRuntimeMetadataMigration(t)
	replacements := dagDesigner0106PromptTextReplacements()
	require0106PromptTextReplacementsInMigration(t, migration, replacements)

	cases := []struct {
		label string
		path  string
	}{
		{
			label: "0084 zh seed",
			path:  filepath.Join(repoRoot(t), "migrations", "0084_seed_dag_designer_prompt_zh.sql"),
		},
		{
			label: "0085 en seed",
			path:  filepath.Join(repoRoot(t), "migrations", "0085_seed_dag_designer_prompt_en.sql"),
		},
	}
	for _, tc := range cases {
		requireDAGDesignerSeedPromptTextCleanedBy0106(t, tc.label, tc.path, replacements)
	}
}

func TestDAGDesignerPromptRuntimeMetadataTracksFailureClassSource(t *testing.T) {
	sourcePath := filepath.Join(repoRoot(t), "cmd", "mcp-orch", "orchestration", "nodeexec", "types.go")
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read nodeexec types.go: %v", err)
	}
	source := string(data)

	for _, current := range currentFailureClassLiterals() {
		if !strings.Contains(source, `FailureClass = "`+current+`"`) {
			t.Fatalf("nodeexec source missing FailureClass %q", current)
		}
	}
	for _, obsolete := range []string{"timeout", "cancelled", "unknown", "not_implemented"} {
		if strings.Contains(source, `FailureClass = "`+obsolete+`"`) {
			t.Fatalf("nodeexec source unexpectedly reintroduced obsolete FailureClass %q", obsolete)
		}
	}
}

func TestDAGDesignerPromptRuntimeRollbackBlock0106(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "ROLLBACK.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ROLLBACK.md: %v", err)
	}
	content := string(data)
	marker := "0106 data restore"
	start := strings.Index(content, marker)
	if start < 0 {
		t.Fatalf("ROLLBACK.md missing %q block", marker)
	}
	block := content[start:]
	for _, marker := range []string{
		"```sql",
		"main/dag_designer_zh",
		"main/dag_designer_en",
		"Restores pre-0106 default-visible English DAG designer scope",
		"enabled = TRUE",
		"created_by IN ('system.seed', 'seed')",
		"updated_by IN ('system.seed', 'seed', 'migration')",
		"updated_by LIKE 'system.%'",
		"updated_by LIKE 'migration:%'",
		"manually_edited = FALSE",
	} {
		if !strings.Contains(block, marker) {
			t.Fatalf("0106 rollback block missing %q", marker)
		}
	}
	if strings.Contains(block, "DELETE FROM schema_migrations") {
		t.Fatal("0106 data restore block must not include schema_migrations bookkeeping")
	}
}

func readDAGDesignerRuntimeMetadataMigration(t *testing.T) string {
	t.Helper()

	path := filepath.Join(repoRoot(t), "migrations", "0106_prompt_template_runtime_metadata.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0106: %v", err)
	}
	return string(data)
}

func requireRuntimeMetadataContainsAll(t *testing.T, content, label string, values []string) {
	t.Helper()

	for _, value := range values {
		if !strings.Contains(content, value) {
			t.Fatalf("%s missing %q", label, value)
		}
	}
}

func require0106PromptTextReplacementsInMigration(t *testing.T, migration string, replacements map[string]string) {
	t.Helper()

	for old, new := range replacements {
		if !containsMigrationReplacementLiteral(migration, old) {
			t.Fatalf("0106 migration missing real-seed replacement source %q", old)
		}
		if !containsMigrationReplacementLiteral(migration, new) {
			t.Fatalf("0106 migration missing real-seed replacement target %q", new)
		}
	}
}

func requireDAGDesignerSeedPromptTextCleanedBy0106(t *testing.T, label, path string, replacements map[string]string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", label, err)
	}
	seed := string(data)
	requireRuntimeMetadataContainsAll(t, seed, label+" historical obsolete FailureClass", obsoleteFailureClassLiterals())
	requireRuntimeMetadataContainsAll(t, seed, label+" historical fixed provider/model examples", fixedProviderModelExampleLiterals())

	migrated := applyPromptTextReplacements(seed, replacements)
	requireRuntimeMetadataExcludesAll(t, migrated, label+" obsolete FailureClass", obsoleteFailureClassLiterals())
	requireRuntimeMetadataExcludesAll(t, migrated, label+" fixed provider/model examples", fixedProviderModelExampleLiterals())
	requireRuntimeMetadataExcludesAll(t, migrated, label+" invalid provider/model placeholders", invalidProviderModelPlaceholderLiterals())
	requireRuntimeMetadataContainsAll(t, migrated, label+" current FailureClass", currentFailureClassLiterals())
	requireRuntimeMetadataContainsAll(t, migrated, label+" provider-neutral model discovery examples", providerNeutralModelDiscoveryLiterals())
	requireRuntimeMetadataContainsAll(t, migrated, label+" schema and tool surface", dagDesignerRequiredRuntimeTokens())
}

func applyPromptTextReplacements(content string, replacements map[string]string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	for old, new := range replacements {
		content = strings.ReplaceAll(content, old, new)
	}
	return content
}

func requireRuntimeMetadataExcludesAll(t *testing.T, content, label string, values []string) {
	t.Helper()

	for _, value := range values {
		if strings.Contains(content, value) {
			t.Fatalf("%s still contains %q after applying 0106 replacements", label, value)
		}
	}
}

func containsMigrationReplacementLiteral(content, literal string) bool {
	return strings.Contains(content, literal) ||
		strings.Contains(content, strings.ReplaceAll(literal, "\n", `\n`))
}

func is0106DAGDesignerPromptTextReplacementLiteral(name, literal string) bool {
	if !strings.Contains(name, promptTemplateRuntimeMetadataMigrationNameForArchtest()) {
		return false
	}
	for _, allowed := range dagDesigner0106PromptTextReplacementLiteralVariants() {
		if literal == allowed {
			return true
		}
	}
	return false
}

func strip0106DAGDesignerPromptTextReplacementLiterals(sql string) string {
	for _, literal := range dagDesigner0106PromptTextReplacementLiteralVariants() {
		sql = strings.ReplaceAll(sql, literal, "dag_designer_prompt_text_replacement")
	}
	return sql
}

func dagDesigner0106PromptTextReplacementLiteralVariants() []string {
	seen := map[string]bool{}
	var variants []string
	for old, new := range dagDesigner0106PromptTextReplacements() {
		for _, literal := range []string{old, new} {
			for _, variant := range []string{literal, strings.ReplaceAll(literal, "\n", `\n`)} {
				if !seen[variant] {
					seen[variant] = true
					variants = append(variants, variant)
				}
			}
		}
	}
	return variants
}

func promptTemplateRuntimeMetadataMigrationNameForArchtest() string {
	return "0106_prompt_template_runtime_metadata.sql"
}

func dagDesignerRuntimeMetadataBlock(t *testing.T, content string) string {
	t.Helper()

	end := strings.Index(content, "\n-- Enterprise workflow preset discovery metadata")
	if end < 0 {
		t.Fatal("0106 DAG designer metadata block missing enterprise boundary")
	}
	return content[:end]
}

func assertDAGDesigner0106DoesNotInlinePromptBodies(t *testing.T, content string) {
	t.Helper()

	normalized := strings.ToLower(content)
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`(?s)\binsert\s+into\s+(?:public\.)?prompt_templates\b`),
		regexp.MustCompile(`(?s)\binsert\s+into\s+(?:public\.)?prompt_template_sections\b`),
		regexp.MustCompile(`(?s)\bon\s+conflict\b`),
		regexp.MustCompile(`(?s)\$[a-z0-9_]*\$`),
	} {
		if forbidden.MatchString(normalized) {
			t.Fatalf("0106 must not re-inline DAG designer prompt bodies; matched %q", forbidden.String())
		}
	}
}

func dagDesigner0106FailureClassReplacements() map[string]string {
	return map[string]string{
		"capability / validation / infrastructure / timeout / cancelled / unknown / not_implemented": "transient / quota / validation / capability / hard / needs_human / infrastructure",
		"`timeout` / `cancelled` / `unknown` / `not_implemented`":                                    "`hard` / `needs_human` / `transient` / `quota`",
	}
}

func dagDesigner0106ProviderModelReplacements() map[string]string {
	return map[string]string{
		`    "provider": "claude",
    "model": "opus",
    "agent_key": "code-debug",`: `    "agent_key": "code-debug",`,
		`    "provider": "claude",
    "model": "opus",`: `    "model": "<selected model from list_models()>",`,
		`"escalation_chain": ["sonnet","opus"]`: `"escalation_chain": []`,
		`"verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review", "cwd": "/absolute/path/to/project" }`: `"verifier":   { "agent_key": "code-review", "cwd": "/absolute/path/to/project" }`,
		`"verifier":   { "provider": "claude", "model": "sonnet", "agent_key": "code-review" }`:                                     `"verifier":   { "agent_key": "code-review" }`,
		`list_models(provider="claude")`: `list_models()`,
		`model=sonnet`:                   `model=<selected model from list_models()>`,
	}
}

func dagDesigner0106PromptTextReplacements() map[string]string {
	replacements := dagDesigner0106FailureClassReplacements()
	for old, new := range dagDesigner0106ProviderModelReplacements() {
		replacements[old] = new
	}
	return replacements
}

func currentFailureClassLiterals() []string {
	return []string{
		"transient",
		"quota",
		"validation",
		"capability",
		"hard",
		"needs_human",
		"infrastructure",
	}
}

func obsoleteFailureClassLiterals() []string {
	return []string{"timeout", "cancelled", "unknown", "not_implemented"}
}

func fixedProviderModelExampleLiterals() []string {
	return []string{
		`"provider": "claude"`,
		`"model": "opus"`,
		`"model": "sonnet"`,
		`"escalation_chain": ["sonnet","opus"]`,
		`list_models(provider="claude")`,
		`model=sonnet`,
	}
}

func invalidProviderModelPlaceholderLiterals() []string {
	return []string{
		`provider_from_list_models`,
		`model_from_list_models`,
		`list_models(provider="provider_from_list_models")`,
	}
}

func providerNeutralModelDiscoveryLiterals() []string {
	return []string{
		`    "agent_key": "code-debug",`,
		`"escalation_chain": []`,
		`"verifier":   { "agent_key": "code-review", "cwd": "/absolute/path/to/project" }`,
		`list_models()`,
		`model=<selected model from list_models()>`,
	}
}

func dagDesignerRequiredRuntimeTokens() []string {
	return []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"task_create_dag",
		"task_dag_apply_ops",
		"task_get_dag",
		"final_node_key",
		"final_output",
		"to_node_result",
		"to_sharedfile",
		"on_failure",
	}
}
