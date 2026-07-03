package skill

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func skillTestContext(cwd string) context.Context {
	return WithCWD(context.Background(), cwd)
}

func TestMatchPreviewFallsBackToAgentID(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	cwd := filepath.Join(t.TempDir(), "repo")
	writeScopedSystemSkill(t, svc.root, cwd, "demo", "---\ntrigger_words: hello\n---\n# demo\n")

	out, err := svc.MatchPreview(skillTestContext(cwd), " agent-42 ", "   ", "hello world", nil)
	if err != nil {
		t.Fatalf("MatchPreview returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("MatchPreview result type mismatch: %T", out)
	}
	if got, _ := result["thread_id"].(string); got != "agent-42" {
		t.Fatalf("thread_id mismatch: got %q", got)
	}
	matches, ok := result["matches"].([]matchItem)
	if !ok {
		t.Fatalf("matches type mismatch: %T", result["matches"])
	}
	if len(matches) != 1 {
		t.Fatalf("matches len mismatch: got %d", len(matches))
	}
	if matches[0].Name != "demo" {
		t.Fatalf("match name mismatch: got %q", matches[0].Name)
	}
}

func TestMatchPreviewAtTriggerWordMatchesAsAuxiliarySignal(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	cwd := filepath.Join(t.TempDir(), "repo")
	writeScopedSystemSkill(t, svc.root, cwd, "deploy", "---\nname: deploy\ntrigger_words: [\"@release\"]\n---\n# deploy\n")

	out, err := svc.MatchPreview(skillTestContext(cwd), "agent", "thread", "please @release now", nil)
	if err != nil {
		t.Fatalf("MatchPreview returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("MatchPreview result type mismatch: %T", out)
	}
	matches, ok := result["matches"].([]matchItem)
	if !ok {
		t.Fatalf("matches type mismatch: %T", result["matches"])
	}
	if len(matches) != 1 {
		t.Fatalf("matches len mismatch: got %d", len(matches))
	}
	if matches[0].Name != "deploy" || matches[0].MatchedBy != "trigger" {
		t.Fatalf("@ trigger match mismatch: %+v", matches[0])
	}
	if len(matches[0].MatchedTerms) != 1 || matches[0].MatchedTerms[0] != "@release" {
		t.Fatalf("@ trigger terms mismatch: %+v", matches[0].MatchedTerms)
	}
}

func TestMatchPreviewUsesResolvedIDForConfiguredSkills(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	cwd := filepath.Join(t.TempDir(), "repo")
	var capturedID string
	svc.readConfigState = func(_ context.Context, resolvedID string) (any, error) {
		capturedID = resolvedID
		return map[string]any{"agent_id": resolvedID, "skills": []any{"configured-skill", "configured-skill", " "}}, nil
	}

	out, err := svc.MatchPreview(skillTestContext(cwd), " agent-7 ", "   ", "", nil)
	if err != nil {
		t.Fatalf("MatchPreview returned error: %v", err)
	}
	if capturedID != "agent-7" {
		t.Fatalf("configured matcher ID mismatch: got %q", capturedID)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("MatchPreview result type mismatch: %T", out)
	}
	matches, ok := result["matches"].([]matchItem)
	if !ok {
		t.Fatalf("matches type mismatch: %T", result["matches"])
	}
	if len(matches) != 1 {
		t.Fatalf("matches len mismatch: got %d", len(matches))
	}
	if matches[0].Name != "configured-skill" || matches[0].MatchedBy != "configured" {
		t.Fatalf("configured match mismatch: %+v", matches[0])
	}
}

func TestMatchPreviewExplicitDisplayNameAliasReturnsCanonicalName(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	cwd := filepath.Join(t.TempDir(), "repo")
	writeSkillContent(t, filepath.Join(cwd, ".agents", "skills", "Docker 容器化部署"), "Docker 容器化部署", "# docker\n")

	out, err := svc.MatchPreview(skillTestContext(cwd), "agent", "thread", "please @Docker 容器化部署 now", nil)
	if err != nil {
		t.Fatalf("MatchPreview returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("MatchPreview result type mismatch: %T", out)
	}
	matches, ok := result["matches"].([]matchItem)
	if !ok {
		t.Fatalf("matches type mismatch: %T", result["matches"])
	}
	if len(matches) != 1 {
		t.Fatalf("matches len mismatch: got %d: %+v", len(matches), matches)
	}
	if matches[0].Name != "docker-容器化部署" || matches[0].MatchedBy != "force" {
		t.Fatalf("display alias match mismatch: %+v", matches[0])
	}
	if len(matches[0].MatchedTerms) != 1 || matches[0].MatchedTerms[0] != "@Docker 容器化部署" {
		t.Fatalf("display alias terms mismatch: %+v", matches[0].MatchedTerms)
	}
}

func TestMatchPreviewConfiguredDisplayNameAliasReturnsCanonicalName(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	cwd := filepath.Join(t.TempDir(), "repo")
	writeSkillContent(t, filepath.Join(cwd, ".agents", "skills", "Docker 容器化部署"), "Docker 容器化部署", "# docker\n")
	svc.readConfigState = func(_ context.Context, resolvedID string) (any, error) {
		return map[string]any{"agent_id": resolvedID, "skills": []any{"Docker 容器化部署"}}, nil
	}

	out, err := svc.MatchPreview(skillTestContext(cwd), "agent", "thread", "", nil)
	if err != nil {
		t.Fatalf("MatchPreview returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("MatchPreview result type mismatch: %T", out)
	}
	matches, ok := result["matches"].([]matchItem)
	if !ok {
		t.Fatalf("matches type mismatch: %T", result["matches"])
	}
	if len(matches) != 1 {
		t.Fatalf("matches len mismatch: got %d: %+v", len(matches), matches)
	}
	if matches[0].Name != "docker-容器化部署" || matches[0].MatchedBy != "configured" {
		t.Fatalf("configured display alias match mismatch: %+v", matches[0])
	}
}

func newTestSkillService(t *testing.T) *service {
	t.Helper()
	return &service{root: t.TempDir(), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}
}

func newTestSuperDolphinHome(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".super-dolphin")
}

func setSkillTestUserHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writeTestSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	path := filepath.Join(dir, skillMainFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	return path
}
