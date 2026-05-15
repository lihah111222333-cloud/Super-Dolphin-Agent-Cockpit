package prompt

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func assertStartAssemblyBaseContent(t *testing.T, assembly StartAssembly, identityContent string) {
	t.Helper()
	if !strings.Contains(assembly.BaseInstructions, identityContent) {
		t.Fatalf("BaseInstructions missing built-in section content: %q", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "## "+SectionIdentity) {
		t.Fatalf("BaseInstructions unexpectedly injected section heading: %q", assembly.BaseInstructions)
	}
	if !strings.Contains(assembly.BaseInstructions, "CWD: /repo") {
		t.Fatalf("BaseInstructions missing dynamic section text: %q", assembly.BaseInstructions)
	}
	if !strings.Contains(assembly.BaseInstructions, "legacy base") {
		t.Fatalf("BaseInstructions missing legacy base payload: %q", assembly.BaseInstructions)
	}
}

func assertStartAssemblyBoundary(t *testing.T, assembly StartAssembly, identityContent string) {
	t.Helper()
	if assembly.Boundary == nil {
		t.Fatalf("Boundary = nil, want cached/uncached split metadata")
	}
	if !strings.Contains(assembly.Boundary.CachedPrefix, identityContent) {
		t.Fatalf("CachedPrefix = %q, want identity section", assembly.Boundary.CachedPrefix)
	}
	if strings.Contains(assembly.Boundary.CachedPrefix, "CWD: /repo") {
		t.Fatalf("CachedPrefix unexpectedly contains dynamic section: %q", assembly.Boundary.CachedPrefix)
	}
	if !strings.Contains(assembly.Boundary.UncachedTail, "CWD: /repo") {
		t.Fatalf("UncachedTail = %q, want dynamic section", assembly.Boundary.UncachedTail)
	}
	if !strings.Contains(assembly.Boundary.UncachedTail, "legacy base") {
		t.Fatalf("UncachedTail = %q, want legacy base", assembly.Boundary.UncachedTail)
	}
	assertBoundarySnapshot(t, assembly)
}

func assertBoundarySnapshot(t *testing.T, assembly StartAssembly) {
	t.Helper()
	boundaryComposed := joinBlocks(assembly.Boundary.CachedPrefix, assembly.Boundary.UncachedTail)
	if !strings.HasPrefix(assembly.BaseInstructions, boundaryComposed) {
		t.Fatalf("BaseInstructions does not start with boundary blocks: boundary=%#v base=%q", assembly.Boundary, assembly.BaseInstructions)
	}
	if assembly.Snapshot.Boundary == nil {
		t.Fatalf("Snapshot.Boundary = nil, want %#v", assembly.Boundary)
	}
	if *assembly.Snapshot.Boundary != *assembly.Boundary {
		t.Fatalf("Snapshot.Boundary = %#v, want %#v", assembly.Snapshot.Boundary, assembly.Boundary)
	}
}

func assertSimpleStartHardEarlyReturn(t *testing.T, assembly StartAssembly, called bool, want, forbiddenText string) {
	t.Helper()
	if called {
		t.Fatal("simple mode still evaluated registered sections")
	}
	if len(assembly.ResolvedSections) != 0 {
		t.Fatalf("ResolvedSections = %#v, want nil/empty in simple mode", assembly.ResolvedSections)
	}
	if assembly.BaseInstructions != want {
		t.Fatalf("BaseInstructions = %q, want strict three-line form %q", assembly.BaseInstructions, want)
	}
	if strings.Contains(assembly.BaseInstructions, "<system-reminder>") {
		t.Fatalf("CLAUDE_CODE_SIMPLE ultraSimple must not inject system-reminder: %q", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "legacy base") {
		t.Fatalf("BaseInstructions unexpectedly kept legacy base: %q", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, forbiddenText) {
		t.Fatalf("BaseInstructions unexpectedly kept registered content: %q", assembly.BaseInstructions)
	}
}

func assertSimpleStartContextEmpty(t *testing.T, assembly StartAssembly) {
	t.Helper()
	if assembly.UserContext != nil {
		t.Fatalf("ultraSimple UserContext = %#v, want nil", assembly.UserContext)
	}
	if assembly.UserContextText != "" {
		t.Fatalf("ultraSimple UserContextText = %q, want empty", assembly.UserContextText)
	}
	if assembly.SystemContext != nil {
		t.Fatalf("ultraSimple SystemContext = %#v, want nil", assembly.SystemContext)
	}
}

func assertMemoryProviderSkippedOnTurn(t *testing.T, assembly TurnAssembly, calls int) {
	t.Helper()
	if calls != 0 {
		t.Fatalf("memory provider calls after turn = %d, want 0", calls)
	}
	if strings.Contains(assembly.UserContextText, "memory build #") {
		t.Fatalf("turn unexpectedly rendered memory content: %q", assembly.UserContextText)
	}
}

func assertCachedMemoryStart(t *testing.T, firstStart, secondStart StartAssembly, calls int) {
	t.Helper()
	if calls != 1 {
		t.Fatalf("memory provider calls after repeated start = %d, want 1", calls)
	}
	if !strings.Contains(firstStart.BaseInstructions, "memory build #1") {
		t.Fatalf("start missing cached memory content: %q", firstStart.BaseInstructions)
	}
	if firstStart.BaseInstructions != secondStart.BaseInstructions {
		t.Fatalf("cached start mismatch: first=%q second=%q", firstStart.BaseInstructions, secondStart.BaseInstructions)
	}
}

func assertMemoryProviderStillSkippedOnTurn(t *testing.T, assembly TurnAssembly, calls int) {
	t.Helper()
	if calls != 1 {
		t.Fatalf("memory provider calls after second turn = %d, want 1", calls)
	}
	if strings.Contains(assembly.UserContextText, "memory build #") {
		t.Fatalf("turn unexpectedly reused cached memory content: %q", assembly.UserContextText)
	}
}

func assertRebuiltMemoryStart(t *testing.T, assembly StartAssembly, calls int) {
	t.Helper()
	if calls != 2 {
		t.Fatalf("memory provider calls after invalidate = %d, want 2", calls)
	}
	if !strings.Contains(assembly.BaseInstructions, "memory build #2") {
		t.Fatalf("start missing rebuilt memory content: %q", assembly.BaseInstructions)
	}
}

func assertInputScopedLanguageCache(t *testing.T, firstLanguage, secondLanguage, thirdLanguage string) {
	t.Helper()
	if firstLanguage != secondLanguage {
		t.Fatalf("input-scoped cache missed on unrelated input changes: first=%q second=%q", firstLanguage, secondLanguage)
	}
	if thirdLanguage == firstLanguage {
		t.Fatalf("language section did not rebuild after dependency change: first=%q third=%q", firstLanguage, thirdLanguage)
	}
}

func waitForParallelSectionsReady(t *testing.T, ctx context.Context, ready <-chan string, errCh <-chan error) {
	t.Helper()
	for range 2 {
		select {
		case <-ready:
		case err := <-errCh:
			t.Fatalf("AssembleTurn() error before both sections started = %v", err)
		case <-ctx.Done():
			t.Fatal("independent sections did not start in parallel")
		}
	}
}

func assertParallelSectionAssembly(t *testing.T, ctx context.Context, resultCh <-chan TurnAssembly, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		t.Fatalf("AssembleTurn() error = %v", err)
	case assembly := <-resultCh:
		assertParallelSectionOrder(t, assembly)
	case <-ctx.Done():
		t.Fatal("AssembleTurn() timed out")
	}
}

func assertParallelSectionOrder(t *testing.T, assembly TurnAssembly) {
	t.Helper()
	outputStyleIndex := resolvedSectionIndex(assembly.ResolvedSections, DynamicSectionOutputStyle)
	scratchpadIndex := resolvedSectionIndex(assembly.ResolvedSections, DynamicSectionScratchpad)
	if outputStyleIndex == -1 {
		t.Fatalf("resolved sections missing %q: %#v", DynamicSectionOutputStyle, assembly.ResolvedSections)
	}
	if scratchpadIndex == -1 {
		t.Fatalf("resolved sections missing %q: %#v", DynamicSectionScratchpad, assembly.ResolvedSections)
	}
	if outputStyleIndex > scratchpadIndex {
		t.Fatalf("resolved section order changed: output_style=%d scratchpad=%d", outputStyleIndex, scratchpadIndex)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v error = %v\n%s", args, err, string(output))
	}
}

func requireResolvedPromptSectionContent(t *testing.T, sections []ResolvedPromptSection, name string) string {
	t.Helper()
	content, ok := resolvedSectionContent(sections, name)
	if !ok {
		t.Fatalf("ResolvedSections missing %q: %#v", name, sections)
	}
	return content
}

func resolvedSectionContent(sections []ResolvedPromptSection, name string) (string, bool) {
	for _, section := range sections {
		if section.Name == name {
			return section.Content, true
		}
	}
	return "", false
}

func resolvedSectionIndex(sections []ResolvedPromptSection, name string) int {
	for idx, section := range sections {
		if section.Name == name {
			return idx
		}
	}
	return -1
}
