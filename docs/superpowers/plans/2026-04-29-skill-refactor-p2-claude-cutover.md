# Skill Refactor — Phase 2: Claude-Side Cutover Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Claude CLI 一侧从自研 MCP 主机 RPC（`skill_mcp_server.go`）切换到 Anthropic 原生发现机制（workspace `.claude/skills` symlink → 共享缓存）。删除 ~1150 行自研 MCP 代码。整合 P1 基础设施到 runtime 启动序列。

**Architecture:** harness 启动 → fx.Invoke 触发 `SeedBuiltins` + `ReconcileAll`，把 14 个内置 skill seed 到 library + forge 到缓存。Claude session 启动前，新 `cliadapter.SetupWorkspaceSkills(cwd, cacheDir)` 在 user CWD 下创建 `.claude/skills` symlink 指向缓存。Claude CLI 子进程 native 发现 + native Read。原 `skill_mcp_server.go`、`cmd/agent-terminal/mcp_skill_mode.go`、`prompt/skill_catalog_provider.go` 大部、`manifestbuilder/appendSkillMCPServer`、`DetectNativeSkills` + `NewSkillInjectionPort` 全部删除。

**Tech Stack:** Go 1.22+、`go.uber.org/fx`（`fx.Invoke` + `fx.Lifecycle.Append OnStart`）、`os.Symlink` / Windows junction fallback、现有 `internal/provider/claudecli/transport.go` + `driver.go` 改造。

**前置阅读：**
- `docs/superpowers/specs/2026-04-29-skill-refactor-design.md` §3.3、§5.2、§6
- `internal/module/skilllibrary/`（P1 产物：`Store`、`Reconciler`、`SeedBuiltins`、`Config`）
- `internal/provider/claudecli/driver.go:189`（`launchCLI` 调用点）
- `internal/provider/claudecli/transport.go:46`（`newTransport` + `cmd.Dir = cwd`）
- `internal/module/prompt/skill_catalog_provider.go`（待删大部）
- `internal/module/prompt/module.go:73-95`（`compositeNativeSkillDetector`，待删）
- `cmd/agent-terminal/mcp_skill_mode.go`（待删）

---

## File Structure

**新增包**：

```
internal/module/cliadapter/         (Task 2-3)
├── symlink.go
├── symlink_test.go
├── symlink_posix.go                 (build tag: !windows)
├── symlink_posix_test.go
├── symlink_windows.go               (build tag: windows)
└── module.go                        (fx.Module 占位)
```

**修改文件**：

```
internal/module/skilllibrary/module.go               (Task 1: + fx.Invoke startup)
internal/module/skilllibrary/startup.go              (Task 1: 新文件，startup logic)
internal/module/skilllibrary/startup_test.go         (Task 1)
internal/provider/claudecli/driver.go                (Task 4: prepareSessionStart 调 SetupWorkspaceSkills)
internal/provider/claudecli/module.go                (Task 6: 移除 NewSkillInjectionPort 注册)
internal/provider/claudecli/skill_inject.go          (Task 6: 删 DetectNativeSkills + NewSkillInjectionPort)
internal/provider/manifestbuilder/manifest.go        (Task 8: 删 appendSkillMCPServer 调用)
internal/module/prompt/module.go                     (Task 6-7: 删 composite detector + catalog provider 注册)
internal/module/prompt/skill_catalog_provider.go     (Task 7: 删整文件或缩到极小)
internal/app/modules.go                              (Task 1: 已含 skilllibrary.Module，无新增；但需经过冒烟验证)
```

**删除文件**：

```
internal/provider/claudecli/skill_mcp_server.go
internal/provider/claudecli/skill_mcp_server_test.go
cmd/agent-terminal/mcp_skill_mode.go
internal/provider/claudecli/skill_injection_test.go  (Task 6: 测试 DetectNativeSkills，整文件删除)
internal/module/prompt/skill_catalog_provider_test.go (Task 7: 整文件删除)
internal/module/prompt/skill_catalog_fx_test.go      (Task 7: 整文件删除)
internal/module/prompt/boundary_invariants_test.go   (Task 7: 涉及 catalog 的部分；整文件删除如全文都是 catalog 测试)
```

---

## Task 1: skilllibrary 启动 hook（seed + reconcile）

**Files:**
- Create: `internal/module/skilllibrary/startup.go`
- Test: `internal/module/skilllibrary/startup_test.go`
- Modify: `internal/module/skilllibrary/module.go`（追加 fx.Invoke）

- [ ] **Step 1: Write the failing test**

`startup_test.go`:

```go
package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

func TestStartup_SeedsAndReconciles(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()

	app := fxtest.New(t,
		skillforge.Module,
		Module,
		fx.Provide(func() Config {
			return Config{LibraryDir: libDir, CacheDir: cacheDir, HarnessVersion: "test-1"}
		}),
	)
	defer app.RequireStart().RequireStop()

	// startup hook 应该在 RequireStart() 后已执行
	names, _ := skillforge.ListEmbeddedSkillNames()
	for _, n := range names[:1] {
		if _, err := os.Stat(filepath.Join(libDir, n, ".skill-meta.json")); err != nil {
			t.Errorf("library missing %s after startup: %v", n, err)
		}
		if _, err := os.Stat(filepath.Join(cacheDir, n, "SKILL.md")); err != nil {
			t.Errorf("cache missing %s after startup: %v", n, err)
		}
	}
}

func TestStartup_IsIdempotent(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()
	cfg := Config{LibraryDir: libDir, CacheDir: cacheDir, HarnessVersion: "test-1"}

	for i := 0; i < 2; i++ {
		app := fxtest.New(t,
			skillforge.Module,
			Module,
			fx.Provide(func() Config { return cfg }),
		)
		app.RequireStart().RequireStop()
	}
	// 第二次启动不应出错；library 应保持稳定
	names, _ := skillforge.ListEmbeddedSkillNames()
	if _, err := os.Stat(filepath.Join(cacheDir, names[0])); err != nil {
		t.Errorf("cache lost between starts: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd /private/tmp/super-dolphin-skill-refactor-p2
go test ./internal/module/skilllibrary/... -run TestStartup -v
```
Expected: FAIL（新 fx.Invoke 还没加，library/cache 不会被填充）。

- [ ] **Step 3: Implementation**

`startup.go`:

```go
package skilllibrary

import (
	"context"
	"fmt"

	"go.uber.org/fx"
)

// runStartup 在 fx OnStart 钩子中跑：seed 内置 skill 到 library，然后 reconcile 到 cache。
// 任一阶段失败则 OnStart 返回 error，阻止 app 启动（fail-closed）。
func runStartup(lc fx.Lifecycle, store *Store, reconciler *Reconciler, cfg Config) {
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			if _, err := SeedBuiltins(store, cfg.HarnessVersion); err != nil {
				return fmt.Errorf("skilllibrary startup: seed builtins: %w", err)
			}
			if _, err := reconciler.ReconcileAll(); err != nil {
				return fmt.Errorf("skilllibrary startup: reconcile: %w", err)
			}
			return nil
		},
	})
}
```

`module.go`（追加 `fx.Invoke(runStartup)`）—— 当前 `module.go` 文件改动：

```go
var Module = fx.Module("skilllibrary",
	fx.Provide(func(c Config) *Store { return NewStore(c.LibraryDir) }),
	fx.Provide(func(s *Store, c Config) *Reconciler { return NewReconciler(s, c.CacheDir) }),
	fx.Invoke(runStartup),
)
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./internal/module/skilllibrary/... -v -count=1
```
Expected: ALL skilllibrary tests pass（含 2 个新 startup test）。

- [ ] **Step 5: Commit**

```bash
git add internal/module/skilllibrary/startup.go \
        internal/module/skilllibrary/startup_test.go \
        internal/module/skilllibrary/module.go
git commit -m "feat(skilllibrary): add fx OnStart hook for seed + reconcile"
```

---

## Task 2: cliadapter 包 + workspace symlink 助手

**Files:**
- Create: `internal/module/cliadapter/symlink.go`（公共 API）
- Create: `internal/module/cliadapter/symlink_posix.go`（POSIX 实现，build tag `!windows`）
- Create: `internal/module/cliadapter/symlink_windows.go`（Windows 实现，build tag `windows`）
- Create: `internal/module/cliadapter/module.go`

- [ ] **Step 1: Write the failing test**（统一测试见 Task 3，本步只占位 stub）

仅创建测试文件骨架，确保 Task 3 有目标：

`symlink_test.go`:

```go
package cliadapter

import "testing"

func TestSetupWorkspaceSkills_Stub(t *testing.T) {
	t.Skip("placeholder; real tests in Task 3")
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd /private/tmp/super-dolphin-skill-refactor-p2
go test ./internal/module/cliadapter/... -v
```
Expected: 包不存在 → FAIL（因为 symlink.go 等还没创建）。

- [ ] **Step 3: Implementation**

`symlink.go`（公共 API）:

```go
// Package cliadapter 封装 harness 与底层 CLI 子进程之间的环境装配，
// 例如把共享 skill cache 挂到 Claude CLI 期望的 <workspace>/.claude/skills/。
package cliadapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrEmptyArgs 表示 SetupWorkspaceSkills 调用时缺必要参数。
var ErrEmptyArgs = errors.New("cliadapter: empty workspace or cache dir")

// SetupWorkspaceSkills 在 workspace/<workspaceDir> 下创建 .claude/skills 引用，
// 让 Claude CLI 子进程 native 发现共享缓存里的 skill。
//
// 行为：
//  1. workspaceDir 或 cacheDir 为空 → ErrEmptyArgs
//  2. cacheDir 不存在 → 立即创建（避免 dangling symlink）
//  3. <workspaceDir>/.claude/ 目录自动创建
//  4. 已有的 <workspaceDir>/.claude/skills（普通目录或 symlink）会被替换
//  5. POSIX 用 os.Symlink；Windows 走 platform-specific 实现（junction / hardlink-copy）
//
// 返回 nil 表示链接就位；调用方此后可安全启动 Claude CLI 子进程。
func SetupWorkspaceSkills(workspaceDir, cacheDir string) error {
	if workspaceDir == "" || cacheDir == "" {
		return ErrEmptyArgs
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("cliadapter: ensure cache dir: %w", err)
	}
	claudeDir := filepath.Join(workspaceDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("cliadapter: mkdir .claude: %w", err)
	}
	target := filepath.Join(claudeDir, "skills")
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("cliadapter: clear stale skills entry: %w", err)
	}
	return platformLink(target, cacheDir)
}
```

`symlink_posix.go`（build tag `!windows`）:

```go
//go:build !windows

package cliadapter

import (
	"fmt"
	"os"
)

// platformLink 在 POSIX 上用 os.Symlink。
func platformLink(target, source string) error {
	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("cliadapter: symlink: %w", err)
	}
	return nil
}
```

`symlink_windows.go`（build tag `windows`）:

```go
//go:build windows

package cliadapter

import (
	"fmt"
	"os"
	"os/exec"
)

// platformLink 在 Windows 上优先用 directory junction（mklink /J），
// 失败再退化到普通 symlink（需 Developer Mode）。
// 跨盘场景 Phase 2 不做硬拷贝兜底（YAGNI；P5/P6 真踩到再补）。
func platformLink(target, source string) error {
	// /J = directory junction, doesn't require admin
	cmd := exec.Command("cmd", "/C", "mklink", "/J", target, source)
	if out, err := cmd.CombinedOutput(); err == nil {
		return nil
	} else {
		// Fall back to symlink
		if err2 := os.Symlink(source, target); err2 != nil {
			return fmt.Errorf("cliadapter: junction failed (%s); symlink failed: %w", string(out), err2)
		}
	}
	return nil
}
```

`module.go`:

```go
package cliadapter

import "go.uber.org/fx"

// Module 当前不暴露 service 单例（API 是纯函数），占位以保 Fx 树一致。
var Module = fx.Module("cliadapter")
```

- [ ] **Step 4: Run test to verify it passes**

```
go test ./internal/module/cliadapter/... -v
```
Expected: PASS（stub test 通过；真测在 Task 3）。

- [ ] **Step 5: Commit**

```bash
git add internal/module/cliadapter/symlink.go \
        internal/module/cliadapter/symlink_posix.go \
        internal/module/cliadapter/symlink_windows.go \
        internal/module/cliadapter/symlink_test.go \
        internal/module/cliadapter/module.go
git commit -m "feat(cliadapter): add workspace skill symlink helper (POSIX + Windows junction)"
```

---

## Task 3: cliadapter symlink 测试（POSIX）

**Files:**
- Modify: `internal/module/cliadapter/symlink_test.go`（替换 stub）
- Create: `internal/module/cliadapter/symlink_posix_test.go`（build tag `!windows`）

- [ ] **Step 1: Write the failing test**

替换 `symlink_test.go`（公共，跨平台）:

```go
package cliadapter

import "testing"

func TestSetupWorkspaceSkills_EmptyArgsError(t *testing.T) {
	if err := SetupWorkspaceSkills("", "/tmp/cache"); err == nil {
		t.Error("empty workspaceDir should error")
	}
	if err := SetupWorkspaceSkills("/tmp/ws", ""); err == nil {
		t.Error("empty cacheDir should error")
	}
}
```

新建 `symlink_posix_test.go`:

```go
//go:build !windows

package cliadapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupWorkspaceSkills_CreatesSymlinkPOSIX(t *testing.T) {
	workspace := t.TempDir()
	cache := t.TempDir()
	// 在缓存里放一个 sentinel 文件
	sentinel := filepath.Join(cache, "marker")
	if err := os.WriteFile(sentinel, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetupWorkspaceSkills(workspace, cache); err != nil {
		t.Fatalf("SetupWorkspaceSkills: %v", err)
	}
	// 通过 symlink 应该能读到 sentinel
	via := filepath.Join(workspace, ".claude", "skills", "marker")
	b, err := os.ReadFile(via)
	if err != nil {
		t.Fatalf("read via symlink: %v", err)
	}
	if string(b) != "ok" {
		t.Errorf("read = %q, want ok", string(b))
	}
	// 验证 symlink 性质
	info, err := os.Lstat(filepath.Join(workspace, ".claude", "skills"))
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".claude/skills is not a symlink")
	}
}

func TestSetupWorkspaceSkills_ReplacesExistingEntry(t *testing.T) {
	workspace := t.TempDir()
	cache := t.TempDir()
	// 预先在 workspace 创建一个普通目录占位
	pre := filepath.Join(workspace, ".claude", "skills")
	if err := os.MkdirAll(pre, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pre, "stale"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetupWorkspaceSkills(workspace, cache); err != nil {
		t.Fatalf("SetupWorkspaceSkills: %v", err)
	}
	// stale 应该被清掉
	if _, err := os.Stat(filepath.Join(pre, "stale")); !os.IsNotExist(err) {
		t.Errorf("stale entry not removed: %v", err)
	}
	info, _ := os.Lstat(pre)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".claude/skills should now be a symlink")
	}
}

func TestSetupWorkspaceSkills_CreatesCacheIfMissing(t *testing.T) {
	workspace := t.TempDir()
	cache := filepath.Join(t.TempDir(), "not-yet")
	if err := SetupWorkspaceSkills(workspace, cache); err != nil {
		t.Fatalf("SetupWorkspaceSkills: %v", err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Errorf("cacheDir not created: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```
go test ./internal/module/cliadapter/... -v -count=1
```
当前 `SetupWorkspaceSkills` 实现已就位（Task 2）；这里测试应**直接通过**，证明 Task 2 实现正确。如不通过则修 Task 2 实现。

- [ ] **Step 3: Implementation**

如果 Step 2 已 PASS，跳过本步；否则回到 Task 2 调整 `symlink.go` / `symlink_posix.go`。

- [ ] **Step 4: Run test to verify it passes**

```
go test ./internal/module/cliadapter/... -v -count=1
```
Expected: 4 个测试 PASS（1 跨平台 + 3 POSIX）。

- [ ] **Step 5: Commit**

```bash
git add internal/module/cliadapter/symlink_test.go internal/module/cliadapter/symlink_posix_test.go
git commit -m "test(cliadapter): cover SetupWorkspaceSkills POSIX paths"
```

---

## Task 4: 把 workspace symlink 接入 claudecli 子进程启动

**Files:**
- Modify: `internal/provider/claudecli/driver.go`（`prepareSessionStart` 中插入调用）
- Modify: `internal/provider/claudecli/module.go`（注入 `skilllibrary.Config` 拿 cacheDir）

- [ ] **Step 1: Write the failing test**

新建 `internal/provider/claudecli/driver_workspace_skills_test.go`:

```go
package claudecli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/cliadapter"
)

// TestSetupWorkspaceSkillsBeforeLaunch 验证 claudecli driver 在启动子进程前
// 会调用 cliadapter.SetupWorkspaceSkills，让 Claude CLI 看到共享缓存。
//
// 测试通过手工构造一个临时 workspace + cache 然后调用 prepareWorkspaceSkills(...)
// 验证 .claude/skills symlink 被建立指向 cache。
func TestSetupWorkspaceSkillsBeforeLaunch(t *testing.T) {
	workspace := t.TempDir()
	cache := t.TempDir()
	sentinel := filepath.Join(cache, "marker")
	if err := os.WriteFile(sentinel, []byte("seen"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := cliadapter.SetupWorkspaceSkills(workspace, cache); err != nil {
		t.Fatalf("SetupWorkspaceSkills: %v", err)
	}
	via := filepath.Join(workspace, ".claude", "skills", "marker")
	b, err := os.ReadFile(via)
	if err != nil {
		t.Fatalf("read via workspace symlink: %v", err)
	}
	if string(b) != "seen" {
		t.Errorf("read = %q, want seen", string(b))
	}
}
```

（注：因 driver.go 内部依赖较多，本测试只验证 `cliadapter.SetupWorkspaceSkills` 与 driver 同包可见可调用；端到端会在 Task 9 冒烟里覆盖。）

- [ ] **Step 2: Run test to verify it fails**

```
go test ./internal/provider/claudecli/... -run TestSetupWorkspaceSkillsBeforeLaunch -v
```

如果 cliadapter 已被 P2 wired 进 go.mod 路径（与 module.go 在同一仓），应直接 PASS。如失败说明 import 路径错。

- [ ] **Step 3: Implementation**

在 `internal/provider/claudecli/driver.go` 的 `prepareSessionStart` 函数（line 189 附近）插入：

```go
// 在 launchCLI 之前，把共享 skill 缓存 symlink 进 workspace 让 Claude native 发现
if d.skillCacheDir != "" {
    if err := cliadapter.SetupWorkspaceSkills(spec.cwd, d.skillCacheDir); err != nil {
        // fail-open：log but don't block session start。
        // 设计取舍：skill native 加载失败仅意味着用户无法触发自定义 skill；
        // 阻止整个 session 不合算。Phase 2/3 后端集成测试会监控成功率。
        d.logger.Warn("workspace skill symlink setup failed",
            "cwd", spec.cwd, "cache", d.skillCacheDir, "err", err)
    }
}

tr, cleanup, err := launchCLI(...)
```

修改 `driver` 结构体加一个字段 `skillCacheDir string` 和 `logger`（如果还没有就用项目现有的 logger 注入约定）。然后调整 `NewDriverFactory` / `New...` 接受 `skilllibrary.Config` 注入：

```go
// 修改 driver 构造，从 skilllibrary.Config 注入 CacheDir
type driverDeps struct {
    fx.In
    // ... existing fields
    SkillLibCfg skilllibrary.Config `optional:"true"`
}

func newDriver(deps driverDeps) *driver {
    d := &driver{
        // ... existing
        skillCacheDir: deps.SkillLibCfg.CacheDir, // 可能为空，下游会跳过
    }
    return d
}
```

如果 driver 已有现有 fx Param 注入结构，遵循它的命名约定。具体融入位置见现有 `claudecli/module.go:15-30`。

- [ ] **Step 4: Run test to verify it passes**

```
go test ./internal/provider/claudecli/... -v -count=1
go build ./...
```

新测试 + 全包测试 + 全项目编译，全绿。

- [ ] **Step 5: Commit**

```bash
git add internal/provider/claudecli/driver.go \
        internal/provider/claudecli/module.go \
        internal/provider/claudecli/driver_workspace_skills_test.go
git commit -m "feat(claudecli): wire workspace skill symlink before subprocess spawn"
```

---

## Task 5: 删除 skill_mcp_server.go + cmd/agent-terminal/mcp_skill_mode.go

**Files:**
- Delete: `internal/provider/claudecli/skill_mcp_server.go`
- Delete: `internal/provider/claudecli/skill_mcp_server_test.go`
- Delete: `cmd/agent-terminal/mcp_skill_mode.go`
- Modify: `cmd/agent-terminal/main.go`（如果显式 register `--mcp-skill-mode` flag，删除该 case）

- [ ] **Step 1: Verify no other callers**

```
cd /private/tmp/super-dolphin-skill-refactor-p2
echo "=== RunSkillMCPMode 调用 ===" && grep -rn "RunSkillMCPMode" --include="*.go" .
echo "=== --mcp-skill-mode flag ===" && grep -rn "mcp-skill-mode" --include="*.go" .
echo "=== LaunchKindSameBinarySkill ===" && grep -rn "LaunchKindSameBinarySkill\|appendSkillMCPServer" --include="*.go" .
```

记录所有命中位置；除了被删除的 3 个文件外，其他命中点都要清理（Task 8 会清 manifestbuilder）。

- [ ] **Step 2: Run test to verify state**

```
go test ./... -short 2>&1 | tail -10
```
记录当前测试通过/失败基线。

- [ ] **Step 3: Delete files + clean call sites**

```bash
rm internal/provider/claudecli/skill_mcp_server.go
rm internal/provider/claudecli/skill_mcp_server_test.go
rm cmd/agent-terminal/mcp_skill_mode.go
```

修改 `cmd/agent-terminal/main.go`：找到 `--mcp-skill-mode` 的 dispatch case（如果存在）并删除整个 case 分支。如果 main.go 只 import 了 mcp_skill_mode 包却没显式 dispatch，则只需删除 import 行。

- [ ] **Step 4: Run test to verify it passes**

```
go build ./...
go test ./internal/provider/claudecli/... -v -count=1
go test ./cmd/agent-terminal/... -short -count=1
```

build + 测试都必须通过。如有编译错误，定位 import 残留并清掉。

- [ ] **Step 5: Commit**

```bash
git add -A internal/provider/claudecli/ cmd/agent-terminal/
git commit -m "refactor(claudecli): delete skill MCP host RPC server (~600 LoC)

Removed:
- internal/provider/claudecli/skill_mcp_server.go (~360 lines)
- internal/provider/claudecli/skill_mcp_server_test.go
- cmd/agent-terminal/mcp_skill_mode.go (--mcp-skill-mode subprocess entry)
- main.go dispatch case for --mcp-skill-mode

Skill body delivery now uses Anthropic native discovery via
<workspace>/.claude/skills symlink (Task 4)."
```

---

## Task 6: 删除 DetectNativeSkills + composite native detector

**Files:**
- Modify: `internal/provider/claudecli/skill_inject.go`（删 `DetectNativeSkills` + `NewSkillInjectionPort`）
- Modify: `internal/provider/claudecli/module.go`（删 `NewSkillInjectionPort` 注册）
- Delete: `internal/provider/claudecli/skill_injection_test.go`（整文件，全是 DetectNativeSkills 测试）
- Modify: `internal/module/prompt/module.go`（删 `compositeNativeSkillDetector` + `NativeSkillDetector` 类型 + 接受这个类型的 dep）
- Modify: `internal/module/prompt/skill_catalog_provider.go`（删 `collectNativeNames` 方法 + nativePort 字段；Task 7 会整体删除此文件，本任务可仅做最少修改让 Task 7 之前不破编译）

- [ ] **Step 1: Failing-state baseline**

```
cd /private/tmp/super-dolphin-skill-refactor-p2
grep -rn "DetectNativeSkills\|NewSkillInjectionPort\|NativeSkillDetector\|compositeNativeSkillDetector\|SkillInjectionPort" --include="*.go" . | head -30
```

记录所有使用点。

- [ ] **Step 2: Apply edits sequentially**

a. `internal/provider/claudecli/skill_inject.go` — 删除整个文件（如果文件除了 DetectNativeSkills 和 NewSkillInjectionPort 没别的）：

```bash
# 先看文件还有什么
cat internal/provider/claudecli/skill_inject.go
```

如果文件只含 DetectNativeSkills + NewSkillInjectionPort + 相关常量（如 `claudeNativeSkillsDir`），直接删除整文件：

```bash
rm internal/provider/claudecli/skill_inject.go
rm internal/provider/claudecli/skill_injection_test.go
```

b. `internal/provider/claudecli/module.go` — 删除 `NewSkillInjectionPort` 的 `fx.Provide` 行（line 18-27 区域）。

c. `internal/module/prompt/module.go` — 删除：
- `compositeNativeSkillDetector` 类型 + 方法（lines 58-95）
- `NativeSkillDetector` interface 类型（如果在此文件）
- `skillCatalogProviderDeps` 中 `Detector NativeSkillDetector` 字段
- `RegisterSkillCatalogProviderIfEnabled` 中关于 native detector 的 wiring

具体行号以现 grep 结果为准。

d. `internal/module/prompt/skill_catalog_provider.go` — 暂时保留文件，但删除：
- `nativePort *NativeSkillDetector` 字段
- `collectNativeNames` 方法
- 所有调用 `collectNativeNames` 的引用

（Task 7 会把整个文件删掉，本任务只做让编译过即可。）

- [ ] **Step 3: Run tests**

```
go build ./...
go test ./internal/provider/claudecli/... -short -count=1
go test ./internal/module/prompt/... -short -count=1
```

build 必须过；prompt 测试可能因 catalog provider 未完全清理而部分 skip — 没关系，Task 7 会全删。

- [ ] **Step 4: Commit**

```bash
git add -A internal/provider/claudecli/ internal/module/prompt/
git commit -m "refactor(prompt,claudecli): remove DetectNativeSkills + composite detector

Native skill discovery is now handled by Claude CLI itself via the
workspace symlink (Phase 2 Task 4). The runtime no longer needs to
enumerate native skills or emit a 'Native' group in the L1 manifest.

Removed:
- internal/provider/claudecli/skill_inject.go (DetectNativeSkills + NewSkillInjectionPort)
- internal/provider/claudecli/skill_injection_test.go
- internal/module/prompt/module.go: compositeNativeSkillDetector + NativeSkillDetector interface
- skill_catalog_provider.go: nativePort field + collectNativeNames method (file shrinks further in Task 7)"
```

---

## Task 7: 缩 / 删除 skill_catalog_provider.go

**Files:**
- Delete: `internal/module/prompt/skill_catalog_provider.go`
- Delete: `internal/module/prompt/skill_catalog_provider_test.go`
- Delete: `internal/module/prompt/skill_catalog_fx_test.go`
- Modify: `internal/module/prompt/module.go`（删除 `NewSkillCatalogProviderFx`、`RegisterSkillCatalogProviderIfEnabled`、`skillCatalogProviderDeps`）
- Modify: `internal/module/prompt/dynamic.go`（如有 catalog provider 引用，移除）
- Modify: `internal/module/prompt/assembler.go`（line 391 注释中的 SkillCatalogProvider 引用）

理由：Claude 通过 native discovery + native Read 自己处理 L1 元数据展示，runtime 完全不需要 SkillCatalogProvider。Codex 端的 L1 渲染会在 P3 用全新的 L1-C `buildSkillManifest` 实现，不复用 catalog provider。

- [ ] **Step 1: Failing-state baseline**

```
grep -rn "SkillCatalogProvider\|NewSkillCatalogProvider\|RegisterSkillCatalogProviderIfEnabled\|skillCatalogProviderDeps" --include="*.go" .
```

记录使用点。

- [ ] **Step 2: Delete + clean**

```bash
rm internal/module/prompt/skill_catalog_provider.go
rm internal/module/prompt/skill_catalog_provider_test.go
rm internal/module/prompt/skill_catalog_fx_test.go
```

修改 `internal/module/prompt/module.go`：
- 删除 `fx.Provide(NewSkillCatalogProviderFx)` 行
- 删除 `fx.Invoke(RegisterSkillCatalogProviderIfEnabled)` 行
- 删除 `NewSkillCatalogProviderFx` 函数
- 删除 `RegisterSkillCatalogProviderIfEnabled` 函数
- 删除 `skillCatalogProviderDeps` / `registerSkillCatalogDeps` 结构体

修改 `internal/module/prompt/dynamic.go`：grep 找 catalog provider 引用，删除。

修改 `internal/module/prompt/assembler.go:391`：删除注释中的 SkillCatalogProvider 引用（代码注释，无功能影响）。

可能受影响的测试：
- `internal/module/prompt/boundary_invariants_test.go` — 如全文都是 catalog 测试，整文件删除；如有其他 boundary 测试，仅删 catalog 部分。
- 其他 prompt 测试若引用 catalog 类型，需调整或删除。

- [ ] **Step 3: Run tests**

```
go build ./...
go test ./internal/module/prompt/... -v -count=1
```

build 必须过；prompt 测试全绿。

- [ ] **Step 4: Commit**

```bash
git add -A internal/module/prompt/
git commit -m "refactor(prompt): delete SkillCatalogProvider entirely (~400 LoC)

Native discovery in Claude CLI now handles L1 skill metadata; runtime
no longer assembles a 'Available Skills' manifest section. Codex-side
L1 will be implemented in Phase 3 as a fresh buildSkillManifest, not
reusing this catalog provider.

Removed:
- skill_catalog_provider.go (~587 lines)
- skill_catalog_provider_test.go
- skill_catalog_fx_test.go
- module.go: NewSkillCatalogProviderFx, RegisterSkillCatalogProviderIfEnabled,
  skillCatalogProviderDeps, registerSkillCatalogDeps
- assembler.go:391 stale comment reference"
```

---

## Task 8: 移除 manifestbuilder.appendSkillMCPServer

**Files:**
- Modify: `internal/provider/manifestbuilder/manifest.go`（删 `appendSkillMCPServer` 函数 + 调用点）
- Modify: 任何 `LaunchKindSameBinarySkill` 引用

- [ ] **Step 1: Find references**

```
grep -rn "appendSkillMCPServer\|LaunchKindSameBinarySkill\|MCPEnvSkillCWD" --include="*.go" .
```

- [ ] **Step 2: Delete function + caller**

修改 `internal/provider/manifestbuilder/manifest.go`：
- 删除 `appendSkillMCPServer` 函数（line 66-88 区域）
- 删除调用 `appendSkillMCPServer` 的行
- 如果 `LaunchKindSameBinarySkill` 常量仅本场景使用，删除常量定义
- 如果 `MCPEnvSkillCWD` / `MCPEnvSkillAgentID` / `MCPEnvSkillThreadID` 等只用于此场景，删除它们

注意：`LaunchKindSameBinarySkill` 可能与其他 launch kind 共用类型；只删此常量值，不动类型。

- [ ] **Step 3: Run tests**

```
go build ./...
go test ./internal/provider/manifestbuilder/... -short -count=1
go test ./... -short
```

- [ ] **Step 4: Commit**

```bash
git add -A internal/provider/manifestbuilder/ internal/dto/
git commit -m "refactor(manifestbuilder): remove appendSkillMCPServer

Skill MCP host RPC was deleted in Task 5; the manifest no longer needs
to register a same-binary skill MCP entry. Native discovery via workspace
symlink (Task 4) replaces this path entirely."
```

---

## Task 9: P2 全测试 + Claude subprocess 冒烟

**Files:** 仅运行测试，不修改代码

- [ ] **Step 1: 两个新包 + 修改包的单元测试**

```
cd /private/tmp/super-dolphin-skill-refactor-p2
go test ./internal/module/cliadapter/... \
        ./internal/module/skilllibrary/... \
        ./internal/module/skillforge/... \
        ./internal/provider/claudecli/... \
        ./internal/module/prompt/... \
        ./internal/provider/manifestbuilder/... -v -count=1 2>&1 | tail -80
```

期望：所有 PASS。

- [ ] **Step 2: 全项目测试**

```
go test ./... -short 2>&1 | tail -30
```

期望：全 PASS（如有 pre-existing 失败但与 P2 无关，记录但不阻塞）。

- [ ] **Step 3: 全项目编译**

```
go build ./...
```

期望：成功。

- [ ] **Step 4: 端到端冒烟**

写一次性冒烟脚本，模拟 fx 启动 + workspace setup + 验证产物：

```bash
TMP=$(mktemp -d)
cat > "$TMP/main.go" <<'EOF'
package main

import (
  "context"
  "fmt"
  "os"

  "go.uber.org/fx"

  "github.com/anthropic-ai/super-agent-v3/internal/module/cliadapter"
  "github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
  "github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

func main() {
  workspace := os.Args[1]
  cache := os.Args[2]
  lib := os.Args[3]

  cfg := skilllibrary.Config{
    LibraryDir: lib,
    CacheDir: cache,
    HarnessVersion: "smoke",
  }

  app := fx.New(
    fx.NopLogger,
    skillforge.Module,
    skilllibrary.Module,
    cliadapter.Module,
    fx.Provide(func() skilllibrary.Config { return cfg }),
  )
  if err := app.Start(context.Background()); err != nil {
    fmt.Println("start err:", err); os.Exit(1)
  }
  defer app.Stop(context.Background())

  if err := cliadapter.SetupWorkspaceSkills(workspace, cache); err != nil {
    fmt.Println("setup err:", err); os.Exit(1)
  }

  // 验证 workspace/.claude/skills/<name>/SKILL.md 可读
  entries, _ := os.ReadDir(workspace + "/.claude/skills")
  fmt.Println("workspace skill count:", len(entries))
  if len(entries) > 0 {
    sample := entries[0].Name()
    fmt.Println("sample skill:", sample)
    skillMD, err := os.ReadFile(workspace + "/.claude/skills/" + sample + "/SKILL.md")
    if err != nil {
      fmt.Println("read SKILL.md err:", err); os.Exit(1)
    }
    fmt.Printf("SKILL.md head: %.80s...\n", string(skillMD))
  }
}
EOF

mkdir -p "$TMP/ws" "$TMP/cache" "$TMP/lib"
cd /private/tmp/super-dolphin-skill-refactor-p2
go run "$TMP/main.go" "$TMP/ws" "$TMP/cache" "$TMP/lib"

ls "$TMP/ws/.claude/skills/" | head -5
echo "---"
ls -la "$TMP/ws/.claude/skills" # 验证是 symlink
echo "---"
rm -rf "$TMP"
```

期望输出：
- `workspace skill count: 14`
- `sample skill: <某个 skill 名>`
- `SKILL.md head: ---\nname: ... description: ...`
- `.claude/skills` 是 symlink 指向 `<TMP>/cache`

- [ ] **Step 5: 不需要 commit；冒烟验证完毕进入 Final review**

如冒烟产物有问题（缺文件、symlink 失效），回到对应 Task 修测试 + 实现。

---

## Phase 2 自审

按 编写计划 技能 §自审：

**1. 规格覆盖：** 对照 spec §6 (Claude 适配) + §11 (删除清单) + §12 (Phase 2):
- §6.1 整目录 symlink B+L1 → Task 2-4
- §6.3 删除项：skill_mcp_server.go (~600) + DetectNativeSkills (~150) + skill_catalog_provider.go 大部 (~400) → Task 5-7 全覆盖
- §3.3 Z' 触发器 startup 全量对账 → Task 1 fx.Invoke
- §11 删除清单累计 ~1150 + 50 (manifest) = ~1200 行 → Task 5-8 覆盖
- §12 P2 = "workspace symlink；删 mcp server / DetectNativeSkills；缩 catalog provider" → 完整对应 Task 1-8

**未覆盖项**（明确延后）：
- Windows symlink 实测 → P5/P6 真上 Windows CI 时验证
- Reconciler 的 ReadDir 错误上报（P1 final review OBS）→ 顺手 cleanup
- SkillRef.Mode / SKILL_WRITER_FORMAT 删除 → P3+P4 范畴
- L1 manifest Codex 形态 → P3

**2. 占位符扫描：** 任务内代码段为完整可编译实现；唯一占位符是 Task 4 中 `d.logger.Warn(...)` —— 假设 driver 已有 logger 字段，若实际没有，Task 4 实施时由 implementer 调整为 `log.Printf` 或类似。

**3. 类型一致性：**
- `SetupWorkspaceSkills(workspaceDir, cacheDir string)` 跨 Task 2/3/4/9 一致
- `skilllibrary.Config{LibraryDir, CacheDir, HarnessVersion}` 跨 Task 1/4/9 一致
- `runStartup(lc fx.Lifecycle, store *Store, reconciler *Reconciler, cfg Config)` 在 Task 1 内一致
- 删除 `DetectNativeSkills` 后无残留引用（Task 6 + 7 + 8 接力清干净）

修复内联：暂无问题。

---

## 执行交接

计划已完成并保存到 `docs/superpowers/plans/2026-04-29-skill-refactor-p2-claude-cutover.md`。两个执行选项：

1. **子代理驱动（推荐）** —— 控制者为每个任务派发新子代理，任务间审查
2. **当前会话内执行** —— 使用 执行计划 在当前会话执行任务，按批次执行并设置检查点
