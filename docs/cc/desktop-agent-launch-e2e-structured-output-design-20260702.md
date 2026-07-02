# 桌面发送链路 E2E 与结构化输出协议设计

日期：2026-07-02

## 背景

近期在桌面新 UI 的“新对话输入并发送”路径中连续暴露了多层后端链路错误：

1. `thread: orchestration service is not configured`
2. `toolbridge: mcp tool lifecycle workspace root is required`
3. `toolbridge: codex tool surface is not prepared for agent ... thread ...`

这些错误都不是纯前端渲染问题。它们发生在：

- 前端 composer 发起 `thread/start` / `turn/start`
- thread 模块触发 `LaunchAgent`
- app facade 通过 toolbridge 调用 `mcp-orch launch_agent`
- mcp-orch remote launcher 再启动 Codex 子 agent thread
- Codex dynamic tool surface prepare / bind / scoped routing

现有验证覆盖了不少单元和局部包测试，但缺少一个能从用户路径稳定触发 agent launch 的端到端 smoke。因此每修一层后，下一层才在手动测试中暴露。

## 目标

建立一个可自动运行的桌面端到端测试体系，用来发现同类型链路错误，并为后续自动化修复循环提供结构化证据。

目标包括：

- 覆盖“新会话 -> 输入 -> 点击发送 -> 启动子 agent -> 子 agent 工具面可用 -> 最终回复”的用户路径。
- 使用结构化 JSON 输出协议，而不是只断言自然语言文本。
- 将验收分为两个分叉点：
  - 未收到合规 JSON：归类为模型输出契约/模型遵循能力/渲染提取问题。
  - 收到合规 JSON：按 JSON 内容和运行时证据继续自动分析。
- 失败时自动归档 Playwright trace、截图、后端日志、前端日志和错误分类 JSON。
- 为 Codex repair loop 提供候选模块、错误签名和最小复现证据。

## 非目标

- 不让浏览器脚本直接盲目修改源码。
- 不把真实模型的随机行为作为 CI 唯一断言来源。
- 不用静默降级隐藏工具面、workspace root 或 provider 配置错误。
- 不把“模型输出了成功文本”当成唯一成功标准；必须交叉检查运行时证据。

## 现有基础

仓库已有可复用组件：

- `frontend-app/scripts/desktop-ux-smoke.mjs`
  - 启动真实 `run-new-ui-desktop.sh`
  - 分配隔离 HTTP/Vite/control/postgres 端口
  - 写入 `.tmp/playwright-ux-smoke/backend.log` 和 `frontend.log`
  - 运行 Playwright
- `frontend-app/tests/e2e/desktop-ux.spec.js`
  - 已覆盖桌面 UI 可见性、导航、设置页、链路追踪页
  - 当前只填 composer 文本，没有点击发送
- `frontend-app/scripts/desktop-smoke.mjs`
  - 通过 WebSocket JSON-RPC 调用真实桌面后端
  - 覆盖 `ui/sidebar/get`、`ui/dashboard/get`、`thread/start`
  - 默认 `defer_spawn: true`，不会触发完整 agent launch 链路

## 需要补上的测试层

### 1. RPC 级 agent launch smoke

建议新增：

```text
frontend-app/scripts/desktop-agent-launch-smoke.mjs
```

职责：

1. 启动真实桌面后端。
2. 通过 WebSocket JSON-RPC 发起 `thread/start`。
3. 通过 `turn/start` 发送固定测试 prompt。
4. 等待 turn 完成或失败。
5. 拉取 thread messages / timeline / observability logs。
6. 提取 assistant 最终文本中的 JSON。
7. 运行结构化协议验收。

RPC 级 smoke 比 Playwright UI 更快，适合作为后端链路回归入口。

### 2. Playwright UI 级发送 smoke

建议新增或扩展：

```text
frontend-app/tests/e2e/desktop-agent-launch.spec.js
```

职责：

1. 打开桌面新 UI。
2. 点击“新会话”。
3. 在 composer 输入固定测试 prompt。
4. 点击发送按钮或按 Enter。
5. 等待最终 assistant 消息。
6. 断言页面不出现：
   - `发送失败`
   - `thread.send.failed`
   - `toolbridge:`
   - `mcp-orch`
7. 提取最终 JSON 并执行同一套结构化验收。

UI 级 smoke 用来证明用户实际路径没有被前端状态、按钮禁用、路由、toast 或渲染层破坏。

## 模型输出结构化协议

### 协议要求

模型最终回答必须只输出一个 JSON object：

- 禁止 Markdown 代码块。
- 禁止解释文字。
- 禁止前后缀。
- 禁止多个 JSON object。
- 必须包含 `schema`、`status`、`run_id`。
- `run_id` 必须和测试脚本注入值完全一致。

### 成功 JSON

```json
{
  "schema": "super-dolphin.agent-launch-smoke.v1",
  "status": "ok",
  "run_id": "agent-launch-smoke-20260702-001",
  "steps": [
    {
      "name": "launch_agent",
      "status": "ok",
      "agent_id": "agent_1782980944895529259",
      "thread_id": "thread_abc"
    },
    {
      "name": "child_tool_surface",
      "status": "ok",
      "evidence": "scoped tool call completed"
    }
  ],
  "final": {
    "message": "E2E_AGENT_LAUNCH_OK"
  }
}
```

### 失败 JSON

```json
{
  "schema": "super-dolphin.agent-launch-smoke.v1",
  "status": "failed",
  "run_id": "agent-launch-smoke-20260702-001",
  "failure": {
    "phase": "launch_agent",
    "code": "tool_surface_not_prepared",
    "message": "toolbridge: codex tool surface is not prepared"
  },
  "steps": [
    {
      "name": "launch_agent",
      "status": "failed"
    }
  ]
}
```

### 最小 JSON Schema 约束

测试脚本不需要一开始引入完整 JSON Schema validator，但必须做等价校验：

- `schema === "super-dolphin.agent-launch-smoke.v1"`
- `status` 只能是 `"ok"` 或 `"failed"`
- `run_id` 等于本轮 run id
- `steps` 必须是数组
- `status === "ok"` 时：
  - `final.message === "E2E_AGENT_LAUNCH_OK"`
  - `steps` 至少包含 `launch_agent: ok`
  - `steps` 至少包含 `child_tool_surface: ok`
- `status === "failed"` 时：
  - `failure.phase` 非空
  - `failure.code` 非空
  - `failure.message` 非空

## 测试 Prompt 规范

真实模型 smoke 使用固定 prompt 模板：

```text
你正在执行 Super-Dolphin 桌面端到端 smoke。

run_id: {{RUN_ID}}

任务：
1. 启动一个 Codex 子 agent。
2. 让子 agent 完成一次最小可验证动作。
3. 最终只输出一个 JSON object。

输出要求：
- 不要输出 Markdown。
- 不要输出代码块。
- 不要输出解释文字。
- 不要输出多个 JSON。
- JSON.schema 必须是 super-dolphin.agent-launch-smoke.v1。
- JSON.run_id 必须完全等于 {{RUN_ID}}。
- 成功时 JSON.status 必须是 ok，final.message 必须是 E2E_AGENT_LAUNCH_OK。
- 失败时 JSON.status 必须是 failed，并填写 failure.phase、failure.code、failure.message。
- 如果任何工具调用失败，不要编造成功，输出 failed JSON。
```

更稳定的 CI 路径应使用 deterministic fixture/provider，使模型输出和工具调用可控。真实 Codex provider 路径作为本机或 nightly smoke。

## 验收分叉

### 分叉 A：未收到正常结构化 JSON

条件：

- assistant 最终消息为空。
- 无法提取单个 JSON object。
- JSON 解析失败。
- JSON schema 不匹配。
- `run_id` 不匹配。
- 有 Markdown 包裹、解释前缀或多个 JSON。

归类：

```json
{
  "classification": "model_output_contract_failed",
  "owner_hint": "provider_or_prompt_contract",
  "repair_candidate": [
    "prompt contract",
    "provider final output extraction",
    "frontend message rendering"
  ]
}
```

该分叉主要说明模型输出遵循能力、prompt 约束、provider 输出提取或前端渲染存在问题。它不直接证明 `launch_agent` 链路失败。

### 分叉 B：收到合规结构化 JSON

进入自动化分析：

1. 如果 `status === "ok"`：
   - 检查 `launch_agent` step 是否 ok。
   - 检查 `child_tool_surface` step 是否 ok。
   - 检查页面和日志中是否有相反证据。
   - 如果 JSON 声称 ok 但日志包含 fatal 错误，归类为 `model_claim_conflicts_with_runtime_evidence`。
2. 如果 `status === "failed"`：
   - 按 `failure.code` 分类。
   - 将 `failure.phase` 映射到模块。
   - 附加后端日志、frontend RPC 事件和 toolbridge 错误签名。

## 同类型错误分类矩阵

| 分类 | 典型签名 | 可能模块 | 测试触发 |
|---|---|---|---|
| `orchestration_facade_missing` | `orchestration service is not configured` | `internal/app`, `internal/module/thread` | 新对话发送触发 lazy launch |
| `mcp_orch_peer_not_ready` | `mcp-orch peer not ready`, `ErrNoPeerAvailable` | `internal/app`, `internal/platform/toolbridge`, `cmd/mcp-orch` | 后端启动后立即发送 |
| `workspace_root_required` | `mcp tool lifecycle workspace root is required` | `internal/app`, `internal/platform/toolbridge` | `launch_agent` lifecycle metadata |
| `codex_surface_not_prepared` | `codex tool surface is not prepared` | `internal/platform/toolbridge`, `internal/provider/codexapp` | 子 agent scoped tool call |
| `dynamic_surface_bind_failed` | `dynamic tools start surface bind` | `internal/provider/codexapp` | provider thread id bind |
| `thread_start_contract_invalid` | `toolSurfaceMode must be chat, auto, or agent`, `cwd is required` | `frontend-app`, `internal/module/thread` | thread/start 参数 |
| `turn_start_session_missing` | `session not found`, `thread id required` | `internal/module/thread`, provider session store | turn/start |
| `codex_identity_missing` | `codex identity required`, `codex binding is not ready` | `internal/app`, `internal/provider/codexapp` | Codex provider 初始化 |
| `tool_lifecycle_denied` | disabled/suspended tool result | `internal/platform/toolbridge`, `internal/module/mcp_server` | lifecycle 配置影响 tool call |

## 失败证据格式

每次失败生成：

```text
.tmp/desktop-agent-launch-smoke/<run_id>/failure.json
.tmp/desktop-agent-launch-smoke/<run_id>/backend.log
.tmp/desktop-agent-launch-smoke/<run_id>/frontend.log
.tmp/desktop-agent-launch-smoke/<run_id>/playwright-trace.zip
.tmp/desktop-agent-launch-smoke/<run_id>/screenshot.png
```

`failure.json` 示例：

```json
{
  "run_id": "agent-launch-smoke-20260702-001",
  "classification": "codex_surface_not_prepared",
  "branch": "structured_json_received",
  "ui_error": "发送失败：thread: launch agent: app: call mcp-orch launch_agent: toolbridge: codex tool surface is not prepared",
  "model_json": {
    "schema": "super-dolphin.agent-launch-smoke.v1",
    "status": "failed",
    "run_id": "agent-launch-smoke-20260702-001",
    "failure": {
      "phase": "child_tool_surface",
      "code": "tool_surface_not_prepared",
      "message": "toolbridge: codex tool surface is not prepared"
    }
  },
  "evidence": {
    "backend_log_tail": "remoteLauncher: thread/start RPC begin ...",
    "frontend_log_tail": "frontend.rpc.failed turn/start ...",
    "toolbridge_signature": "codex tool surface is not prepared"
  },
  "owner_hint": [
    "internal/platform/toolbridge",
    "internal/provider/codexapp",
    "cmd/mcp-orch/orchestration"
  ],
  "recommended_tests": [
    "./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/provider/codexapp -count=1",
    "cd frontend-app && npm run smoke:desktop:agent-launch"
  ]
}
```

## 自动修复循环边界

浏览器/E2E 脚本只负责：

- 复现。
- 分类。
- 归档证据。
- 给出候选模块。

Codex repair loop 负责：

1. 读取 `failure.json`。
2. 根据 `classification` 选择候选模块。
3. 先补 RED 测试。
4. 实现最小修复。
5. 跑 scoped Go/frontend 测试。
6. 复跑 desktop agent launch smoke。
7. 只在验证通过后提交。

禁止：

- E2E 脚本直接改源码。
- 失败分类不明确时自动改多个模块。
- 只因为模型 JSON 声称成功就跳过 runtime evidence。

## 分阶段实施计划

### Phase 1：文档与错误分类

- 落地本文档。
- 实现错误签名分类表。
- 为 `failure.json` 格式补脚本单元测试。

### Phase 2：RPC 级 smoke

- 新增 `frontend-app/scripts/desktop-agent-launch-smoke.mjs`。
- 增加 `package.json` script：

```json
{
  "smoke:desktop:agent-launch": "node scripts/desktop-agent-launch-smoke.mjs"
}
```

- 覆盖 `thread/start`、`turn/start`、最终 JSON 提取和分类。

### Phase 3：Playwright UI 级 smoke

- 新增 `frontend-app/tests/e2e/desktop-agent-launch.spec.js`。
- 使用同一 prompt 和同一 JSON 验收函数。
- 失败时保存 screenshot 和 trace。

### Phase 4：deterministic fixture

- 增加可控 provider 或 fixture，使 CI 不依赖真实模型随机行为。
- fixture 必须能稳定触发：
  - `launch_agent`
  - 子 agent scoped tool call
  - 固定结构化 JSON 输出

### Phase 5：repair loop 集成

- 失败后生成修复任务输入。
- Codex 在隔离 worktree 中处理。
- 通过 scoped tests + smoke 后再提交。

## 验收标准

最小可用版本必须满足：

- 能自动启动真实桌面后端。
- 能发起一次新对话发送。
- 能捕获 `发送失败` 并分类。
- 能解析合规 JSON，并按 JSON 内容继续分析。
- 无法解析 JSON 时明确归类为 `model_output_contract_failed`。
- 失败 artifact 可供人工或 Codex repair loop 复盘。
- 不引入静默兜底。

完整版本必须满足：

- RPC smoke 和 Playwright smoke 都覆盖。
- 同类型错误矩阵至少覆盖前三个历史问题：
  - orchestration facade 缺失
  - workspace root 缺失
  - codex surface 未准备
- 成功路径断言 runtime evidence 和模型 JSON 一致。
- nightly 或手工真实 Codex smoke 可运行；CI 使用 deterministic fixture。
