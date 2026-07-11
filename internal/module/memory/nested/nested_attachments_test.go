package nested

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestNestedResolveTurnAttachmentsLifecycle(t *testing.T) {
	repoRoot := t.TempDir()
	cwd := filepath.Join(repoRoot, "service")
	targetDir := filepath.Join(cwd, "nested")
	for _, dir := range []string{cwd, targetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, filepath.Join(cwd, "CLAUDE.md"), "cwd base")
	writeClaudeFile(t, filepath.Join(targetDir, "CLAUDE.md"), "nested base")
	rulePath := filepath.Join(cwd, ".claude", "rules", "go.md")
	writeClaudeFile(t, rulePath, "---\npaths:\n  - nested/**/*.go\n---\nuse Go style")
	target := filepath.Join(targetDir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", target, err)
	}
	deps := newTestDependencies(testDepsOptions{nestedEnabled: true})
	provider := NewClaudeMdSourcesProvider(deps, nil, NewNestedRuntime(deps))
	buildCtx := contract.BuildCtx{GitRoot: repoRoot, CWD: cwd}
	turn := contract.TurnInput{ThreadID: "thread-1", Attachments: []string{target}}
	baseSources := mustProviderResolveClaudeMdSources(t, provider, buildCtx)

	first := mustResolveNestedTurnAttachments(t, provider, buildCtx, turn, baseSources)
	if len(first) != 1 {
		t.Fatalf("ResolveTurnAttachments(first) len = %d, want 1", len(first))
	}
	if got := first[0].Kind; got != dto.AttachmentKindNestedMemory {
		t.Fatalf("attachment kind = %q, want %q", got, dto.AttachmentKindNestedMemory)
	}
	if got, want := first[0].Path, filepath.ToSlash(mustResolvedClaudePath(t, rulePath)); got != want {
		t.Fatalf("attachment path = %q, want %q", got, want)
	}
	if got := first[0].Content; got != "use Go style" {
		t.Fatalf("attachment content = %q, want %q", got, "use Go style")
	}

	second := mustResolveNestedTurnAttachments(t, provider, buildCtx, turn, baseSources)
	if len(second) != 0 {
		t.Fatalf("ResolveTurnAttachments(second) = %#v, want no duplicate attachments", second)
	}

	provider.OnPromptInvalidate(contract.InvalidateCompact)
	third := mustResolveNestedTurnAttachments(t, provider, buildCtx, turn, baseSources)
	if len(third) != 1 {
		t.Fatalf("ResolveTurnAttachments(after compact) len = %d, want 1", len(third))
	}
}

func TestNestedResolveTurnAttachmentsHardDeniesManagedPaths(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(base, "repo")
	autoRoot := filepath.Join(base, "automem")
	deps := newTestDependencies(testDepsOptions{nestedEnabled: true, autoMemRoot: autoRoot})
	provider := NewClaudeMdSourcesProvider(deps, nil, NewNestedRuntime(deps))
	turn := contract.TurnInput{ThreadID: "thread-1", Attachments: []string{filepath.Join(autoRoot, "project.md")}}
	buildCtx := contract.BuildCtx{GitRoot: projectRoot, CWD: projectRoot}
	got := mustResolveNestedTurnAttachments(t, provider, buildCtx, turn, nil)
	if len(got) != 0 {
		t.Fatalf("ResolveTurnAttachments(managed) = %#v, want nil", got)
	}
	snapshot := provider.nested.snapshot("thread-1")
	if len(snapshot.PendingTriggers) != 0 || len(snapshot.LoadedPaths) != 0 {
		t.Fatalf("snapshot after managed trigger = %#v, want empty state", snapshot)
	}
}

func TestNestedResolveTurnAttachmentsUsesSharedAttachmentLaneForMentionAndIDEPaths(t *testing.T) {
	repoRoot := t.TempDir()
	cwd := filepath.Join(repoRoot, "service")
	targetDir := filepath.Join(cwd, "nested")
	for _, dir := range []string{cwd, targetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, filepath.Join(cwd, "CLAUDE.md"), "cwd base")
	rulePath := filepath.Join(cwd, ".claude", "rules", "go.md")
	writeClaudeFile(t, rulePath, "---\npaths:\n  - nested/**/*.go\n---\nuse Go style")
	deps := newTestDependencies(testDepsOptions{nestedEnabled: true})
	provider := NewClaudeMdSourcesProvider(deps, nil, NewNestedRuntime(deps))
	buildCtx := contract.BuildCtx{GitRoot: repoRoot, CWD: cwd}
	baseSources := mustProviderResolveClaudeMdSources(t, provider, buildCtx)
	for _, name := range []string{"mentioned_file", "ide_opened_file"} {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(targetDir, name+".go")
			if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
				t.Fatalf("WriteFile(%q) error = %v", target, err)
			}
			turn := contract.TurnInput{ThreadID: name, Attachments: []string{target}}
			attachments := mustResolveNestedTurnAttachments(t, provider, buildCtx, turn, baseSources)
			want := filepath.ToSlash(mustResolvedClaudePath(t, rulePath))
			if len(attachments) != 1 || attachments[0].Path != want {
				t.Fatalf("ResolveTurnAttachments(%s) = %#v, want rule %q", name, attachments, want)
			}
		})
	}
}

func TestNestedMemoryAttachmentReturnsStatErrors(t *testing.T) {
	_, ok, err := nestedMemoryAttachment(ClaudeMdSource{
		Path:    filepath.Join(t.TempDir(), "bad\x00path"),
		Content: "use Go style",
	})
	if err == nil {
		t.Fatal("nestedMemoryAttachment() error = nil, want stat error")
	}
	if ok {
		t.Fatal("nestedMemoryAttachment() ok = true, want false on stat error")
	}
	if !strings.Contains(err.Error(), "nested memory stat") {
		t.Fatalf("nestedMemoryAttachment() error = %v, want stat context", err)
	}
}

func mustProviderResolveClaudeMdSources(t *testing.T, provider *ClaudeMdSourcesProvider, buildCtx contract.BuildCtx) []ClaudeMdSource {
	t.Helper()
	sources, err := provider.ResolveClaudeMdSources(context.Background(), buildCtx)
	if err != nil {
		t.Fatalf("ResolveClaudeMdSources() error = %v", err)
	}
	return sources
}

func mustResolveNestedTurnAttachments(
	t *testing.T,
	provider *ClaudeMdSourcesProvider,
	buildCtx contract.BuildCtx,
	turn contract.TurnInput,
	baseSources []ClaudeMdSource,
) []dto.AttachmentEnvelope {
	t.Helper()
	attachments, err := provider.ResolveTurnAttachments(context.Background(), buildCtx, turn, baseSources)
	if err != nil {
		t.Fatalf("ResolveTurnAttachments() error = %v", err)
	}
	return attachments
}
