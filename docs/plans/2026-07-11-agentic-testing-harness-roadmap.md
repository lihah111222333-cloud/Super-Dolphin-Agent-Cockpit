# Agentic Testing Harness Implementation Roadmap

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 从 Super-Dolphin 的实验性 trial 建立独立的 `agentic-testing-harness` 项目，并在可信边界完成后迁移现有 14 个场景。

**Architecture:** 独立 npm workspace 以 contracts 为稳定边界，Core 管理 session、policy 和 evidence，SDK 装配 runtime、adapter 与 isolation，`ath session stream` 通过单个长连接 JSONL 进程向 Codex Skill 暴露能力。实现被拆成三个可独立验收的计划，禁止在 Foundation 尚未通过时并行开放写路径或迁移产品场景。

**Tech Stack:** Node.js 20、TypeScript 5.9、npm workspaces、Vitest 4、Playwright 1.61、TypeBox、Docker、Electron、Codex `SKILL.md`。

**Verification Surface:** 独立仓库 contracts/core/SDK/CLI、Web/Electron/Wails mock fixtures、Docker 隔离与 adversarial tests、Super-Dolphin `frontend-app`、codemap 与 repo guards。

---

## Execution roots

- 独立项目：`/Users/l4place/Documents/agentic-testing-harness`
- 当前迁移 worktree：`/Users/l4place/Documents/Super-Dolphin/.worktrees/agentic-e2e-harness`
- 已批准规格：`docs/superpowers/specs/2026-07-11-agentic-testing-harness-extraction-design.md`

## Plan order

### Plan 1: Foundation

文件：`docs/plans/2026-07-11-agentic-testing-harness-foundation.md`

交付一个可安装、可运行的只读 Web 纵切：

- versioned contracts；
- Core session/policy/budget/evidence ledger；
- light isolation；
- Playwright runtime 与 Web adapter；
- TypeScript SDK；
- `ath session stream --jsonl`；
- Web fixture E2E；
- Codex Skill；
- fresh-install package smoke。

进入下一计划的门槛：

```bash
cd /Users/l4place/Documents/agentic-testing-harness
npm ci
npx playwright install chromium
npm run verify
npm run test:e2e:web
npm pack --dry-run
```

预期：全部 exit 0；read session 的写动作返回 `POLICY_BLOCKED`；EOF、`SIGINT` 和非法 JSONL 都完成 target/browser cleanup。

### Plan 2: Safety and Desktop

文件：`docs/plans/2026-07-11-agentic-testing-harness-safety-desktop.md`

在 Foundation 之上交付：

- 采集边界 redaction、截图遮罩和 evidence budgets；
- 当前 trial 所有已知 P1 adversarial regressions；
- Docker hard-isolation provider 与 attestation；
- hard-isolated write session；
- 真实 Electron adapter/fixture；
- 严格 Wails mock adapter/fixture；
- Linux/macOS/Windows 基础矩阵与 Linux Docker gate。

进入迁移计划的门槛：

```bash
cd /Users/l4place/Documents/agentic-testing-harness
npm run verify
ATH_RUN_DOCKER_TESTS=1 npm run test:docker
npm run test:e2e:electron
npm run test:e2e:wails-mock
npm run test:adversarial
```

预期：全部 exit 0；无 hard attestation 的 write session 无法执行动作；mock 缺失、隐藏 DOM、网络逃逸、secret 落盘、symlink escape 和错误 target identity 均稳定失败。

### Plan 3: Super-Dolphin Adoption

文件：`docs/plans/2026-07-11-agentic-testing-harness-super-dolphin-adoption.md`

交付：

- Super-Dolphin 产品 adapter、RPC contracts、observations 和 oracles；
- 14 个现有 goals 的 promoted scenarios；
- 新旧 harness 双跑 parity；
- 目标身份与专用端口守卫；
- 连续三次 clean replay promotion evidence；
- 旧 runner/sandbox/reporter/mock 的独立可回滚删除提交。

最终门槛：

```bash
cd /Users/l4place/Documents/Super-Dolphin/.worktrees/agentic-e2e-harness/frontend-app
npm run lint
npm test
npm run build
npm run agentic:e2e:v2:matrix
```

预期：全部 exit 0；14 个场景逐个产生 `replay_passed`；旧 trial 删除前新旧结果 parity 为 14/14。

## Sequencing rules

- [ ] Plan 1 未完成时，不创建 Electron/Wails adapter 的生产实现。
- [ ] Docker attestation adversarial tests 未通过时，不开放 `mode=write`。
- [ ] 三类独立 fixture 未通过时，不接入 Super-Dolphin。
- [ ] 新旧 14 场景 parity 未达到 14/14 时，不删除旧 trial。
- [ ] 删除旧 trial 前，保留一个只恢复旧文件的可回滚提交点。
- [ ] 任何计划中的 hook、guard 或 browser provisioning 失败都必须修根因，禁止 `--no-verify` 或隐式 fallback。

## Cross-plan invariants

- CLI stdout 在 `--jsonl` 模式下每行只能是一个 protocol envelope。
- Skill 只能调用 `ath`，不能 import SDK 或取得 Playwright page。
- Core 不 import Playwright、Electron、Wails 或 Super-Dolphin。
- Read mode 只允许观察、导航和 adapter 显式声明的只读动作。
- Write mode 必须持有与 session nonce 匹配的 hard-isolation attestation。
- 产品或基础设施失败不能被归类为成功探索。
- Secret 原文不能进入 argv、stdout、events、candidate、Markdown 或 artifact。
- Target server 不得复用未知 worktree/commit/nonce 的现有进程。

## Specification coverage map

| Approved specification area | Implemented by |
|---|---|
| Workspace boundaries and dependency direction | Foundation Tasks 1–2 |
| CLI/SDK contracts and long-lived JSONL stream | Foundation Tasks 2, 8–10 |
| External Agent observation/action loop | Foundation Tasks 3, 6, 8–9, 11 |
| Read-mode light isolation | Foundation Task 5 |
| Collection-boundary redaction and run layout | Foundation Task 4; Safety/Desktop Task 1 |
| Docker hard isolation and attestation | Safety/Desktop Tasks 3–4 |
| Web adapter | Foundation Task 7 |
| Electron and Wails mock adapters | Safety/Desktop Tasks 5–6 |
| Oracle evaluation and three-run promotion | Foundation Tasks 4, 10; Safety/Desktop Task 7 |
| Error taxonomy, adversarial tests, CI | Foundation Tasks 2, 9, 12; Safety/Desktop Tasks 2, 8 |
| Codex Skill thin adapter | Foundation Task 12 |
| Super-Dolphin product boundary and 14 scenarios | Adoption Tasks 1–6 |
| Business/desktop parity and legacy removal | Adoption Tasks 7–10 |

Coverage self-review result: every approved specification section has at least one implementation task and one explicit verification command. No implementation task introduces an in-process LLM, daemon, real Wails WebView automation, remote worker, automatic CI promotion, or other first-version non-goal.
