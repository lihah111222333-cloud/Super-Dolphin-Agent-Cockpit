package archtest_test

import (
	"strings"
	"testing"

	archtest "github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

// TestSkillProductionCodeDoesNotImportPersistence 锁定 Skill 领域层不能直接知道数据库和 Store 实现。
func TestSkillProductionCodeDoesNotImportPersistence(t *testing.T) {
	t.Parallel()

	files := parseImportFiles(t, repoRoot(t), "internal/module/skill")
	for _, file := range files {
		if strings.HasSuffix(file.RelPath, "_test.go") {
			continue
		}
		for _, imported := range file.Imports {
			if imported == "database/sql" || strings.HasPrefix(imported, internalPrefix("internal/store")) {
				t.Errorf("%s imports persistence package %q", file.RelPath, imported)
			}
		}
	}
}

// TestSkillDatabaseBoundaryHasNoExceptions 防止 Skill 数据库依赖以换名例外重新进入 registry。
func TestSkillDatabaseBoundaryHasNoExceptions(t *testing.T) {
	t.Parallel()

	registry := archtest.DefaultBackendBoundaryRegistry()
	for _, rule := range registry.Rules {
		if rule.ID != "module_no_direct_db_imports" {
			continue
		}
		for _, exception := range rule.Exceptions {
			if strings.HasPrefix(exception.FilePattern, "internal/module/skill/") {
				t.Errorf("Skill database boundary exception remains: %s (%s)", exception.ID, exception.FilePattern)
			}
		}
	}
}

// TestSkillToolStoreDoesNotImportSkill 锁定 Store 只能由 app adapter 映射，不能反向知道 Skill。
func TestSkillToolStoreDoesNotImportSkill(t *testing.T) {
	t.Parallel()

	assertNoImportPrefixes(t, parseImportFiles(t, repoRoot(t), "internal/store/skilltool"), []string{
		internalPrefix("internal/module/skill"),
	})
}
