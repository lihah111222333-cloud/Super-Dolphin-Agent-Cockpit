package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacySkillPipelineIsNotInProductionGraph(t *testing.T) {
	root := repoRoot(t)
	for _, check := range legacySkillPipelineForbiddenChecks() {
		assertFileDoesNotContain(t, root, check.relPath, check.forbidden)
	}
}

type forbiddenFileCheck struct {
	relPath   string
	forbidden []string
}

func legacySkillPipelineForbiddenChecks() []forbiddenFileCheck {
	return []forbiddenFileCheck{
		{relPath: "internal/app/modules.go", forbidden: []string{
			`internal/module/fbsd`, `internal/module/skillforge`, `internal/module/skilllibrary`,
			`provideSkillLibraryConfig`, `provideSkillManifestRenderer`,
		}},
		{relPath: "internal/app/runtimeadapter/toolbridge/adapter.go", forbidden: []string{
			`NewSkillReadSectionTool`, `SkillSectionReader`, `SkillCallRecorder`, `internal/module/skilllibrary`,
		}},
		{relPath: "internal/platform/toolbridge/module.go", forbidden: []string{
			`*SkillReadSectionTool`, `NewSkillReadSectionRegistry`,
		}},
		{relPath: "internal/store/module.go", forbidden: []string{
			`internal/store/skillforge`, `internal/store/skilllibrary`,
			`internal/store/skillcandidate`, `skillcandidate.Module`,
		}},
		{relPath: "internal/module/turn/feedback_proposer.go", forbidden: []string{
			`internal/store/skillcandidate`, `skillcandidate.InsertParams`, `fp.store.Insert`,
		}},
		{relPath: "internal/module/turn/module.go", forbidden: []string{
			`NewDefaultExtractor`, `NewExtractorRunner`,
		}},
		{relPath: "internal/provider/shared/provider_home.go", forbidden: []string{
			"ProviderMirrorTargets", "EnsureNoSkillMirrorConflicts", "AppManagedProviderSkillsRoot", "skill mirror",
		}},
		{relPath: "internal/provider/claudecli/module.go", forbidden: []string{`FBSDRecorder`, `SetFBSDRecorder`}},
		{relPath: "internal/provider/claudecli/factory.go", forbidden: []string{`recordSkillReadIfApplicable`}},
	}
}

func TestLegacySkillPhysicalArtifactsRemoved(t *testing.T) {
	root := repoRoot(t)
	for _, relPath := range []string{
		"internal/contract/skill.go",
		"internal/module/cliadapter",
		"internal/module/fbsd",
		"internal/module/skill",
		"internal/app/storeadapter/skill",
		"internal/store/skilltool",
		"internal/module/skillforge",
		"internal/module/skilllibrary",
		"internal/dto/skill",
		"internal/platform/toolbridge/skill_read_section.go",
		"internal/platform/toolbridge/skill_read_section_test.go",
		"internal/platform/toolbridge/host_tools_skill_read_section_shard19_test.go",
		"internal/provider/claudecli/driver_workspace_skills_test.go",
		"internal/provider/claudecli/fbsd_hook.go",
		"internal/provider/claudecli/fbsd_hook_test.go",
		"internal/module/skill/candidate_review.go",
		"internal/module/skill/candidate_audit.go",
		"internal/module/skill/rpc_candidate_types.go",
		"internal/module/skill/candidate_review_test.go",
		"internal/module/turn/trajectory_collector.go",
		"internal/module/turn/skill_extractor.go",
		"internal/module/turn/skill_evaluator.go",
		"internal/module/turn/expanded_state.go",
	} {
		assertLegacyArtifactRemoved(t, root, relPath)
	}
}

func assertLegacyArtifactRemoved(t *testing.T, root, relPath string) {
	t.Helper()
	path := filepath.Join(root, relPath)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("stat %s: %v", relPath, err)
	}
	if !info.IsDir() {
		t.Fatalf("legacy skill artifact still exists: %s", relPath)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read dir %s: %v", relPath, err)
	}
	if len(entries) > 0 {
		t.Fatalf("legacy skill artifact directory still contains files: %s", relPath)
	}
}

func assertFileDoesNotContain(t *testing.T, root, relPath string, forbidden []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	text := string(raw)
	var violations []string
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			violations = append(violations, relPath+" contains "+needle)
		}
	}
	failIfViolations(t, violations)
}
