# Skill Refactor — Phase 3: Codex-Side Cutover Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Codex 端从"per-turn 全文内联 skill body"切换到"base instructions L1-C 元数据 + DynamicTools `skill_read_section` 按需读节"。删除 ~250 行 buildSkillPromptInput 系列 + `SkillRef.Mode` 三态消费 + `SKILL_WRITER_FORMAT` env + `skill_expand_body` / `skill_read_resource` 在 Codex 的注册。新增 `skill_read_section` 工具 + `buildSkillManifest` 渲染器。

**Architecture:** Codex session 启动时 → `startAssemblyInstructions` 读 skilllibrary.Store 列出 enabled skill → 渲染 L1-C 文本（name + description + 节索引 + 摘要）→ 拼到 baseInstructions。Codex agent 触发某 skill 时 → 调 `skill_read_section(name, anchor)` DynamicTool → toolbridge 实现读 `<cacheDir>/<name>/references/<NN-anchor>.md` → 返回单节正文。

**Tech Stack:** Go 1.22+、`go.uber.org/fx`、`internal/module/skilllibrary` (P1)、`internal/platform/toolbridge`、`internal/provider/codexapp`、现有 `dto.SkillRef` schema。

**前置阅读：**
- `docs/superpowers/specs/2026-04-29-skill-refactor-design.md` §7、§9.1（FBSD tier 暂留 placeholder，本期不实施）
- `internal/module/skilllibrary/`（P1 产物：`Store`、`SkillEntry`）
- `internal/provider/codexapp/module.go:288-360`（待删 buildSkillPromptInput 等）
- `internal/provider/codexapp/session_turn.go:35-120`（per-turn 注入路径，待改）
- `internal/provider/codexapp/driver.go:259-273`（startAssemblyInstructions，注入点）
- `internal/platform/toolbridge/host_tools.go:41-146`（DynamicTools 注册）
- `internal/platform/toolbridge/handler_host_tools.go:71-129`（ListToolsForCodex）

---

## File Structure

**新增**：
```
internal/module/skilllibrary/section.go          (Task 1: ReadSection method)
internal/module/skilllibrary/section_test.go     (Task 1)
internal/provider/codexapp/skill_manifest.go     (Task 4: buildSkillManifest)
internal/provider/codexapp/skill_manifest_test.go (Task 4)
internal/platform/toolbridge/skill_read_section.go     (Task 2: tool impl)
internal/platform/toolbridge/skill_read_section_test.go (Task 2)
```

**修改**：
```
internal/platform/toolbridge/module.go           (Task 1: 注入 skilllibrary.Config / Store)
internal/platform/toolbridge/host_tools.go       (Task 3: 删 skill_expand_body / skill_read_resource 注册；加 skill_read_section)
internal/provider/codexapp/driver.go             (Task 5: startAssemblyInstructions 调 buildSkillManifest)
internal/provider/codexapp/module.go             (Task 6+7: 删 buildSkillPromptInput, renderSkillBlock, skillWriterFormat)
internal/provider/codexapp/session_turn.go       (Task 6: 移除 per-turn skill block 注入)
internal/provider/codexapp/skill_mode_override.go (Task 6: 删整文件)
internal/provider/codexapp/skill_inject.go        (Task 8: 删整文件，P2 残留)
```

**删除**：
```
internal/provider/codexapp/skill_mode_override.go
internal/provider/codexapp/skill_mode_override_test.go
internal/provider/codexapp/skill_inject.go
internal/provider/codexapp/skill_injection_test.go
```

---

## Task 1: skilllibrary.Store.ReadSection

**Files:**
- Create: `internal/module/skilllibrary/section.go`
- Test: `internal/module/skilllibrary/section_test.go`

skilllibrary 添加 `ReadSection(name, anchor string) ([]byte, error)` 方法，从 `<cacheDir>/<name>/references/` 找匹配 anchor 的文件并读取。

### Step 1: Failing test

```go
// section_test.go
package skilllibrary

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSection_FindsByAnchor(t *testing.T) {
	cacheDir := t.TempDir()
	skillDir := filepath.Join(cacheDir, "tdd")
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(refDir, "01-红绿重构.md"), []byte("body content"), 0o644); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(refDir, "02-反模式.md"), []byte("anti pattern"), 0o644); err != nil { t.Fatal(err) }

	got, err := ReadSection(cacheDir, "tdd", "红绿重构")
	if err != nil { t.Fatalf("ReadSection: %v", err) }
	if string(got) != "body content" { t.Errorf("got %q, want body content", string(got)) }

	got, err = ReadSection(cacheDir, "tdd", "反模式")
	if err != nil { t.Fatalf("ReadSection: %v", err) }
	if string(got) != "anti pattern" { t.Errorf("got %q, want anti pattern", string(got)) }
}

func TestReadSection_UnknownSkillReturnsErrNotExist(t *testing.T) {
	cacheDir := t.TempDir()
	_, err := ReadSection(cacheDir, "nope", "any")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want fs.ErrNotExist, got %v", err)
	}
}

func TestReadSection_UnknownAnchorReturnsErrNotExist(t *testing.T) {
	cacheDir := t.TempDir()
	skillDir := filepath.Join(cacheDir, "x")
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(refDir, "01-something.md"), []byte("x"), 0o644); err != nil { t.Fatal(err) }

	_, err := ReadSection(cacheDir, "x", "missing-anchor")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want fs.ErrNotExist, got %v", err)
	}
}

func TestReadSection_EmptyArgsErrors(t *testing.T) {
	if _, err := ReadSection("", "x", "y"); err == nil { t.Error("empty cacheDir should error") }
	if _, err := ReadSection("/tmp", "", "y"); err == nil { t.Error("empty name should error") }
	if _, err := ReadSection("/tmp", "x", ""); err == nil { t.Error("empty anchor should error") }
}
```

### Step 2: Run (fail)

```
cd /private/tmp/super-dolphin-skill-refactor-p3
go test ./internal/module/skilllibrary/... -run TestReadSection -v
```

### Step 3: Implementation

```go
// section.go
package skilllibrary

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadSection 读取 cacheDir/<name>/references/<NN-anchor>.md。
// anchor 是 H2 标题原文（不含数字前缀），通过遍历目录后缀匹配找到对应文件。
//
// 错误规约：
//   - cacheDir/name/anchor 任意为空 → 错误
//   - skill 目录不存在或 references 目录不存在 → fs.ErrNotExist
//   - anchor 在该 skill 下找不到对应文件 → fs.ErrNotExist
func ReadSection(cacheDir, name, anchor string) ([]byte, error) {
	if cacheDir == "" || name == "" || anchor == "" {
		return nil, errors.New("skilllibrary: ReadSection empty cacheDir/name/anchor")
	}
	refDir := filepath.Join(cacheDir, name, "references")
	entries, err := os.ReadDir(refDir)
	if err != nil { return nil, err }

	// 文件名形如 "<NN>-<title>.md"；title 部分应等于 anchor（已过 SectionFilename 清洗）
	suffix := "-" + anchor + ".md"
	for _, e := range entries {
		if e.IsDir() { continue }
		if strings.HasSuffix(e.Name(), suffix) {
			return os.ReadFile(filepath.Join(refDir, e.Name()))
		}
	}
	return nil, fmt.Errorf("skilllibrary: no section %q in skill %q: %w", anchor, name, fs.ErrNotExist)
}
```

### Step 4: Run (pass)

```
go test ./internal/module/skilllibrary/... -v -count=1
```

### Step 5: Commit

```
git add internal/module/skilllibrary/section.go internal/module/skilllibrary/section_test.go
git commit -m "feat(skilllibrary): add ReadSection lookup by anchor suffix"
```

---

## Task 2: toolbridge `skill_read_section` DynamicTool

**Files:**
- Create: `internal/platform/toolbridge/skill_read_section.go`
- Test: `internal/platform/toolbridge/skill_read_section_test.go`

新增 DynamicTool 实现，调 `skilllibrary.ReadSection` 并返回结果给 codex CLI。

### Step 1: Failing test

```go
// skill_read_section_test.go
package toolbridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillReadSectionTool_ReturnsSectionBody(t *testing.T) {
	cacheDir := t.TempDir()
	refDir := filepath.Join(cacheDir, "tdd", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(refDir, "01-红绿重构.md"), []byte("RGR cycle"), 0o644); err != nil { t.Fatal(err) }

	tool := NewSkillReadSectionTool(cacheDir)
	args, _ := json.Marshal(map[string]string{"name": "tdd", "anchor": "红绿重构"})
	out, err := tool.Call(context.Background(), args)
	if err != nil { t.Fatalf("Call: %v", err) }
	if !strings.Contains(string(out), "RGR cycle") { t.Errorf("output missing body: %s", string(out)) }
}

func TestSkillReadSectionTool_MissingSection(t *testing.T) {
	tool := NewSkillReadSectionTool(t.TempDir())
	args, _ := json.Marshal(map[string]string{"name": "x", "anchor": "y"})
	_, err := tool.Call(context.Background(), args)
	if err == nil { t.Error("expected error on missing section") }
}

func TestSkillReadSectionTool_TruncatesByMaxBytes(t *testing.T) {
	cacheDir := t.TempDir()
	refDir := filepath.Join(cacheDir, "z", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil { t.Fatal(err) }
	body := strings.Repeat("a", 1000)
	if err := os.WriteFile(filepath.Join(refDir, "01-sec.md"), []byte(body), 0o644); err != nil { t.Fatal(err) }

	tool := NewSkillReadSectionTool(cacheDir)
	args, _ := json.Marshal(map[string]any{"name": "z", "anchor": "sec", "max_bytes": 100})
	out, err := tool.Call(context.Background(), args)
	if err != nil { t.Fatal(err) }
	if len(out) > 110 { t.Errorf("output not truncated: len=%d", len(out)) }
}
```

### Step 2: Run (fail)

```
go test ./internal/platform/toolbridge/... -run TestSkillReadSectionTool -v
```

### Step 3: Implementation

```go
// skill_read_section.go
package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// SkillReadSectionTool 是 codex DynamicTool 端的 skill_read_section 实现。
// 通过 skilllibrary.ReadSection 从 <cacheDir>/<name>/references/<NN-anchor>.md 读单节。
type SkillReadSectionTool struct {
	cacheDir string
}

func NewSkillReadSectionTool(cacheDir string) *SkillReadSectionTool {
	return &SkillReadSectionTool{cacheDir: cacheDir}
}

type skillReadSectionArgs struct {
	Name     string `json:"name"`
	Anchor   string `json:"anchor"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// Call 实现 DynamicTool 接口；返回该节正文（可选 max_bytes 截断）。
func (t *SkillReadSectionTool) Call(ctx context.Context, raw json.RawMessage) ([]byte, error) {
	var a skillReadSectionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("skill_read_section: parse args: %w", err)
	}
	body, err := skilllibrary.ReadSection(t.cacheDir, a.Name, a.Anchor)
	if err != nil {
		return nil, fmt.Errorf("skill_read_section: %w", err)
	}
	if a.MaxBytes > 0 && len(body) > a.MaxBytes {
		body = body[:a.MaxBytes]
	}
	return body, nil
}
```

注：上面的 `Call(ctx, raw) ([]byte, error)` 接口签名是占位——实际项目里 toolbridge 可能用不同的 DynamicTool interface。按现有 `SkillHostTools` 的实现模式调整签名。具体见 `host_tools.go:41-146`。

### Step 4: Run (pass)

```
go test ./internal/platform/toolbridge/... -v -count=1
```

### Step 5: Commit

```
git add internal/platform/toolbridge/skill_read_section.go internal/platform/toolbridge/skill_read_section_test.go
git commit -m "feat(toolbridge): add skill_read_section DynamicTool"
```

---

## Task 3: 替换 Codex 端 DynamicTools 注册

**Files:**
- Modify: `internal/platform/toolbridge/host_tools.go`
- Modify: `internal/platform/toolbridge/module.go`（注入 skilllibrary.Config）

从 Codex 看到的工具列表里：移除 `skill_expand_body` / `skill_read_resource`，新增 `skill_read_section`。注意：底层实现（`internal/module/skill/skills_expand.go`）保留，由 P4 删除；本任务只改注册。

### Step 1: Failing test

```go
// host_tools_test.go (扩展或新建)
func TestListHostTools_HasSkillReadSection(t *testing.T) {
	tools := ListHostTools(/* 注入参数 */)
	names := toolNames(tools)
	if !contains(names, "skill_read_section") {
		t.Error("expected skill_read_section in host tools")
	}
	if contains(names, "skill_expand_body") {
		t.Error("skill_expand_body should not be in host tools after P3")
	}
	if contains(names, "skill_read_resource") {
		t.Error("skill_read_resource should not be in host tools after P3")
	}
}
```

### Step 2: Run (fail)

```
go test ./internal/platform/toolbridge/... -run TestListHostTools_HasSkillReadSection -v
```

### Step 3: Implementation

修改 `host_tools.go: ListHostTools`：
- 删除 `skill_expand_body` 和 `skill_read_resource` 工具构造
- 新增 `skill_read_section` 工具构造（使用 Task 2 的 `SkillReadSectionTool`）

修改 `internal/platform/toolbridge/module.go`：
- 在 fx Module 内通过 `fx.In` 注入 `skilllibrary.Config`（optional）
- 把 `Config.CacheDir` 传给 `NewSkillReadSectionTool`

### Step 4: Run (pass)

```
go test ./internal/platform/toolbridge/... -v -count=1
```

### Step 5: Commit

```
git add internal/platform/toolbridge/host_tools.go internal/platform/toolbridge/module.go internal/platform/toolbridge/host_tools_test.go
git commit -m "refactor(toolbridge): swap skill_expand_body/skill_read_resource for skill_read_section"
```

---

## Task 4: codexapp `buildSkillManifest` L1-C 渲染器

**Files:**
- Create: `internal/provider/codexapp/skill_manifest.go`
- Create: `internal/provider/codexapp/skill_manifest_test.go`

新渲染器：输入 skill 列表 + 摘要数据 + budget；输出文本块（L1-C 优先，超 budget 截断）。

### Step 1: Failing test

```go
// skill_manifest_test.go
package codexapp

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

func TestBuildSkillManifest_BasicL1C(t *testing.T) {
	// 构造 fake library entries
	entries := []skilllibrary.SkillEntry{
		{
			Meta: &skilllibrary.SkillMeta{
				Name: "测试驱动开发",
				SectionSummaries: map[string]string{
					"红绿重构": "三步循环",
					"反模式":   "常见信号",
				},
			},
			SkillMD: "---\nname: 测试驱动开发\ndescription: 实现功能前使用\n---\n",
		},
	}
	out := buildSkillManifest(entries, 8192)
	if !strings.Contains(out, "测试驱动开发") { t.Error("missing skill name") }
	if !strings.Contains(out, "实现功能前使用") { t.Error("missing description") }
	if !strings.Contains(out, "skill_read_section") { t.Error("missing tool hint") }
}

func TestBuildSkillManifest_EmptyEntries(t *testing.T) {
	out := buildSkillManifest(nil, 8192)
	if out != "" { t.Errorf("empty entries should produce empty output, got %q", out) }
}

func TestBuildSkillManifest_BudgetTruncation(t *testing.T) {
	// 100 个 skill，每个名字 + description ~100 chars，超 budget
	var entries []skilllibrary.SkillEntry
	for i := 0; i < 100; i++ {
		entries = append(entries, skilllibrary.SkillEntry{
			Meta: &skilllibrary.SkillMeta{Name: fmt.Sprintf("skill%03d", i)},
			SkillMD: fmt.Sprintf("---\nname: skill%03d\ndescription: %s\n---\n", i, strings.Repeat("x", 80)),
		})
	}
	out := buildSkillManifest(entries, 1024)
	if len(out) > 1500 { t.Errorf("output exceeds budget+headroom: %d", len(out)) }
}
```

### Step 2: Run (fail)

```
go test ./internal/provider/codexapp/... -run TestBuildSkillManifest -v
```

### Step 3: Implementation

```go
// skill_manifest.go
package codexapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// buildSkillManifest 渲染 Codex base instructions 中的 skill 列表（L1-C 形态）：
// 每个 skill 输出 name + description + 节索引（标题 + 摘要）。
// 总长度超过 budgetChars 时截断（按 skill 边界，保证不撕裂单条）。
//
// FBSD 频次降级（spec §9）暂未实现；本期所有 skill 一律 L1-C；超 budget 简单截尾。
func buildSkillManifest(entries []skilllibrary.SkillEntry, budgetChars int) string {
	if len(entries) == 0 {
		return ""
	}
	// 按名字排序保证输出稳定
	sort.Slice(entries, func(i, j int) bool { return entries[i].Meta.Name < entries[j].Meta.Name })

	var b strings.Builder
	b.WriteString("## 可用 skills（按需读，勿全文加载）\n\n")
	b.WriteString("调用 skill_read_section(name, anchor) 读某节正文。\n\n")

	for _, e := range entries {
		block := renderL1CBlock(e)
		if b.Len()+len(block) > budgetChars {
			b.WriteString("\n（更多 skill 因 budget 截断省略）\n")
			break
		}
		b.WriteString(block)
	}
	return b.String()
}

func renderL1CBlock(e skilllibrary.SkillEntry) string {
	desc := extractDescriptionFromSkillMD(e.SkillMD)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- %s — %s\n", e.Meta.Name, desc))
	if len(e.Meta.SectionSummaries) > 0 {
		b.WriteString("  节索引：\n")
		anchors := sortedKeys(e.Meta.SectionSummaries)
		for _, a := range anchors {
			b.WriteString(fmt.Sprintf("    - %s — %s\n", a, e.Meta.SectionSummaries[a]))
		}
	}
	return b.String()
}

// extractDescriptionFromSkillMD 从瘦身 SKILL.md 的 frontmatter 解析 description。
// 实现简化：前 10 行内找 "description: <value>"。
func extractDescriptionFromSkillMD(src string) string {
	for i, ln := range strings.Split(src, "\n") {
		if i > 10 { break }
		if strings.HasPrefix(ln, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "description:"))
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m { keys = append(keys, k) }
	sort.Strings(keys)
	return keys
}
```

### Step 4: Run (pass)

```
go test ./internal/provider/codexapp/... -run TestBuildSkillManifest -v
```

### Step 5: Commit

```
git add internal/provider/codexapp/skill_manifest.go internal/provider/codexapp/skill_manifest_test.go
git commit -m "feat(codexapp): add buildSkillManifest L1-C renderer"
```

---

## Task 5: 把 buildSkillManifest 接入 startAssemblyInstructions

**Files:**
- Modify: `internal/provider/codexapp/driver.go`
- Modify: `internal/provider/codexapp/module.go`（注入 skilllibrary.Store）

让 `startAssemblyInstructions` 在拼装 baseInstructions 时调 `buildSkillManifest`，把结果 prepend 到 baseInstructions。

### Step 1: Failing test

```go
// driver_skill_manifest_test.go
func TestStartAssembly_PrependsSkillManifest(t *testing.T) {
	// 构造一个 driver with a populated store (use t.TempDir for libraryDir + cacheDir)
	// Call startAssemblyInstructions(req)
	// Verify returned baseInstructions contains "## 可用 skills"
}
```

### Step 2: Implementation

```go
// 在 driver 结构体加 store *skilllibrary.Store 字段
// startAssemblyInstructions 中：
func (d *driver) startAssemblyInstructions(req dto.StartSessionRequest) (string, string) {
	base, dev := /* existing logic */
	if d.skillStore != nil {
		entries, _ := d.skillStore.List()
		manifest := buildSkillManifest(entries, defaultManifestBudget)
		if manifest != "" {
			base = manifest + "\n\n" + base
		}
	}
	return base, dev
}

const defaultManifestBudget = 8192
```

`module.go` 的 driver provider 接受 `skilllibrary.Store`（optional fx tag）。

### Step 3-5: 标准 TDD + commit

```
git commit -m "feat(codexapp): inject skill manifest into base instructions"
```

---

## Task 6: 删除 buildSkillPromptInput / renderSkillBlock / overrideSkillsToSummary

**Files:**
- Modify: `internal/provider/codexapp/module.go` (删 buildSkillPromptInput, renderSkillBlock)
- Modify: `internal/provider/codexapp/session_turn.go` (移除 turnInputsFromRequest 中的 skill block)
- Delete: `internal/provider/codexapp/skill_mode_override.go`
- Delete: `internal/provider/codexapp/skill_mode_override_test.go`
- Delete: `internal/provider/codexapp/skill_injection_test.go`（per-turn 注入测试）

### Step 1: Find references

```
grep -rn "buildSkillPromptInput\|renderSkillBlock\|overrideSkillsToSummary" --include="*.go" .
```

### Step 2: Delete + clean

```bash
rm internal/provider/codexapp/skill_mode_override.go
rm internal/provider/codexapp/skill_mode_override_test.go
rm internal/provider/codexapp/skill_injection_test.go
```

修改 `module.go`：删 `buildSkillPromptInput`、`renderSkillBlock`、`skillWriterFormat` 函数。

修改 `session_turn.go`：从 `turnInputsFromRequest` 中删除 `buildSkillPromptInput(skills)` 那一段；turn input 不再含 skill block。

### Step 3: Verify

```
go build ./...
go test ./internal/provider/codexapp/... -short -count=1
```

### Step 4: Commit

```
git commit -m "refactor(codexapp): delete buildSkillPromptInput + renderSkillBlock + Mode override"
```

---

## Task 7: 删除 SKILL_WRITER_FORMAT 残留

**Files:**
- Modify: `internal/provider/claudecli/session_turn.go`（如果还有 v1/legacy 渲染）
- Modify: `internal/module/prompt/config.go`（如果还有 SkillWriterFormat 字段）

### Step 1: Find references

```
grep -rn "SKILL_WRITER_FORMAT\|skillWriterFormat\|RenderSkillBlockV1\|RenderLegacySkillBlock" --include="*.go" .
```

### Step 2: Delete

P2 应该已经删了 Claude 侧的相关代码；P3 收尾 codex 侧。如果有任何 prompt config 字段死掉，一并删。

### Step 3: Commit

```
git commit -m "refactor: remove SKILL_WRITER_FORMAT env var and v1/legacy formats"
```

---

## Task 8: 删 codexapp 残留 skill_inject.go

**Files:**
- Delete: `internal/provider/codexapp/skill_inject.go`
- Delete: `internal/provider/codexapp/skill_injection_test.go`（如果还存在）
- Modify: `internal/provider/codexapp/module.go`（删任何 NewSkillInjectionPort 残留 import 或 provider）

P2 留下的 codex 侧 stub，本期一并清理。

```bash
rm internal/provider/codexapp/skill_inject.go
# skill_injection_test.go 已在 Task 6 删除
git add -A
git commit -m "refactor(codexapp): delete leftover skill_inject.go stub from P2"
```

---

## Task 9: 端到端测试 — Codex 看到 skill manifest + 调用 skill_read_section

**Files:** 新增集成测试

```go
// codex_skill_e2e_test.go
func TestCodexEndToEnd_SkillManifestAndReadSection(t *testing.T) {
	// 1. 启动 fx app with skilllibrary + toolbridge + codexapp
	// 2. seed builtins via SeedBuiltins (P1 startup hook 自动跑)
	// 3. 验证 codex driver 拿到的 baseInstructions 含 "## 可用 skills"
	// 4. 模拟 skill_read_section 工具调用，验证返回该节内容
}
```

### Step 1-5: 标准 TDD + commit

```
git commit -m "test(codexapp): add e2e test for skill manifest + read_section"
```

---

## Task 10: P3 全测试 + 冒烟

仅运行测试，不改代码。

### Step 1: 触动包测试

```
cd /private/tmp/super-dolphin-skill-refactor-p3
go test -v -count=1 \
  ./internal/module/skilllibrary/... \
  ./internal/platform/toolbridge/... \
  ./internal/provider/codexapp/... \
  ./internal/dto/provider/...
```

### Step 2: 全项目测试

```
go test -short ./...
```

### Step 3: build + vet

```
go build ./...
go vet ./...
```

### Step 4: 冒烟

```
TMP=$(mktemp -d)
mkdir -p "$TMP/lib" "$TMP/cache"

cat > "$TMP/main.go" <<'EOF'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
)

func main() {
	app := fx.New(
		fx.NopLogger,
		skillforge.Module,
		skilllibrary.Module,
		fx.Provide(func() skilllibrary.Config {
			return skilllibrary.Config{
				LibraryDir:     os.Args[1],
				CacheDir:       os.Args[2],
				HarnessVersion: "smoke",
			}
		}),
	)
	if err := app.Start(context.Background()); err != nil { fmt.Println("start err:", err); os.Exit(1) }
	defer app.Stop(context.Background())

	store := /* extract from app */
	entries, _ := store.List()
	manifest := codexapp.BuildSkillManifest(entries, 8192)
	fmt.Printf("manifest len=%d\n", len(manifest))
	fmt.Println("---")
	fmt.Println(manifest[:300])
	fmt.Println("...")

	tool := toolbridge.NewSkillReadSectionTool(os.Args[2])
	args, _ := json.Marshal(map[string]string{"name": entries[0].Meta.Name, "anchor": "概览"})
	out, err := tool.Call(context.Background(), args)
	fmt.Printf("read_section: err=%v len=%d\n", err, len(out))
}
EOF

cd /private/tmp/super-dolphin-skill-refactor-p3
go run "$TMP/main.go" "$TMP/lib" "$TMP/cache"

rm -rf "$TMP"
```

期望：
- manifest 包含 "## 可用 skills" 头
- 至少 1 个 skill 的 name + description
- skill_read_section 调用成功返回该节正文

---

## Phase 3 自审

按 编写计划 §自审：

**1. 规格覆盖：** 对照 spec §7 + §11:
- §7.1 L1-C 注入 → Task 4 + Task 5
- §7.2 DynamicTools `skill_read_section` → Task 1 + Task 2 + Task 3
- §7.3 budget 兜底降级 → Task 4 内
- §7.4 删除项：buildSkillPromptInput / Mode 三态 / SKILL_WRITER_FORMAT → Task 6 + Task 7
- FBSD（§9）→ 明确 P6 范畴，本期不做；只渲染 L1-C + budget 截尾

**未覆盖项**（明确延后到 P4/P6）：
- `dto.SkillRef.Mode` 字段定义本身保留（Task 6 只删 codex 消费方），P4 删字段定义
- `skill_expand_body` / `skill_read_resource` 在 `internal/module/skill/skills_expand.go` 的实现保留，P4 删
- FBSD tier 算法 → P6
- F1 native CLI 过滤 → P5

**2. 占位符扫描：** 仅 Task 5 / Task 9 测试中的"构造 driver"细节由 implementer 根据现有 codexapp/driver.go 结构填充；其他全完整。

**3. 类型一致性：**
- `skilllibrary.ReadSection(cacheDir, name, anchor)` 跨 Task 1/2 一致
- `buildSkillManifest(entries []SkillEntry, budgetChars int) string` 跨 Task 4/5/9 一致
- `NewSkillReadSectionTool(cacheDir)` 跨 Task 2/3/9 一致

修复内联：暂无问题。

---

## 执行交接

计划已完成并保存到 `docs/superpowers/plans/2026-04-29-skill-refactor-p3-codex-cutover.md`。两个执行选项：

1. **子代理驱动（推荐）**
2. **当前会话内执行**
