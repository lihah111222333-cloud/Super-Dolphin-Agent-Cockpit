package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
)

func TestAgentMemoryManagerGetAgentMemoryDirScopeIsolation(t *testing.T) {
	manager, userRoot, projectRoot := newTestAgentMemoryManager(t)
	cases := []struct {
		scope MemoryScope
		want  string
	}{
		{scope: MemoryScopeUser, want: filepath.Join(userRoot, "agent-memory", "Writer")},
		{scope: MemoryScopeProject, want: filepath.Join(projectRoot, ".claude", "agent-memory", "Writer")},
		{scope: MemoryScopeLocal, want: filepath.Join(projectRoot, ".claude", "agent-memory-local", "Writer")},
	}
	for _, tc := range cases {
		got, err := manager.GetAgentMemoryDir("Writer", tc.scope)
		if err != nil {
			t.Fatalf("GetAgentMemoryDir(%q, %q) error = %v", "Writer", tc.scope, err)
		}
		if got != tc.want {
			t.Fatalf("GetAgentMemoryDir(%q, %q) = %q, want %q", "Writer", tc.scope, got, tc.want)
		}
	}
}

func TestAgentMemoryManagerLocalScopeUsesRemoteMemoryMount(t *testing.T) {
	manager, _, projectRoot := newTestAgentMemoryManager(t)
	remoteRoot := filepath.Join(t.TempDir(), "remote-memory")
	t.Setenv(envClaudeRemoteMemoryDir, remoteRoot)

	got, err := manager.GetAgentMemoryDir("Writer", MemoryScopeLocal)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir(%q, %q) error = %v", "Writer", MemoryScopeLocal, err)
	}
	want := filepath.Join(remoteRoot, "projects", testPathHelper{}.SanitizePath(projectRoot), "agent-memory-local", "Writer")
	if got != want {
		t.Fatalf("GetAgentMemoryDir(%q, %q) = %q, want %q", "Writer", MemoryScopeLocal, got, want)
	}
}

func TestAgentMemoryManagerGetAgentMemoryDirFallsBackOnConflict(t *testing.T) {
	manager, _, projectRoot := newTestAgentMemoryManager(t)
	parent := filepath.Join(projectRoot, ".claude", "agent-memory")
	if err := os.MkdirAll(filepath.Join(parent, "Writer"), 0o755); err != nil {
		t.Fatalf("MkdirAll(conflict dir) error = %v", err)
	}
	got, err := manager.GetAgentMemoryDir("writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir() error = %v", err)
	}
	base := filepath.Base(got)
	if base == "writer" {
		t.Fatalf("GetAgentMemoryDir() = %q, want hashed fallback under conflict", got)
	}
	if !strings.HasPrefix(base, "writer-") {
		t.Fatalf("GetAgentMemoryDir() base = %q, want readable prefix fallback", base)
	}
}

func TestEnsureAgentMemoryDirCreatesAllScopes(t *testing.T) {
	manager, _, _ := newTestAgentMemoryManager(t)
	for _, scope := range []MemoryScope{MemoryScopeUser, MemoryScopeProject, MemoryScopeLocal} {
		t.Run(string(scope), func(t *testing.T) {
			dir, err := manager.GetAgentMemoryDir("Writer", scope)
			if err != nil {
				t.Fatalf("GetAgentMemoryDir(%q, %q) error = %v", "Writer", scope, err)
			}
			if err := manager.EnsureAgentMemoryDir("Writer", scope); err != nil {
				t.Fatalf("EnsureAgentMemoryDir(%q, %q) error = %v", "Writer", scope, err)
			}
			assertEmptyAgentMemoryEntrypoint(t, dir)
		})
	}
}

func TestLoadAgentMemoryPromptAutoCreatesDir(t *testing.T) {
	manager, _, _ := newTestAgentMemoryManager(t)
	dir, err := manager.GetAgentMemoryDir("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir() error = %v", err)
	}
	prompt, err := manager.LoadAgentMemoryPrompt("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("LoadAgentMemoryPrompt() error = %v", err)
	}
	assertEmptyAgentMemoryEntrypoint(t, dir)
	if !strings.Contains(prompt, emptyAgentMemoryPrompt) {
		t.Fatalf("LoadAgentMemoryPrompt() missing empty placeholder: %q", prompt)
	}
}

func TestAgentMemoryIsolation(t *testing.T) {
	manager, _, _ := newTestAgentMemoryManager(t)
	writerBody := "Remember writer-specific review preferences."
	reviewerBody := "Remember reviewer-specific risk checklist."
	writerDir := writeAgentMemoryEntrypoint(t, manager, "Writer", MemoryScopeProject, writerBody)
	reviewerDir := writeAgentMemoryEntrypoint(t, manager, "Reviewer", MemoryScopeProject, reviewerBody)
	if writerDir == reviewerDir {
		t.Fatalf("agent memory dirs must differ: %q", writerDir)
	}
	writerPrompt, err := manager.LoadAgentMemoryPrompt("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("LoadAgentMemoryPrompt(writer) error = %v", err)
	}
	reviewerPrompt, err := manager.LoadAgentMemoryPrompt("Reviewer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("LoadAgentMemoryPrompt(reviewer) error = %v", err)
	}
	assertPromptContainsOnly(t, writerPrompt, writerBody, reviewerBody)
	assertPromptContainsOnly(t, reviewerPrompt, reviewerBody, writerBody)
}

func TestAgentMemoryScopePermissions(t *testing.T) {
	manager, _, _ := newTestAgentMemoryManager(t)
	contents := map[MemoryScope]string{
		MemoryScopeUser:    "Remember cross-project writer context.",
		MemoryScopeProject: "Remember repo-specific writer context.",
		MemoryScopeLocal:   "Remember machine-local writer context.",
	}
	dirs := make(map[MemoryScope]string, len(contents))
	for scope, body := range contents {
		dir := writeAgentMemoryEntrypoint(t, manager, "Writer", scope, body)
		dirs[scope] = dir
		if _, err := validateMemoryWritePathForTest(dir, testPathHelper{}.MemoryIndexPath(dir)); err != nil {
			t.Fatalf("validateMemoryWritePathForTest(%q, MEMORY.md) error = %v", scope, err)
		}
		escaped := filepath.Join(dir, "..", "escape.md")
		if _, err := validateMemoryWritePathForTest(dir, escaped); err == nil {
			t.Fatalf("validateMemoryWritePathForTest(%q, %q) = nil, want error", scope, escaped)
		}
	}
	for scope, body := range contents {
		if dirs[scope] == "" {
			t.Fatalf("missing dir for scope %q", scope)
		}
		prompt, err := manager.LoadAgentMemoryPrompt("Writer", scope)
		if err != nil {
			t.Fatalf("LoadAgentMemoryPrompt(%q) error = %v", scope, err)
		}
		if !strings.Contains(prompt, body) {
			t.Fatalf("LoadAgentMemoryPrompt(%q) missing own scope content: %q", scope, prompt)
		}
		for otherScope, otherBody := range contents {
			if otherScope != scope && strings.Contains(prompt, otherBody) {
				t.Fatalf("LoadAgentMemoryPrompt(%q) leaked %q scope content: %q", scope, otherScope, prompt)
			}
		}
	}
}

func TestAgentMemoryHelpersExposeEntrypointAndClassification(t *testing.T) {
	manager, _, projectRoot := newTestAgentMemoryManager(t)
	entrypoint, err := manager.GetAgentMemoryEntrypoint("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("GetAgentMemoryEntrypoint() error = %v", err)
	}
	wantEntrypoint := filepath.Join(projectRoot, ".claude", "agent-memory", "Writer", "MEMORY.md")
	if entrypoint != wantEntrypoint {
		t.Fatalf("GetAgentMemoryEntrypoint() = %q, want %q", entrypoint, wantEntrypoint)
	}
	if got := ScopeDisplay(MemoryScopeLocal); got != "local-scoped agent memory" {
		t.Fatalf("ScopeDisplay(local) = %q", got)
	}
	agentPath := writeAgentMemoryEntrypoint(t, manager, "Writer", MemoryScopeProject, "remember writer context")
	if !manager.IsAgentMemoryPath(filepath.Join(agentPath, "MEMORY.md")) {
		t.Fatal("IsAgentMemoryPath(agent MEMORY.md) = false, want true")
	}
}

func TestAgentMemoryManagerLoadAgentMemoryPromptIncludesEntrypoint(t *testing.T) {
	manager, _, projectRoot := newTestAgentMemoryManager(t)
	dir := filepath.Join(projectRoot, ".claude", "agent-memory", "Writer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	const body = "Remember the preferred review style.\nWhy: smaller diffs are faster to review."
	if err := os.WriteFile(testPathHelper{}.MemoryIndexPath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
	prompt, err := manager.LoadAgentMemoryPrompt("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("LoadAgentMemoryPrompt() error = %v", err)
	}
	for _, snippet := range []string{"### 1. memory system", "## MEMORY.md", body, "project-scoped agent memory"} {
		if !strings.Contains(prompt, snippet) {
			t.Fatalf("LoadAgentMemoryPrompt() missing %q in prompt:\n%s", snippet, prompt)
		}
	}
}

func TestAgentMemoryManagerLoadAgentMemoryPromptStripsUTF8BOM(t *testing.T) {
	manager, _, projectRoot := newTestAgentMemoryManager(t)
	dir := filepath.Join(projectRoot, ".claude", "agent-memory", "Writer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	body := "\uFEFFRemember the preferred review style."
	if err := os.WriteFile(testPathHelper{}.MemoryIndexPath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
	prompt, err := manager.LoadAgentMemoryPrompt("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("LoadAgentMemoryPrompt() error = %v", err)
	}
	if strings.Contains(prompt, "\uFEFF") {
		t.Fatalf("LoadAgentMemoryPrompt() prompt still contains BOM: %q", prompt)
	}
	if !strings.Contains(prompt, "Remember the preferred review style.") {
		t.Fatalf("LoadAgentMemoryPrompt() missing BOM-stripped entrypoint in prompt: %q", prompt)
	}
}

func TestResolveChildAgentStartRequiresExplicitTypeAndScope(t *testing.T) {
	meta, ok := ResolveChildAgentStart(contract.SectionContext{
		Start: &contract.StartInput{
			ParentAgentID:    "agent-root",
			AgentType:        "worker",
			AgentMemoryScope: "project",
			Name:             "Worker UI",
		},
	})
	if !ok {
		t.Fatal("ResolveChildAgentStart() = false, want true")
	}
	if meta.AgentType != "worker" || meta.Scope != MemoryScopeProject {
		t.Fatalf("ResolveChildAgentStart() = %+v", meta)
	}
	if _, ok := ResolveChildAgentStart(contract.SectionContext{
		Start: &contract.StartInput{
			ParentAgentID: "agent-root",
			Name:          "Worker UI",
		},
	}); ok {
		t.Fatal("ResolveChildAgentStart() used display name fallback, want false")
	}
}

func TestTruncateAgentMemoryContentWarnsForLineLimit(t *testing.T) {
	lines := make([]string, agentMemoryMaxLines+5)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %03d", i)
	}

	result := truncateAgentMemoryContent(strings.Join(lines, "\n"))
	if !result.wasLineTruncated || result.wasByteTruncated {
		t.Fatalf("truncateAgentMemoryContent() flags = %+v, want line-only truncation", result)
	}
	if !strings.Contains(result.content, "> WARNING: MEMORY.md is "+truncateAgentMemoryReason(result)+".") {
		t.Fatalf("content = %q, want line truncation warning", result.content)
	}
	if !strings.Contains(result.content, fmt.Sprintf("line %03d", agentMemoryMaxLines-1)) {
		t.Fatalf("content missing final retained line: %q", result.content)
	}
	if strings.Contains(result.content, fmt.Sprintf("line %03d", agentMemoryMaxLines)) {
		t.Fatalf("content still contains first discarded line: %q", result.content)
	}
}

func TestTruncateAgentMemoryContentTracksOriginalCodeUnitCount(t *testing.T) {
	line := strings.Repeat("x", 124)
	lines := make([]string, agentMemoryMaxLines+1)
	for i := range lines {
		lines[i] = line
	}
	trimmedByLines := strings.Join(lines[:agentMemoryMaxLines], "\n")
	if parse.JSStringLength(trimmedByLines) > agentMemoryMaxCodeUnits {
		t.Fatalf("test setup invalid: %d > %d", parse.JSStringLength(trimmedByLines), agentMemoryMaxCodeUnits)
	}

	result := truncateAgentMemoryContent(strings.Join(lines, "\n"))
	if !result.wasLineTruncated {
		t.Fatalf("truncateAgentMemoryContent() flags = %+v, want line truncation", result)
	}
	if result.codeUnitCount <= agentMemoryMaxCodeUnits {
		t.Fatalf("truncateAgentMemoryContent() codeUnitCount = %d, want > %d", result.codeUnitCount, agentMemoryMaxCodeUnits)
	}
	wantReason := fmt.Sprintf("%d lines and %s", len(lines), formatEntrypointSize(result.codeUnitCount))
	if !strings.Contains(result.content, wantReason) {
		t.Fatalf("content = %q, want reason %q", result.content, wantReason)
	}
}

func TestTruncateAgentMemoryContentUsesJSCodeUnitSemantics(t *testing.T) {
	content := strings.Repeat("界", agentMemoryMaxCodeUnits)
	result := truncateAgentMemoryContent(content)
	if result.wasByteTruncated {
		t.Fatalf("truncateAgentMemoryContent() unexpectedly byte-truncated CJK content: %+v", result)
	}
	if result.content != content {
		t.Fatalf("truncateAgentMemoryContent() content changed for in-limit CJK text")
	}
}

func TestTruncateAgentMemoryContentTruncatesAtPriorNewlineForCodeUnitLimit(t *testing.T) {
	content := strings.Repeat("a", 10_000) + "\n" + strings.Repeat("b", 16_000)
	result := truncateAgentMemoryContent(content)
	if result.wasLineTruncated || !result.wasByteTruncated {
		t.Fatalf("truncateAgentMemoryContent() flags = %+v, want byte-only truncation", result)
	}
	if !strings.HasPrefix(result.content, strings.Repeat("a", 10_000)) {
		t.Fatalf("content = %q, want retained prefix before newline", result.content)
	}
	if strings.Contains(result.content, strings.Repeat("b", 100)) {
		t.Fatalf("content = %q, want truncation at prior newline before long tail", result.content)
	}
	if !strings.Contains(result.content, "index entries are too long") {
		t.Fatalf("content = %q, want byte truncation guidance", result.content)
	}
}

func newTestAgentMemoryManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	userRoot := filepath.Join(t.TempDir(), "user-memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project-root")
	manager := NewManager(&Config{RootDir: userRoot, ProjectRoot: projectRoot}, testPathHelper{}, testPromptBuilder{})
	return manager, userRoot, projectRoot
}

func assertEmptyAgentMemoryEntrypoint(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("path %q is not a directory", dir)
	}
	entrypoint := testPathHelper{}.MemoryIndexPath(dir)
	info, err = os.Stat(entrypoint)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", entrypoint, err)
	}
	if info.IsDir() {
		t.Fatalf("path %q is a directory, want file", entrypoint)
	}
	content, err := os.ReadFile(entrypoint)
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) error = %v", err)
	}
	if strings.TrimSpace(parse.StripUTF8BOM(string(content))) != "" {
		t.Fatalf("MEMORY.md = %q, want empty entrypoint", string(content))
	}
}

func writeAgentMemoryEntrypoint(t *testing.T, manager *Manager, agentType string, scope MemoryScope, body string) string {
	t.Helper()
	dir, err := manager.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir(%q, %q) error = %v", agentType, scope, err)
	}
	if err := manager.EnsureAgentMemoryDir(agentType, scope); err != nil {
		t.Fatalf("EnsureAgentMemoryDir(%q, %q) error = %v", agentType, scope, err)
	}
	if err := os.WriteFile(testPathHelper{}.MemoryIndexPath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
	return dir
}

func assertPromptContainsOnly(t *testing.T, prompt, want, notWant string) {
	t.Helper()
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt = %q, want substring %q", prompt, want)
	}
	if notWant != "" && strings.Contains(prompt, notWant) {
		t.Fatalf("prompt = %q, want substring %q to stay isolated", prompt, notWant)
	}
}

type testPromptBuilder struct{}

func (testPromptBuilder) BuildPrompt(_ MemoryScope, extraGuidelines []string) string {
	parts := []string{"### 1. memory system"}
	if len(extraGuidelines) > 0 {
		parts = append(parts, strings.Join(extraGuidelines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

type testPathHelper struct{}

func (testPathHelper) ValidateRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	cleaned, err := cleanAbsolutePathFallback(raw)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(cleaned, string(os.PathSeparator)) + string(os.PathSeparator), nil
}

func (testPathHelper) CleanAbsolute(raw string) (string, error) {
	return cleanAbsolutePathFallback(raw)
}

func (testPathHelper) CanonicalGitRoot(_ context.Context, projectRoot string) (string, error) {
	return cleanAbsolutePathFallback(projectRoot)
}

func (testPathHelper) SanitizePath(raw string) string {
	return defaultPathHelper{}.SanitizePath(raw)
}

func (testPathHelper) MemoryIndexPath(root string) string {
	return filepath.Join(root, "MEMORY.md")
}

func validateMemoryWritePathForTest(root, file string) (string, error) {
	cleanRoot, err := cleanAbsolutePathFallback(root)
	if err != nil {
		return "", err
	}
	candidate := file
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cleanRoot, candidate)
	}
	candidate, err = cleanAbsolutePathFallback(candidate)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(candidate, cleanRoot+string(os.PathSeparator)) && candidate != cleanRoot {
		return "", errors.New("path escapes root")
	}
	return candidate, nil
}

type fakeGate struct {
	auto     bool
	suppress bool
}

func (g fakeGate) AutoEnabled(contract.BuildCtx) bool       { return g.auto }
func (g fakeGate) SuppressForOverlay(contract.BuildCtx) bool { return g.suppress }

// TestPromptProviderResolveSuppressedByOverlay closes the regression that R3
// flagged: under claude_code overlay the root MemoryEntrypointProvider drops
// MEMORY.md, but PromptProvider used to keep injecting agent-scope MEMORY.md
// into spawned children, producing the parent-suppressed / child-injected
// split. The Resolve path now consults SuppressForOverlay before reaching
// child-agent metadata extraction.
func TestPromptProviderResolveSuppressedByOverlay(t *testing.T) {
	p := NewPromptProvider(&Config{}, &Manager{}, fakeGate{auto: true, suppress: true}, nil)
	got, err := p.Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{
			ParentAgentID:    "agent-root",
			AgentType:        "worker",
			AgentMemoryScope: "project",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != nil {
		t.Fatalf("Resolve() returned content under overlay suppression: %q", *got)
	}
}

