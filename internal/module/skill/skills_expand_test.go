package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newExpandTestService 在临时目录下准备一个 project-skills root，返回 service + skills root。
func newExpandTestService(t *testing.T) (*service, string) {
	t.Helper()
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "proj")
	skillsRoot := filepath.Join(projectRoot, ".agent", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	svc := NewService(projectRoot).(*service)
	// 不持久化 approval cache 到用户 home
	svc.approval = nil
	return svc, skillsRoot
}

// writeExpandTestSkill 与 skills_match_test.go 的 writeTestSkill 类似，但取名区分
// 避免同包测试符号冲突。
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

// ---- ExpandBody ----

func TestExpandBody_FullContent(t *testing.T) {
	svc, root := newExpandTestService(t)
	writeExpandTestSkill(t, root, "foo", "---\nname: foo\ndescription: hi\n---\n# Intro\nhello world")
	res, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "foo"})
	if err != nil {
		t.Fatalf("ExpandBody: %v", err)
	}
	if res.Name != "foo" {
		t.Fatalf("Name=%q", res.Name)
	}
	if !strings.Contains(res.Content, "hello world") {
		t.Fatalf("Content should contain body: %q", res.Content)
	}
	if strings.Contains(res.Content, "description: hi") {
		t.Fatalf("Content should NOT contain frontmatter: %q", res.Content)
	}
	if res.Version == "" || len(res.Version) > 12 {
		t.Fatalf("Version should be 12 hex chars or less: %q", res.Version)
	}
	if res.Truncated {
		t.Fatalf("short body should not be truncated")
	}
}

func TestExpandBody_AnchorSlice(t *testing.T) {
	svc, root := newExpandTestService(t)
	body := "---\nname: foo\n---\n\n## Intro\nhello\n\n## Usage\nrun it\ndetails\n\n## Done\nend"
	writeExpandTestSkill(t, root, "foo", body)
	res, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "foo", Anchor: "Usage"})
	if err != nil {
		t.Fatalf("ExpandBody: %v", err)
	}
	if !strings.Contains(res.Content, "## Usage") {
		t.Fatalf("should include anchor heading: %q", res.Content)
	}
	if !strings.Contains(res.Content, "run it") {
		t.Fatalf("should include anchor body: %q", res.Content)
	}
	if strings.Contains(res.Content, "## Done") {
		t.Fatalf("should stop before next same-level heading: %q", res.Content)
	}
	if strings.Contains(res.Content, "## Intro") {
		t.Fatalf("should not include prior section: %q", res.Content)
	}
	if res.Anchor != "Usage" {
		t.Fatalf("Anchor field = %q", res.Anchor)
	}
}

func TestExpandBody_AnchorCaseInsensitiveAndSlug(t *testing.T) {
	svc, root := newExpandTestService(t)
	body := "---\nname: foo\n---\n\n## Usage Guide\ncontent"
	writeExpandTestSkill(t, root, "foo", body)
	// 大小写不敏感
	if _, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "foo", Anchor: "USAGE GUIDE"}); err != nil {
		t.Fatalf("case-insensitive match should work: %v", err)
	}
	// slug 匹配
	res, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "foo", Anchor: "usage-guide"})
	if err != nil {
		t.Fatalf("slug match: %v", err)
	}
	if !strings.Contains(res.Content, "## Usage Guide") {
		t.Fatalf("slug should match title: %q", res.Content)
	}
}

func TestExpandBody_AnchorNotFound(t *testing.T) {
	svc, root := newExpandTestService(t)
	writeExpandTestSkill(t, root, "foo", "---\nname: foo\n---\n## Only\nx")
	_, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "foo", Anchor: "Missing"})
	if err == nil {
		t.Fatalf("missing anchor should error")
	}
	if !strings.Contains(err.Error(), "anchor not found") {
		t.Fatalf("error text: %v", err)
	}
}

func TestExpandBody_SkillNotFound(t *testing.T) {
	svc, _ := newExpandTestService(t)
	_, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "ghost"})
	if err == nil || !strings.Contains(err.Error(), "skill not found") {
		t.Fatalf("expected skill-not-found, got %v", err)
	}
}

func TestExpandBody_InvalidNameRejected(t *testing.T) {
	svc, _ := newExpandTestService(t)
	_, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "Foo Bar"})
	if err == nil {
		t.Fatalf("invalid name should be rejected")
	}
}

func TestExpandBody_Truncation(t *testing.T) {
	svc, root := newExpandTestService(t)
	big := strings.Repeat("x", 50_000)
	writeExpandTestSkill(t, root, "big", "---\nname: big\n---\n"+big)
	res, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "big", MaxBytes: 1000})
	if err != nil {
		t.Fatalf("ExpandBody: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("large body should be truncated")
	}
	if int64(len(res.Content)) > 1000 {
		t.Fatalf("content exceeds MaxBytes: got %d", len(res.Content))
	}
	if res.TotalBytes < 50_000 {
		t.Fatalf("TotalBytes should reflect full body, got %d", res.TotalBytes)
	}
}

func TestExpandBody_DefaultMaxBytes(t *testing.T) {
	svc, root := newExpandTestService(t)
	body := strings.Repeat("a", 30_000)
	writeExpandTestSkill(t, root, "m", "---\nname: m\n---\n"+body)
	res, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "m"}) // MaxBytes 未设
	if err != nil {
		t.Fatalf("ExpandBody: %v", err)
	}
	if !res.Truncated || len(res.Content) != defaultExpandMaxBytes {
		t.Fatalf("default MaxBytes should cap at %d, got Truncated=%v len=%d", defaultExpandMaxBytes, res.Truncated, len(res.Content))
	}
}

// TestExpandBody_OnlyFrontmatterYieldsEmptyBody 锁定 splitFrontmatter fallback bug 修复：
// SKILL.md 仅有 frontmatter 时，ExpandBody 返回内容必须为空（而非泄漏 frontmatter
// 作为 body）。P20.1 §3.3 untrusted metadata 纯化依赖该不变量。
func TestExpandBody_OnlyFrontmatterYieldsEmptyBody(t *testing.T) {
	svc, root := newExpandTestService(t)
	// 内容只有 frontmatter，后续 body 为空
	writeExpandTestSkill(t, root, "fmonly", "---\nname: fmonly\ndescription: secret metadata\nsummary: leaked!\n---\n")
	res, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "fmonly"})
	if err != nil {
		t.Fatalf("ExpandBody: %v", err)
	}
	if res.Content != "" {
		t.Fatalf("body-only frontmatter MUST yield empty content (bug: leaked frontmatter), got %q", res.Content)
	}
	// frontmatter 内的任何字段（含“secret metadata”/"leaked!"）都不得出现在 Content
	if strings.Contains(res.Content, "secret") || strings.Contains(res.Content, "leaked") {
		t.Fatalf("frontmatter must NOT leak into content: %q", res.Content)
	}
}

// TestExpandBody_NoFrontmatterReturnsFullFile 无 frontmatter 时，整个文件作为 body。
func TestExpandBody_NoFrontmatterReturnsFullFile(t *testing.T) {
	svc, root := newExpandTestService(t)
	writeExpandTestSkill(t, root, "nofm", "just body content\nline 2")
	res, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "nofm"})
	if err != nil {
		t.Fatalf("ExpandBody: %v", err)
	}
	if !strings.Contains(res.Content, "just body content") || !strings.Contains(res.Content, "line 2") {
		t.Fatalf("no-frontmatter file should be returned as body, got %q", res.Content)
	}
}

// ---- ReadResource ----

func TestReadResource_Normal(t *testing.T) {
	svc, root := newExpandTestService(t)
	dir := writeExpandTestSkill(t, root, "foo", "---\nname: foo\n---\nbody")
	refPath := filepath.Join(dir, "references")
	if err := os.MkdirAll(refPath, 0o755); err != nil {
		t.Fatalf("mkdir ref: %v", err)
	}
	if err := os.WriteFile(filepath.Join(refPath, "api.md"), []byte("# API\nrefs here"), 0o644); err != nil {
		t.Fatalf("write ref: %v", err)
	}
	res, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: "references/api.md"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if !strings.Contains(res.Content, "refs here") {
		t.Fatalf("content missing: %q", res.Content)
	}
	if res.Path != "references/api.md" {
		t.Fatalf("normalized path: %q", res.Path)
	}
	// macOS /tmp 是 /private/tmp 的 symlink，SkillDir 经 EvalSymlinks 后可能带
	// /private 前缀。用 EvalSymlinks 规范化两侧再比对。
	wantDir, _ := filepath.EvalSymlinks(dir)
	gotDir, _ := filepath.EvalSymlinks(res.SkillDir)
	if filepath.Clean(gotDir) != filepath.Clean(wantDir) {
		t.Fatalf("SkillDir mismatch after EvalSymlinks:\ngot  %q\nwant %q", gotDir, wantDir)
	}
}

func TestReadResource_PathEscapeRejected(t *testing.T) {
	svc, root := newExpandTestService(t)
	writeExpandTestSkill(t, root, "foo", "---\nname: foo\n---\nbody")
	cases := []string{
		"../evil",
		"../../etc/passwd",
		"references/../../../escape",
		"/abs/path",
	}
	for _, p := range cases {
		if _, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: p}); err == nil {
			t.Fatalf("path %q MUST be rejected", p)
		}
	}
}

func TestReadResource_EmptyPathRejected(t *testing.T) {
	svc, root := newExpandTestService(t)
	writeExpandTestSkill(t, root, "foo", "---\nname: foo\n---\nbody")
	if _, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: ""}); err == nil {
		t.Fatalf("empty path should be rejected")
	}
}

func TestReadResource_SkillNotFound(t *testing.T) {
	svc, _ := newExpandTestService(t)
	_, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "ghost", Path: "x.md"})
	if err == nil || !strings.Contains(err.Error(), "skill not found") {
		t.Fatalf("expected skill-not-found, got %v", err)
	}
}

func TestReadResource_DirectoryRejected(t *testing.T) {
	svc, root := newExpandTestService(t)
	dir := writeExpandTestSkill(t, root, "foo", "---\nname: foo\n---\nbody")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: "references"})
	if err == nil {
		t.Fatalf("directory target should be rejected")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error text: %v", err)
	}
}

// TestReadResource_EmptyFile 空文件（0 字节）合法读取，返回 Content="" TotalBytes=0。
func TestReadResource_EmptyFile(t *testing.T) {
	svc, root := newExpandTestService(t)
	dir := writeExpandTestSkill(t, root, "foo", "---\nname: foo\n---\nbody")
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), []byte{}, 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	res, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: "empty.txt"})
	if err != nil {
		t.Fatalf("ReadResource empty file: %v", err)
	}
	if res.Content != "" {
		t.Fatalf("empty file should yield empty content, got %q", res.Content)
	}
	if res.TotalBytes != 0 {
		t.Fatalf("empty file TotalBytes = %d, want 0", res.TotalBytes)
	}
	if res.Truncated {
		t.Fatalf("empty file should not be truncated")
	}
}

// TestReadResource_VersionReflectsResourceContent P20.1 Phase 6 审核第 2 轮
// 发现的 bug：ReadResource 的 Version 必须反映读取到的资源文件 hash，
// 不能错用 SKILL.md hash——否则资源文件改动时调用方无法感知。
func TestReadResource_VersionReflectsResourceContent(t *testing.T) {
	svc, root := newExpandTestService(t)
	dir := writeExpandTestSkill(t, root, "foo", "---\nname: foo\n---\nbody")
	refPath := filepath.Join(dir, "ref.md")

	// 版本 1
	if err := os.WriteFile(refPath, []byte("content v1"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r1, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: "ref.md"})
	if err != nil {
		t.Fatalf("read v1: %v", err)
	}
	if r1.Version == "" {
		t.Fatalf("Version must be non-empty")
	}

	// 仅改动 resource 文件、不改 SKILL.md。之前的 bug 下 Version 会沉默不变。
	if err := os.WriteFile(refPath, []byte("content v2 different bytes"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	r2, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: "ref.md"})
	if err != nil {
		t.Fatalf("read v2: %v", err)
	}
	if r1.Version == r2.Version {
		t.Fatalf("Version MUST change when resource content changes (was %q, still %q)", r1.Version, r2.Version)
	}

	// SKILL.md 改动但 ref.md 不改——Version 不变（此时不再用 skill hash）
	if err := os.WriteFile(filepath.Join(dir, skillMainFile), []byte("---\nname: foo\n---\nALTERED SKILL BODY"), 0o644); err != nil {
		t.Fatalf("alter SKILL.md: %v", err)
	}
	r3, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: "ref.md"})
	if err != nil {
		t.Fatalf("read v3: %v", err)
	}
	if r3.Version != r2.Version {
		t.Fatalf("SKILL.md change should NOT affect resource Version (was %q, now %q)", r2.Version, r3.Version)
	}
}

func TestReadResource_Truncation(t *testing.T) {
	svc, root := newExpandTestService(t)
	dir := writeExpandTestSkill(t, root, "foo", "---\nname: foo\n---\nbody")
	big := strings.Repeat("z", 30_000)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	res, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "foo", Path: "big.txt", MaxBytes: 500})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if !res.Truncated || len(res.Content) != 500 {
		t.Fatalf("truncation failed: Truncated=%v len=%d", res.Truncated, len(res.Content))
	}
	if res.TotalBytes != 30_000 {
		t.Fatalf("TotalBytes = %d, want 30000", res.TotalBytes)
	}
}

// ---- Markdown slicer ----

func TestSliceMarkdownSection_NestedLevels(t *testing.T) {
	body := "## A\naaa\n### A.1\na1\n### A.2\na2\n## B\nbbb"
	out, ok := sliceMarkdownSection(body, "A")
	if !ok {
		t.Fatalf("A should be found")
	}
	if !strings.Contains(out, "### A.1") || !strings.Contains(out, "### A.2") {
		t.Fatalf("should include nested H3: %q", out)
	}
	if strings.Contains(out, "## B") {
		t.Fatalf("should stop before ## B: %q", out)
	}
}

func TestSliceMarkdownSection_EmptyAnchorReturnsAll(t *testing.T) {
	out, ok := sliceMarkdownSection("## X\ny", "")
	if !ok || out != "## X\ny" {
		t.Fatalf("empty anchor should return full body: %q ok=%v", out, ok)
	}
}

func TestSliceMarkdownSection_NotFound(t *testing.T) {
	_, ok := sliceMarkdownSection("## X\n", "missing")
	if ok {
		t.Fatalf("missing anchor should return ok=false")
	}
}

func TestNormalizeAnchorSlug(t *testing.T) {
	cases := map[string]string{
		"Usage":        "usage",
		"Usage Guide":  "usage-guide",
		"  Hello  ":    "hello",
		"a/b":          "ab",
		"A_B C":        "a-b-c",
		"---edge---":   "edge",
		"":             "",
	}
	for in, want := range cases {
		if got := normalizeAnchorSlug(in); got != want {
			t.Fatalf("normalizeAnchorSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateBytes(t *testing.T) {
	if s, tr := truncateBytes("hello", 10); s != "hello" || tr {
		t.Fatalf("no trunc: %q %v", s, tr)
	}
	if s, tr := truncateBytes("hello", 3); s != "hel" || !tr {
		t.Fatalf("trunc: %q %v", s, tr)
	}
	if s, tr := truncateBytes("x", 0); s != "x" || tr {
		t.Fatalf("0 limit should no-op: %q %v", s, tr)
	}
}

// ---- Max bytes helpers ----

func TestResolveMaxBytes(t *testing.T) {
	if got := resolveMaxBytes(0); got != defaultExpandMaxBytes {
		t.Fatalf("0 should default: %d", got)
	}
	if got := resolveMaxBytes(-1); got != defaultExpandMaxBytes {
		t.Fatalf("negative should default: %d", got)
	}
	if got := resolveMaxBytes(500); got != 500 {
		t.Fatalf("mid should pass: %d", got)
	}
	if got := resolveMaxBytes(int64(maxSkillFileBytes) + 1); got != int64(maxSkillFileBytes) {
		t.Fatalf("above cap should clamp: %d", got)
	}
}
