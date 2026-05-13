package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDAGSkillSeedMigration_UsesManualEditGuard(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "0086_prompt_template_manually_edited.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0086: %v", err)
	}
	content := string(data)

	for _, must := range []string{
		"ALTER TABLE public.prompt_templates",
		"ADD COLUMN IF NOT EXISTS manually_edited BOOLEAN NOT NULL DEFAULT FALSE",
	} {
		if !strings.Contains(content, must) {
			t.Errorf("migration 0086 missing manual-edit guard DDL %q", must)
		}
	}
}

func TestDAGSkillPromptSeeds_CoverSkillCardLibrary(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "0087_seed_prompt_template_skill_cards.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0087: %v", err)
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		t.Fatalf("migration 0087 is empty; F7.3 must seed prompt_template skill cards")
	}

	requiredKeys := []string{
		"main/morning_briefer",
		"main/paper_summarizer",
		"main/pr_summarizer",
		"main/weekly_reviewer",
		"main/data_inspector",
		"main/email_drafter",
		"main/health_reporter",
		"main/topic_curator",
		"main/source_monitor",
		"main/note_organizer",
		"main/todo_prioritizer",
		"main/learning_card",
		"main/trip_briefer",
	}
	seedBlocks := promptTemplateSeedBlocks(content)
	if got := len(seedBlocks); got != len(requiredKeys) {
		t.Fatalf("migration 0087 seed count = %d, want exactly %d", got, len(requiredKeys))
	}
	for _, key := range requiredKeys {
		block, ok := seedBlocks[key]
		if !ok {
			t.Errorf("migration 0087 missing required skill seed %q", key)
			continue
		}
		for _, must := range []string{
			"    '{}'::jsonb,",
			"\n    TRUE,\n    FALSE,\n    'system.seed',\n    'system.seed',",
		} {
			if !strings.Contains(block, must) {
				t.Errorf("migration 0087 seed %q missing per-row contract %q", key, must)
			}
		}
	}

	for _, must := range []string{
		"INSERT INTO public.prompt_templates",
		"ON CONFLICT (prompt_key) DO UPDATE SET",
		"WHERE public.prompt_templates.manually_edited = FALSE",
	} {
		if !strings.Contains(content, must) {
			t.Errorf("migration 0087 missing seed contract marker %q", must)
		}
	}
}

func promptTemplateSeedBlocks(content string) map[string]string {
	const rowStart = "(\n    '"
	parts := strings.Split(content, rowStart)
	blocks := make(map[string]string)
	for _, part := range parts[1:] {
		keyEnd := strings.Index(part, "',")
		if keyEnd < 0 {
			continue
		}
		key := part[:keyEnd]
		if !strings.HasPrefix(key, "main/") {
			continue
		}
		blocks[key] = rowStart + part
	}
	return blocks
}

func TestDAGSkillPromptSeeds_AvoidDeadRoutingAndTemplatePlaceholders(t *testing.T) {
	path := filepath.Join(repoRoot(t), "migrations", "0087_seed_prompt_template_skill_cards.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration 0087: %v", err)
	}
	content := string(data)

	for _, forbidden := range []string{
		"router_priority",
		"{{",
		"}}",
		"routing keyword",
		"router hit",
		"靠 tag 命中",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("migration 0087 must not contain %q", forbidden)
		}
	}
}
