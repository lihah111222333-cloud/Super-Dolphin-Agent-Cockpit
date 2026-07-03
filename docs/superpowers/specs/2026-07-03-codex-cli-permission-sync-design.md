# Codex CLI 权限设置生效设计

日期：2026-07-03

## 目标

修复 Super-Dolphin v3 设置页中 Codex 权限配置保存后，新建 Codex 对话没有真实影响 Codex CLI / `codex app-server` 沙盒权限的问题。

本设计采用“协议对齐的最小修复”：把设置页已保存的 Codex sandbox、approval policy、summary、personality 纳入新线程启动偏好，并保证后端发给 app-server 的 sandbox payload 不丢失 writable roots 和 network access。

## 已批准范围

- 只保证 **修改设置后新建 Codex 线程生效**。
- 不追溯修改已经启动的旧线程。
- 前端从现有 UI preference 读取 `settings.provider.codex.sandbox`、`settings.provider.codex.approvalPolicy`、`settings.provider.codex.personality`、`settings.provider.codex.summary`。
- 新建线程时把上述偏好带入 `thread/start` payload。
- 后端继续 fail-fast 校验 sandbox 和 approval policy。
- Codex provider 在发送 `thread/start` / 必要时 `turn/start` 前，保留 app-server schema 支持的 sandbox 对象字段。
- 设置页保存成功提示需要让用户知道权限配置对新线程生效。
- 补齐前端和 provider 参数捕获测试，证明配置从 UI preference 进入 Codex app-server 请求。

## 不做的事

- 不把“手动编辑 `~/.codex/config.toml`”作为主实现路径。
- 不默认提升到 `danger-full-access`。
- 不绕过 Codex CLI 自身 sandbox 和 approval 机制。
- 不扩展到 Claude provider。
- 不新增完整 permissions profile 管理，不做 domain allow/deny UI。
- 不做旧线程运行时重配置；旧线程行为保持现状。

## 当前链路结论

当前设置页已经保存权限偏好：

- `frontend-app/src/pages/settings/SettingsPage.jsx` 读取并保存 `settings.provider.codex.sandbox` 和 `settings.provider.codex.personality`。
- Provider Preferences 面板读取并保存 `settings.provider.codex.summary` 和 `settings.provider.codex.approvalPolicy`。

当前启动链路缺口在 `frontend-app/src/entities/client/model/useClientStore.js`：

- `resolveLaunchPreferences()` 是新建线程、fork、prompt 向导共用的启动偏好入口。
- 它当前只读取 active provider、model、effort、active prompt key、Codex identity。
- 它没有读取 sandbox、approvalPolicy、summary、personality，所以设置页保存的权限不会进入新线程启动 payload。

后端已有承接面：

- `internal/module/thread/contract.go` 的 `StartRequest` 已包含 `ApprovalPolicy`、`Sandbox`、`Summary`、`Personality`。
- `internal/module/thread/start_session.go` 会校验 sandbox 与 approval policy。
- `internal/module/thread/start_session_helpers.go` 会把 `ApprovalPolicy` 和 `Sandbox` 放入 provider config。
- `internal/provider/codexapp/support.go` 会把 provider config 映射到 `threadStartParams`。

需要修正的后端风险：

- `internal/provider/codexapp/driver.go` 的 `codexSandboxWireJSON()` 会把带 `type` 的对象型 sandbox 转成字符串模式。
- 如果 app-server 支持对象型 sandbox 或 `sandboxPolicy`，这种压缩会丢失 `writableRoots` 和 `networkAccess`，属于静默降级。

## 架构设计

### 前端启动偏好

在 `resolveLaunchPreferences(cwd)` 中加入 provider runtime preference 读取：

```text
settings.provider.codex.sandbox
settings.provider.codex.approvalPolicy
settings.provider.codex.personality
settings.provider.codex.summary
```

读取规则与设置页保持一致：

- scoped preference 优先：`getPreference({ cwd, key })`
- scoped preference 缺失时回退 global：`getPreference({ key })`
- tombstone 表示显式清空，不再回退 global
- 空值不写入启动 payload

输出 payload 继续使用后端已支持的字段：

```json
{
  "modelProvider": "codex",
  "model": "gpt-5.5",
  "effort": "medium",
  "prompt_key": "main/reviewer",
  "approvalPolicy": "on-request",
  "sandbox": {
    "type": "workspaceWrite",
    "writableRoots": ["/repo/app"],
    "networkAccess": true
  },
  "personality": "pragmatic",
  "summary": "concise"
}
```

### 后端 thread/start

后端 `thread/start` 继续作为新线程权限配置入口：

- `backendApi.js` 白名单保持允许 `approvalPolicy`、`approval_policy`、`sandbox`。
- `StartRequest.Sandbox` 继续使用 `json.RawMessage`，避免前端对象字段被结构体提前截断。
- `resolveStartConfig()` 保持 fail-fast：非法 sandbox JSON、未知 sandbox type、非法 approval policy 直接报错。
- `buildStartSessionConfig()` 把 request 字段放入 provider config，不引入默认 sandbox。

如果 Codex app-server 当前 schema 要求 `sandboxPolicy` 而不是 `sandbox`，应在 provider adapter 层做 wire mapping，而不是改变整个产品内部字段名。

### Codex app-server wire 映射

Codex provider 负责把内部启动 config 转成 app-server 接受的 wire 参数。

实现时必须先用当前本地 Codex CLI 生成 schema 或查官方 app-server schema：

```bash
codex app-server generate-json-schema --out /tmp/codex-app-server-schema
```

根据 schema 决定最终映射：

- 如果 `thread/start` 支持对象型 `sandbox`，对象字段应原样保留，只做命名 canonicalization。
- 如果 `thread/start` 只支持字符串 `sandbox`，但 `turn/start` 支持对象型 `sandboxPolicy`，则首个 `turn/start` 需要携带 `sandboxPolicy`，并用测试证明。
- 如果当前 CLI 版本不支持 writable roots 或 network access 的 wire 字段，启动应 fail-fast 或明确报错，不能压成 `"workspace-write"` 后假装生效。

兼容要求：

- 历史字符串 `"read-only"`、`"workspace-write"`、`"danger-full-access"` 仍可接受。
- 新对象 payload 不得被无条件转成字符串。
- camelCase UI 持久化字段可以作为输入，但 provider wire 应按 app-server schema 输出。

### 用户提示

设置页保存 provider runtime preferences 后，保存成功提示应体现：

```text
Provider 设置已保存，将在新建线程时生效
```

该提示只改变文案，不引入旧线程重配置按钮。

## 错误处理

保持 fail-fast：

- active provider 为空，继续阻止 startThread。
- sandbox 不是字符串或对象，报错。
- sandbox 对象缺少可识别 mode/type，报错。
- approval policy 不在当前允许集合，报错。
- app-server 返回 `SandboxError` 或 `BadRequest` 时，错误需要带 thread/turn 上下文进入现有错误事件流。

不得做这些兜底：

- 不因 sandbox 不合法而改用 workspace-write。
- 不因 app-server 不支持细粒度字段而只发送 `"workspace-write"`。
- 不吞掉 network access 或 writable roots。

## 可观测性

日志只记录结构摘要，不记录 secret：

- 前端启动偏好解析：记录是否有 sandbox、approvalPolicy、summary、personality。
- `thread/start` 后端日志：记录 provider config keys 和 sandbox shape。
- Codex provider 发送 app-server 前：记录 sandbox mode、是否含 writable roots、是否含 network access、approval policy。
- app-server 返回错误时：记录 agent id、cwd、method、错误分类。

## 测试策略

前端测试：

- `resolveLaunchPreferences()` 会读取 scoped Codex sandbox / approval / summary / personality，并返回启动 payload。
- scoped tombstone 会清空对应字段，不回退 global。
- `sendDraft()` 新建线程时，`backend.startThread()` 收到包含 sandbox 和 approvalPolicy 的 payload。
- 设置页保存 Provider 设置后的文案说明“新建线程生效”。

后端 thread 测试：

- `thread/start` 仍接受 `approvalPolicy` 和对象型 `sandbox`。
- 非法 approval policy 和非法 sandbox 继续 fail-fast。
- `buildStartSessionConfig()` 保留对象型 sandbox。

Codex provider 测试：

- 捕获 `thread/start` 或首个 `turn/start` JSON-RPC 参数，验证 approval policy 被传出。
- 对象型 workspace write sandbox 不被压成纯字符串，writable roots 和 network access 不丢失。
- 历史字符串 sandbox 仍保持兼容。
- 不支持的 sandbox shape 不静默降级。

## 验证命令

前端局部：

```bash
cd frontend-app
npm test -- useClientStore SettingsPage
```

后端局部：

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/codexapp -count=1
```

前端完整：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

需要手工验证：

- 设置 workspaceWrite + 当前项目 writable root + network access，保存后新建线程。
- 让 Codex 执行写文件或需要审批的命令。
- 日志显示新线程 app-server 请求携带 sandbox / approval 摘要。
- 再设置 readOnly，新建线程后写文件应失败或进入预期审批/拒绝路径。

## 实施顺序

1. 先补前端失败测试，证明保存的 sandbox / approval 没进入 start payload。
2. 修改 `resolveLaunchPreferences()` 读取并输出权限偏好。
3. 补 provider 参数捕获测试，锁定 app-server wire shape。
4. 按 schema 修改 `codexSandboxWireJSON()` 或新增 wire mapping。
5. 更新设置页保存成功文案和测试。
6. 跑局部验证，再跑完整前端验证。

## 开放问题

1. 本地和打包环境的 Codex CLI 版本是否一致；实现前需要生成 schema 确认字段名。
2. network access v1 只保留布尔值，不做 domain allow/deny；后续如需域名规则应另起规格。
3. 如果 schema 显示权限只能在 `turn/start` 传递，首个 user turn 之前的 `thread/start` 是否还可能执行需要权限的动作；实现计划需要用实际 app-server 行为确认。
