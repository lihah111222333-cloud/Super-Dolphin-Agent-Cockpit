# 修复点 1：prompt 资源发现恢复

## 目标

让聊天创建自动化任务时，DAG designer 能稳定发现可用自动化执行模板。即使 SQLite DB 的 `prompt_templates` 为空，`prompt_list` 也应该返回内置 runtime-visible prompt，`prompt_get` 也应该能读取这些 prompt。

这是真正让用户无感创建自动化任务的关键修复。用户不需要知道 `prompt_key`，聊天 agent 通过资源发现自动选择并写入 DAG 节点。

## 当前问题

`cmd/mcp-orch/tools/prompt_tools.go` 的 `prompt_list` 只调用 `cmd/mcp-orch/store/prompt.Store.List`。该 store 只查 DB 表 `prompt_templates`。

内置 prompt registry 由 `internal/platform/shared/builtinprompts.NewDefaultRegistry()` 提供，但没有接入 `mcp-orch` prompt tools。因此：

```text
SQLite 新库或迁移后 prompt_templates 为空
-> prompt_list 返回空
-> DAG designer 按约束不能编造 prompt_key
-> task_create_dag 保存缺 prompt_key/agent_key 的节点
-> 运行时自动派发失败
```

## 修改范围

预计修改文件：

- `cmd/mcp-orch/tools/prompt_tools.go`
- `cmd/mcp-orch/tools/registry.go`
- `cmd/mcp-orch/runtime.go`
- `cmd/mcp-orch/tools/parity_v2_test.go` 或新增 `cmd/mcp-orch/tools/prompt_builtin_test.go`
- 现有调用 `HandlePromptList`、`HandlePromptGet`、`promptToolDefinitions` 的测试文件，需要按新签名补 `nil` 或 fake builtin registry。

可能需要引用：

- `internal/contract/prompt.go`
- `internal/platform/shared/builtinprompts/load.go`
- `internal/module/threadprompt/runtime_catalog.go`

## 设计原则

- `mcp-orch` prompt tools 应返回 DB prompt + builtin prompt 的合并视图。
- builtin prompt 与 DB prompt 同 key 时，优先 builtin。主线程 `threadprompt.RuntimeCatalog` 已采用类似策略，避免历史 system seed 覆盖内置定义。
- 不要在 `task_create_dag` 或运行时静默填默认 prompt。prompt 身份必须来自显式资源发现结果。
- `prompt_get` 必须能读取 `prompt_list` 返回的所有 key，否则资源发现链路不闭环。
- `prompt_list` 和 `prompt_get` 必须使用同一套优先级。若 list 中 builtin 胜出，get 同 key 也必须返回 builtin。
- builtin registry 必须由 fx 启动时注入，工具调用路径只读内存 registry，不能每次调用重新构建 registry。
- `prompt_list` 必须查询 DB，DB 查询错误必须 fail-fast；DB 空列表可以与 builtin 合并返回。
- `prompt_get` 对 builtin key 可直接返回 builtin，避免为了同 key 旧 DB seed 再查 DB；builtin 未命中后查询 DB，DB 非 not found 错误必须 fail-fast。
- 合并后的排序和截断必须稳定，避免模型因返回顺序抖动选择不同 prompt。

## 如何修改

### 1. 给 tools 依赖增加 builtin registry

在 `cmd/mcp-orch/tools/registry.go` 的 `Dependencies` 增加字段：

```go
BuiltinPrompts contract.BuiltinPromptRegistry
```

把 `promptToolDefinitions(deps.Prompt)` 改为接收 builtin registry：

```go
promptToolDefinitions(deps.Prompt, deps.BuiltinPrompts)
```

### 2. 在 mcp-orch runtime 注入 builtin registry

在 `cmd/mcp-orch/runtime.go` 中引入：

```go
github.com/anthropic-ai/super-agent-v3/internal/platform/shared/builtinprompts
```

在 fx providers 中加入：

```go
builtinprompts.NewDefaultRegistry
```

在 `newRegistryParams` 增加：

```go
BuiltinPrompts contract.BuiltinPromptRegistry
```

在 `newRegistry` 中传给 `tools.Dependencies`。

### 3. 改造 prompt tool handler

在 `cmd/mcp-orch/tools/prompt_tools.go` 中让 handler 接收 builtin registry：

```go
func HandlePromptList(store promptstore.Store, builtin contract.BuiltinPromptRegistry) ToolHandler
func HandlePromptGet(store promptstore.Store, builtin contract.BuiltinPromptRegistry) ToolHandler
func promptToolDefinitions(store promptstore.Store, builtin contract.BuiltinPromptRegistry) []ToolDefinition
```

实现一个小的合并 catalog，不建议直接跨包复用 `internal/module/threadprompt.RuntimeCatalog`，因为它依赖的是 `internal/store/prompt` 类型，而 `mcp-orch` 使用的是 `cmd/mcp-orch/store/prompt` 类型。

建议新增本地 helper：

```go
func listPromptTemplates(ctx context.Context, store promptstore.Store, builtin contract.BuiltinPromptRegistry, input promptListInput) ([]promptTemplateDTO, error)
func builtinPromptTemplateDTOs(builtin contract.BuiltinPromptRegistry, cwd, keyword string) ([]promptTemplateDTO, map[string]struct{})
func getBuiltinPromptTemplate(builtin contract.BuiltinPromptRegistry, promptKey, cwd string) (promptTemplateDTO, bool)
```

合并逻辑：

1. 先读取 builtin 列表，过滤 `Enabled=true`、scope 可见。
2. 记录所有可见 builtin prompt_key 集合。
3. 对输出结果再应用 keyword 过滤，只有匹配 keyword 的 builtin 进入返回列表。
4. 再读取 DB 列表。DB 查询失败时直接返回错误。
5. DB 中与 builtin 同 key 的模板跳过。
6. 合并后按稳定规则排序并按 `resourceListLimit` 截断。

注意：builtin key 的去重集合必须在 keyword 过滤之前建立。否则 builtin 本身没有匹配某个 keyword 时，历史 DB seed 的同 key 行可能被返回，破坏“builtin key 永远胜出”的规则。

排序建议：

1. `Priority` 值高的优先。
2. `UpdatedAt` 新的优先。
3. `PromptKey` 字典序兜底。

不要为了排序强制改变对外 DTO。可以新增内部候选结构，例如：

```go
type promptListCandidate struct {
	dto      promptTemplateDTO
	priority int32
}
```

如果实现者决定把 `priority`、`when_to_use` 或 `match_when` 暴露到 `promptTemplateDTO`，需要同步更新现有返回结构测试，确认不会破坏工具客户端。

scope 建议：

- builtin `Scope == ""` 或 `Scope == "global"` 视为全局可见。
- 如果后续 builtin 支持项目 scope，再扩展到 cwd 过滤。
- DB prompt 仍沿用现有 `RuntimeVisible=true + CWD` 查询，不放宽 DB 可见性规则。

keyword 匹配字段：

- `prompt_key`
- `title`
- `prompt_text`
- `description`
- `when_to_use`

DB 模板的 `when_to_use` 不在当前 `promptTemplateDTO` 中，但 `promptstore.PromptTemplate` 有该字段；keyword 过滤应在映射 DTO 之前完成，或通过内部候选结构携带。

### 4. 支持 prompt_get 读取 builtin

`getPromptTemplate` 当前 DB not found 直接返回 not found。应调整为：

1. 先查 builtin。builtin 命中且 scope 可见时，返回 builtin DTO。
2. builtin 未命中时，再查 DB。
3. DB 命中且 runtime visible，则返回 DB DTO。
4. DB not found 时返回 prompt not found。
5. DB 出现非 not found 错误时 fail-fast 返回错误。

这样 `prompt_get` 与 `prompt_list` 的同 key 优先级一致，避免 list 显示 builtin、get 却读到 DB 旧 seed。

### 5. 内置 section preview

内置 prompt 的 `PromptText` 可能为空或较短，应从 `builtin.SectionsByTemplateID(template.ID)` 组装 preview，规则与 `promptSectionsPreview` 对 DB sections 的处理一致：

- 只取 enabled section。
- 跳过 `TriggerType == "recall"`。
- 按 `Region`、`Ordinal`、`ID`、`SectionKey` 排序。
- 截断到 `promptSectionsPreviewMaxRunes`。

## 回归测试

建议新增测试：

### 空 DB 时 prompt_list 返回 builtin

测试输入：

- stub store `List` 返回空。
- fake builtin registry 提供一个 enabled global prompt：

```go
contract.BuiltinPromptTemplate{
	ID:        -1,
	PromptKey: "main/test-auto-task",
	Title:    "测试自动化任务执行者",
	Enabled:  true,
	Scope:    "global",
	Tags:     []string{"scope.global", "intent:expert"},
}
```

期望：

- `HandlePromptList(store, builtin)` 返回该 prompt。
- `PromptKey == "main/test-auto-task"`。

### prompt_get 可读取 builtin

测试输入：

- store `Get` 返回 not found。
- fake builtin registry 返回同一个 prompt 和两个 enabled static sections。

期望：

- `HandlePromptGet(store, builtin)` 返回 prompt。
- `PromptText` 包含 section preview。

### builtin key 隐藏 DB 同 key

测试输入：

- builtin 和 DB 都有 `PromptKey == "main/test-auto-task"`。

期望：

- list 结果只出现一次。
- 内容来自 builtin 或按设计确定的优先级。

### keyword 过滤不能让旧 DB seed 绕过 builtin 优先级

测试输入：

- builtin 有 `PromptKey == "main/test-auto-task"`，标题不包含 `"legacy"`。
- DB 有同 key 旧 seed，标题包含 `"legacy"`。
- 调用 `prompt_list(keyword="legacy")`。

期望：

- 结果不返回 DB 旧 seed。
- 如果 builtin 本身不匹配 `"legacy"`，该 key 不出现在结果里。
- 这证明 builtin key 的隐藏集合在 keyword 过滤前建立。

### DB 错误不能被 builtin 吞掉

测试输入：

- store `List` 返回非 not found 错误。

期望：

- `prompt_list` 返回错误。
- 不静默降级到只有 builtin。

### prompt_get 同 key 优先级与 prompt_list 一致

测试输入：

- builtin 和 DB 都有 `PromptKey == "main/test-auto-task"`。
- DB 版本的 `Title` 为 `"旧 DB seed"`。
- builtin 版本的 `Title` 为 `"内置执行者"`。

期望：

- `prompt_list` 只返回一次该 key，标题为 `"内置执行者"`。
- `prompt_get` 返回标题也是 `"内置执行者"`。

### registry 不在请求路径重复加载

测试输入：

- fake builtin registry 记录 `ListTemplates` 调用次数。
- 连续调用两次 `HandlePromptList(store, builtin)`。

期望：

- handler 使用传入的 registry，不调用 `builtinprompts.NewDefaultRegistry()`。
- 测试里不需要断言调用次数为 1，因为 list 本身会读 registry；重点是没有在 handler 内构造新 registry。

### 既有测试调用方按新签名更新

测试输入：

- 原有只验证 DB 行为的测试继续传 `nil` builtin registry。

期望：

- 旧测试语义不变。
- `promptToolDefinitions(nil, nil)`、`HandlePromptList(store, nil)`、`HandlePromptGet(store, nil)` 均可用于旧路径测试。

## 验收标准

- 空 DB 环境下，`prompt_list` 至少能返回内置 runtime-visible prompt。
- `prompt_get` 能读取 `prompt_list` 返回的 builtin prompt_key。
- 同 key 时不会重复返回 prompt。
- 同 key 时 `prompt_list` 和 `prompt_get` 返回同一个来源。
- builtin key 隐藏 DB 同 key 的规则在 keyword 过滤前生效。
- builtin registry 在启动时注入，工具调用路径不重复加载 embedded assets。
- DB 非 not found 错误保持 fail-fast。
- 聊天 DAG designer 可以从 `prompt_list` 自动选择 `exec.prompt_key`，用户无需填写内部参数。
- 合并后的排序稳定，连续调用返回顺序一致。

## 验证命令

修改 Go 文件后先跑单文件 guard：

```bash
./scripts/test_with_guard.sh cmd/mcp-orch/tools/prompt_tools.go
./scripts/test_with_guard.sh cmd/mcp-orch/tools/registry.go
./scripts/test_with_guard.sh cmd/mcp-orch/runtime.go
```

再跑工具包测试：

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -count=1
```

如修改 fx wiring 后需要扩大验证：

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1
```

端到端验收建议：

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -run 'Test.*Prompt.*Builtin|Test.*Prompt.*Runtime' -count=1
```
