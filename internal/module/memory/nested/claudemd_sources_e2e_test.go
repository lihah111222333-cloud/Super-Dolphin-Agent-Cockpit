//go:build e2e
// +build e2e

package nested

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

func TestCombinedClaudeMdSourcesInjectTeamEntrypointThroughBuildBaseUserContext(t *testing.T) {
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	autoRoot := filepath.Join(base, "automem")
	teamRoot := filepath.Join(base, "teammem")
	for _, dir := range []string{repoRoot, autoRoot, teamRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, memoryIndexPath(autoRoot), "- [Private](private.md) — private memory")
	writeClaudeFile(t, memoryIndexPath(teamRoot), "<!-- hidden -->\n- [Team](team.md) — shared memory")

	deps := newTestDependencies(testDepsOptions{nestedEnabled: true, autoMemRoot: autoRoot, teamRoot: teamRoot})
	provider := NewClaudeMdSourcesProvider(deps, stubTeamMemoryManager{path: teamRoot}, nil)
	sources := provider.ResolveClaudeMdSources(context.Background(), contract.BuildCtx{
		GitRoot: repoRoot,
		CWD:     repoRoot,
	})
	teamSource := sourceByType(t, sources, sourceTypeTeamMem)
	if teamSource.Path != mustResolvedClaudePath(t, memoryIndexPath(teamRoot)) {
		t.Fatalf("team source path = %q, want %q", teamSource.Path, mustResolvedClaudePath(t, memoryIndexPath(teamRoot)))
	}
	if strings.Contains(teamSource.Content, "<!--") {
		t.Fatalf("team source content still contains stripped comments: %q", teamSource.Content)
	}
	basePayload := prompt.BuildBaseUserContext(sources)
	claudeMd := basePayload["claudeMd"]
	for _, snippet := range []string{
		"Contents of " + teamSource.Path,
		"<team-memory-content source=\"shared\">",
		"- [Team](team.md) — shared memory",
	} {
		if !strings.Contains(claudeMd, snippet) {
			t.Fatalf("BuildBaseUserContext() missing %q:\n%s", snippet, claudeMd)
		}
	}
}
