# Codex CLI 权限设置生效需求整理

## 背景

用户在 Super-Dolphin v3 设置页修改 Codex 访问权限后，新建对话仍然表现为 Codex CLI 侧真实权限没有变化。对话中判断方向是：当前产品调用的是 `codex app-server` / Codex CLI，权限不是只靠前端状态或配置文件展示，而应在创建线程或启动 turn 时按 Codex app-server JSON-RPC 协议传给 CLI。

官方文档依据：

- `codex app-server` 使用 JSON-RPC 2.0 风格消息；客户端通过 `thread/start`、`turn/start` 等方法启动线程和回合。
- 官方 `thread/start` 示例包含 `approvalPolicy` 与 `sandbox` 字段。
- 官方 `turn/start` 示例包含 `approvalPolicy` 与 `sandboxPolicy` 对象，示例对象带 `type`、`writableRoots`、`networkAccess`。
- app-server 会按用户 Codex 设置触发命令执行和文件变更审批，审批请求由客户端响应。

参考文档：https://developers.openai.com/codex/app-server

## 当前项目事实

### 前端设置页已经保存权限偏好

`frontend-app/src/pages/settings/SettingsPage.jsx` 当前会读取并保存 provider 运行偏好：

- 读取 `settings.provider.<provider>.personality`
- 读取 `settings.provider.<provider>.sandbox`
- 保存 `settings.provider.<provider>.personality`
- 保存 `settings.provider.<provider>.sandbox`
- 单独的 Provider Preferences 面板还保存 `settings.provider.<provider>.summary` 和 `settings.provider.<provider>.approvalPolicy`

相关位置：

- `frontend-app/src/pages/settings/SettingsPage.jsx:573`
- `frontend-app/src/pages/settings/SettingsPage.jsx:665`
- `frontend-app/src/pages/settings/SettingsPage.jsx:736`

### 新线程启动偏好当前没有读取 sandbox / approval

`frontend-app/src/entities/client/model/useClientStore.js` 的 `resolveLaunchPreferences()` 是新建线程、fork、prompt 向导共用的启动偏好入口。当前它只读取：

- `settings.provider.active`
- provider `model`
- provider `effort`
- `settings.activePromptKey`
- Codex identity：`codexHome`、`codexInstanceKey`、`codexModelProvider`

它没有读取 `settings.provider.codex.sandbox`、`settings.provider.codex.approvalPolicy`、`settings.provider.codex.personality`、`settings.provider.codex.summary`。这会导致设置页已保存的权限配置没有进入 `thread/start` payload。

相关位置：

- `frontend-app/src/entities/client/model/useClientStore.js:347`
- `frontend-app/src/entities/client/model/useClientStore.js:363`

### 后端 thread/start 已有承接字段

`internal/module/thread/contract.go` 的 `StartRequest` 已包含：

- `ApprovalPolicy string`
- `Sandbox json.RawMessage`
- `Summary string`
- `Effort string`
- `Personality string`

`internal/module/thread/start_session.go` 会校验 provider、cwd、sandbox 与 approval policy。`internal/module/thread/start_session_helpers.go` 会把 `ApprovalPolicy` 和 `Sandbox` 放进 provider config。

相关位置：

- `internal/module/thread/contract.go:49`
- `internal/module/thread/start_session.go:194`
- `internal/module/thread/start_session_helpers.go:367`

### Codex provider 会转发到 app-server，但 sandbox 对象可能被压扁

`internal/provider/codexapp/support.go` 的 `buildThreadStartParams()` 会从 provider config 取：

- `ApprovalPolicy`
- `Personality`
- `Summary`
- `Effort`
- `Sandbox`

但 `internal/provider/codexapp/driver.go` 的 `codexSandboxWireJSON()` 当前会把带 `type` 的 sandbox 对象转换成字符串模式。例如 `{ "type": "workspaceWrite", "writableRoots": [...], "networkAccess": true }` 可能被压成 `"workspace-write"`，这会丢失 `writableRoots` 和 `networkAccess` 这类细粒度信息。需要按当前 Codex CLI app-server schema 核对，不能只看历史兼容字符串。

相关位置：

- `internal/provider/codexapp/support.go:280`
- `internal/provider/codexapp/driver.go:571`

## 需求目标

设置页中修改的 Codex 权限配置，必须在新建 Codex 线程时真实传递到 Codex app-server，并能通过日志、自动化测试和手工验证证明生效。

这里的“生效”定义为：

1. 用户保存 `settings.provider.codex.sandbox` 后，新建线程的 `thread/start` 或首个 `turn/start` 请求包含等价 sandbox 配置。
2. 用户保存 `settings.provider.codex.approvalPolicy` 后，新建线程的 app-server 请求包含对应 approval policy。
3. workspace 写权限、额外 writable roots、network access 不得在前端到 provider 的链路中被静默丢弃。
4. 如果当前 Codex CLI 版本不支持某个 sandbox 形态，系统必须 fail-fast 并输出可定位错误，而不是显示“已保存”但启动时静默回退。
5. 老线程不要求自动套用新设置；本轮优先保证“修改设置后新建对话生效”。

## 功能需求

1. 统一启动偏好读取

`resolveLaunchPreferences()` 需要读取并返回 Codex 启动所需的运行偏好：

- `settings.provider.codex.sandbox`
- `settings.provider.codex.approvalPolicy`
- `settings.provider.codex.personality`
- `settings.provider.codex.summary`

读取规则应与设置页一致，优先项目 scoped preference，再回退全局 preference；如果某个 scoped preference 是 tombstone，则按现有偏好语义清空。

2. 统一启动 payload

新建线程时，`sessionApi.start()` 的 payload 需要包含：

- `approvalPolicy`
- `sandbox`
- `personality`
- `summary`

字段名优先使用当前后端已经允许并解析的 camelCase 字段；后端兼容 snake_case 只作为旧调用方支持。

3. Codex app-server wire schema 对齐

需要用当前项目依赖的 Codex CLI 版本确认 app-server schema。优先方式：

```bash
codex app-server generate-json-schema --out /tmp/codex-app-server-schema
```

确认后决定：

- `thread/start` 是否继续发送 `sandbox`
- `turn/start` 是否需要发送 `sandboxPolicy`
- workspace 写根和网络开关应保留为对象，还是由 CLI 字符串 profile 另行表达

不能把对象形态的权限配置无条件压成字符串，除非 schema 明确只接受字符串且细粒度字段已有其它等价传输位置。

4. 可观测性

需要在不泄露 secret 的前提下增加或确认日志能串起这条链路：

- 前端启动偏好解析结果：是否有 sandbox、approvalPolicy、summary、personality，不记录敏感 env。
- 后端 `thread/start` 收到的 provider config keys。
- Codex provider 发送 app-server 前的 sandbox / approval 摘要。
- app-server 返回 `SandboxError`、`BadRequest`、approval request 时能带 thread/turn 上下文。

5. 用户提示

设置页保存成功后，应明确该配置影响新建线程。老线程若仍沿用旧配置，不应让用户误以为当前正在运行的线程会立即变化。

## 非目标

- 不把“手动编辑 `~/.codex/config.toml`”作为 Super-Dolphin 设置页的主实现路径。
- 不要求把新设置 retroactively 应用到已经启动的旧线程。
- 不默认提升到 `danger-full-access`。
- 不绕过 Codex CLI sandbox 或 approval 机制。
- 不扩大到 Claude provider；当前问题是 Codex app-server / Codex CLI 权限链路。

## 建议修改点

1. `frontend-app/src/entities/client/model/useClientStore.js`

扩展 `resolveLaunchPreferences()`，把 sandbox、approvalPolicy、personality、summary 加入启动偏好。复用或抽出设置页已有的 scoped preference 读取与 preference value 归一化逻辑，避免 Settings 页面和启动链路对同一 key 解释不同。

2. `frontend-app/src/entities/client/model/useClientStore.test.js`

补充 `resolveLaunchPreferences()` 和 `sendDraft()` 回归用例，断言保存的 Codex sandbox / approvalPolicy 会进入 `backend.startThread()` payload。

3. `frontend-app/src/shared/api/backendApi.js`

确认 `thread/start` payload 白名单继续允许 `approvalPolicy` 与 `sandbox`。如果引入 `sandboxPolicy`，需要同步白名单、payload 归一化和测试。

4. `internal/provider/codexapp/driver.go`

调整 `codexSandboxWireJSON()`：保留当前 Codex app-server schema 支持的对象字段，避免丢弃 `writableRoots` / `networkAccess`。历史字符串兼容仍可保留，但不得覆盖更精确的对象配置。

5. `internal/provider/codexapp/*_test.go`

补充 provider 级测试，捕获 `thread/start` 或 `turn/start` 的实际 JSON-RPC 参数，验证：

- approval policy 被传出。
- workspace write object 不被压成纯字符串。
- read-only / danger-full-access 历史字符串仍兼容。
- 不支持的 sandbox shape 失败可见。

## 验收标准

1. 前端测试覆盖：

```bash
cd frontend-app
npm test -- useClientStore
```

2. 后端 focused 测试覆盖：

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/codexapp -count=1
```

3. 新 UI 前端完整验证：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

4. 手工验证场景：

- 在设置页把 Codex sandbox 设置为 `workspaceWrite`，writable roots 包含当前项目目录，network access 按需开启，approval policy 设为预期值。
- 新建对话，让 Codex 创建一个测试文件或执行需要权限的命令。
- 日志中能看到新线程启动 payload 带有对应 sandbox / approval 摘要。
- 操作成功或触发预期 approval；不能再出现“设置页已改但 CLI 真实权限仍是旧值”的表现。
- 再切换到 `readOnly` 并新建对话，写文件应失败或触发符合预期的拒绝路径。

## 需要进一步确认的问题

1. 当前打包或用户环境使用的 Codex CLI 版本。不同版本的 app-server schema 可能影响 `sandbox` / `sandboxPolicy` 的最终字段名。
2. network access 是否只需要布尔开关，还是后续要支持官方 permissions profile 里的 domain allow/deny 规则。
3. 是否要在 UI 上增加“仅对新线程生效”的提示文案；该提示有助于解释老线程不变的问题。
