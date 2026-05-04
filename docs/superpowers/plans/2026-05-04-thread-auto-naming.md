# 线程自动命名 Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让每个对话在侧边栏显示 ≤ 8 显示单元的可读标题，替代不可读的 `agent_177...` ID。

**Architecture:** 纯 harness 侧实现，零 LLM 成本。三个功能点：(1) 创建时默认命名 `对话 #N`；(2) 首条消息到达时从 prompt 提取关键词标题；(3) Fork 时继承父标题 + `(续)` 后缀。新增 `manually_renamed` 列防止自动提取覆盖手动改名。两条创建路径都需处理：eager launch 走 `lifecycle.go:persistStartedSession`，pending launch 走 `spawn.go:startPendingThread`。

**Tech Stack:** Go, PostgreSQL, sqlc

**Spec:** `docs/superpowers/specs/2026-05-04-thread-auto-naming-design.md`

---

## 文件结构

| 操作 | 文件路径 | 职责 |
|------|---------|------|
| Create | `internal/module/thread/title_extract.go` | 标题提取纯函数 + continuationName |
| Create | `internal/module/thread/title_extract_test.go` | 提取函数单元测试 |
| Create | `migrations/0064_thread_manually_renamed.sql` | 新增 manually_renamed 列 |
| Modify | `sql/queries/agent_thread.sql` | 新增 CountAllThreads 查询，UpsertAgentThread 增加 manually_renamed |
| Regen | `internal/store/sqlc/agent_thread.sql.go` | sqlc 重新生成 |
| Modify | `internal/store/thread/contract.go:70-94` | Thread 结构体加 ManuallyRenamed 字段 |
| Modify | `internal/store/thread/contract.go:31-50` | UpsertParams 加 ManuallyRenamed 字段 |
| Modify | `internal/store/thread/contract.go:9-29` | Store 接口加 CountAll 方法 |
| Modify | `internal/store/thread/store.go:101-122` | Upsert 映射加 ManuallyRenamed，新增 CountAll 方法 |
| Modify | `internal/module/thread/factory.go:95-116` | newThreadUpsertParams 映射加 ManuallyRenamed |
| Modify | `internal/module/thread/lifecycle.go:272-346` | persistStartedSession 加默认命名 + 提取逻辑 |
| Modify | `internal/module/thread/spawn.go:53-133` | startPendingThread 加默认命名 + 提取逻辑 |
| Modify | `internal/module/thread/lifecycle_fork.go:14-91` | Fork 加续对话后缀 |
| Modify | `internal/module/thread/service.go:172-208` | SetName 置 ManuallyRenamed=true |

---

## Task 1: 标题提取纯函数

**Files:**
- Create: `internal/module/thread/title_extract.go`
- Create: `internal/module/thread/title_extract_test.go`

- [ ] **Step 1: 编写失败测试**

在 `internal/module/thread/title_extract_test.go` 中：

```go
package thread

import "testing"

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
	}{
		{"去@mention", "@头脑风暴，订单表优化", "订单表优化"},
		{"去寒暄前缀", "帮我优化一下订单表", "优化订单表"},
		{"去多个前缀", "请帮我看看这个bug", "这个bug"},
		{"去代码块", "看看 `func SetName()` 这个函数", "看看这个函数"},
		{"取首句", "修复bug。然后跑测试", "修复bug"},
		{"问号分句", "对话框命名怎么运行的？后面还有内容", "对话框命名怎么运行"},
		{"技术词保留", "spawn.go race condition 问题", "spawn.go race condition 问题"},
		{"中文虚词丢弃", "看一下订单表的JOIN优化了吧", "看订单表JOIN优化"},
		{"截断8单元", "优化订单表的JOIN查询索引重建方案讨论", "优化订单表JOIN查询索引重建"},
		{"兜底太短", "好", ""},
		{"兜底代词", "这个", ""},
		{"空字符串", "", ""},
		{"纯英文", "fix the race condition in spawn", "fix race condition in spawn"},
		{"英文截断", "refactor the entire thread module to support automatic naming with tests", "refactor entire thread module to support automatic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTitle(tt.input)
			if got != tt.want {
				t.Errorf("ExtractTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountDisplayUnits(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"订单表 JOIN 优化", 6},
		{"spawn.go race condition", 3},
		{"hello", 1},
		{"你好世界", 4},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := countDisplayUnits(tt.input)
			if got != tt.want {
				t.Errorf("countDisplayUnits(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestContinuationName(t *testing.T) {
	tests := []struct {
		parentName string
		want       string
	}{
		{"订单表 JOIN 优化", "订单表 JOIN 优化 (续)"},
		{"订单表 JOIN 优化 (续)", "订单表 JOIN 优化 (续 2)"},
		{"订单表 JOIN 优化 (续 2)", "订单表 JOIN 优化 (续 3)"},
		{"对话 #3", "对话 #3 (续)"},
	}
	for _, tt := range tests {
		t.Run(tt.parentName, func(t *testing.T) {
			got := continuationName(tt.parentName)
			if got != tt.want {
				t.Errorf("continuationName(%q) = %q, want %q", tt.parentName, got, tt.want)
			}
		})
	}
}

func TestDefaultName(t *testing.T) {
	tests := []struct {
		count int64
		want  string
	}{
		{0, "对话 #1"},
		{5, "对话 #6"},
		{99, "对话 #100"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := defaultThreadName(tt.count)
			if got != tt.want {
				t.Errorf("defaultThreadName(%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/module/thread/ -run "TestExtractTitle|TestCountDisplayUnits|TestContinuationName|TestDefaultName" -v`
Expected: FAIL — 函数未定义

- [ ] **Step 3: 实现提取函数**

在 `internal/module/thread/title_extract.go` 中：

```go
package thread

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const maxDisplayUnits = 8

var (
	mentionRe   = regexp.MustCompile(`@\S+[，,\s]*`)
	codeBlockRe = regexp.MustCompile("`[^`]*`")
	fillerPrefixes = []string{
		"帮我", "请帮我", "请", "能不能", "可不可以",
		"你帮我", "你", "我想", "我要", "麻烦",
		"看看", "一下",
	}
	fillerWords = map[rune]bool{
		'的': true, '了': true, '吧': true,
		'啊': true, '呢': true, '呀': true,
		'嘛': true, '哦': true,
	}
	pronouns = map[string]bool{
		"这个": true, "那个": true, "这": true, "那": true,
		"它": true, "他": true, "她": true,
		"this": true, "that": true, "it": true,
	}
	sentenceSeps    = regexp.MustCompile(`[。？！，\n,.!?]`)
	continuationRe  = regexp.MustCompile(`^(.*?)\s*\(续(?:\s+(\d+))?\)$`)
)

// ExtractTitle 从用户 prompt 提取 ≤ 8 显示单元的标题。
// 返回空字符串表示应使用兜底名。
func ExtractTitle(prompt string) string {
	s := strings.TrimSpace(prompt)
	if s == "" {
		return ""
	}

	s = mentionRe.ReplaceAllString(s, "")
	s = codeBlockRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)

	for _, prefix := range fillerPrefixes {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimSpace(s)

	if loc := sentenceSeps.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]]
	}
	s = strings.TrimSpace(s)

	s = removeFiller(s)
	s = strings.TrimSpace(s)

	s = truncateToUnits(s, maxDisplayUnits)

	if countDisplayUnits(s) <= 2 || isAllPronouns(s) {
		return ""
	}

	return s
}

// defaultThreadName 生成兜底名 "对话 #N"。count 是当前已有线程数。
func defaultThreadName(count int64) string {
	return fmt.Sprintf("对话 #%d", count+1)
}

// continuationName 生成续对话名称。
// "标题" → "标题 (续)"
// "标题 (续)" → "标题 (续 2)"
// "标题 (续 N)" → "标题 (续 N+1)"
func continuationName(parentName string) string {
	if m := continuationRe.FindStringSubmatch(parentName); m != nil {
		base := m[1]
		if m[2] == "" {
			return base + " (续 2)"
		}
		n, _ := strconv.Atoi(m[2])
		return fmt.Sprintf("%s (续 %d)", base, n+1)
	}
	return parentName + " (续)"
}

func countDisplayUnits(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			if inWord {
				count++
				inWord = false
			}
			count++
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '/' {
			if !inWord {
				inWord = true
			}
		} else {
			if inWord {
				count++
				inWord = false
			}
		}
	}
	if inWord {
		count++
	}
	return count
}

func truncateToUnits(s string, maxUnits int) string {
	count := 0
	inWord := false
	wordStart := 0
	for i, r := range s {
		if unicode.Is(unicode.Han, r) {
			if inWord {
				count++
				inWord = false
				if count >= maxUnits {
					return strings.TrimSpace(s[:i])
				}
			}
			count++
			if count >= maxUnits {
				return strings.TrimSpace(s[:i+len(string(r))])
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '/' {
			if !inWord {
				inWord = true
				wordStart = i
			}
		} else {
			if inWord {
				count++
				inWord = false
				if count >= maxUnits {
					return strings.TrimSpace(s[:i])
				}
			}
		}
	}
	return strings.TrimSpace(s)
}

func removeFiller(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !fillerWords[r] {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isAllPronouns(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || pronouns[s]
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/module/thread/ -run "TestExtractTitle|TestCountDisplayUnits|TestContinuationName|TestDefaultName" -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/module/thread/title_extract.go internal/module/thread/title_extract_test.go
git commit -m "feat(thread): 新增标题提取纯函数 ExtractTitle、continuationName、defaultThreadName"
```

---

## Task 2: 数据库 migration + store 层

**Files:**
- Create: `migrations/0064_thread_manually_renamed.sql`
- Modify: `sql/queries/agent_thread.sql`
- Modify: `internal/store/thread/contract.go:70-94` (Thread struct)
- Modify: `internal/store/thread/contract.go:31-50` (UpsertParams struct)
- Modify: `internal/store/thread/contract.go:9-29` (Store interface)
- Modify: `internal/store/thread/store.go:101-122` (Upsert method)
- Modify: `internal/module/thread/factory.go:95-116` (newThreadUpsertParams)
- Regen: `internal/store/sqlc/`

- [ ] **Step 1: 创建 migration**

在 `migrations/0064_thread_manually_renamed.sql` 中：

```sql
ALTER TABLE agent_threads
    ADD COLUMN IF NOT EXISTS manually_renamed boolean NOT NULL DEFAULT false;
```

- [ ] **Step 2: 更新 SQL 查询**

在 `sql/queries/agent_thread.sql` 中：

a) 修改 UpsertAgentThread（行 97-126），在 INSERT 列列表末尾加 `manually_renamed`，VALUES 中加 `sqlc.arg(manually_renamed)`，ON CONFLICT SET 中加 `manually_renamed = $19`。

b) 文件末尾新增：

```sql
-- name: CountAllThreads :one
SELECT COUNT(*) FROM agent_threads;
```

- [ ] **Step 3: 运行 sqlc generate**

Run: `sqlc generate`
Expected: `internal/store/sqlc/agent_thread.sql.go` 更新

- [ ] **Step 4: 更新 Thread 结构体**

在 `internal/store/thread/contract.go:93` 行 `PendingLaunch bool` 之后加：

```go
ManuallyRenamed bool
```

- [ ] **Step 5: 更新 UpsertParams 结构体**

在 `internal/store/thread/contract.go:50` 的 UpsertParams `PendingLaunch bool` 之后加：

```go
ManuallyRenamed bool
```

- [ ] **Step 6: 更新 Store 接口**

在 `internal/store/thread/contract.go` 的 Store 接口（行 9-29），`CountChildren` 之后加：

```go
CountAll(ctx context.Context) (int64, error)
```

- [ ] **Step 7: 实现 CountAll 和更新 Upsert**

在 `internal/store/thread/store.go` 中新增：

```go
func (s *store) CountAll(ctx context.Context) (int64, error) {
	count, err := s.q.CountAllThreads(ctx)
	if err != nil {
		return 0, wrapThreadError(err, "count_all")
	}
	return count, nil
}
```

在 Upsert 方法（行 101-122）的 sqlc 参数映射中加：

```go
ManuallyRenamed: params.ManuallyRenamed,
```

- [ ] **Step 8: 更新 factory.go 映射**

在 `internal/module/thread/factory.go:95-116` 的 `newThreadUpsertParams` 中，`PendingLaunch` 之后加：

```go
ManuallyRenamed: thread.ManuallyRenamed,
```

- [ ] **Step 9: 运行验证**

Run: `go test ./internal/store/thread/ -v -count=1`
Expected: PASS

- [ ] **Step 10: 提交**

```bash
git add migrations/0064_thread_manually_renamed.sql sql/queries/agent_thread.sql \
      internal/store/sqlc/ internal/store/thread/ internal/module/thread/factory.go
git commit -m "feat(store): agent_threads 新增 manually_renamed 列和 CountAll 查询"
```

---

## Task 3: 默认命名 + 自动提取 — 两条创建路径

**Files:**
- Modify: `internal/module/thread/lifecycle.go:272-346` (eager launch 路径)
- Modify: `internal/module/thread/spawn.go:53-133` (pending launch 路径)

- [ ] **Step 1: 在 lifecycle.go 的 persistStartedSession 中加自动命名**

在 `internal/module/thread/lifecycle.go` 的 `persistStartedSession` 函数中（行 272 之后），在构建 threadState 之前，增加：

```go
if displayName == "" {
	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		displayName = ExtractTitle(prompt)
	}
}
if displayName == "" {
	count, _ := s.threadStore.CountAll(ctx)
	displayName = defaultThreadName(count)
}
```

注意：`req.Prompt` 字段名需要核对 StartRequest 结构体定义，可能是 `req.Name` 或 `req.Prompt`。

- [ ] **Step 2: 在 spawn.go 的 startPendingThread 中加同样逻辑**

在 `internal/module/thread/spawn.go` 的 `startPendingThread` 函数中（行 61 附近），当前代码：

```go
displayName := strings.TrimSpace(shared.FirstNonEmpty(req.Name, req.Prompt))
```

之后增加：

```go
if displayName == "" {
	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		displayName = ExtractTitle(prompt)
	}
}
if displayName == "" {
	count, _ := s.threadStore.CountAll(ctx)
	displayName = defaultThreadName(count)
}
```

- [ ] **Step 3: 运行现有测试验证不破坏**

Run: `go test ./internal/module/thread/ -v -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 4: 提交**

```bash
git add internal/module/thread/lifecycle.go internal/module/thread/spawn.go
git commit -m "feat(thread): 两条创建路径加自动命名 — 提取标题或兜底 对话 #N"
```

---

## Task 4: 续对话命名 — Fork

**Files:**
- Modify: `internal/module/thread/lifecycle_fork.go:25`

- [ ] **Step 1: 修改 Fork 中的 displayName 赋值**

在 `internal/module/thread/lifecycle_fork.go:25`，当前代码：

```go
displayName := strings.TrimSpace(meta.Name)
```

改为：

```go
displayName := continuationName(strings.TrimSpace(meta.Name))
```

- [ ] **Step 2: 运行 Fork 相关测试**

Run: `go test ./internal/module/thread/ -run "Fork" -v`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add internal/module/thread/lifecycle_fork.go
git commit -m "feat(thread): Fork 续对话自动追加 (续) 后缀"
```

---

## Task 5: SetName 置 ManuallyRenamed + 跳过 guard

**Files:**
- Modify: `internal/module/thread/service.go:172-208`
- Modify: `internal/module/thread/lifecycle.go` (Task 3 已改的位置)
- Modify: `internal/module/thread/spawn.go` (Task 3 已改的位置)

- [ ] **Step 1: 修改 SetName 标记 ManuallyRenamed**

在 `internal/module/thread/service.go` 的 SetName 方法中（行 178-180），`thread.Name = name` 之后加：

```go
thread.ManuallyRenamed = true
```

- [ ] **Step 2: 在两条创建路径加 ManuallyRenamed guard**

在 Task 3 添加的自动命名逻辑（lifecycle.go 和 spawn.go）最前面，加 guard：

```go
// lifecycle.go 的 persistStartedSession 中：
if displayName == "" {
	existing, err := s.threadStore.Get(ctx, agentID)
	if err == nil && existing.ManuallyRenamed {
		displayName = existing.Name
	}
}
// 后续提取/兜底逻辑不变
```

spawn.go 中同理，在自动命名逻辑前检查。对新创建的线程（首次 Start），Get 会返回 error（不存在），自然跳过 guard 走正常提取。

- [ ] **Step 3: 运行全部测试**

Run: `go test ./internal/module/thread/ -v -count=1`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/module/thread/service.go internal/module/thread/lifecycle.go \
      internal/module/thread/spawn.go
git commit -m "feat(thread): SetName 标记 manuallyRenamed，自动提取跳过手动命名"
```

---

## Task 6: 端到端验证

- [ ] **Step 1: 运行全模块测试**

Run: `go test ./internal/module/thread/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 2: 运行 store 层测试**

Run: `go test ./internal/store/thread/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 3: 运行 go vet**

Run: `go vet ./...`
Expected: 无错误

- [ ] **Step 4: 运行 scripts/test_with_guard.sh（如果存在）**

Run: `scripts/test_with_guard.sh`
Expected: PASS

- [ ] **Step 5: 最终提交（如有遗漏修复）**

```bash
git add -A
git commit -m "fix: 线程自动命名端到端修复"
```
