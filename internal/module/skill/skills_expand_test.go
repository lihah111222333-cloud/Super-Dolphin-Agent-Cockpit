package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newExpandTestService 在临时目录下准备一个 project-skills root，返回 service + skills root。
//
// 保留：被 skills_fs_test.go 引用（驱动 Expand 的复用 helper）。
func newExpandTestService(t *testing.T) (*service, string) {
	t.Helper()
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "proj")
	skillsRoot := filepath.Join(tmp, "user-skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	svc := NewService(projectRoot).(*service)
	svc.root = skillsRoot
	svc.approval = nil
	return svc, skillsRoot
}

// expandTestContext 保留：被 skills_fs_test.go 与 cwd_scope_test.go 引用。
func expandTestContext(svc *service) context.Context {
	return skillTestContext(svc.projectRoot)
}

// writeExpandTestSkill 写一个 SKILL.md fixture；保留：被 skills_fs_test.go 引用。
func writeExpandTestSkill(t *testing.T, skillsRoot, name, body string) string {
	t.Helper()
	dir := filepath.Join(skillsRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, skillMainFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return dir
}

// ---- sliceMarkdownSection / normalizeAnchorSlug / truncateBytes / resolveMaxBytes 单元测试 ----
//
// 这些 helper 在 skills_expand.go 中保留（被 skills_fs.go 复用），所以测试也保留。

func TestSliceMarkdownSection_NestedLevels(t *testing.T) {
	body := "## Intro\nhello\n\n## Usage\nrun it\n### Sub\ndetail\n## Done\nend"
	slice, ok := sliceMarkdownSection(body, "Usage")
	if !ok {
		t.Fatalf("expected found")
	}
	if !strings.Contains(slice, "run it") || !strings.Contains(slice, "### Sub") || !strings.Contains(slice, "detail") {
		t.Fatalf("slice missing nested children: %q", slice)
	}
	if strings.Contains(slice, "## Done") {
		t.Fatalf("slice should not include next sibling: %q", slice)
	}
}

func TestSliceMarkdownSection_EmptyAnchorReturnsAll(t *testing.T) {
	slice, ok := sliceMarkdownSection("body content", "")
	if !ok || slice != "body content" {
		t.Fatalf("got (%q,%v), want (body content,true)", slice, ok)
	}
}

func TestSliceMarkdownSection_NotFound(t *testing.T) {
	_, ok := sliceMarkdownSection("## Real\nhi", "Missing")
	if ok {
		t.Fatalf("expected not found")
	}
}

func TestNormalizeAnchorSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Usage", "usage"},
		{"Usage Guide", "usage-guide"},
		{"  Multi  Space  ", "multi-space"},
		{"snake_case", "snake-case"},
		{"Already-Slug", "already-slug"},
		{"v1.2 release", "v12-release"},
	}
	for _, c := range cases {
		if got := normalizeAnchorSlug(c.in); got != c.want {
			t.Errorf("normalizeAnchorSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncateBytes(t *testing.T) {
	s, trunc := truncateBytes("hello", 100)
	if trunc || s != "hello" {
		t.Fatalf("under limit: %q,%v", s, trunc)
	}
	s, trunc = truncateBytes("hello world", 5)
	if !trunc || s != "hello" {
		t.Fatalf("over limit: %q,%v", s, trunc)
	}
	s, trunc = truncateBytes("any", 0)
	if trunc || s != "any" {
		t.Fatalf("zero limit means no truncation: %q,%v", s, trunc)
	}
}

func TestResolveMaxBytes(t *testing.T) {
	if got := resolveMaxBytes(0); got != defaultExpandMaxBytes {
		t.Errorf("resolveMaxBytes(0) = %d, want default %d", got, defaultExpandMaxBytes)
	}
	if got := resolveMaxBytes(-5); got != defaultExpandMaxBytes {
		t.Errorf("resolveMaxBytes(neg) = %d, want default %d", got, defaultExpandMaxBytes)
	}
	if got := resolveMaxBytes(123); got != 123 {
		t.Errorf("resolveMaxBytes(123) = %d, want 123", got)
	}
	if got := resolveMaxBytes(int64(maxSkillFileBytes) + 1); got != int64(maxSkillFileBytes) {
		t.Errorf("resolveMaxBytes(over hard cap) = %d, want %d", got, maxSkillFileBytes)
	}
}
