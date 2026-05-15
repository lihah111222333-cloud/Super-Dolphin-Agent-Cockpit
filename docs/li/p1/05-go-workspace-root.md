# P1-05：Go workspace root / go.work / 子模块识别

## 目标

为 v3 `cmd/mcp-lsp/multilsp` 的 Go route 提供稳定 Go root resolver，使 gopls 在 repo root、linked worktree、`go.work`、单子模块、多子模块、nested module 场景下都以正确 workspace 初始化。

## 当前证据

- `cmd/mcp-lsp/multilsp/manager.go:210-213`：Go 语言路径调用 `findGoModRoot(absPath)`。
- `cmd/mcp-lsp/multilsp/gomod.go:13-31`：`findGoModRoot` 只向上查 `go.mod`。
- `cmd/mcp-lsp/multilsp/gomod.go:87-94`：`shouldUseGoWorkspace` 包含 `gowork`，但 root resolver 缺 `go.work` 语义。
- `cmd/mcp-lsp/multilsp/client.go:341-349`：`workspaceFolders` 只返回单个 root。
- `cmd/mcp-lsp/protocol/ext.go:28-30`：有 `WorkspaceClientCapability`，但当前 client capabilities 未完整设置 workspaceFolders。

## RootInfo

```go
type GoRootInfo struct {
    RootKind      string // go_work, go_mod, single_submodule, multi_module, dir_fallback
    WorkspaceRoot string
    GoWorkPath    string
    ModuleRoot    string
    GoModPath      string
    ModuleRoots   []string
    GOWORKMode    string // auto, off, explicit
}
```

## Resolver 输入

```go
type GoRootRequest struct {
    CWD      string
    FilePath string
    Env      []string
}
```

优先级：

1. trusted `_cwd`。
2. target file path。
3. manager startup root fallback。

## 算法

### Step 1：解析 GOWORK

- `GOWORK=off`：忽略所有上层 `go.work`，只找最近 `go.mod`。
- `GOWORK=/abs/go.work`：验证文件存在，workspace root 为 `dir(go.work)`。
- unset/auto：从 target file 向上找最近 `go.work`。

### Step 2：go.work 模式

如果找到 `go.work`：

1. 解析 `use` entries。
2. 将相对路径解析到 `go.work` 所在目录。
3. 若 target file 位于某个 module root 下：
   - `WorkspaceRoot = dir(go.work)`
   - `ModuleRoot = matched module root`
   - `RootKind = go_work`
4. 若 target 是 `go.work` 文件本身：
   - `WorkspaceRoot = dir(go.work)`
   - `RootKind = go_work`
5. workspaceFolders 包含 go.work root + module roots。

### Step 3：go.mod 模式

没有 go.work 或 GOWORK=off：

1. 从 target file 向上找最近 `go.mod`。
2. `WorkspaceRoot = dir(go.mod)`。
3. `ModuleRoot = WorkspaceRoot`。
4. `RootKind = go_mod`。

### Step 4：root 无 marker 的单/多子模块

当 `_cwd` root 无 `go.work/go.mod`：

- 一级子目录恰好一个 `go.mod`：
  - `WorkspaceRoot = that subdir`
  - `RootKind = single_submodule`
- 一级子目录多个 `go.mod`：
  - `WorkspaceRoot = _cwd`
  - `ModuleRoots = all first-level modules`
  - `RootKind = multi_module`
  - workspaceFolders 包含 root + modules
- 找不到：
  - `WorkspaceRoot = _cwd or dir(file)`
  - `RootKind = dir_fallback`

## WorkspaceFolders

改造 `workspaceFolders`：

```go
func workspaceFolders(root GoRootInfo) []protocol.WorkspaceFolder
```

规则：

- 第一个 folder 必须是 `WorkspaceRoot`。
- `go_work` 模式追加 `ModuleRoots`。
- linked worktree 场景追加物理 worktree root，不用 common git root。
- 去重、排序稳定。
- `clientCapabilities().Workspace.WorkspaceFolders = true`。

## Client Env

gopls process env：

- `GOWORK=off` 时显式注入。
- explicit go.work 时注入 `GOWORK=/abs/go.work`。
- auto/go_work 时可显式注入找到的 go.work，避免受 mcp-lsp 启动目录影响。

## Key 设计

Go route 必须映射到 `03-lsp-manager-pool.md` 的通用 `WorkspaceKey` 公式：

```text
language \x00 rootKind \x00 workspaceRoot \x00 languageWorkspaceRoot \x00 projectRoot \x00 languageSpecific
```

Go 字段映射：

- `language = "go"`。
- `rootKind = GoRootInfo.RootKind`。
- `workspaceRoot = GoRootInfo.WorkspaceRoot`。
- `languageWorkspaceRoot = GoRootInfo.ModuleRoot`；如果 `ModuleRoot` 为空，则使用 `WorkspaceRoot`。
- `projectRoot = trusted _cwd` 归一化后的物理 worktree/root；如果为空，则使用 `WorkspaceRoot`。
- `languageSpecific` 是稳定排序后的 Go 拓扑摘要，至少包含：
  - `goWorkPath`
  - `goModPath`
  - `moduleRoot`
  - `goworkMode`
  - `moduleRootsHash = hash(sort(ModuleRoots))`
  - `workspaceFoldersHash = hash(workspaceFolders(GoRootInfo))`

规则：

- 不要使用 common git root 或 prompt git root。
- `go.work use` 列表变化、多子模块集合变化、workspaceFolders 拓扑变化必须改变 `languageSpecific`，从而改变 `WorkspaceKey`，避免复用旧 gopls client/cache/bootstrap。
- `ModuleRoots` / workspaceFolders 参与 hash 前必须清理、绝对化、去重、稳定排序。

## 实现步骤

1. 新增 `cmd/mcp-lsp/multilsp/go_root_resolver.go`。
2. 用 `GoRootInfo` 替换 `findGoModRoot` 在 Go 分支中的决策。
3. 扩展 `workspaceConfig`，包含 root kind/goWork/module roots/env。
4. `ClientFactory.NewClient` 支持 per-client env 注入。
5. `client.Initialize` 接收 workspaceFolders。
6. 增加 go.work parser；优先用 Go 标准包解析模块文件，避免手写 fragile parser。

## 测试矩阵

- repo root 有 `go.mod`。
- repo root 有 `go.work`，use `./backend`、`./tools`。
- target file 位于 `backend` module。
- target file 是 `go.work`。
- `GOWORK=off` 忽略上层 go.work。
- root 无 marker，只有 `backend/go.mod`。
- root 无 marker，有 `backend/go.mod` + `tools/go.mod`。
- nested module：`repo/go.mod` + `repo/plugins/x/go.mod`。
- linked worktree A/B 物理路径不同，不共享 workspace key。

## 完成定义

- gopls 初始化 root 与 workspaceFolders 可从日志/测试证明。
- `go.work` 下 definition/diagnostics 不再报 module not included。
- 单子模块 repo 不需要用户手动 `cd backend`。
- worktree A/B 不串 gopls client/cache。
