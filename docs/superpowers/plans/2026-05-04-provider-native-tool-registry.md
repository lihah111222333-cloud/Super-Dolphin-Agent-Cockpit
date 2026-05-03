# Provider 自注册原生工具 实现计划

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把原生工具清单从 uistate 包的硬编码 `builtinToolRegistry` 改为 Provider 自注册模式，支持多模型（Claude、Codex、未来模型）。

**Architecture:** 在 `contract.DriverFactory` 上增加 `NativeTools` 字段，每个 provider（claudecli、codexapp）在自己的 Module 初始化时声明自己的原生工具。`unified.Registry` 聚合所有 provider 的工具，通过 fx 桥接传给 `uistate` 和 `prompt` 模块。uistate 删除硬编码注册表，改为消费注入的工具列表。

**Tech Stack:** Go (fx DI), Vue 3 (TypeScript)

**Spec:** 头脑风暴讨论确认方案 A（Provider 自注册）+ 方案 B（现有 prompt 通用指令兜底）

---

## File Map

| 操作 | 文件 | 职责 |
|------|------|------|
| Modify | `internal/contract/provider.go:17-21` | 新增 `NativeToolDescriptor` 类型 + `DriverFactory.NativeTools` 字段 |
| Modify | `internal/provider/claudecli/module.go:30-43` | 声明 Claude 14 个原生工具 |
| Modify | `internal/provider/codexapp/driver.go:121-126` | 声明 Codex 原生工具 |
| Modify | `internal/provider/unified/registry.go:11-56` | 新增 `NativeTools()` 聚合方法 |
| Modify | `internal/app/modules.go:84-107` | 桥接函数：从 Registry 提取工具列表，注入 uistate 和 prompt |
| Modify | `internal/module/uistate/builtin_tools.go` | 删除硬编码注册表，所有函数改为接收注入的工具列表 |
| Modify | `internal/module/uistate/config_rpc.go:68-92` | `NewConfigHandlers` 签名增加 `[]contract.NativeToolDescriptor` |
| Modify | `internal/module/uistate/module.go:44-48` | `NewConfigHandlers` ParamTags 更新 |
| Modify | `internal/module/uistate/builtin_tools_test.go` | 更新测试使用注入工具列表，删除 ProviderNotes 测试 |
| Modify | `internal/module/uistate/config_rpc_test.go:169-178` | 更新 `newConfigTestServer` helper |
| Modify | `cmd/agent-terminal/frontend/vue-app/builtin-tools-settings.behavior.test.js` | 删除 providerNotes 相关断言，新增多 provider 工具测试 |

---

### Task 1: contract 层定义 NativeToolDescriptor

**Files:**
- Modify: `internal/contract/provider.go:17-21`

- [ ] **Step 1: 编写失败测试**

在 `internal/contract/provider_test.go` 中新建：

```go
package contract

import "testing"

func TestDriverFactoryNativeToolsField(t *testing.T) {
	factory := DriverFactory{
		Name:   "test",
		Create: func() Driver { return nil },
		NativeTools: []NativeToolDescriptor{
			{ID: "Read", Label: "读文件", Description: "读取文件", DefaultDisabled: true, Provider: "test"},
		},
	}
	if len(factory.NativeTools) != 1 {
		t.Fatalf("NativeTools len = %d, want 1", len(factory.NativeTools))
	}
	if factory.NativeTools[0].ID != "Read" {
		t.Fatalf("NativeTools[0].ID = %q, want %q", factory.NativeTools[0].ID, "Read")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/contract/... -run TestDriverFactoryNativeToolsField -v`
Expected: FAIL（`NativeToolDescriptor` 和 `NativeTools` 字段不存在）

- [ ] **Step 3: 实现**

在 `internal/contract/provider.go` 中，在 `DriverFactory` 之前添加类型定义，并在 `DriverFactory` 上加字段：

```go
// NativeToolDescriptor describes an upstream CLI built-in tool that the
// settings UI can render and the prompt layer can suppress.
type NativeToolDescriptor struct {
	ID              string
	Label           string
	Description     string
	DefaultDisabled bool
	Provider        string
}

// DriverFactory constructs Driver instances for DI registration.
type DriverFactory struct {
	Name        string
	Create      func() Driver
	NativeTools []NativeToolDescriptor
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/contract/... -run TestDriverFactoryNativeToolsField -v`
Expected: PASS

- [ ] **Step 5: 编译全局确认无破坏**

Run: `go build ./...`
Expected: SUCCESS（新增字段是可选的，现有代码不受影响）

- [ ] **Step 6: 提交**

```bash
git add internal/contract/provider.go internal/contract/provider_test.go
git commit -m "feat(contract): add NativeToolDescriptor type and DriverFactory.NativeTools field"
```

---

### Task 2: Claude provider 声明原生工具

**Files:**
- Modify: `internal/provider/claudecli/module.go:30-43`

- [ ] **Step 1: 在 NewDriverFactory 中声明 NativeTools**

修改 `internal/provider/claudecli/module.go` 的 `NewDriverFactory` 函数，在返回的 `contract.DriverFactory` 中添加 `NativeTools` 字段：

```go
func NewDriverFactory(p driverFactoryParams) contract.DriverFactory {
	if p.Tracker != nil {
		SetFBSDRecorder(p.Tracker.Record)
	}
	return contract.DriverFactory{
		Name: "claude",
		Create: func() contract.Driver {
			return newDriver(p.Logger, p.Dispatcher, p.Reporter, p.Reg, p.ProxyAddrFn, p.SkillLibConfig.CacheDir)
		},
		NativeTools: []contract.NativeToolDescriptor{
			{ID: "Read", Label: "读文件", Description: "上游 Agent 直接读取工作区文件", DefaultDisabled: true, Provider: "claude"},
			{ID: "Write", Label: "写文件", Description: "上游 Agent 直接写入新文件", DefaultDisabled: true, Provider: "claude"},
			{ID: "Edit", Label: "编辑文件", Description: "上游 Agent 直接修改现有文件", DefaultDisabled: true, Provider: "claude"},
			{ID: "MultiEdit", Label: "批量编辑", Description: "一次调用内批量修改多个位置", DefaultDisabled: true, Provider: "claude"},
			{ID: "Bash", Label: "执行命令", Description: "在本地 shell 中执行任意命令", DefaultDisabled: true, Provider: "claude"},
			{ID: "Grep", Label: "代码搜索", Description: "使用上游内置 grep 在工作区查找", DefaultDisabled: true, Provider: "claude"},
			{ID: "Glob", Label: "文件匹配", Description: "按 glob 模式列出匹配文件", DefaultDisabled: true, Provider: "claude"},
			{ID: "LS", Label: "列目录", Description: "列出目录内容", DefaultDisabled: true, Provider: "claude"},
			{ID: "WebFetch", Label: "抓取网页", Description: "按 URL 拉取网页内容", DefaultDisabled: false, Provider: "claude"},
			{ID: "WebSearch", Label: "网页搜索", Description: "调用内置网页搜索", DefaultDisabled: false, Provider: "claude"},
			{ID: "TodoWrite", Label: "待办记录", Description: "写入上游自带的任务清单", DefaultDisabled: false, Provider: "claude"},
			{ID: "NotebookEdit", Label: "Notebook 编辑", Description: "编辑 Jupyter Notebook", DefaultDisabled: false, Provider: "claude"},
			{ID: "Task", Label: "派生子 Agent", Description: "派生子 Agent 执行任务", DefaultDisabled: false, Provider: "claude"},
			{ID: "ExitPlanMode", Label: "退出计划模式", Description: "离开 Plan Mode 审批界面", DefaultDisabled: false, Provider: "claude"},
		},
	}
}
```

- [ ] **Step 2: 编译确认**

Run: `go build ./internal/provider/claudecli/...`
Expected: SUCCESS

- [ ] **Step 3: 提交**

```bash
git add internal/provider/claudecli/module.go
git commit -m "feat(claudecli): declare Claude native tools on DriverFactory"
```

---

### Task 3: Codex provider 声明原生工具

**Files:**
- Modify: `internal/provider/codexapp/driver.go:121-126`

Codex 的 `DriverFactory` 是自定义 struct（内嵌 `contract.DriverFactory`）。`contract.DriverFactory` 的赋值在 `driver.go:121-126`：

```go
// 现状
factory.DriverFactory = contract.DriverFactory{
    Name: "codex",
    Create: func() contract.Driver { ... },
}
```

`module.go:71-76` 的 `provideContractDriverFactory` 只是 `return factory.DriverFactory`，不要改那里。

- [ ] **Step 1: 在 driver.go 的 NewDriverFactory 中声明 NativeTools**

修改 `internal/provider/codexapp/driver.go` 的 `NewDriverFactory` 函数（line 121-126）：

```go
factory.DriverFactory = contract.DriverFactory{
	Name: "codex",
	Create: func() contract.Driver {
		return newDriver(logger, dispatcher, approvals, reporter, manager, pool, factory.skillStore, factory.tracker, factory.currentListTools())
	},
	NativeTools: []contract.NativeToolDescriptor{
		{ID: "read_file", Label: "读文件", Description: "Codex 内置读文件", DefaultDisabled: true, Provider: "codex"},
		{ID: "write_new_file", Label: "写新文件", Description: "Codex 内置写新文件", DefaultDisabled: true, Provider: "codex"},
		{ID: "apply_patch", Label: "应用补丁", Description: "Codex 内置 apply_patch 修改文件", DefaultDisabled: true, Provider: "codex"},
		{ID: "shell", Label: "执行命令", Description: "Codex 内置 shell 执行", DefaultDisabled: true, Provider: "codex"},
		{ID: "list_dir", Label: "列目录", Description: "Codex 内置列目录", DefaultDisabled: true, Provider: "codex"},
	},
}
```

> **注意**：Codex 工具 ID 需与 Codex CLI 实际暴露的工具名完全一致。实施前先在 Codex 会话中确认实际工具名列表。如有出入，以实际名称为准。

- [ ] **Step 2: 编译确认**

Run: `go build ./internal/provider/codexapp/...`
Expected: SUCCESS

- [ ] **Step 3: 提交**

```bash
git add internal/provider/codexapp/driver.go
git commit -m "feat(codexapp): declare Codex native tools on DriverFactory"
```

---

### Task 4: unified.Registry 聚合 NativeTools

**Files:**
- Modify: `internal/provider/unified/registry.go:11-56`
- Create: `internal/provider/unified/registry_test.go`（如不存在）

- [ ] **Step 1: 编写失败测试**

在 `internal/provider/unified/registry_test.go` 中：

```go
package unified

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestRegistryNativeToolsAggregatesAcrossDrivers(t *testing.T) {
	reg := NewRegistry(RegistryParams{
		Drivers: []contract.DriverFactory{
			{
				Name:   "claude",
				Create: func() contract.Driver { return nil },
				NativeTools: []contract.NativeToolDescriptor{
					{ID: "Read", Provider: "claude"},
					{ID: "Bash", Provider: "claude"},
				},
			},
			{
				Name:   "codex",
				Create: func() contract.Driver { return nil },
				NativeTools: []contract.NativeToolDescriptor{
					{ID: "read_file", Provider: "codex"},
					{ID: "shell", Provider: "codex"},
				},
			},
		},
	})
	tools := reg.NativeTools()
	if len(tools) != 4 {
		t.Fatalf("NativeTools() len = %d, want 4", len(tools))
	}
}

func TestRegistryNativeToolsPreservesOrder(t *testing.T) {
	reg := NewRegistry(RegistryParams{
		Drivers: []contract.DriverFactory{
			{
				Name:   "a",
				Create: func() contract.Driver { return nil },
				NativeTools: []contract.NativeToolDescriptor{
					{ID: "tool1", Provider: "a"},
				},
			},
			{
				Name:   "b",
				Create: func() contract.Driver { return nil },
				NativeTools: []contract.NativeToolDescriptor{
					{ID: "tool2", Provider: "b"},
				},
			},
		},
	})
	tools := reg.NativeTools()
	if tools[0].ID != "tool1" || tools[1].ID != "tool2" {
		t.Fatalf("order not preserved: got [%s, %s]", tools[0].ID, tools[1].ID)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/provider/unified/... -run TestRegistryNativeTools -v`
Expected: FAIL（`NativeTools()` 方法不存在）

- [ ] **Step 3: 实现**

在 `internal/provider/unified/registry.go` 的 `Registry` struct 中加字段和方法：

```go
type Registry struct {
	drivers     map[string]contract.DriverFactory
	nativeTools []contract.NativeToolDescriptor
}

func NewRegistry(params RegistryParams) *Registry {
	drivers := make(map[string]contract.DriverFactory, len(params.Drivers))
	var nativeTools []contract.NativeToolDescriptor
	for _, factory := range params.Drivers {
		name := normalizeProviderName(factory.Name)
		if name == "" || factory.Create == nil {
			continue
		}
		drivers[name] = factory
		nativeTools = append(nativeTools, factory.NativeTools...)
	}
	return &Registry{drivers: drivers, nativeTools: nativeTools}
}

// NativeTools returns the aggregated native tool descriptors from all
// registered providers. Order follows provider registration order.
func (r *Registry) NativeTools() []contract.NativeToolDescriptor {
	if r == nil {
		return nil
	}
	out := make([]contract.NativeToolDescriptor, len(r.nativeTools))
	copy(out, r.nativeTools)
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/provider/unified/... -run TestRegistryNativeTools -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/provider/unified/registry.go internal/provider/unified/registry_test.go
git commit -m "feat(unified): aggregate NativeTools across registered providers"
```

---

### Task 5: uistate 改为接收注入的工具列表

**Files:**
- Modify: `internal/module/uistate/builtin_tools.go`
- Modify: `internal/module/uistate/config_rpc.go:68-92`
- Modify: `internal/module/uistate/module.go:44-48`

这是核心改造任务。删除硬编码 `builtinToolRegistry`，所有函数改为使用注入的 `[]contract.NativeToolDescriptor`。

- [ ] **Step 1: 修改 NewConfigHandlers 签名**

在 `internal/module/uistate/config_rpc.go` 中，给 `NewConfigHandlers` 增加 `nativeTools []contract.NativeToolDescriptor` 参数：

```go
func NewConfigHandlers(
	cfg *platformconfig.Config,
	prefs uipreference.Store,
	sharedFiles sharedfilestore.Reader,
	threads thread.Service,
	skillStore *skilllibrary.Store,
	nativeTools []contract.NativeToolDescriptor,
) rpc.HandlerMapResult {
	toolIndex := buildNativeToolIndex(nativeTools)
	return rpc.HandlerMapResult{Handlers: handler.Map{
		// ...existing handlers unchanged...
		"config/builtinTools/read": rpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
			return readBuiltinTools(ctx, prefs, skillStore, nativeTools, toolIndex, p.Cwd)
		}),
		"config/builtinTools/write": rpc.StrictHandler(func(ctx context.Context, p builtinToolsWriteParams) (any, error) {
			return writeBuiltinTool(ctx, prefs, skillStore, nativeTools, toolIndex, p)
		}),
	}}
}
```

在 `internal/module/uistate/module.go` 中更新 ParamTags（第 5 个参数 skillStore 保持 optional，第 6 个参数 nativeTools 由 fx.Provide 始终提供，不需要 optional）：

```go
fx.Provide(fx.Annotate(
	NewConfigHandlers,
	fx.ParamTags("", "", "", "", `optional:"true"`, ""),
)),
```

- [ ] **Step 2: 重构 builtin_tools.go**

在 `internal/module/uistate/builtin_tools.go` 中：

1. 删除 `builtinToolRegistry` 变量（整个 var 块，line 45-60）
2. 删除 `builtinToolIndex` 变量（line 64-70）
3. 删除 `BuiltinToolProviderClaude`、`BuiltinToolProviderCodex` 常量（line 23-26）
4. 删除 `BuiltinToolDescriptor` 类型（line 31-40，改用 `contract.NativeToolDescriptor`）
5. 删除 `builtinToolProviderNotes` 变量和 `BuiltinToolProviderNote` 类型（line 86-112）
6. 在 import 中加 `"github.com/anthropic-ai/super-agent-v3/internal/contract"`

新增 `buildNativeToolIndex` 函数：

```go
func buildNativeToolIndex(tools []contract.NativeToolDescriptor) map[string]contract.NativeToolDescriptor {
	out := make(map[string]contract.NativeToolDescriptor, len(tools))
	for _, item := range tools {
		out[item.ID] = item
	}
	return out
}
```

修改 `readBuiltinTools`：

```go
func readBuiltinTools(
	ctx context.Context,
	prefs uipreference.Store,
	store *skilllibrary.Store,
	registry []contract.NativeToolDescriptor,
	index map[string]contract.NativeToolDescriptor,
	cwd string,
) (*builtinToolsReadResult, error) {
	var replaced map[string]string
	if store != nil {
		entries, err := store.List()
		if err == nil {
			replaced = aggregateReplacementSources(entries)
		}
	}
	disabled, err := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd, registry, index)
	if err != nil {
		return nil, err
	}
	tools := make([]BuiltinToolView, 0, len(registry))
	for _, item := range registry {
		skillName := replaced[item.ID]
		_, isDisabled := disabled[item.ID]
		tools = append(tools, BuiltinToolView{
			ID:          item.ID,
			Label:       item.Label,
			Description: item.Description,
			Enabled:     !isDisabled && skillName == "",
			Provider:    item.Provider,
			ReplacedBy:  skillName,
		})
	}
	return &builtinToolsReadResult{Tools: tools}, nil
}
```

注意：`builtinToolsReadResult` 中删除 `ProviderNotes` 字段（不再需要静态说明卡片，Codex 工具已在注册表中）。

修改 `writeBuiltinTool`：

```go
func writeBuiltinTool(
	ctx context.Context,
	prefs uipreference.Store,
	store *skilllibrary.Store,
	registry []contract.NativeToolDescriptor,
	index map[string]contract.NativeToolDescriptor,
	p builtinToolsWriteParams,
) (*builtinToolsReadResult, error) {
	if prefs == nil {
		return nil, errConfigPreferenceStoreRequired
	}
	id := strings.TrimSpace(p.ID)
	if _, ok := index[id]; !ok {
		return nil, errUnknownBuiltinTool
	}
	current, err := effectiveDisabledBuiltinToolSet(ctx, prefs, p.Cwd, registry, index)
	if err != nil {
		return nil, err
	}
	if p.Enabled {
		delete(current, id)
	} else {
		current[id] = struct{}{}
	}
	if err := storeDisabledBuiltinToolSet(ctx, prefs, p.Cwd, current); err != nil {
		return nil, err
	}
	return readBuiltinTools(ctx, prefs, store, registry, index, p.Cwd)
}
```

修改 `ResolveDisabledBuiltinTools`：

```go
func ResolveDisabledBuiltinTools(
	ctx context.Context,
	prefs uipreference.Store,
	cwd string,
	registry []contract.NativeToolDescriptor,
	index map[string]contract.NativeToolDescriptor,
) []string {
	disabled, err := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd, registry, index)
	if err != nil {
		disabled = defaultDisabledBuiltinToolSet(registry)
	}
	out := make([]string, 0, len(disabled))
	for id := range disabled {
		if _, ok := index[id]; ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
```

修改 `effectiveDisabledBuiltinToolSet`：

```go
func effectiveDisabledBuiltinToolSet(
	ctx context.Context,
	prefs uipreference.Store,
	cwd string,
	registry []contract.NativeToolDescriptor,
	index map[string]contract.NativeToolDescriptor,
) (map[string]struct{}, error) {
	stored, present, err := loadStoredDisabledBuiltinToolSet(ctx, prefs, cwd, index)
	if err != nil {
		return nil, err
	}
	if !present {
		return defaultDisabledBuiltinToolSet(registry), nil
	}
	return stored, nil
}
```

修改 `defaultDisabledBuiltinToolSet`：

```go
func defaultDisabledBuiltinToolSet(registry []contract.NativeToolDescriptor) map[string]struct{} {
	out := make(map[string]struct{})
	for _, item := range registry {
		if item.DefaultDisabled {
			out[item.ID] = struct{}{}
		}
	}
	return out
}
```

修改 `loadStoredDisabledBuiltinToolSet`：

```go
func loadStoredDisabledBuiltinToolSet(
	ctx context.Context,
	prefs uipreference.Store,
	cwd string,
	index map[string]contract.NativeToolDescriptor,
) (map[string]struct{}, bool, error) {
	if prefs == nil {
		return nil, false, nil
	}
	raw, err := prefs.GetValue(ctx, strings.TrimSpace(cwd), builtinToolsDisabledKey)
	switch {
	case err == nil:
	case platformdb.IsNotFound(err):
		return nil, false, nil
	default:
		return nil, false, err
	}
	value := decodePreferenceValue(raw)
	ids, ok := value.([]any)
	if !ok {
		return nil, false, nil
	}
	out := make(map[string]struct{}, len(ids))
	for _, entry := range ids {
		id, _ := entry.(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, known := index[id]; !known {
			continue
		}
		out[id] = struct{}{}
	}
	return out, true, nil
}
```

- [ ] **Step 3: 编译确认**

Run: `go build ./internal/module/uistate/...`
Expected: SUCCESS（可能会有 test 编译失败，下一个 Task 修复）

- [ ] **Step 4: 提交**

```bash
git add internal/module/uistate/builtin_tools.go internal/module/uistate/config_rpc.go internal/module/uistate/module.go
git commit -m "refactor(uistate): replace hardcoded builtinToolRegistry with injected NativeToolDescriptor slice"
```

---

### Task 6: app/modules.go 桥接

**Files:**
- Modify: `internal/app/modules.go:84-107`

- [ ] **Step 1: 添加桥接函数并更新 provideDisabledBuiltinToolsFn**

在 `internal/app/modules.go` 中：

1. 在 import 中加 `"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"`

2. 添加提供 `[]contract.NativeToolDescriptor` 的桥接函数：

```go
func provideNativeToolDescriptors(registry *unified.Registry) []contract.NativeToolDescriptor {
	if registry == nil {
		return nil
	}
	return registry.NativeTools()
}
```

3. 修改 `provideDisabledBuiltinToolsFn`，让它也接收注入的工具列表：

```go
func provideDisabledBuiltinToolsFn(prefs uipreference.Store, tools []contract.NativeToolDescriptor) prompt.DisabledBuiltinToolsFn {
	index := make(map[string]contract.NativeToolDescriptor, len(tools))
	for _, t := range tools {
		index[t.ID] = t
	}
	return func(ctx context.Context, cwd string) []string {
		return uistate.ResolveDisabledBuiltinTools(ctx, prefs, cwd, tools, index)
	}
}
```

4. 在 `Module` 的 `fx.Provide` 块中注册桥接函数：

```go
fx.Provide(
	AsRPCRunner,
	newThreadOrchestrationFacade,
	newRuntimeReporter,
	provideNativeToolDescriptors,
	provideDisabledBuiltinToolsFn,
),
```

- [ ] **Step 2: 编译确认**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 3: 提交**

```bash
git add internal/app/modules.go
git commit -m "feat(app): bridge NativeToolDescriptors from unified.Registry to uistate and prompt"
```

---

### Task 7: 更新测试

**Files:**
- Modify: `internal/module/uistate/builtin_tools_test.go`
- Modify: `internal/module/uistate/config_rpc_test.go:169-178`

- [ ] **Step 1: 在 builtin_tools_test.go 中定义测试用工具列表**

删除所有对 `builtinToolRegistry` 和 `builtinToolIndex` 的引用，替换为本地测试数据：

```go
var testNativeTools = []contract.NativeToolDescriptor{
	{ID: "Read", Label: "读文件", Description: "读取文件", DefaultDisabled: true, Provider: "claude"},
	{ID: "Write", Label: "写文件", Description: "写入文件", DefaultDisabled: true, Provider: "claude"},
	{ID: "Bash", Label: "执行命令", Description: "执行命令", DefaultDisabled: true, Provider: "claude"},
	{ID: "WebFetch", Label: "抓取网页", Description: "抓取网页", DefaultDisabled: false, Provider: "claude"},
	{ID: "shell", Label: "执行命令", Description: "Codex shell", DefaultDisabled: true, Provider: "codex"},
}

var testNativeToolIndex = func() map[string]contract.NativeToolDescriptor {
	out := make(map[string]contract.NativeToolDescriptor, len(testNativeTools))
	for _, t := range testNativeTools {
		out[t.ID] = t
	}
	return out
}()
```

在 import 中添加 `"github.com/anthropic-ai/super-agent-v3/internal/contract"`。

- [ ] **Step 2: 更新 TestBuiltinToolsReadReturnsDefaultsWhenNoPreferenceStored**

```go
func TestBuiltinToolsReadReturnsDefaultsWhenNoPreferenceStored(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{}
	res, err := readBuiltinTools(context.Background(), prefs, nil, testNativeTools, testNativeToolIndex, "/repo")
	if err != nil {
		t.Fatalf("readBuiltinTools error = %v", err)
	}
	if len(res.Tools) != len(testNativeTools) {
		t.Fatalf("tools length = %d, want %d", len(res.Tools), len(testNativeTools))
	}
	for _, view := range res.Tools {
		descriptor, ok := testNativeToolIndex[view.ID]
		if !ok {
			t.Fatalf("unknown tool id in response: %q", view.ID)
		}
		wantEnabled := !descriptor.DefaultDisabled
		if view.Enabled != wantEnabled {
			t.Fatalf("tool %q enabled = %v, want %v", view.ID, view.Enabled, wantEnabled)
		}
	}
}
```

- [ ] **Step 3: 更新 TestBuiltinToolsWritePersistsDisabledAndReturnsCurrentView**

该测试通过 RPC 调用 write，然后验证 `ResolveDisabledBuiltinTools` 的返回值。用 `testNativeTools`（5 个工具）后，want 值需调整。`testNativeTools` 中默认禁用的是 Read、Write、Bash、shell（4 个）：

```go
func TestBuiltinToolsWritePersistsDisabledAndReturnsCurrentView(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{}
	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
		prefs,
		&sharedFileStoreStub{},
		nil,
	)
	// Enable the default-disabled Read tool.
	res := dispatchConfig[builtinToolsReadResult](t, server, "config/builtinTools/write", `{"cwd":"/repo","id":"Read","enabled":true}`)
	readEnabled := toolViewByID(res.Tools, "Read")
	if readEnabled == nil || !readEnabled.Enabled {
		t.Fatalf("Read enabled after write = %#v", readEnabled)
	}
	// Disable WebFetch (default enabled).
	res = dispatchConfig[builtinToolsReadResult](t, server, "config/builtinTools/write", `{"cwd":"/repo","id":"WebFetch","enabled":false}`)
	webFetch := toolViewByID(res.Tools, "WebFetch")
	if webFetch == nil || webFetch.Enabled {
		t.Fatalf("WebFetch disabled after write = %#v", webFetch)
	}
	// ResolveDisabledBuiltinTools should reflect the persisted state.
	// testNativeTools defaults: Read(disabled→enabled), Write(disabled), Bash(disabled), WebFetch(enabled→disabled), shell(disabled)
	got := ResolveDisabledBuiltinTools(context.Background(), prefs, "/repo", testNativeTools, testNativeToolIndex)
	want := []string{"Bash", "WebFetch", "Write", "shell"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveDisabledBuiltinTools = %#v, want %#v", got, want)
	}
}
```

- [ ] **Step 4: 更新 TestResolveDisabledBuiltinTools 系列测试**

```go
func TestResolveDisabledBuiltinToolsFallsBackToDefaults(t *testing.T) {
	t.Parallel()
	got := ResolveDisabledBuiltinTools(context.Background(), nil, "/repo", testNativeTools, testNativeToolIndex)
	// testNativeTools 默认禁用：Read, Write, Bash, shell
	want := []string{"Bash", "Read", "Write", "shell"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveDisabledBuiltinTools(nil prefs) = %#v, want %#v", got, want)
	}
}

func TestResolveDisabledBuiltinToolsHonorsExplicitEmptyOverride(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", builtinToolsDisabledKey): json.RawMessage(`[]`),
	}}
	got := ResolveDisabledBuiltinTools(context.Background(), prefs, "/repo", testNativeTools, testNativeToolIndex)
	if len(got) != 0 {
		t.Fatalf("ResolveDisabledBuiltinTools(explicit empty) = %#v, want empty", got)
	}
}
```

- [ ] **Step 5: 删除 TestBuiltinToolsReadIncludesProviderAndCodexNote，新增多 Provider 测试**

删除原有的 `TestBuiltinToolsReadIncludesProviderAndCodexNote`（它测试已删除的 `ProviderNotes` 和 `BuiltinToolProviderClaude`/`BuiltinToolProviderCodex` 常量），替换为：

```go
func TestBuiltinToolsReadIncludesMultipleProviders(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{}
	res, err := readBuiltinTools(context.Background(), prefs, nil, testNativeTools, testNativeToolIndex, "/repo")
	if err != nil {
		t.Fatalf("readBuiltinTools error = %v", err)
	}
	providers := make(map[string]bool)
	for _, view := range res.Tools {
		providers[view.Provider] = true
	}
	if !providers["claude"] {
		t.Errorf("expected claude provider in tools")
	}
	if !providers["codex"] {
		t.Errorf("expected codex provider in tools")
	}
}
```

- [ ] **Step 4: 更新 config_rpc_test.go 的 newConfigTestServer**

```go
func newConfigTestServer(
	cfg *platformconfig.Config,
	prefs uipreference.Store,
	sharedFiles sharedfilestore.Reader,
	threads thread.Service,
) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: cfg})
	server.Register(NewConfigHandlers(cfg, prefs, sharedFiles, threads, nil, testNativeTools).Handlers)
	return server
}
```

在 `config_rpc_test.go` 的 import 中也添加 contract 包，并复用 `testNativeTools`（或把测试工具列表提取到 `test_helpers_test.go`）。

- [ ] **Step 5: 运行全部测试**

Run: `go test ./internal/module/uistate/... -v`
Expected: ALL PASS

- [ ] **Step 6: 提交**

```bash
git add internal/module/uistate/builtin_tools_test.go internal/module/uistate/config_rpc_test.go
git commit -m "test(uistate): update builtin tools tests to use injected NativeToolDescriptor"
```

---

### Task 8: 前端移除 providerNotes 渲染

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/settings/BuiltinToolsSettings.ts`
- Modify: `cmd/agent-terminal/frontend/vue-app/builtin-tools-settings.behavior.test.js`

- [ ] **Step 1: 清理前端 providerNotes 相关代码**

在 `BuiltinToolsSettings.ts` 中：

1. 删除 `BuiltinToolProviderNote` 类型定义（line 8）
2. 从 `BuiltinToolsReadResult` 类型中删除 `providerNotes` 字段（line 9）
3. 删除 `providerNotes` ref 及其在 `applyToolsPayload` 中的处理（line 29, 51-56）
4. 删除模板中对 `providerNotes` 的引用

**保留** `PROVIDER_LABELS` 常量（line 22-25）——未来如果 UI 需要按 provider 显示中文分组标题（如 "Claude 内置工具" / "Codex 内置工具"）仍然需要它。

保留分组逻辑（auto / manual / unfiltered）不变——它已经能正确处理多 provider 的工具。

- [ ] **Step 2: 更新前端测试**

在 `builtin-tools-settings.behavior.test.js` 中：

1. 删除所有 `providerNotes` 相关断言（line 93 的 codex providerNote mock、line 106 的 `expect(codexGroup.label).toBe('Codex 内置工具')` 等）
2. 新增测试用例：验证 mock 返回包含多个 provider 的 tools 时，组件能正确渲染

- [ ] **Step 3: 手动验证**

启动前端 dev server，打开 Settings → 原生工具过滤，确认：
- Claude 和 Codex 的工具都出现在列表中
- 分组（自动替代 / 手动过滤 / 未过滤）正常工作
- 不再显示 Codex 的静态说明卡片

- [ ] **Step 4: 提交**

```bash
git add cmd/agent-terminal/frontend/vue-app/pages/settings/BuiltinToolsSettings.ts \
      cmd/agent-terminal/frontend/vue-app/builtin-tools-settings.behavior.test.js
git commit -m "feat(frontend): remove providerNotes, Codex tools now appear in unified list"
```

---

### Task 9: 全量验证

- [ ] **Step 1: 运行 Go 全量测试**

Run: `go test ./internal/... -count=1`
Expected: ALL PASS

- [ ] **Step 2: 运行前端测试**

Run: `cd cmd/agent-terminal/frontend && npx vitest run`
Expected: ALL PASS

- [ ] **Step 3: 运行 guard 脚本**

Run: `scripts/test_with_guard.sh`
Expected: PASS

---

## Self-Review Checklist

### 需求覆盖
| 需求 | 对应任务 |
|------|----------|
| NativeToolDescriptor 定义 | Task 1 |
| Claude 工具声明 | Task 2 |
| Codex 工具声明 | Task 3 |
| 跨 provider 聚合 | Task 4 |
| uistate 消费注入列表 | Task 5 |
| fx 桥接 | Task 6 |
| 测试更新 | Task 7 |
| 前端适配 | Task 8 |
| 全量验证 | Task 9 |

### 占位符扫描
- 无 TBD / TODO / "implement later"
- 所有代码块包含完整实现
- 所有测试包含断言

### 类型一致性
- `NativeToolDescriptor` — Task 1 定义，Task 2-7 使用，类型一致 `contract.NativeToolDescriptor`
- `DriverFactory.NativeTools` — Task 1 定义，Task 2/3 填充，Task 4 聚合
- `buildNativeToolIndex` — Task 5 定义，Task 5/6/7 使用，签名一致 `func([]contract.NativeToolDescriptor) map[string]contract.NativeToolDescriptor`
- `ResolveDisabledBuiltinTools` — Task 5 修改签名（加 registry + index），Task 6/7 调用，参数一致

### 审核修复记录
- **问题 1**：Task 3 改错文件 → 已修正为 `codexapp/driver.go:121-126`（不是 module.go）
- **问题 2**：Task 7 遗漏 `TestBuiltinToolsReadIncludesProviderAndCodexNote` → 已添加 Step 5 删除并替换为多 Provider 测试
- **问题 3**：Task 7 缺少具体 want 值 → 已补充 Step 3/4 的完整测试代码
- **问题 4**：Task 8 未覆盖前端测试文件 → 已添加 `builtin-tools-settings.behavior.test.js`
- **问题 5**：Task 8 误删 `PROVIDER_LABELS` → 已改为保留
- **问题 6**：ParamTags 多余 optional → 已改为第 6 参数不标 optional

### 未来扩展
- 新增模型只需在对应 provider 的 `NewDriverFactory` 中声明 `NativeTools`，无需修改任何其他文件
