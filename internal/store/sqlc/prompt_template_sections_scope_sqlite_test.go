package sqlc

import (
	"context"
	"database/sql"
	"testing"
)

func TestPromptSectionQueriesPreferProjectScopeOverOlderGlobalRows(t *testing.T) {
	t.Parallel()

	db := openSQLCTestSQLiteDB(t)
	seedPromptSectionScopeRows(t, db)
	q := New(db)
	ctx := context.Background()
	cwd := "/repo/project"

	recall, err := q.ListRecallSections(ctx, ListRecallSectionsParams{CWD: &cwd})
	if err != nil {
		t.Fatalf("ListRecallSections() error = %v", err)
	}
	if len(recall) != 1 || recall[0].TemplatePromptKey != "project-recall" {
		t.Fatalf("ListRecallSections() = %+v, want project-recall override", recall)
	}

	rules, err := q.ListDefaultRuleSections(ctx, ListDefaultRuleSectionsParams{CWD: &cwd})
	if err != nil {
		t.Fatalf("ListDefaultRuleSections() error = %v", err)
	}
	if len(rules) != 1 || rules[0].TemplatePromptKey != "project-rule" {
		t.Fatalf("ListDefaultRuleSections() = %+v, want project-rule override", rules)
	}
}

func seedPromptSectionScopeRows(t *testing.T, db *sql.DB) {
	t.Helper()

	statements := []string{
		`INSERT INTO prompt_templates (
			id, prompt_key, title, agent_key, prompt_text, tags, enabled, priority, created_at, updated_at
		) VALUES
			(101, 'global-recall', 'Recall', 'main', 'global recall', '["scope.global"]', 1, 0, 1, 1),
			(102, 'project-recall', 'Recall', 'main', 'project recall', '["scope.cwd:/repo/project"]', 1, 0, 2, 2),
			(103, 'global-rule', 'Shared rule', 'default_rule', 'global rule', '["scope.global"]', 1, 100, 1, 1),
			(104, 'project-rule', 'Shared rule', 'default_rule', 'project rule', '["scope.cwd:/repo/project"]', 1, 1, 2, 2)`,
		`INSERT INTO prompt_template_sections (
			id, template_id, section_key, region, ordinal, body, enable_when,
			enabled, created_at, updated_at, trigger_type, recall_topic
		) VALUES
			(201, 101, 'recall', 'dynamic', 0, 'global recall body', '{}', 1, 1, 1, 'recall', 'shared-topic'),
			(202, 102, 'recall', 'dynamic', 0, 'project recall body', '{}', 1, 2, 2, 'recall', 'shared-topic'),
			(203, 103, 'rule', 'static', 0, 'global rule body', '{}', 1, 1, 1, 'always', ''),
			(204, 104, 'rule', 'static', 0, 'project rule body', '{}', 1, 2, 2, 'always', '')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed prompt section scope rows: %v", err)
		}
	}
}
