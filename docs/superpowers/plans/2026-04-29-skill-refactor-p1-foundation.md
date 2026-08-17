# Skill Refactor — Phase 1: Foundation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 skill 重构的纯基础设施层 —— `skillforge` + `skilllibrary` 两个新包，提供 library/cache 的 parse / render / atomic write / scan / seed / reconcile 能力，**不接任何上游调用方**（claudecli/codexapp 在 P2/P3 才切换）。

**Architecture:** 单一 library (`~/.super-dolphin/skills-library/`) → forge 纯函数转换 → 单一 cache (`~/.super-dolphin/skills-cache/`)。Library 入口：embedded source seed + 用户/商店 install + dev override fsnotify。Cache 写入：单 skill 粒度 `.tmp + rename` 原子替换。本 Phase 完成后 main 永远绿，但 runtime 行为不变（旧 skill 路径仍工作）。

**Tech Stack:** Go 1.22+、`go.uber.org/fx`、`github.com/fsnotify/fsnotify`、`embed` (stdlib)、现有 frontmatter 解析模式参考 `internal/module/skill/skills_meta.go`。

**前置阅读：**
- `docs/superpowers/specs/2026-04-29-skill-refactor-design.md` §1-§5、§11-§12
- `internal/module/memory/path.go`（atomic write helper 现有模式）
- `internal/module/skill/skills_meta.go`（现有 frontmatter parse 风格，仅参考不复用）

---

## File Structure

新增两个包，**不修改任何现有文件**（Fx wire-up 是唯一例外，见 Task 14）：

```
internal/module/skillforge/
├── parse.go            (Task 1)
├── parse_test.go
├── summary.go          (Task 2)
├── summary_test.go
├── naming.go           (Task 3)
├── naming_test.go
├── render.go           (Task 4)
├── render_test.go
├── atomic.go           (Task 5)
├── atomic_test.go
├── forge.go            (Task 6)
├── forge_test.go
├── embed.go            (Task 7)
├── embed_test.go
└── module.go           (Task 14)

internal/module/skilllibrary/
├── meta.go             (Task 8)
├── meta_test.go
├── scan.go             (Task 9)
├── scan_test.go
├── store.go            (Task 10)
├── store_test.go
├── seed.go             (Task 11)
├── seed_test.go
├── reconcile.go        (Task 12)
├── reconcile_test.go
├── watcher.go          (Task 13)
├── watcher_test.go
└── module.go           (Task 14)
```

测试 fixtures（小型 SKILL.md 样本）放在各 `_test.go` 同文件以 string literal 形式存在，不创建额外 testdata 目录。

---

## Task 1: SKILL.md Parser

**Files:**
- Create: `internal/module/skillforge/parse.go`
- Test: `internal/module/skillforge/parse_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skillforge/parse_test.go
package skillforge

import (
	"strings"
	"testing"
)

func TestParse_FrontmatterAndH2Sections(t *testing.T) {
	src := `---
name: 测试驱动开发
description: 实现任何功能前使用
---

# 测试驱动开发

## 红绿重构循环

先写失败测试，再实现，最后重构。

具体：
- 红：写测试

## 反模式与诊断

常见反模式列表。
`
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if got.Name != "测试驱动开发" {
		t.Errorf("Name = %q, want 测试驱动开发", got.Name)
	}
	if got.Description != "实现任何功能前使用" {
		t.Errorf("Description = %q", got.Description)
	}
	if len(got.Sections) != 2 {
		t.Fatalf("len(Sections) = %d, want 2", len(got.Sections))
	}
	if got.Sections[0].Title != "红绿重构循环" {
		t.Errorf("Sections[0].Title = %q", got.Sections[0].Title)
	}
	if !strings.Contains(got.Sections[0].Body, "先写失败测试") {
		t.Errorf("Sections[0].Body missing expected content")
	}
}

func TestParse_NoFrontmatterReturnsError(t *testing.T) {
	src := `# Title without frontmatter

## H2

Body
`
	_, err := Parse(src)
	if err == nil {
		t.Fatal("Parse should fail without frontmatter")
	}
}

func TestParse_NoH2SectionsIsAllowed(t *testing.T) {
	src := `---
name: simple
description: only intro
---

# Simple

Just intro text, no H2.
`
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(got.Sections) != 0 {
		t.Errorf("len(Sections) = %d, want 0", len(got.Sections))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skillforge/... -run TestParse -v`
Expected: FAIL with `undefined: Parse`

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skillforge/parse.go
package skillforge

import (
	"errors"
	"strings"
)

// ParsedSkill 是 SKILL.md 解析后的中间表示。
// Sections 严格按 H2 切分；H3 留在所属 H2 的 Body 里。
type ParsedSkill struct {
	Name        string
	Description string
	Frontmatter map[string]string // 原始 frontmatter，含 name/description 之外的字段
	Sections    []Section
}

type Section struct {
	Title string // H2 标题（不含 "## " 前缀）
	Body  string // H2 标题之后到下一个 H2 之前的全部内容（去除前后空白）
}

var ErrMissingFrontmatter = errors.New("skillforge: SKILL.md must start with --- frontmatter ---")

// Parse 把 SKILL.md 完整内容解析为 ParsedSkill。
// frontmatter 必须以 "---\n" 开头并以 "---\n" 结束。
// H2 用 "^## " 识别。
func Parse(src string) (*ParsedSkill, error) {
	src = strings.TrimPrefix(src, "﻿") // 去 BOM
	if !strings.HasPrefix(src, "---\n") {
		return nil, ErrMissingFrontmatter
	}
	rest := strings.TrimPrefix(src, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return nil, ErrMissingFrontmatter
	}
	fmText := rest[:end]
	body := rest[end+len("\n---\n"):]

	fm := parseFrontmatter(fmText)
	ps := &ParsedSkill{
		Name:        fm["name"],
		Description: fm["description"],
		Frontmatter: fm,
	}
	ps.Sections = splitH2(body)
	return ps, nil
}

// parseFrontmatter 解析极简 YAML（仅 key: value 单行，不支持嵌套）。
// 设计选择：本期不引入 yaml 库；多行值通过 frontmatter 的 sidecar 表达。
func parseFrontmatter(text string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out
}

func splitH2(body string) []Section {
	lines := strings.Split(body, "\n")
	var sections []Section
	var cur *Section
	for _, ln := range lines {
		if strings.HasPrefix(ln, "## ") {
			if cur != nil {
				cur.Body = strings.TrimSpace(cur.Body)
				sections = append(sections, *cur)
			}
			cur = &Section{Title: strings.TrimSpace(strings.TrimPrefix(ln, "## "))}
			continue
		}
		if cur != nil {
			cur.Body += ln + "\n"
		}
	}
	if cur != nil {
		cur.Body = strings.TrimSpace(cur.Body)
		sections = append(sections, *cur)
	}
	return sections
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skillforge/... -run TestParse -v`
Expected: PASS（3 个用例）

- [ ] **Step 5: Commit**

```bash
git add internal/module/skillforge/parse.go internal/module/skillforge/parse_test.go
git commit -m "feat(skillforge): add SKILL.md parser with frontmatter + H2 split"
```

---

## Task 2: Section Summary Extraction

**Files:**
- Create: `internal/module/skillforge/summary.go`
- Test: `internal/module/skillforge/summary_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skillforge/summary_test.go
package skillforge

import "testing"

func TestExtractSummary_FirstSentence(t *testing.T) {
	body := "先写失败测试，再实现，最后重构。具体步骤如下：\n- 红：写测试"
	got := ExtractSummary(body, 80)
	want := "先写失败测试，再实现，最后重构。"
	if got != want {
		t.Errorf("ExtractSummary = %q, want %q", got, want)
	}
}

func TestExtractSummary_TruncatesLongFirstSentence(t *testing.T) {
	body := strings.Repeat("a", 200) + "."
	got := ExtractSummary(body, 80)
	if len(got) > 80 {
		t.Errorf("len(got) = %d, want <= 80", len(got))
	}
}

func TestExtractSummary_EmptyBody(t *testing.T) {
	if got := ExtractSummary("", 80); got != "" {
		t.Errorf("ExtractSummary(empty) = %q, want \"\"", got)
	}
}

func TestExtractSummary_SkipsLeadingHeading(t *testing.T) {
	body := "# Inner heading should be skipped\n\n这才是正文第一句。"
	got := ExtractSummary(body, 80)
	want := "这才是正文第一句。"
	if got != want {
		t.Errorf("ExtractSummary = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skillforge/... -run TestExtractSummary -v`
Expected: FAIL（undefined）

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skillforge/summary.go
package skillforge

import (
	"strings"
	"unicode/utf8"
)

// ExtractSummary 从 H2 段正文中抽取 1-2 句作为摘要，截到 maxRunes（按 rune 计）。
// 规则：
//  1. 跳过空行和以 "#" / "-" / "*" 开头的标题/列表起始行。
//  2. 第一段（连续非空非标题行）作为候选。
//  3. 用句号/问号/叹号（中文 + 英文）切分，取第一句。
//  4. 句末补回中文句号；若超出 maxRunes，按 rune 截断并加 "…"。
func ExtractSummary(body string, maxRunes int) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	first := firstParagraph(body)
	if first == "" {
		return ""
	}
	first = strings.ReplaceAll(first, "\n", " ")

	// 切句
	stops := []string{"。", "！", "？", ".", "!", "?"}
	cutAt := -1
	for _, s := range stops {
		if i := strings.Index(first, s); i >= 0 {
			if cutAt == -1 || i < cutAt {
				cutAt = i + len(s)
			}
		}
	}
	out := first
	if cutAt > 0 {
		out = first[:cutAt]
	} else {
		// 没有句末标点，整段作为一句
		out = first + "。"
	}
	out = strings.TrimSpace(out)
	if utf8.RuneCountInString(out) > maxRunes {
		out = truncateRunes(out, maxRunes-1) + "…"
	}
	return out
}

func firstParagraph(body string) string {
	var buf strings.Builder
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			if buf.Len() > 0 {
				return strings.TrimSpace(buf.String())
			}
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") {
			if buf.Len() > 0 {
				return strings.TrimSpace(buf.String())
			}
			continue
		}
		buf.WriteString(t)
		buf.WriteString(" ")
	}
	return strings.TrimSpace(buf.String())
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}
```

补加 `summary_test.go` 顶部 import：
```go
import (
	"strings"
	"testing"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skillforge/... -run TestExtractSummary -v`
Expected: PASS（4 个用例）

- [ ] **Step 5: Commit**

```bash
git add internal/module/skillforge/summary.go internal/module/skillforge/summary_test.go
git commit -m "feat(skillforge): add section summary auto-extractor"
```

---

## Task 3: Filename Sanitization (N1)

**Files:**
- Create: `internal/module/skillforge/naming.go`
- Test: `internal/module/skillforge/naming_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skillforge/naming_test.go
package skillforge

import "testing"

func TestSectionFilename(t *testing.T) {
	cases := []struct {
		index int
		title string
		want  string
	}{
		{1, "红绿重构循环", "01-红绿重构循环.md"},
		{12, "反模式：这太简单", "12-反模式-这太简单.md"},
		{1, "Path/with/slashes", "01-Path-with-slashes.md"},
		{1, "  trim  ", "01-trim.md"},
		{1, `Quotes "and" stuff`, "01-Quotes -and- stuff.md"},
	}
	for _, tc := range cases {
		got := SectionFilename(tc.index, tc.title)
		if got != tc.want {
			t.Errorf("SectionFilename(%d, %q) = %q, want %q", tc.index, tc.title, got, tc.want)
		}
	}
}

func TestSectionFilename_TruncatesVeryLong(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := SectionFilename(1, long)
	if len(got) > 100 {
		t.Errorf("filename too long: %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skillforge/... -run TestSectionFilename -v`
Expected: FAIL（undefined）

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skillforge/naming.go
package skillforge

import (
	"fmt"
	"regexp"
	"strings"
)

// 文件系统非法字符（POSIX + Windows 共集）替换为 "-"。
var illegalFilenameChars = regexp.MustCompile(`[/\\:\*\?"<>\|]`)

// SectionFilename 按 N1 规则生成 references/<NN-标题>.md 的文件名（仅文件名部分，不含目录）。
// - index：从 1 开始的 H2 出现序号
// - title：H2 标题原文（保留中文，仅替换非法字符）
// - 长标题截到 80 runes（保护文件系统兼容性，留出前缀和扩展名）
func SectionFilename(index int, title string) string {
	t := strings.TrimSpace(title)
	t = illegalFilenameChars.ReplaceAllString(t, "-")
	if rc := []rune(t); len(rc) > 80 {
		t = string(rc[:80])
	}
	return fmt.Sprintf("%02d-%s.md", index, t)
}
```

补加 `naming_test.go` 的 import：
```go
import (
	"strings"
	"testing"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skillforge/... -run TestSectionFilename -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/skillforge/naming.go internal/module/skillforge/naming_test.go
git commit -m "feat(skillforge): add N1 filename sanitization for sections"
```

---

## Task 4: Renderer (slim SKILL.md + references)

**Files:**
- Create: `internal/module/skillforge/render.go`
- Test: `internal/module/skillforge/render_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skillforge/render_test.go
package skillforge

import (
	"strings"
	"testing"
)

func TestRender_SlimSKILLMDContainsTOC(t *testing.T) {
	ps := &ParsedSkill{
		Name:        "测试驱动开发",
		Description: "实现任何功能前使用",
		Sections: []Section{
			{Title: "红绿重构循环", Body: "先写失败测试，再实现，最后重构。详细：..."},
			{Title: "反模式与诊断", Body: "常见反模式信号。"},
		},
	}
	out, err := Render(ps, nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	skillMD := out.SkillMD
	if !strings.Contains(skillMD, "name: 测试驱动开发") {
		t.Errorf("SkillMD missing frontmatter name")
	}
	if !strings.Contains(skillMD, "01-红绿重构循环.md") {
		t.Errorf("SkillMD missing references[0] link")
	}
	if !strings.Contains(skillMD, "先写失败测试") {
		t.Errorf("SkillMD missing summary[0]")
	}
	if len(out.References) != 2 {
		t.Fatalf("len(References) = %d, want 2", len(out.References))
	}
	if !strings.Contains(out.References["01-红绿重构循环.md"], "先写失败测试") {
		t.Errorf("references[01].body missing original content")
	}
}

func TestRender_RespectsSummaryOverride(t *testing.T) {
	ps := &ParsedSkill{
		Name:        "demo",
		Description: "d",
		Sections:    []Section{{Title: "节A", Body: "原文摘要"}},
	}
	override := map[string]string{"节A": "手写摘要覆盖"}
	out, err := Render(ps, override)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(out.SkillMD, "手写摘要覆盖") {
		t.Errorf("SkillMD did not pick up overridden summary")
	}
	if strings.Contains(out.SkillMD, "原文摘要") {
		t.Errorf("SkillMD should not contain auto summary when override present")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skillforge/... -run TestRender -v`
Expected: FAIL（undefined）

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skillforge/render.go
package skillforge

import (
	"fmt"
	"strings"
)

const summaryRunes = 80

// RenderResult 是 forge 单 skill 转换后的内存产物，
// 由 atomic.go 落盘到 cache/<name>/。
type RenderResult struct {
	SkillMD    string            // 瘦身 SKILL.md 全文
	References map[string]string // filename -> H2 段正文
}

// Render 把 ParsedSkill 渲染为瘦身 SKILL.md + 各节 references 文件。
// summaryOverride: anchor -> 手写摘要；nil 表示全部自动抽。
func Render(ps *ParsedSkill, summaryOverride map[string]string) (*RenderResult, error) {
	if ps == nil {
		return nil, fmt.Errorf("skillforge: Render(nil)")
	}
	res := &RenderResult{References: map[string]string{}}

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", ps.Name))
	b.WriteString(fmt.Sprintf("description: %s\n", ps.Description))
	b.WriteString("---\n\n")
	b.WriteString(fmt.Sprintf("# %s\n\n", ps.Name))

	if len(ps.Sections) == 0 {
		b.WriteString("（本 skill 无 H2 分节。）\n")
		res.SkillMD = b.String()
		return res, nil
	}

	b.WriteString("## 节索引（按需读，勿全文加载）\n\n")
	for i, sec := range ps.Sections {
		idx := i + 1
		fname := SectionFilename(idx, sec.Title)
		summary := summaryOverride[sec.Title]
		if summary == "" {
			summary = ExtractSummary(sec.Body, summaryRunes)
		}
		b.WriteString(fmt.Sprintf("- %s — %s\n", sec.Title, summary))
		b.WriteString(fmt.Sprintf("  详见 references/%s\n", fname))
		res.References[fname] = sec.Body + "\n"
	}
	b.WriteString("\n> 需要某节内容时，使用 Read 工具读取对应 references/ 文件，不要整文加载。\n")

	res.SkillMD = b.String()
	return res, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skillforge/... -run TestRender -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/skillforge/render.go internal/module/skillforge/render_test.go
git commit -m "feat(skillforge): add renderer for slim SKILL.md + references"
```

---

## Task 5: Atomic Per-Skill Cache Write (A2)

**Files:**
- Create: `internal/module/skillforge/atomic.go`
- Test: `internal/module/skillforge/atomic_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skillforge/atomic_test.go
package skillforge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteSkill_CreatesTargetDir(t *testing.T) {
	tmp := t.TempDir()
	res := &RenderResult{
		SkillMD:    "---\nname: x\n---\n",
		References: map[string]string{"01-foo.md": "foo body"},
	}
	if err := AtomicWriteSkill(tmp, "x", res); err != nil {
		t.Fatalf("AtomicWriteSkill: %v", err)
	}
	skillFile := filepath.Join(tmp, "x", "SKILL.md")
	refFile := filepath.Join(tmp, "x", "references", "01-foo.md")

	checkFile(t, skillFile, "name: x")
	checkFile(t, refFile, "foo body")
}

func TestAtomicWriteSkill_OverwritesExisting(t *testing.T) {
	tmp := t.TempDir()
	first := &RenderResult{SkillMD: "v1", References: map[string]string{"01-a.md": "a1"}}
	second := &RenderResult{SkillMD: "v2", References: map[string]string{"01-a.md": "a2"}}

	if err := AtomicWriteSkill(tmp, "x", first); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteSkill(tmp, "x", second); err != nil {
		t.Fatal(err)
	}
	checkFile(t, filepath.Join(tmp, "x", "SKILL.md"), "v2")
	checkFile(t, filepath.Join(tmp, "x", "references", "01-a.md"), "a2")
}

func TestAtomicWriteSkill_RemovesStaleTmp(t *testing.T) {
	tmp := t.TempDir()
	// Pre-create stale .tmp dir to simulate previous crash
	stalePath := filepath.Join(tmp, "x.tmp")
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stalePath, "junk"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := &RenderResult{SkillMD: "ok"}
	if err := AtomicWriteSkill(tmp, "x", res); err != nil {
		t.Fatalf("AtomicWriteSkill: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("stale .tmp not cleaned: err=%v", err)
	}
}

func checkFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !contains(string(b), want) {
		t.Errorf("file %s: want substring %q, got %q", path, want, string(b))
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || index(s, sub) >= 0) }
func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skillforge/... -run TestAtomicWriteSkill -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skillforge/atomic.go
package skillforge

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteSkill 把 RenderResult 原子写入 cacheDir/<name>/。
// 实现 A2：写到 <name>.tmp/ → 删旧 <name>/ → rename(.tmp, <name>)。
// 短暂窗口期（毫秒级）；读端遇 ENOENT 应重试一次。
func AtomicWriteSkill(cacheDir, name string, res *RenderResult) error {
	target := filepath.Join(cacheDir, name)
	tmp := filepath.Join(cacheDir, name+".tmp")

	// 1. 清残留 .tmp
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf("skillforge: cleanup stale tmp: %w", err)
	}
	// 2. 写入 .tmp
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return fmt.Errorf("skillforge: mkdir tmp: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "SKILL.md"), []byte(res.SkillMD), 0o644); err != nil {
		return fmt.Errorf("skillforge: write SKILL.md: %w", err)
	}
	if len(res.References) > 0 {
		refDir := filepath.Join(tmp, "references")
		if err := os.MkdirAll(refDir, 0o755); err != nil {
			return fmt.Errorf("skillforge: mkdir references: %w", err)
		}
		for fname, body := range res.References {
			if err := os.WriteFile(filepath.Join(refDir, fname), []byte(body), 0o644); err != nil {
				return fmt.Errorf("skillforge: write reference %s: %w", fname, err)
			}
		}
	}
	// 3. 删旧目标，rename .tmp 上位
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("skillforge: remove old target: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("skillforge: rename tmp to target: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skillforge/... -run TestAtomicWriteSkill -v`
Expected: PASS（3 个用例）

- [ ] **Step 5: Commit**

```bash
git add internal/module/skillforge/atomic.go internal/module/skillforge/atomic_test.go
git commit -m "feat(skillforge): add per-skill atomic cache write (A2)"
```

---

## Task 6: Forge Public API

**Files:**
- Create: `internal/module/skillforge/forge.go`
- Test: `internal/module/skillforge/forge_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skillforge/forge_test.go
package skillforge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForge_EndToEnd(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()

	src := `---
name: tdd
description: write tests first
---

# tdd

## red

write a failing test first.

## green

implement until test passes.
`
	skillRoot := filepath.Join(libDir, "tdd")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Forge(libDir, cacheDir, "tdd", nil); err != nil {
		t.Fatalf("Forge: %v", err)
	}

	want := []string{
		filepath.Join(cacheDir, "tdd", "SKILL.md"),
		filepath.Join(cacheDir, "tdd", "references", "01-red.md"),
		filepath.Join(cacheDir, "tdd", "references", "02-green.md"),
	}
	for _, p := range want {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file missing: %s (%v)", p, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skillforge/... -run TestForge_EndToEnd -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skillforge/forge.go
package skillforge

import (
	"fmt"
	"os"
	"path/filepath"
)

// Forge 把 libDir/<name>/SKILL.md 转换为 cacheDir/<name>/{SKILL.md, references/...}。
// summaryOverride: 手写节摘要表，nil 表示全部自动抽。
//
// 此函数无副作用地处理单个 skill；批量调度由 skilllibrary.Reconcile 负责。
func Forge(libDir, cacheDir, name string, summaryOverride map[string]string) error {
	src := filepath.Join(libDir, name, "SKILL.md")
	raw, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("skillforge: read source: %w", err)
	}
	ps, err := Parse(string(raw))
	if err != nil {
		return fmt.Errorf("skillforge: parse %s: %w", name, err)
	}
	res, err := Render(ps, summaryOverride)
	if err != nil {
		return fmt.Errorf("skillforge: render %s: %w", name, err)
	}
	if err := AtomicWriteSkill(cacheDir, name, res); err != nil {
		return fmt.Errorf("skillforge: write %s: %w", name, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skillforge/... -run TestForge_EndToEnd -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/skillforge/forge.go internal/module/skillforge/forge_test.go
git commit -m "feat(skillforge): add Forge public API integrating parse/render/atomic"
```

---

## Task 7: Embedded Source Skills

**Files:**
- Create: `internal/module/skillforge/embed.go`
- Test: `internal/module/skillforge/embed_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skillforge/embed_test.go
package skillforge

import "testing"

func TestEmbeddedSkillsAccessor(t *testing.T) {
	names, err := ListEmbeddedSkillNames()
	if err != nil {
		t.Fatalf("ListEmbeddedSkillNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no embedded skills found; expected at least 1")
	}

	// 至少 14 个内置 skill 都应该在
	min := 14
	if len(names) < min {
		t.Errorf("len(names) = %d, want >= %d", len(names), min)
	}

	// 抽一个读读看
	body, err := ReadEmbeddedSkill(names[0])
	if err != nil {
		t.Fatalf("ReadEmbeddedSkill(%s): %v", names[0], err)
	}
	if len(body) == 0 {
		t.Errorf("empty body for %s", names[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skillforge/... -run TestEmbeddedSkillsAccessor -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skillforge/embed.go
package skillforge

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed all:embedded_skills
var embeddedFS embed.FS

// embeddedRoot 是 //go:embed 路径前缀；与 go:embed 指令一致。
const embeddedRoot = "embedded_skills"

// ListEmbeddedSkillNames 返回所有内置 skill 名（embedded_skills/<name>/SKILL.md 存在的）。
func ListEmbeddedSkillNames() ([]string, error) {
	entries, err := embeddedFS.ReadDir(embeddedRoot)
	if err != nil {
		return nil, fmt.Errorf("skillforge: list embedded: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// 验证 SKILL.md 存在
		if _, err := embeddedFS.ReadFile(embeddedRoot + "/" + e.Name() + "/SKILL.md"); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// ReadEmbeddedSkill 返回 embedded_skills/<name>/SKILL.md 的字节内容。
func ReadEmbeddedSkill(name string) ([]byte, error) {
	if strings.ContainsAny(name, "/\\.") {
		return nil, fmt.Errorf("skillforge: invalid skill name %q", name)
	}
	return embeddedFS.ReadFile(embeddedRoot + "/" + name + "/SKILL.md")
}
```

接着把现有 `.agent/skills/` 整体复制到 `internal/module/skillforge/embedded_skills/`：

```bash
cp -R .agent/skills internal/module/skillforge/embedded_skills
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skillforge/... -run TestEmbeddedSkillsAccessor -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/skillforge/embed.go \
        internal/module/skillforge/embed_test.go \
        internal/module/skillforge/embedded_skills
git commit -m "feat(skillforge): add //go:embed accessor for builtin skills"
```

---

## Task 8: Sidecar Metadata (.skill-meta.json)

**Files:**
- Create: `internal/module/skilllibrary/meta.go`
- Test: `internal/module/skilllibrary/meta_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skilllibrary/meta_test.go
package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := SkillMeta{
		Name:           "tdd",
		Origin:         OriginBuiltin,
		Version:        "1.0.0",
		VersionHash:    "sha256:abc",
		Pinned:         true,
		ReplacesNative: map[string][]string{"claude": {"feature-dev/feature-dev"}},
	}
	if err := WriteMeta(dir, want); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".skill-meta.json")); err != nil {
		t.Fatalf("meta file missing: %v", err)
	}
	got, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if got.Name != want.Name || got.Origin != want.Origin || got.VersionHash != want.VersionHash {
		t.Errorf("ReadMeta = %+v, want %+v", got, want)
	}
	if !got.Pinned {
		t.Errorf("Pinned not preserved")
	}
	if len(got.ReplacesNative["claude"]) != 1 {
		t.Errorf("ReplacesNative not preserved")
	}
}

func TestMeta_MissingFileReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadMeta(dir)
	if err == nil || !os.IsNotExist(err) {
		t.Errorf("ReadMeta: want IsNotExist, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skilllibrary/... -run TestMeta -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skilllibrary/meta.go
package skilllibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Origin 表示 skill 来源（spec §5.1）。
type Origin string

const (
	OriginBuiltin     Origin = "builtin"
	OriginMarketplace Origin = "marketplace"
	OriginLocal       Origin = "local"
	OriginDevOverride Origin = "dev-override"
)

// SkillMeta 是 .skill-meta.json sidecar 的完整 schema（spec §3.1）。
type SkillMeta struct {
	Name                   string              `json:"name"`
	Origin                 Origin              `json:"origin"`
	Version                string              `json:"version"`
	VersionHash            string              `json:"version_hash"`
	InstalledAt            string              `json:"installed_at,omitempty"`
	Signature              *string             `json:"signature"`
	AllowedTools           []string            `json:"allowed_tools,omitempty"`
	DisableModelInvocation bool                `json:"disable_model_invocation,omitempty"`
	Pinned                 bool                `json:"pinned,omitempty"`
	Disabled               bool                `json:"disabled,omitempty"`
	ReplacesNative         map[string][]string `json:"replaces_native,omitempty"`
	SectionSummaries       map[string]string   `json:"section_summaries,omitempty"`
}

const metaFilename = ".skill-meta.json"

func WriteMeta(skillDir string, m SkillMeta) error {
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("skilllibrary: mkdir: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("skilllibrary: marshal meta: %w", err)
	}
	return os.WriteFile(filepath.Join(skillDir, metaFilename), b, 0o644)
}

// ReadMeta 读 sidecar；文件不存在时返回 fs.ErrNotExist。
func ReadMeta(skillDir string) (*SkillMeta, error) {
	p := filepath.Join(skillDir, metaFilename)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m SkillMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("skilllibrary: unmarshal meta %s: %w", p, err)
	}
	if m.Name == "" {
		return nil, errors.New("skilllibrary: meta missing name field")
	}
	return &m, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skilllibrary/... -run TestMeta -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/skilllibrary/meta.go internal/module/skilllibrary/meta_test.go
git commit -m "feat(skilllibrary): add sidecar .skill-meta.json schema and IO"
```

---

## Task 9: Library Scan

**Files:**
- Create: `internal/module/skilllibrary/scan.go`
- Test: `internal/module/skilllibrary/scan_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skilllibrary/scan_test.go
package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan_FindsValidSkillsOnly(t *testing.T) {
	root := t.TempDir()
	mkSkill(t, root, "alpha", true)
	mkSkill(t, root, "beta", false) // missing meta -> skipped
	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "loose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Meta.Name != "alpha" {
		t.Errorf("got[0].Meta.Name = %s, want alpha", got[0].Meta.Name)
	}
}

func mkSkill(t *testing.T, root, name string, withMeta bool) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withMeta {
		if err := WriteMeta(dir, SkillMeta{Name: name, Origin: OriginBuiltin, Version: "1"}); err != nil {
			t.Fatal(err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skilllibrary/... -run TestScan -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skilllibrary/scan.go
package skilllibrary

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillEntry 是一条已扫描的 library 条目。
type SkillEntry struct {
	Dir     string     // 该 skill 在 library 里的绝对目录
	SkillMD string     // SKILL.md 的字节内容（保持 string 易于直接 hash/parse）
	Meta    *SkillMeta // 同目录下的 .skill-meta.json（必须存在；否则 skill 被跳过）
}

// Scan 扫描 libraryDir 下所有形如 <dir>/{SKILL.md, .skill-meta.json} 的子目录。
// 缺 SKILL.md 或 sidecar 一律跳过。隐藏（以 . 开头）目录跳过。
func Scan(libraryDir string) ([]SkillEntry, error) {
	entries, err := os.ReadDir(libraryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("skilllibrary: scan: %w", err)
	}
	var out []SkillEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(libraryDir, e.Name())
		skillBytes, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}
		meta, err := ReadMeta(dir)
		if err != nil {
			continue // 缺 sidecar 视为非法，本期不自动修复
		}
		out = append(out, SkillEntry{Dir: dir, SkillMD: string(skillBytes), Meta: meta})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.Name < out[j].Meta.Name })
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skilllibrary/... -run TestScan -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/skilllibrary/scan.go internal/module/skilllibrary/scan_test.go
git commit -m "feat(skilllibrary): add Scan to enumerate library entries"
```

---

## Task 10: Library Store API (Install / Uninstall)

**Files:**
- Create: `internal/module/skilllibrary/store.go`
- Test: `internal/module/skilllibrary/store_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skilllibrary/store_test.go
package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_InstallAndUninstall(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)

	skillSrc := []byte("---\nname: x\ndescription: d\n---\n# x\n")
	meta := SkillMeta{Name: "x", Origin: OriginMarketplace, Version: "0.1.0"}

	if err := s.Install("x", skillSrc, meta); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "x", "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "x", ".skill-meta.json")); err != nil {
		t.Errorf("meta missing: %v", err)
	}

	if err := s.Uninstall("x"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "x")); !os.IsNotExist(err) {
		t.Errorf("dir not removed: %v", err)
	}
}

func TestStore_GetReturnsErrNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Get("missing"); !os.IsNotExist(err) {
		t.Errorf("Get(missing): want IsNotExist, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skilllibrary/... -run TestStore -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skilllibrary/store.go
package skilllibrary

import (
	"fmt"
	"os"
	"path/filepath"
)

// Store 是 library 物理目录的读写 API。
// 不做并发保护——调用方（reconcile / event handler）应序列化调用。
type Store struct {
	root string
}

func NewStore(root string) *Store { return &Store{root: root} }

// Install 写入 SKILL.md + sidecar；目录不存在自动创建；同名条目原子覆盖。
func (s *Store) Install(name string, skillMD []byte, meta SkillMeta) error {
	if name == "" {
		return fmt.Errorf("skilllibrary: install empty name")
	}
	dir := filepath.Join(s.root, name)
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return fmt.Errorf("skilllibrary: mkdir tmp: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "SKILL.md"), skillMD, 0o644); err != nil {
		return fmt.Errorf("skilllibrary: write SKILL.md: %w", err)
	}
	if err := WriteMeta(tmp, meta); err != nil {
		return fmt.Errorf("skilllibrary: write meta: %w", err)
	}
	_ = os.RemoveAll(dir)
	return os.Rename(tmp, dir)
}

// Uninstall 删除整个 skill 目录；不存在视为成功（idempotent）。
func (s *Store) Uninstall(name string) error {
	if name == "" {
		return fmt.Errorf("skilllibrary: uninstall empty name")
	}
	dir := filepath.Join(s.root, name)
	return os.RemoveAll(dir)
}

// Get 读单个 skill；返回 fs.ErrNotExist 表示 skill 不存在。
func (s *Store) Get(name string) (*SkillEntry, error) {
	dir := filepath.Join(s.root, name)
	skillBytes, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	meta, err := ReadMeta(dir)
	if err != nil {
		return nil, err
	}
	return &SkillEntry{Dir: dir, SkillMD: string(skillBytes), Meta: meta}, nil
}

// List 是 Scan 的便捷封装。
func (s *Store) List() ([]SkillEntry, error) { return Scan(s.root) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skilllibrary/... -run TestStore -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/skilllibrary/store.go internal/module/skilllibrary/store_test.go
git commit -m "feat(skilllibrary): add Store with Install/Uninstall/Get/List"
```

---

## Task 11: Builtin Seed (Embedded → Library)

**Files:**
- Create: `internal/module/skilllibrary/seed.go`
- Test: `internal/module/skilllibrary/seed_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skilllibrary/seed_test.go
package skilllibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

func TestSeed_FreshLibraryInstallsAllBuiltins(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)

	count, err := SeedBuiltins(s, "test-version-1")
	if err != nil {
		t.Fatalf("SeedBuiltins: %v", err)
	}
	names, _ := skillforge.ListEmbeddedSkillNames()
	if count != len(names) {
		t.Errorf("seeded %d, embedded = %d", count, len(names))
	}
	for _, name := range names {
		if _, err := s.Get(name); err != nil {
			t.Errorf("missing seeded skill %s: %v", name, err)
		}
	}
}

func TestSeed_PreservesUserModifiedNonBuiltin(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	// 预置一个用户安装版（origin = marketplace），同名也不应被 builtin seed 覆盖
	names, _ := skillforge.ListEmbeddedSkillNames()
	if len(names) == 0 {
		t.Skip("no embedded skills")
	}
	target := names[0]
	if err := s.Install(target, []byte("---\nname: "+target+"\ndescription: user version\n---\n"),
		SkillMeta{Name: target, Origin: OriginMarketplace, Version: "user-1"}); err != nil {
		t.Fatal(err)
	}

	if _, err := SeedBuiltins(s, "test-version-1"); err != nil {
		t.Fatalf("SeedBuiltins: %v", err)
	}

	got, err := s.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Origin != OriginMarketplace {
		t.Errorf("user version overwritten; origin = %s, want marketplace", got.Meta.Origin)
	}
}

func TestSeed_OverwritesOlderBuiltin(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	names, _ := skillforge.ListEmbeddedSkillNames()
	if len(names) == 0 {
		t.Skip("no embedded skills")
	}
	target := names[0]
	// 写入旧版（哈希假的）
	if err := s.Install(target, []byte("old"),
		SkillMeta{Name: target, Origin: OriginBuiltin, Version: "0", VersionHash: "stale"}); err != nil {
		t.Fatal(err)
	}

	if _, err := SeedBuiltins(s, "test-version-2"); err != nil {
		t.Fatalf("SeedBuiltins: %v", err)
	}
	got, err := s.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.VersionHash == "stale" {
		t.Error("stale builtin not overwritten")
	}

	// 验证新版 hash 与 embedded 一致
	body, _ := skillforge.ReadEmbeddedSkill(target)
	want := sha256OfBytes(body)
	if got.Meta.VersionHash != want {
		t.Errorf("VersionHash = %s, want %s", got.Meta.VersionHash, want)
	}
	_ = filepath.Join // keep import
}

func sha256OfBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skilllibrary/... -run TestSeed -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skilllibrary/seed.go
package skilllibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

// SeedBuiltins 把 //go:embed 出的内置 skill 写入 library。
// 规则（spec §5.2）：
//   - 库里没有 → 全新安装为 origin=builtin
//   - 库里有但 origin=builtin 且 version_hash 不一致 → 覆盖
//   - 库里有但 origin != builtin → 保留（用户已自定义）
//
// 返回实际写入的 skill 数。
func SeedBuiltins(store *Store, harnessVersion string) (int, error) {
	names, err := skillforge.ListEmbeddedSkillNames()
	if err != nil {
		return 0, fmt.Errorf("skilllibrary: list embedded: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	written := 0
	for _, name := range names {
		body, err := skillforge.ReadEmbeddedSkill(name)
		if err != nil {
			return written, fmt.Errorf("skilllibrary: read embedded %s: %w", name, err)
		}
		hash := sha256Hex(body)
		existing, err := store.Get(name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return written, fmt.Errorf("skilllibrary: get %s: %w", name, err)
		}
		if existing != nil {
			if existing.Meta.Origin != OriginBuiltin {
				continue // user-modified, skip
			}
			if existing.Meta.VersionHash == hash {
				continue // already up to date
			}
		}
		meta := SkillMeta{
			Name:        name,
			Origin:      OriginBuiltin,
			Version:     harnessVersion,
			VersionHash: hash,
			InstalledAt: now,
		}
		if err := store.Install(name, body, meta); err != nil {
			return written, fmt.Errorf("skilllibrary: seed install %s: %w", name, err)
		}
		written++
	}
	return written, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skilllibrary/... -run TestSeed -v`
Expected: PASS（3 个用例）

- [ ] **Step 5: Commit**

```bash
git add internal/module/skilllibrary/seed.go internal/module/skilllibrary/seed_test.go
git commit -m "feat(skilllibrary): add SeedBuiltins for embedded → library"
```

---

## Task 12: Library → Cache Reconcile

**Files:**
- Create: `internal/module/skilllibrary/reconcile.go`
- Test: `internal/module/skilllibrary/reconcile_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skilllibrary/reconcile_test.go
package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

func TestReconcile_BuildsCacheFromLibrary(t *testing.T) {
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	s := NewStore(libRoot)

	if _, err := SeedBuiltins(s, "test-1"); err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(s, cacheRoot)
	report, err := r.ReconcileAll()
	if err != nil {
		t.Fatalf("ReconcileAll: %v", err)
	}
	names, _ := skillforge.ListEmbeddedSkillNames()
	if report.Built != len(names) {
		t.Errorf("Built = %d, want %d", report.Built, len(names))
	}
	// 抽查
	for _, n := range names[:1] {
		if _, err := os.Stat(filepath.Join(cacheRoot, n, "SKILL.md")); err != nil {
			t.Errorf("missing cache for %s: %v", n, err)
		}
	}
}

func TestReconcile_RemovesOrphanCacheEntries(t *testing.T) {
	libRoot := t.TempDir()
	cacheRoot := t.TempDir()
	// 预先在 cache 里塞一个 library 没有的 skill
	orphan := filepath.Join(cacheRoot, "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewReconciler(NewStore(libRoot), cacheRoot)
	report, err := r.ReconcileAll()
	if err != nil {
		t.Fatal(err)
	}
	if report.Removed != 1 {
		t.Errorf("Removed = %d, want 1", report.Removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan not removed: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skilllibrary/... -run TestReconcile -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skilllibrary/reconcile.go
package skilllibrary

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

// Reconciler 把 library 的当前状态投影到 cache。
// 增量调用 ReconcileOne(name)；全量调用 ReconcileAll。
type Reconciler struct {
	store    *Store
	cacheDir string
}

func NewReconciler(s *Store, cacheDir string) *Reconciler {
	return &Reconciler{store: s, cacheDir: cacheDir}
}

type ReconcileReport struct {
	Built   int // 重建/新建的 skill 数
	Skipped int // hash 一致跳过的数量
	Removed int // 从 cache 删除的孤儿数
	Errors  []error
}

func (r *Reconciler) ReconcileOne(name string) error {
	entry, err := r.store.Get(name)
	if err != nil {
		if os.IsNotExist(err) {
			return os.RemoveAll(filepath.Join(r.cacheDir, name))
		}
		return err
	}
	if entry.Meta.Disabled {
		return os.RemoveAll(filepath.Join(r.cacheDir, name))
	}
	override := entry.Meta.SectionSummaries
	return skillforge.Forge(filepath.Dir(entry.Dir), r.cacheDir, name, override)
}

func (r *Reconciler) ReconcileAll() (*ReconcileReport, error) {
	if err := os.MkdirAll(r.cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("skilllibrary: mkdir cache: %w", err)
	}
	report := &ReconcileReport{}
	libEntries, err := r.store.List()
	if err != nil {
		return nil, err
	}
	libNames := map[string]struct{}{}
	for _, e := range libEntries {
		libNames[e.Meta.Name] = struct{}{}
		if e.Meta.Disabled {
			continue
		}
		if err := skillforge.Forge(filepath.Dir(e.Dir), r.cacheDir, e.Meta.Name, e.Meta.SectionSummaries); err != nil {
			report.Errors = append(report.Errors, err)
			continue
		}
		report.Built++
	}
	// 清孤儿
	cacheEntries, err := os.ReadDir(r.cacheDir)
	if err != nil && !os.IsNotExist(err) {
		return report, err
	}
	for _, e := range cacheEntries {
		if !e.IsDir() {
			continue
		}
		if _, ok := libNames[e.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.cacheDir, e.Name())); err != nil {
			report.Errors = append(report.Errors, err)
			continue
		}
		report.Removed++
	}
	return report, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skilllibrary/... -run TestReconcile -v`
Expected: PASS（2 个用例）

- [ ] **Step 5: Commit**

```bash
git add internal/module/skilllibrary/reconcile.go internal/module/skilllibrary/reconcile_test.go
git commit -m "feat(skilllibrary): add Reconciler for library → cache projection"
```

---

## Task 13: Dev Override fsnotify Watcher

**Files:**
- Create: `internal/module/skilllibrary/watcher.go`
- Test: `internal/module/skilllibrary/watcher_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skilllibrary/watcher_test.go
package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_FiresOnSkillFileChange(t *testing.T) {
	srcRoot := t.TempDir()
	mkDevSkill(t, srcRoot, "demo", "v1")

	events := make(chan string, 4)
	w, err := NewDevWatcher(srcRoot, func(name string) {
		events <- name
	})
	if err != nil {
		t.Fatalf("NewDevWatcher: %v", err)
	}
	defer w.Close()

	// 触发一次变更
	mkDevSkill(t, srcRoot, "demo", "v2")

	select {
	case got := <-events:
		if got != "demo" {
			t.Errorf("event name = %s, want demo", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after 2s")
	}
}

func mkDevSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: "+body+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skilllibrary/... -run TestWatcher -v`
Expected: FAIL（undefined NewDevWatcher）

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skilllibrary/watcher.go
package skilllibrary

import (
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// DevWatcher 监听 dev override 源目录变更（spec §5.4）。
// onChange 接收变更的 skill 名（不含路径）；运行在 watcher 内部 goroutine，
// 调用方应快速 dispatch 到 reconcile。
type DevWatcher struct {
	w  *fsnotify.Watcher
	ch chan struct{}
}

func NewDevWatcher(srcRoot string, onChange func(name string)) (*DevWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(srcRoot); err != nil {
		_ = fw.Close()
		return nil, err
	}
	// 把已有子目录也加上
	matches, _ := filepath.Glob(filepath.Join(srcRoot, "*"))
	for _, p := range matches {
		_ = fw.Add(p)
	}

	dw := &DevWatcher{w: fw, ch: make(chan struct{})}
	go dw.loop(srcRoot, onChange)
	return dw, nil
}

func (d *DevWatcher) loop(srcRoot string, onChange func(name string)) {
	for {
		select {
		case <-d.ch:
			return
		case ev, ok := <-d.w.Events:
			if !ok {
				return
			}
			if name := skillNameFromEvent(srcRoot, ev.Name); name != "" {
				onChange(name)
			}
		case <-d.w.Errors:
			// 错误不致命；继续监听
		}
	}
}

// skillNameFromEvent 把 fsnotify 事件路径映射回 skill 名（srcRoot 下的一级目录名）。
func skillNameFromEvent(srcRoot, evPath string) string {
	rel, err := filepath.Rel(srcRoot, evPath)
	if err != nil {
		return ""
	}
	rel = strings.TrimPrefix(rel, string(filepath.Separator))
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	if len(parts) == 0 || parts[0] == "" || parts[0] == "." {
		return ""
	}
	return parts[0]
}

func (d *DevWatcher) Close() error {
	close(d.ch)
	return d.w.Close()
}
```

如果 `go.mod` 还没有 `github.com/fsnotify/fsnotify`：
```bash
go get github.com/fsnotify/fsnotify
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/skilllibrary/... -run TestWatcher -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/skilllibrary/watcher.go internal/module/skilllibrary/watcher_test.go go.mod go.sum
git commit -m "feat(skilllibrary): add fsnotify dev-override watcher"
```

---

## Task 14: Fx Module Wire-up (no callers)

**Files:**
- Create: `internal/module/skillforge/module.go`
- Create: `internal/module/skilllibrary/module.go`
- Modify: `cmd/agent-terminal/wire.go`（或项目实际的 Fx 根装配文件）—— 仅注册 module，不修改任何调用方

- [ ] **Step 1: Write the failing test**

```go
// internal/module/skilllibrary/module_test.go
package skilllibrary

import (
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

func TestModuleWiresUpReconciler(t *testing.T) {
	var r *Reconciler
	app := fxtest.New(t,
		skillforge.Module,
		Module,
		fx.Provide(func() Config { return Config{LibraryDir: t.TempDir(), CacheDir: t.TempDir()} }),
		fx.Populate(&r),
	)
	defer app.RequireStart().RequireStop()
	if r == nil {
		t.Fatal("Reconciler not provided")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/skilllibrary/... -run TestModuleWiresUp -v`
Expected: FAIL（undefined `Module`、`Config`）

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/skillforge/module.go
package skillforge

import "go.uber.org/fx"

// Module 当前不暴露 service 单例（forge 是纯函数式 API）；
// 这里占位以保持 Fx 树的一致性，并为未来 wrapper 留口。
var Module = fx.Module("skillforge")
```

```go
// internal/module/skilllibrary/module.go
package skilllibrary

import "go.uber.org/fx"

// Config 是 skilllibrary 的启动配置。注入方负责填充。
type Config struct {
	LibraryDir     string // ~/.super-dolphin/skills-library/
	CacheDir       string // ~/.super-dolphin/skills-cache/
	HarnessVersion string // 用于 SeedBuiltins
}

var Module = fx.Module("skilllibrary",
	fx.Provide(func(c Config) *Store { return NewStore(c.LibraryDir) }),
	fx.Provide(func(s *Store, c Config) *Reconciler { return NewReconciler(s, c.CacheDir) }),
)
```

将两个 module 注册到 Fx 根装配（先用 Explore 找实际位置）：

```bash
# 找到 fx.New 入口
grep -rln "fx.New(" cmd/ internal/app/ 2>/dev/null
```

找到后把 `skillforge.Module` 和 `skilllibrary.Module` 加入 fx.New 的参数列表，**并提供** `skilllibrary.Config`（暂用默认值，由调用方覆盖）：

```go
// cmd/agent-terminal/<wire-file>.go (示意；具体文件由 grep 结果决定)
import (
	"path/filepath"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// ... 在 fx.New(...) 调用里加入：
//     skillforge.Module,
//     skilllibrary.Module,
//     fx.Provide(provideSkillLibraryConfig),

func provideSkillLibraryConfig(home string, version string) skilllibrary.Config {
	return skilllibrary.Config{
		LibraryDir:     filepath.Join(home, ".super-dolphin", "skills-library"),
		CacheDir:       filepath.Join(home, ".super-dolphin", "skills-cache"),
		HarnessVersion: version,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```
go test ./internal/module/skilllibrary/... -run TestModuleWiresUp -v
go build ./...   # 确保 Fx 根装配编译通过
```
Expected: PASS + 全项目可编译

- [ ] **Step 5: Commit**

```bash
git add internal/module/skillforge/module.go \
        internal/module/skilllibrary/module.go \
        internal/module/skilllibrary/module_test.go \
        cmd/agent-terminal/<wire-file>.go
git commit -m "feat(skill): wire skillforge + skilllibrary into fx graph"
```

---

## Task 15: 运行全 Phase 1 测试 + 整体冒烟

**Files:** —— 仅运行测试，不修改代码

- [ ] **Step 1: 跑两个新包的全部单元测试**

Run: `go test ./internal/module/skillforge/... ./internal/module/skilllibrary/... -v`
Expected: 所有 Task 1-14 的测试 PASS

- [ ] **Step 2: 跑整个项目测试**

Run: `go test ./... -short`
Expected: 全部 PASS（Phase 1 没动旧代码，旧测试应不受影响）

- [ ] **Step 3: 跑 lint**

Run: `golangci-lint run ./internal/module/skillforge/... ./internal/module/skilllibrary/...`
Expected: 0 issues

- [ ] **Step 4: 验证手动冒烟**

```bash
# 写一个一次性的 CLI 触发 SeedBuiltins + ReconcileAll，验证产物
mkdir -p /tmp/skill-smoke/lib /tmp/skill-smoke/cache
cat > /tmp/skill-smoke/main.go <<'EOF'
package main
import (
  "fmt"
  "github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)
func main() {
  s := skilllibrary.NewStore("/tmp/skill-smoke/lib")
  n, _ := skilllibrary.SeedBuiltins(s, "smoke-1")
  fmt.Println("seeded:", n)
  r := skilllibrary.NewReconciler(s, "/tmp/skill-smoke/cache")
  rep, _ := r.ReconcileAll()
  fmt.Printf("built=%d removed=%d errors=%d\n", rep.Built, rep.Removed, len(rep.Errors))
}
EOF
go run /tmp/skill-smoke/main.go
ls /tmp/skill-smoke/cache/
ls /tmp/skill-smoke/cache/测试驱动开发/
ls /tmp/skill-smoke/cache/测试驱动开发/references/
```
Expected：
- `seeded: 14`（或匹配 embedded 数量）
- `built=14 removed=0 errors=0`
- `cache/测试驱动开发/SKILL.md` 是瘦身版（含 `## 节索引`）
- `cache/测试驱动开发/references/01-*.md` 等若干文件存在

- [ ] **Step 5: 不需要单独 commit；冒烟验证完毕直接进 Phase 2**

如冒烟产物有问题（缺文件、内容异常），回到对应 Task 修测试 + 实现，不要继续。

---

## Phase 1 自审

按 编写计划 技能 §自审：

**1. 规格覆盖：** 对照 spec §3 / §4 / §5 / §11：
- §3.1 Library 结构 → Task 8/9/10
- §3.2 Cache 结构 → Task 4/5
- §3.3 触发器 → Task 12（启动对账）+ Task 13（dev fsnotify）；install/uninstall 事件类型 schema 在本期未拆 task，因为 Store 的 Install/Uninstall 已经是事件等价物，event-bus 包装放到 P5 与 native filter 一并做（届时 native filter 监听 install/uninstall 重写 settings）。
- §4.1 拆段 → Task 1
- §4.2 文件命名 → Task 3
- §4.3 摘要生成 → Task 2
- §4.4 原子性 → Task 5
- §4.5 对账 → Task 12
- §5.1 Origin 类型 → Task 8
- §5.2 Builtin Seed → Task 11
- §5.3 install 流程 → Task 10（API 层；UI 层延后）
- §5.4 dev override → Task 13

**未覆盖项**（明确延后，不是漏）：
- install/uninstall 事件总线 → 推到 P5 / P6 实施时叠加
- Windows symlink fallback → P2 才需要（Phase 1 不接 workspace）
- FBSD 打点 hooks → P6
- Fx Config 由谁注入实际 home / version → P2 接入 harness 启动序列时具体化

**2. 占位符扫描：** 无 TODO/TBD/FIXME；任务中的所有代码都是完整可编译的实现。

**3. 类型一致性：** 跨任务引用：
- `ParsedSkill` (Task 1) → 被 Task 4 `Render(ps *ParsedSkill, ...)` 使用 ✓
- `RenderResult` (Task 4) → 被 Task 5 `AtomicWriteSkill(..., res *RenderResult)` 使用 ✓
- `SkillEntry` (Task 9) → 被 Task 10/11/12 引用 ✓
- `SkillMeta` (Task 8) → 在 Task 9/10/11 一致 ✓
- `Store` (Task 10) → Task 11/12 接受 `*Store` ✓
- `skillforge.Forge` (Task 6) → Task 12 调用签名 `skillforge.Forge(libDir, cacheDir, name, summaryOverride)` 一致 ✓

修复内联：暂无问题。

---

## 执行交接

计划已完成并保存到 `docs/superpowers/plans/2026-04-29-skill-refactor-p1-foundation.md`。有两个执行选项：

1. **子代理驱动（推荐）** —— 我为每个任务派发新的子代理，在任务之间审查，迭代更快
2. **当前会话内执行** —— 使用 执行计划 在当前会话执行任务，按批次执行并设置检查点

你想用哪种方式？
