package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillDoesNotImportFBSDModule(t *testing.T) {
	root := repoRoot(t)
	assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/module/skill"), []string{internalPrefix("internal/module/fbsd")})
}

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
		{relPath: "internal/module/skill/rpc.go", forbidden: []string{
			`skills/candidate/`, `skillCandidateApproveHandler`, `skillCandidateRejectHandler`,
			`skillCandidateGetHandler`, `skillCandidateListPendingHandler`,
			`skillRPCCandidateParamError`, `ErrCandidate`,
		}},
		{relPath: "internal/module/skill/contract.go", forbidden: []string{
			`skillCandidateReviewer`, `ApproveCandidate`, `RejectCandidate`,
			`ListPendingCandidates`, `LookupApproval`, `GetCandidateByID`,
			`ApproveCandidateParams`, `RejectCandidateParams`, `CandidateListItem`,
			`ErrCandidate`,
		}},
		{relPath: "internal/module/skill/service.go", forbidden: []string{
			`internal/store/skillcandidate`, `candidateStore`, `skillcandidate.Store`,
		}},
		{relPath: "internal/module/skill/module.go", forbidden: []string{
			`internal/store/skillcandidate`, `CandidateStore`,
		}},
		{relPath: "internal/module/turn/feedback_proposer.go", forbidden: []string{
			`internal/store/skillcandidate`, `skillcandidate.InsertParams`, `fp.store.Insert`,
		}},
		{relPath: "internal/module/turn/skill_extractor.go", forbidden: []string{
			`internal/store/skillcandidate`, `skillcandidate.InsertParams`, `e.store.Insert`,
		}},
		{relPath: "internal/module/memory/kairos.go", forbidden: []string{
			`internal/store/skillcandidate`, `candidateInsertStore`, `skillcandidatedto.InsertParams`,
			`feedbackSkillPropose`, `insertCandidate`,
		}},
		{relPath: "internal/module/turn/module.go", forbidden: []string{
			`NewDefaultExtractor`, `NewExtractorRunner`,
		}},
		{relPath: "internal/contract/skill.go", forbidden: legacySkillContractNames()},
		{relPath: "internal/provider/claudecli/module.go", forbidden: []string{`FBSDRecorder`, `SetFBSDRecorder`}},
		{relPath: "internal/provider/claudecli/factory.go", forbidden: []string{`recordSkillReadIfApplicable`}},
	}
}

func legacySkillContractNames() []string {
	return []string{
		`SkillManifestRenderer`, `SkillManifestEntryLister`, `SkillDescriptionParser`, `FBSDRecorder`,
		`SkillLibraryLister`, `SkillForger`, `EmbeddedSkillReader`, `SkillReplacementAggregator`,
		`SkillDisclosureStats`, `SkillDisclosureTierSource`, `StagingRecoveryReport`, `SkillEntryMeta`, `SkillEntry`,
	}
}

func TestLegacySkillPhysicalArtifactsRemoved(t *testing.T) {
	root := repoRoot(t)
	for _, relPath := range []string{
		"internal/module/cliadapter",
		"internal/module/fbsd",
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
