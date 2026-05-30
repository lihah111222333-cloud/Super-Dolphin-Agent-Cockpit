# package-embedded-pg runtime mode 隔离与 run-debug 兼容性修复计划

> **For agentic workers:** 强制要求 TDD。先写红测并确认失败，再实现最小修复，最后跑绿测与手动验收。执行本计划时建议使用 `子代理驱动开发` 或 `执行计划`，按 Work Package 逐项推进。

**Goal:** 恢复 `dev/run-debug`、`packaged app`、`sidecar/mcp` 三类运行模式的硬边界，确保 packaged 开箱即用能力不再污染开发者本机运行能力。

**Architecture:** 新增或收敛到单一 `RuntimeMode` 判定源。所有 DB、provider、desktop preflight、sidecar、前端 launch defaults 只能消费该模式与 runtime capabilities，禁止各模块再用路径、空配置、空 home、缺省 provider 自行推断 packaged。packaged 完整性校验必须保留并增强，不能为了保护 dev 而削弱 release app。

**Tech Stack:** Go runtime/config/db/provider，Vue 3 buildless frontend stores/settings，shell/PowerShell run-debug scripts，packaging scripts，PostgreSQL migrations。

日期：2026-05-29
工作区：`/Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg`

---

## 当前执行优先级

clean macOS VM 上的打包产物已完成初步验证：应用可以启动。仍存在的小 bug 可以延后处理。

当前最紧急目标是：**快速解耦 packaged runtime 与 dev/run-debug，让其他开发人员也能在本机参与测试。**

因此本轮执行优先级调整为：

1. 先恢复开发者本机 `run-debug` / Makefile / PowerShell 入口。
2. 先阻断 packaged relay、embedded PG、bundled runtime、runtime manifest 对 dev 的污染。
3. 保留 packaged clean VM first-run 作为不可回归检查，但暂不扩大打包功能修复面。
4. clean VM 小 bug、Linux verifier、企业级 release hardening、完整 artifact pipeline 延后。

本轮不应继续围绕 packaging 脚本做大规模完善，除非它直接阻断 macOS clean VM 启动或破坏 MVP 最低脚本治理。

## 0. 严重性判断：运行模式边界被污染

本问题不是单点兼容性 bug，而是架构边界问题：为了满足“最终用户拿到打包文件后开箱即用”的目标，当前改动把 packaged app 的默认假设下沉成了全局默认，导致原有开发能力被破坏。

在该边界落实前，分支不应继续向生产打包或主干合并推进。

## 1. 运行模式定义

| 模式 | 目标用户 | 允许的默认能力 | 禁止污染的能力 |
| --- | --- | --- | --- |
| `dev/run-debug` | 开发者 | 使用本机 PostgreSQL、git、Codex、Claude、`.env`、`~/.codex`、`~/.claude` | 不得要求 packaged relay URL/token、bundled Codex、embedded PostgreSQL、runtime manifest、LSP bundle |
| `packaged app` | 最终用户 | embedded PostgreSQL、app-managed Codex home、relay/bootstrap、bundled runtime、runtime manifest | 不得覆盖开发者本机配置，也不得成为 dev 默认 |
| `sidecar/mcp` | 被桌面或外部进程拉起的子进程 | 继承父进程明确传入的模式、资源路径与环境 | 不得自行猜测 packaged/dev，也不得启动 owner-only embedded 资源 |

### 1.1 术语表

- **RuntimeMode**：进程当前运行模式，只能是 `dev`、`packaged`、`sidecar` 中的明确值。它必须先被解析，再被 DB/provider/preflight/frontend 使用。
- **packaged runtime sentinel**：证明当前进程是真实 release package 的强证据。不能只是 `.app/Contents/MacOS` 路径或某个文件名存在。
- **runtime manifest**：release package 中描述 bundled binaries、LSP bundle、embedded PG、migrations 等产物的 manifest。manifest 必须可解析、schema/version 合法、路径归属于当前 package root。
- **app-managed home**：应用为最终用户管理的 provider home，例如 packaged Codex home。dev 缺省不能使用它。
- **relay/bootstrap**：packaged Codex 使用的 relay base URL 与 bootstrap token。它是 packaged-only 配置，不是 dev 默认。
- **provider prefs scope**：provider 设置来源。project scope 优先于 global scope，但 partial project prefs 不能吞掉 global 中的有效本机配置。
- **sidecar/mcp**：由 owner 进程拉起的 mcp-orch/mcp-lsp/mcp-ida 等子进程。sidecar 只能继承 owner 明确传入的 runtime mode 和资源路径。
- **baseline migration**：embedded/fresh DB 初始化时的 `001_baseline.sql` 与 `schema_migrations` 标记。执行与标记必须原子化。

## 2. 硬性设计原则

1. **packaged 的开箱即用能力只能在 packaged runtime sentinel 命中后生效。**
   - sentinel 必须是强证据：manifest 位置正确、schema 合法、路径与当前 executable/package root 绑定。
   - 不能仅凭 `.app/Contents/MacOS`、可执行路径、空配置、空 home、缺省 provider 来推断 packaged runtime。

2. **dev/run-debug 默认必须保护开发者已有环境。**
   - DB 默认走本机 PostgreSQL 或明确的 dev DSN。
   - Codex 默认走 `CODEX_HOME`/`~/.codex` 与本机 CLI auth。
   - Claude 默认走本机 Claude CLI/auth/home。
   - 缺省 provider/sandbox/home 不得 materialize packaged 默认值。

3. **packaged-only 配置不得在 dev preflight 中 fail。**
   - `SUPER_DOLPHIN_CODEX_RELAY_*`、bundled Codex、embedded PostgreSQL、runtime manifest、LSP bundle 检查只能属于 packaged runtime。
   - 非 packaged dev 中即使存在残留 packaged env，也不得阻断 desktop 启动；最多在真正进入 packaged/app-managed 路径时 fail-fast。

4. **配置解析必须先判定运行模式，再选择默认值。**
   - 禁止“空配置 => packaged 默认”的隐式分支。
   - 禁止前端或后端在缺省偏好时主动注入 `super-dolphin-relay`、app-managed home、embedded PG。

5. **Fail-Fast 仍然保留，但必须在正确边界内 fail。**
   - packaged runtime 缺少必须产物时应立即失败。
   - dev runtime 缺少开发配置时应给出开发者可操作错误。
   - 不允许用 packaged fallback 掩盖 dev 配置错误，也不允许用 dev fallback 掩盖 packaged 完整性错误。

## 3. RuntimeMode 单一判定规则

必须新增或收敛到一个中心化 `runtimeenv.ResolveMode(input)`，并让所有模块消费同一个结果。

优先级：

1. `run-debug.sh`、`run-debug.ps1`、`make run-agent-terminal-debug*` 明确设置 `RuntimeMode=dev`。这是开发启动最高优先级。
2. packaged owner 进程只有在命中有效 packaged sentinel 后才是 `RuntimeMode=packaged`。
3. sidecar/mcp 只能继承父进程传入的 `RuntimeMode` 与资源路径，不能自行 auto-detect。
4. 其他情况默认为 `dev`，不得 materialize packaged 默认。

禁止事项：

- DB、provider、preflight、frontend store、sidecar main 各自重新根据 `.app` 路径、manifest 文件存在性、空 home、空 provider 推断模式。
- `run-debug` 被 `.env` 中残留的 packaged env 反转成 packaged。
- sidecar 看到自己在 `Contents/Resources/bin` 就自行改写 `PROJECT_ROOT`、`PATH`、`CODEX_HOME` 或要求 bundled 产物。

## 4. packaged sentinel 规则

packaged sentinel 必须满足以下条件：

- macOS：manifest 必须位于当前 app bundle 的 `Contents/Resources/runtime-manifest.json`，且 `Resources` 目录必须由当前 executable 所属 bundle 推导得到。
- Linux/package bundle：manifest 必须位于 package root，并由 package launcher 显式传入该 root。
- manifest 必须解析成功，包含 schema/version、bundled runtime paths、LSP manifest、embedded PG resource、migrations baseline 等必需字段。
- manifest 中相对路径必须落在 package root 内，且指向的文件存在、权限正确、必要时 digest 匹配。
- dev repo 根目录或 `.env` 中残留的 `runtime-manifest.json`/packaged env 不能单独触发 packaged mode。

显式 packaged env 只能触发 packaged 校验流程，不能替代 manifest 校验。

## 5. Runtime capabilities 合约

前端不得自行推断 packaged。后端需要提供只读 runtime capabilities，例如：

```json
{
  "mode": "dev|packaged|sidecar",
  "packagedCodexReady": false,
  "appManagedHomeEnabled": false,
  "embeddedPostgresOwner": false
}
```

规则：

- 前端只有在 `mode=packaged` 且 `packagedCodexReady=true` 时，才可 materialize `super-dolphin-relay`、app-managed Codex home、bundled runtime defaults。
- `dev/run-debug` 下，无论偏好为空还是 `.env` 残留 packaged relay 变量，launch payload 都不得包含 packaged-only provider/home。
- sidecar capabilities 由 owner 进程传入，sidecar 不提供自我推断结果。

## 6. 文件地图

| 子系统 | 主要文件 | 责任 |
| --- | --- | --- |
| runtime mode / sentinel | `internal/platform/runtimeenv/**` | 单一 RuntimeMode 判定、packaged manifest 校验、sidecar 继承规则 |
| desktop preflight | `internal/app/app.go`、`internal/app/*preflight*_test.go` | dev vs packaged preflight 分流，relay/bootstrap 校验边界 |
| config / DB | `internal/platform/config/config.go`、`internal/platform/db/module.go`、`internal/platform/embeddedpg/**` | dev DSN、embedded PG owner、baseline migration 原子性 |
| provider home / Codex | `internal/provider/codexapp/**`、`internal/provider/shared/**`、`internal/provider/claudecli/**` | Codex/Claude home 解析、本机 CLI auth、app-managed home gating |
| thread / preferences | `internal/module/thread/**`、`internal/module/uistate/**` | provider config contract、global/project prefs 解析 |
| frontend settings/store | `cmd/agent-terminal/frontend/vue-app/provider-config-options.js`、`stores/**`、`pages/settings/ProviderSettings.ts` | launch payload defaults、provider prefs UI、runtime capabilities consumption |
| dev entrypoints | `run-debug.sh`、`run-debug.ps1`、`Makefile` | 显式 dev mode、dev DB preflight、residual packaged env inert |
| packaging / release smoke | `scripts/package_*.sh`、`scripts/verify_packaged_app_macos.sh`、`scripts/*guard_test.go` | release package 完整性、Linux/macOS verifier、runtime manifest contracts |

## 7. Packaging Script Governance：当前只要求 MVP 最低要求

当前打包脚本还存在另一类严重工程化风险：脚本绑定私人 URL、通过交互输入 key、从打包机固定路径复制依赖。这类做法即使能在某台机器上完成打包，也不满足企业级项目的可复现、可审计、可交接要求。

本阶段要求：**先满足 MVP 最低要求**。企业级 release 流程作为后续演进方向记录，但不作为本轮修复的 hard gate。

### 7.1 本轮 MVP 最低要求

MVP 可以不是完全自动下载和完全 CI 化，但不能是机器绑定、秘密不透明、路径不可复现。

本轮必须做到：

1. 删除脚本中的私人 URL 默认值。
2. 删除交互输入 release key 的流程。
3. 固定路径改成显式参数或 env，例如：
   - `SUPER_DOLPHIN_POSTGRES_BUNDLE_DIR`
   - `SUPER_DOLPHIN_LSP_BUNDLE_DIR`
   - `SUPER_DOLPHIN_CODEX_BUNDLE_PATH`
   - `SUPER_DOLPHIN_CODEX_RELAY_BASE_URL`
   - `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN`
4. 提供 `.env.packaging.example`，只包含变量名和说明，不包含真实 secret。
5. 缺少依赖或 secret 时 fail-fast，错误信息说明如何准备，而不是回退到打包机路径。
6. local helper 可以支持本机依赖，但必须明确是 `dev-local` profile，不能伪装成 release 流程。
7. clean machine 打包要么能根据文档准备依赖并运行，要么明确 fail-fast 指出缺哪个 bundle 和如何获取。
8. 增加 guard，禁止 release 脚本出现：
   - `/Users/ai`
   - 私人域名默认值
   - `read -s` / `read -p` 获取 release key
   - 未校验 checksum 的 release artifact 复制

### 7.2 后续企业级目标

企业级 release 打包必须满足：

1. **不硬编码私人 URL/key/path。**
   - release 脚本中不得内置私人 relay URL、个人目录、个人机器路径、临时下载目录。
   - relay base URL、bootstrap token、release channel 等必须来自 CI secrets、secret manager、明确 env file 或显式参数。

2. **不交互输入敏感 key。**
   - 禁止 `read -p` / `read -s` 采集 release key 后继续打包。
   - 缺少 secret 时应 fail-fast，并说明需要设置哪个变量或 secret。
   - key/token 不得写入产物、日志、shell history 或 runtime manifest。

3. **依赖来源可复现。**
   - 不能从 `/Users/ai/...`、某台打包机的 Homebrew Cellar、下载目录或临时本机路径复制 release 依赖。
   - 依赖必须来自以下来源之一：
     - 仓库声明的 manifest/lockfile/checksum；
     - 内部 artifact registry / GitHub Release / S3 等可下载 artifact；
     - CI 构建并上传的 artifact；
     - 本地 MVP 显式传参路径，但不得作为 release 默认。

4. **产物必须可审计。**
   - package 中的 runtime manifest 应记录版本、commit、平台、架构、关键 bundled artifact、checksum、build profile。
   - verifier 必须校验 manifest、bundle 文件、权限、checksum、embedded PG、Codex、LSP、baseline migration。

5. **profile 必须分层。**
   - `dev-local`：开发者快速试包，可使用本机依赖，但必须显式传参，不保证可复现。
   - `release-local`：本地 release 试包，必须使用 manifest/checksum，不允许私人默认路径。
   - `ci-release`：企业级发布路径，从 CI secrets/artifacts 获取依赖，产出可审计 package。

### 7.3 本计划中的处理原则

- run-debug 兼容性修复不能继续扩大脚本的机器绑定问题。
- packaged release integrity gate 本轮只覆盖 MVP 最低 script governance，至少防止新增硬编码 URL/key/path。
- 若 MVP 暂时保留手动准备 bundle，必须在文档中声明 profile、变量、准备步骤和限制。
- 企业级 CI release、artifact registry、secret manager 集成后续实施；当前脚本不得再把个人机器状态当成默认 release 环境。

## 8. 高可信问题清单

本清单来自 5 个初审 agent 和 5 个复审 agent 的交叉审查。只保留有代码证据、能解释开发或生产破坏路径、且符合 Fail-Fast/生产可修复原则的问题。

### P0/P1-1：非 packaged dev 无 `DATABASE_URL` 时，`run-debug` 会启动到空 DSN 后失败

证据：

- `internal/platform/config/config.go:30` 调用 `embeddedpg.ResolveFromEnvironment(projectRoot)` 生成 DB 配置。
- `internal/platform/embeddedpg/config.go:54-58` 在非 packaged、未显式请求 embedded PostgreSQL、且无 `DATABASE_URL`/`POSTGRES_CONNECTION_STRING` 时返回空 DSN。
- `internal/platform/db/module.go:49-53` 对空 `DatabaseURL` fail-fast。
- `run-debug.sh` 的 DB preflight 使用局部 `DB_URL="${DATABASE_URL:-postgres://postgres:123@127.0.0.1:5432/go_agent_v2?sslmode=disable}"` 检查，但不会导出同一 DSN 给后端进程。
- `run-debug.ps1` 需要单独覆盖，不能只修 shell 路径。

影响：

开发者本机已有 PostgreSQL，但未显式设置 `DATABASE_URL` 时，preflight 可能显示检查通过，真实桌面进程仍因空 DSN 失败。该行为把“开发默认本机 PG”的预期改成了“必须额外配置 DB”。

本轮决策：

- 采用 **Option A：恢复 dev/run-debug 明确 dev DSN**。
- `run-debug.sh` 与 `run-debug.ps1` 必须把 preflight 使用的同一 DSN 传给后端进程。
- 如果开发者显式设置 `DATABASE_URL`，使用显式值。
- 如果默认 dev DSN 不可连接，run-debug 在启动前给出开发者可操作错误，不能等 FX/db provider 后置失败。

### P0/P1-2：packaged Codex relay preflight 无条件进入 dev desktop 启动路径

证据：

- `internal/app/app.go:158-165` 每次 desktop preflight 都调用 `ensurePackagedCodexBootstrap`。
- `internal/app/app.go:210-228` 在判断是否 packaged 之前，拒绝 `SUPER_DOLPHIN_CODEX_RELAY_API_KEY`，并要求 `SUPER_DOLPHIN_CODEX_RELAY_BASE_URL` 与 `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN` 成对存在。
- `run-debug.sh` 会 source 项目 `.env`，因此开发残留 relay 变量会直接影响 dev 启动。

影响：

开发者本机已有 Codex/Claude，不需要 packaged relay。只要 shell 或 `.env` 中残留打包用 relay URL/token/API key，`run-debug` 会在 UI 启动前失败。

修复方向：

- 先判定 `RuntimeMode`，只有 `packaged` 或显式 app-managed Codex relay 模式才读取/校验 packaged-only relay env。
- 非 packaged dev 下忽略这些 packaged-only env，或仅在真正启动 app-managed Codex identity 时处理。
- 现有方向相反的测试必须翻转或拆成 packaged-only 语义：非 packaged partial relay env/privileged API key 不阻断；packaged sentinel 命中后必须 fail-fast。

### P1-3：Codex 缺省 home 被选成 app-managed home，绕开开发者已有 `~/.codex`

证据：

- `cmd/agent-terminal/frontend/vue-app/stores/codex-sandbox-defaults.js:9-15` 即使没有 `codexHome`，仍构造 `codexInstanceKey` 与 `codexModelProvider`。
- `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:419-424` Codex launch 总是把该 config 放入 `thread/start`。
- `internal/provider/codexapp/driver_pool_routing.go:133-135` 空 `rawHome` 直接选择 `useAppManagedHome: true`。

影响：

开发者已有本机 Codex CLI 与 `~/.codex` 登录态，但新建 Codex thread 可能被切到 app-managed home，从而要求 packaged bootstrap/relay 配置，表现为本机 Codex 可用但应用内 Codex 启动失败。

修复方向：

- 空 `codexHome` 不应默认 app-managed；仅 packaged runtime 或用户显式选择 app-managed 时使用 app-managed home。
- 普通 dev/CLI 模式下，空 home 应解析为 Codex CLI 默认 home（`CODEX_HOME` 或 `~/.codex`），或者前端不要发送部分 identity。
- 补测试：无 Codex home 偏好时，dev Codex 启动不得设置 app-managed `CODEX_HOME`。

### P1-4：前端默认注入 `super-dolphin-relay`，且设置页无法改回本机 provider

证据：

- `cmd/agent-terminal/frontend/vue-app/provider-config-options.js:24-28` 将 `codexModelProvider` 默认值设为 `super-dolphin-relay`。
- `cmd/agent-terminal/frontend/vue-app/stores/codex-sandbox-defaults.js:9-15` 在缺省偏好时仍注入该 provider。
- `ProviderSettings.ts` 没有足够清晰的本机 provider 恢复入口。

影响：

新开发环境或清空偏好后，UI 启动 Codex 会默认带 packaged relay provider。开发者本机 `~/.codex/config.toml` 通常没有该 provider，导致本机 Codex auth/config 被绕过或启动失败。

修复方向：

- `super-dolphin-relay` 只在 packaged app-managed relay bootstrap 成功或用户显式选择时 materialize。
- dev/local 默认不要发送 `codexModelProvider`，让本机 Codex 配置决定；或恢复可编辑 provider 设置并默认 `openai`/local。
- 增加开发者迁移提示：提供 UI reset 或文档指引，把已保存的 `super-dolphin-relay`/app-managed home 偏好恢复为 local CLI。

### P1-5：provider active scope 与 provider 细节 scope 不一致，可能丢失全局本机配置

证据：

- `thread-actions-helpers.js` 中 active provider 会从 project scope fallback 到 global，但 model/effort/sandbox/codexHome/codexModelProvider 等 provider 细节只按 launch `cwd` 读取。

影响：

开发者已有全局本机 Codex/Claude 配置，但项目 scope 没有配置时，启动时 provider 来自 global，provider 细节却落入空 project prefs，从而触发 packaged defaults、partial identity 或 app-managed home。

修复方向：

- 明确 provider prefs 优先级：`explicit launch > project non-empty > global non-empty > omit/backend default`。
- 如果 active provider 来自 global，则 provider 细节也从 global 读取。
- project partial prefs 不得吞掉 global 中的有效本机配置。
- 补测试：global-only、project override、project partial、Codex/Claude 双 provider。

### P1-6：`runtimeenv` 仅凭 `.app/Contents/MacOS` 路径判定 packaged，debug `.app` 可能被强制要求打包产物

证据：

- `ConfigurePackagedApp`/packaged 判定依赖 `.app/Contents/MacOS` 或 `Contents/Resources/bin` 路径形态。
- packaged runtime 后续会要求 `Resources/bin/mcp-*`、`Resources/lsp/lsp-manifest.json` 等打包产物。
- `embeddedpg.ResolveConfig` 还会在 packaged 且无 `DATABASE_URL` 时生成 embedded PostgreSQL DSN。

影响：

Wails/IDE/debug 如果产生 `.app` 形态但没有完整 package 产物，会启动即失败，或错误切到 embedded PostgreSQL，而不是使用开发者本机 PG。

修复方向：

- `.app` 路径不能单独代表 packaged runtime。
- `.app + 无有效 runtime manifest` 必须是 dev。
- `.app + 有效 runtime manifest + 当前 bundle 绑定` 才能是 packaged。
- repo 根目录存在 `runtime-manifest.json` 但 executable 非 package bundle 时仍是 dev。

### P1-7：sidecar/mcp 进程自行执行 packaged runtime auto-detect，破坏父进程边界

证据：

- `cmd/mcp-orch/main.go`、`cmd/mcp-lsp/main.go`、`cmd/mcp-ida/main.go` 在 sidecar 角色下仍有独立 runtimeenv 配置路径。

影响：

sidecar 可能因为自身二进制位于 `Contents/Resources/bin` 而自行切换 packaged、改写环境或要求 bundled 产物。sidecar 应服从 owner 进程传入配置，而不是成为第二个 runtime owner。

修复方向：

- sidecar 不得自行调用 packaged auto-detect。
- packaged desktop owner 负责解析 RuntimeMode 与资源路径，并显式传给 sidecar。
- sidecar 只校验父进程传入的必需 env；缺失则 fail-fast，不回退路径猜测。

### P1-8：embedded PostgreSQL baseline 迁移存在半损坏风险

证据：

- `internal/platform/db/module.go:197` 附近的 `applyBaselineIfMissing()` 忽略 `threadsExist` 查询错误。
- 读取 `001_baseline.sql` 失败后仍可能继续写 `schema_migrations` 标记。

影响：

fresh embedded DB 或 packaged migrations 缺失/不可读时，可能未执行 baseline 却把 baseline 标记为 applied，导致数据库进入不可自动恢复的半损坏状态。该项属于 packaged 生产完整性 gate。

修复方向：

- baseline 探测、baseline SQL 读取、执行任一步失败都必须立即返回错误。
- baseline SQL 执行与 marker 写入必须原子化。
- 只有 baseline SQL 成功执行，或明确检测到既有 schema 时，才写入 `schema_migrations`。

### P2-9：Codex sandbox 缺省被前端覆盖为 `workspace-write`，且丢弃 writable roots/network access

证据：

- `cmd/agent-terminal/frontend/vue-app/stores/codex-sandbox-defaults.js:18-38` 对 undefined 返回 `{ 'workspace-write': null }`。
- 同函数把 `{type, writableRoots, networkAccess}` 压成单一 sandbox mode，丢弃细节。

影响：

开发者未显式设置 sandbox 时，本机 Codex 默认 sandbox 被 UI 覆盖；用户保存的 writable roots/network access 也可能不生效。

修复方向：

- 缺省 sandbox 返回 `null`，只在用户显式保存时发送。
- 若 UI 保留 writable roots/network access 字段，则按后端支持格式完整传递；否则移除不可生效字段。

## 8. 不在首轮实现但仍是 release-blocking 的相邻风险

这些问题不一定阻断 `run-debug` 修复，但不能从 packaged release gate 中移除：

- packaged 脚本中 bundled Codex opt-out 与 runtime manifest/verifier 不一致。
- macOS local packaging helper 写死 `/Users/ai` 与私人 relay URL。
- release 脚本交互输入 key，或默认绑定私人 relay URL。
- release 依赖从具体打包机路径复制，缺少 manifest/checksum/source-of-truth。
- embedded PostgreSQL crash 后 ownership 只在进程内保存，socket dir 未包含 app/home/data hash。
- `.env` dev malformed line 静默忽略。
- Linux packaged 缺少与 macOS 等价的 release verifier。
- runtime 启动未完整校验 runtime manifest、LSP digest、Codex manifest、embedded PG binaries/share、baseline migration 资源。

要求：本轮修复不能削弱 packaged 完整性。若触碰 packaged runtime、manifest、embedded PG、packaging scripts，必须补对应 release smoke 或 verifier 测试。

## 9. Work Packages

### 本轮优先执行路径

为尽快让其他开发人员参与测试，本轮先执行解耦最小闭环：

1. `WP0`：建立 RuntimeMode / ProcessRole 边界，阻止各模块自行猜 packaged。
2. `WP1`：修复 `run-debug.sh`、`run-debug.ps1`、Makefile 的 dev mode 与 dev DB DSN。
3. `WP2`：限定 packaged Codex relay preflight，不让 dev 被 relay env 阻断。
4. `WP3`：修复 Codex home/provider 默认，dev 默认使用本机 CLI/auth。
5. `WP4`：修复 sidecar 继承边界，避免 sidecar 自行 auto-detect packaged。

`WP5`、`WP7` 只处理会直接破坏 macOS clean VM 已验证启动路径或 MVP 最低脚本治理的问题；其余 packaged hardening 延后。

`WP6` sandbox 默认问题除非被证明会触发 packaged relay/app-managed home 污染，否则移到解耦之后处理。

### WP0：建立 RuntimeMode 单一判定源

**Files:**

- Modify: `internal/platform/runtimeenv/**`
- Modify: `cmd/agent-terminal/main.go`
- Modify: `cmd/mcp-orch/main.go`
- Modify: `cmd/mcp-lsp/main.go`
- Modify: `cmd/mcp-ida/main.go`
- Test: `internal/platform/runtimeenv/*_test.go`

**Steps:**

- [ ] 写红测：`.app/Contents/MacOS` 无有效 manifest 时为 dev。
- [ ] 写红测：`.app + 有效 bundle manifest` 时为 packaged。
- [ ] 写红测：repo 根目录残留 `runtime-manifest.json` 但 executable 非 package bundle 时为 dev。
- [ ] 写红测：sidecar 未收到父进程 packaged mode 时不 auto-detect packaged。
- [ ] 实现 `ResolveMode(input)` 或等价中心入口。
- [ ] 替换 DB/preflight/provider/sidecar 中分散的 packaged 猜测。

### WP1：修复 run-debug DB 与 dev mode 显式入口

**Files:**

- Modify: `run-debug.sh`
- Modify: `run-debug.ps1`
- Modify: `Makefile`
- Modify/Test: `internal/platform/config/*_test.go`
- Test: scripts 相关测试或新增脚本静态/行为测试

**Steps:**

- [ ] 写红测：无 `DATABASE_URL` 时 shell run-debug preflight 与后端使用同一 dev DSN。
- [ ] 写红测：PowerShell run-debug 覆盖同样行为。
- [ ] 写红测：`make run-agent-terminal-debug*` 设置 dev mode。
- [ ] 实现 shell/PowerShell/Makefile 显式 dev mode 与 DSN 传递。
- [ ] 错误信息包含可操作提示：使用哪个 DSN、如何设置 `.env`、如何启动本机 PG。

### WP2：限定 packaged Codex relay preflight

**Files:**

- Modify: `internal/app/app.go`
- Test: `internal/app/desktop_preflight_test.go`

**Steps:**

- [ ] 翻转现有错误方向测试：非 packaged partial relay env 不阻断 preflight。
- [ ] 写红测：非 packaged privileged relay API key 不阻断 dev desktop preflight。
- [ ] 写红测：packaged sentinel 命中时 partial relay env 必须 fail-fast。
- [ ] 写红测：packaged sentinel 命中时 privileged relay API key 必须 fail-fast。
- [ ] 实现 preflight 按 RuntimeMode 分支。

### WP3：修复 Codex home/provider 默认与前端 capabilities

**Files:**

- Modify: `internal/provider/codexapp/**`
- Modify: `internal/provider/shared/**`
- Modify: `internal/module/thread/**`
- Modify: `internal/module/uistate/**`
- Modify: `cmd/agent-terminal/frontend/vue-app/provider-config-options.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/stores/**`
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/settings/ProviderSettings.ts`
- Test: related Go tests + frontend vitest

**Steps:**

- [ ] 写红测：dev 无 `codexHome` 偏好时不选择 app-managed home。
- [ ] 写红测：无偏好时 launch payload 不包含 `super-dolphin-relay`。
- [ ] 写红测：runtime capabilities 为 packaged ready 时才允许 packaged defaults。
- [ ] 写红测：global-only/provider project partial prefs 按优先级解析。
- [ ] 实现 backend runtime capabilities。
- [ ] 实现 frontend 消费 capabilities，不再自行推断 packaged。
- [ ] 增加开发者迁移/重置入口或明确提示。

### WP4：修复 sidecar 边界

**Files:**

- Modify: `cmd/mcp-orch/main.go`
- Modify: `cmd/mcp-lsp/main.go`
- Modify: `cmd/mcp-ida/main.go`
- Modify: sidecar spawn/env 相关 provider/runtime code
- Test: sidecar/runtimeenv tests

**Steps:**

- [ ] 写红测：sidecar 位于 package bin 但未收到父进程 mode 时不 auto-detect packaged。
- [ ] 写红测：packaged owner 传入完整 mode/resource env 时 sidecar 正常使用。
- [ ] 实现 sidecar 只消费父进程配置，缺失必需 env 时 fail-fast。

### WP5：修复 embedded DB baseline 原子性

**Files:**

- Modify: `internal/platform/db/module.go`
- Test: `internal/platform/db/*baseline*_test.go` 或相邻测试

**Steps:**

- [ ] 写红测：baseline 探测 query 失败不写 marker。
- [ ] 写红测：`001_baseline.sql` 缺失/不可读不写 marker。
- [ ] 写红测：baseline exec 失败不写 marker。
- [ ] 实现 baseline exec 与 marker 写入原子化。

### WP6：清理 Codex sandbox 默认与 UI 一致性

**Files:**

- Modify: `cmd/agent-terminal/frontend/vue-app/stores/codex-sandbox-defaults.js`
- Modify: related frontend tests

**Steps:**

- [ ] 写红测：无 sandbox 偏好时 launch payload 不包含 sandbox。
- [ ] 写红测：writable roots/network access 不被静默丢弃，或 UI 不展示不可生效字段。
- [ ] 实现最小修复。

### WP7：packaged release integrity gate + MVP script governance

**Files:**

- Modify: `internal/platform/runtimeenv/**`
- Modify: `scripts/package_*.sh`
- Modify: `scripts/package_*_local.sh`
- Modify/Add: `.env.packaging.example`
- Modify: `scripts/verify_packaged_app_macos.sh`
- Add/Modify: Linux verifier or package guard tests
- Test: `scripts/*guard_test.go`

**Steps:**

- [ ] 写红测：packaged manifest 缺必需字段 fail-fast。
- [ ] 写红测：LSP manifest digest 不匹配 fail-fast。
- [ ] 写红测：Codex manifest/binary 缺失 fail-fast。
- [ ] 写红测：embedded PG binaries/share 缺失 fail-fast。
- [ ] 写红测：Linux package 也有 release verifier 或等价 guard。
- [ ] 写红测：release scripts 不得包含 `/Users/ai`、私人 relay URL 默认值、交互输入 release key。
- [ ] 写红测：release dependency copy 必须来自显式 env/profile/manifest，缺失时 fail-fast。
- [ ] 实现 runtime manifest 与 release smoke 校验增强。
- [ ] 增加 `.env.packaging.example`，说明 MVP 手动 bundle 准备变量，不包含真实 secret。

## 10. TDD 红测矩阵

| 问题 | 红测位置 | 红测预期失败 | 绿测预期 |
| --- | --- | --- | --- |
| P0/P1-1 dev DB | `run-debug.sh`/`run-debug.ps1` 脚本测试、`internal/platform/config` | preflight DSN 与后端 DSN 不一致或空 DSN | shell/PowerShell/Makefile 都显式 dev mode，并传同一 DSN |
| P0/P1-2 relay preflight | `internal/app/desktop_preflight_test.go` | 非 packaged partial relay env 当前会失败 | 非 packaged 不失败；packaged sentinel 命中时失败 |
| P1-3 Codex home | `internal/provider/codexapp`、thread launch tests | 空 home 选择 app-managed | dev 空 home 使用 CLI default 或 omit identity |
| P1-4 relay provider | `provider-config-options.test.js`、thread-store tests | 默认包含 `super-dolphin-relay` | dev 默认不 materialize packaged provider |
| P1-5 prefs scope | `thread-store-provider-preference.test.js`、uistate tests | global-only 配置被 project 空值吞掉 | 按 `explicit > project non-empty > global non-empty > omit` |
| P1-6 sentinel | `internal/platform/runtimeenv`、`embeddedpg` tests | `.app` 路径本身被判 packaged | 只有有效 sentinel 判 packaged |
| P1-7 sidecar | sidecar/runtimeenv tests | sidecar 自行 auto-detect packaged | sidecar 只继承父进程 mode |
| P1-8 baseline | `internal/platform/db` tests | baseline read/query/exec 失败仍写 marker | 任一步失败都不写 marker |
| P2-9 sandbox | `codex-sandbox-defaults.test.js`、thread-store tests | undefined 变成 workspace-write | undefined 不发送 sandbox |
| packaged integrity | `scripts/*guard_test.go`、runtimeenv tests | 缺/篡改 manifest 或 bundle 不 fail | packaged release smoke fail-fast |
| MVP script governance | `scripts/*guard_test.go` | release scripts 含私人 URL/key prompt/`/Users/ai` 固定路径 | scripts 只接受显式 env/profile/manifest，缺失 fail-fast |

## 11. 验证矩阵

Go：

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/platform/config ./internal/platform/db ./internal/platform/embeddedpg ./internal/platform/runtimeenv ./internal/provider/codexapp ./internal/provider/shared ./internal/module/thread ./internal/module/uistate -count=1
```

Scripts / packaging guards：

```bash
./scripts/test_with_guard.sh ./scripts -count=1
bash -n run-debug.sh scripts/package_*.sh scripts/prepare_lsp_bundle_*.sh scripts/build_relocatable_postgres_macos.sh scripts/verify_packaged_app_macos.sh
```

Frontend：

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  vue-app/provider-config-options.test.js \
  vue-app/thread-store-provider-preference.test.js \
  vue-app/codex-sandbox-defaults.test.js \
  vue-app/thread-store-codex-default-home.test.js \
  vue-app/provider-settings.behavior.test.js \
  vue-app/thread-store.actions.test.js
```

Release smoke：

```bash
# macOS package verifier must still pass after dev isolation fixes.
scripts/package_macos.sh
scripts/verify_packaged_app_macos.sh <path-to-built-app>

# Linux must have equivalent verifier or package guard before release.
scripts/package_linux.sh
```

## 12. 手动验收矩阵

| 场景 | 命令/入口 | 预期 |
| --- | --- | --- |
| shell full debug | `./run-debug.sh` option 1 | 显式 dev mode；使用本机 PG/dev DSN；不要求 relay/bundled artifacts |
| shell run-only | `./run-debug.sh` option 4 | 与 option 1 同样使用 dev mode 与同一 DSN |
| PowerShell full debug | `pwsh ./run-debug.ps1` option 1 | 与 shell 一致，不能遗漏 DB/env 行为 |
| PowerShell run-only | `pwsh ./run-debug.ps1` option 4 | 与 shell run-only 一致 |
| Makefile debug | `make run-agent-terminal-debug` | 显式 dev mode，不因 packaged env 残留失败 |
| Makefile plain | `make run-agent-terminal-debug-plain` | 显式 dev mode，不要求 runtime manifest/LSP bundle |
| residual relay env | 只设 relay URL、只设 bootstrap token、或设 privileged API key 后运行 dev | desktop preflight 不阻断；只有进入 packaged/app-managed 路径才 fail-fast |
| local Codex | 无 Codex 偏好但 `~/.codex` 已登录 | launch payload 不包含 app-managed home / `super-dolphin-relay` |
| global prefs | 仅 global provider prefs，无 project prefs | 项目内 thread 继承 global provider 细节 |
| debug `.app` | `.app/Contents/MacOS` 无有效 manifest | dev mode，不要求 `Resources/bin`/LSP bundle/embedded PG |
| packaged baseline missing | packaged fresh embedded DB 缺失 baseline | fail-fast，且不得写假的 applied migration |

## 13. 用户可见错误信息要求

- dev DB 缺失/不可连：必须说明当前使用的 DSN、如何设置 `DATABASE_URL`、如何启动或创建本机 PostgreSQL DB。
- dev 中残留 packaged relay env：不得阻断；如显示 warning，必须说明这些变量在 dev 中 inert。
- packaged 中 relay/bootstrap 缺失：必须说明缺哪个变量、来源是 packaged runtime、不能建议使用 privileged API key。
- Codex home/provider 冲突：必须说明当前使用 project 还是 global prefs、如何 reset to local CLI。
- sidecar 缺父进程 env：必须说明是 parent launch contract 错误，而不是让用户配置 packaged bundle。

## 14. 非目标

- 不重构整个 provider 设置系统；只修本轮涉及的默认值、scope fallback、runtime capabilities。
- 不改变 packaged app 开箱即用目标；只把 packaged 能力限制在 packaged runtime 内。
- 不把 dev 缺配置改成静默降级；dev 仍要 fail-fast，但错误必须属于 dev 边界。
- 不一次性解决所有 embedded PG 稳定性问题；但不能引入或保留会破坏 release startup 的 baseline 半损坏风险。
- 不把 release verifier 全面重写成新系统；先补缺失的关键完整性 gate。

## 15. 风险、回滚与阻断条件

| 风险 | 防护 |
| --- | --- |
| 修 dev 隔离时削弱 packaged 完整性 | packaged sentinel、runtime manifest、release smoke 必须继续 fail-fast |
| MVP 打包继续依赖个人机器状态 | MVP script governance guard 禁止私人 URL/key/path；MVP 依赖必须显式 env/profile |
| RuntimeMode 中心化引入大范围回归 | 先红测锁定 dev/package/sidecar 正负例，再逐模块替换 |
| 前端 omit defaults 导致 packaged Codex 不可用 | runtime capabilities 明确告诉前端 packaged ready 后再 materialize defaults |
| global/project prefs 修复改变用户已有偏好行为 | 增加迁移提示和 reset to local CLI；测试 project override 优先级 |
| DB 策略误伤开发者 | 本轮明确采用 dev DSN，shell/PowerShell/Makefile 三入口验收 |

Hard stop：

- 任一 guard 失败。
- 任一 P0/P1 没有对应红测。
- `run-debug` 仍要求 packaged relay、runtime manifest、bundled Codex、embedded PG。
- release 脚本仍硬编码私人 URL、`/Users/ai` 路径，或交互输入 release key。
- 已验证的 macOS clean VM 启动路径发生回归。
- `.app` debug layout 无 manifest 仍被判 packaged。
- sidecar 仍自行 auto-detect packaged。

## 16. 完成标准

- [ ] 代码中存在清晰的 runtime mode 单一判定源，且 packaged/dev/sidecar 的默认值和 preflight 不再互相污染。
- [ ] `run-debug.sh`、`run-debug.ps1`、`make run-agent-terminal-debug*` 对开发者已有 PG/git/Codex/Claude 环境保持兼容。
- [ ] packaged-only API key/url、bundled Codex、embedded PostgreSQL、runtime manifest 不污染非 packaged dev。
- [ ] packaged runtime 仍严格校验 release manifest、bundled artifacts、embedded PG、baseline migration、LSP/Codex resources。
- [ ] 打包脚本符合 MVP 最低 Packaging Script Governance：无私人 URL/key/path 默认值，无交互式 release key 输入，MVP 依赖全部显式参数化。
- [ ] 每个 P0/P1 都有先红后绿的自动测试。
- [ ] 手动验收矩阵通过，且错误信息符合第 13 节。
- [ ] 若保留任何行为变更，必须在错误信息或设置 UI 中明确告知用户需要的显式配置。
