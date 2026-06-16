# 应用打包改动评审：package-embedded-pg

评审日期：2026-05-28
评审对象：`/Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg` 中应用打包、embedded PostgreSQL、runtime/provider、UI 配置、打包脚本与相关文档改动。
评审方式：3 个子 agent 首轮并行评审；随后 2 个子 agent 对本总评审做交叉评审并校准优先级。

## 重要范围说明

本评审不是整个 worktree 相对 `origin/main` 的全量合并评审。交叉评审确认当前 worktree diff 很大，仍有一部分非打包主题文件未覆盖。本文件只代表上述打包相关范围内的评审结论。

交叉评审还确认：原首轮总评审中的“前端发送 partial Codex identity 会导致 `codexHome is required`”证据与当前代码/测试相反，已删除，不作为问题或阻塞项。

产品约束补充：本次打包目标是开箱即用，当前方案会暂时写死内部中转站配置，并希望内置 Codex 能力。内置 relay URL 本身可接受；随包分发的凭据必须被设计成公开、低权限、可撤销、可限流的 bootstrap/client 凭据。Codex CLI 也应优先作为已校验的打包资产随包内置，避免首次启动依赖 GitHub 下载；如果仍保留运行期下载作为兜底，必须有来源和完整性校验，且下载失败不能破坏已内置 Codex 的可用性。

## 结论

当前改动不建议合入。已确认 14 个问题。P0-2 是条件式 P0：在随包 relay 凭据尚未被证明为 public/bootstrap credential 前，按 privileged secret 风险处理。

| 优先级 | 数量 | 合入/发布判断 |
| --- | ---: | --- |
| P0 | 2 | 合入硬阻塞。未修复不得合入。 |
| P1 | 4 | packaged app 发布阻塞；若目标平台或功能在本次交付范围内，未修复不得发布。 |
| P2 | 6 | 发布前应修；不单独作为合入硬阻塞，但必须有 owner、测试或明确 follow-up。 |
| P3 | 2 | 非阻塞清理项。 |

## 优先级定义

- P0：合入硬阻塞。涉及 secret 泄露、执行未验证二进制、开箱即用核心能力依赖不可靠网络下载、明确供应链风险，或其他不可接受安全风险。
- P1：发布阻塞。会导致 packaged app 目标平台不可用、DB/进程生命周期错误、跨实例互相影响，或 fail-fast 契约破坏。
- P2：发布前应修。影响默认行为确定性、诊断准确性、验证可靠性、边界场景安全性或业务/基础设施耦合度，但不是当前合入硬阻塞。
- P3：非阻塞。文档、脚本体验或后续一致性清理。

## 合入门槛

本次改动不应合入，除非：

1. 所有 P0 已修复，并有回归测试或可执行验证。
2. 本次 release scope 内的 P1 已修复，并通过对应平台 packaged app 验证。
3. 不再把 privileged relay API key 或等价 secret 打入任何发布包；若为开箱即用随包分发凭据，必须明确其 public/bootstrap 属性和权限边界。
4. Codex CLI 作为开箱即用能力时，优先使用随包内置且已校验的二进制；任何运行期下载兜底都不得执行来源和完整性未校验的二进制。
5. 对未覆盖的 worktree diff 另做全量合并评审，不能用本文件替代。

## P0 — 合入硬阻塞

### P0-1 — Codex 开箱即用路径不能依赖运行期网络下载，fallback 不能执行未校验二进制

位置：`internal/provider/codexapp/codex_autoinstall.go:337`, `internal/provider/codexapp/codex_autoinstall.go:457`

问题：Codex 是 packaged app 开箱即用需求的一部分，但当前 autoinstall 路径会在运行期下载并安装可执行文件。这个路径同时有两个风险：首次启动依赖 GitHub/网络下载会破坏开箱即用；运行期 fallback 又没有校验 release API / asset URL 的 scheme、host、签名或 checksum，且 `SUPER_DOLPHIN_CODEX_RELEASE_API_URL` 可把 release JSON 指向任意地址。

影响：如果安装后首次启动才去 GitHub 下载，网络不可用会导致 Codex 应用能力不可用；如果下载源被误配或污染，应用还可能下载、解压并执行非官方二进制，形成供应链风险。

建议：将 Codex CLI 作为 release 构建阶段的受控资产随包内置，并在打包时记录版本、checksum 或签名校验结果。运行期下载只作为显式 fallback，且必须限制官方 HTTPS 来源并校验摘要/签名；下载失败不能破坏随包内置 Codex 的可用性。测试注入使用 fake client 或 test-only override，不要让生产 env 直接改下载源。

证据/触发条件：release API 接受 env 覆盖；asset URL 直接 GET；安装成功后会把安装目录 prepend 到 PATH。

### P0-2 — 随包 relay 凭据属性未证明，当前按 privileged secret 泄露风险处理

位置：`scripts/package_macos.sh:56`, `scripts/package_linux.sh:44`

问题：打包脚本把 `SUPER_DOLPHIN_CODEX_RELAY_API_KEY` 明文写入 app/tarball 内的 `.env`。该做法可能是为了开箱即用地写死内部中转站配置，但当前命名、脚本行为和文档都无法证明它是允许公开分发的 bootstrap/client 凭据，因此评审按 privileged secret 泄露风险处理。

影响：DMG/tar.gz 分发后，任何获得包的人都能读取该值；`chmod 600` 只保护本机文件权限，不保护分发包内容。若该值是 privileged production secret，已发布即应按 secret 泄露处理。若该值是 public bootstrap/client token，则风险取决于服务端是否限权、限流、可撤销。

建议：保留开箱即用目标，但不要把 privileged API key 打进包。若确实需要随包内置内部中转站凭据，应先把它在产品和服务端上定义为 public/bootstrap credential：改名为 public/bootstrap 语义，在 relay 服务端限制权限、配额、来源和撤销能力，并在文档中明确“这是公开分发凭据，不是 secret”。完成这些证明后，P0-2 可降级为配置契约/文档项。

证据/触发条件：macOS/Linux 脚本都会写 `.env`；文档要求 release 使用 production relay values。

## P1 — 发布阻塞

### P1-1 — embedded PostgreSQL 目录权限未校验，trust auth 下有本机越权连接风险

位置：`internal/platform/embeddedpg/runtime.go:122`

问题：只用 `os.MkdirAll(dir, 0o700)` 创建目录，但没有校验或收紧已存在目录权限；同时 embedded DB 使用 `--auth=trust` 并通过 Unix socket 暴露。

影响：如果 `RuntimeDir`、data dir 或 log dir 已由旧版本/用户预创建为 `0755`，`MkdirAll` 不会修改权限。同机其他用户可能连接 app-managed DB。

建议：创建后对 runtime/data/log 目录做权限校验或 fail-fast；至少要求 `mode&0o077 == 0`，并增加“预先存在 0755 runtime dir 必须失败或被修正”的回归测试。

证据/触发条件：目录创建未校验既有权限；initdb 使用 trust auth；配置禁 TCP 但启用 Unix socket 目录。

### P1-2 — 第二个桌面实例会复用并无条件停止第一个实例的 PostgreSQL

位置：`internal/platform/embeddedpg/runtime.go:31`

问题：`Start` 遇到已有运行中的 PostgreSQL 直接返回 nil，但 lifecycle 后续仍按“本进程启动并拥有它”处理，并在 shutdown/failure 时无条件 stop。

影响：如果用户启动两个桌面实例，第二个实例会复用第一个实例的 PostgreSQL；第二个实例退出或启动失败时，会停止第一个实例正在使用的数据库。

建议：让 `embeddedpg.Start` 返回 ownership/started 状态，或增加 data-dir lock / single-instance gate。只有本进程实际启动或取得 ownership 时才 stop；否则 fail-fast 提示已有桌面实例。

证据/触发条件：`Start` 对已运行返回 nil；DB module 只用 `cfg.EmbeddedPostgres.Enabled` 作为 cleanup 标志；shutdown 时无条件 `embeddedpg.Stop`。

### P1-3 — `mcp-orch` 连接池 lazy connect，缺少 OnStart DB/schema fail-fast

位置：`cmd/mcp-orch/runtime.go:73`, `cmd/mcp-orch/runtime.go:85`

问题：`mcp-orch` 现在只 `NewPool` 并在 OnStop 关闭连接池，没有 OnStart 的 DB 连通性、migration 或 schema version gate。

影响：`pgxpool.NewWithConfig` 是 lazy connect。打包后子进程可能已经启动并暴露 MCP tools，但 DB 不可连或 schema 缺失时要到首次 tool call 才失败，违反 fail-fast。

建议：保留不由 sidecar 启停 embedded PostgreSQL 可以，但至少在 OnStart 执行 `pool.Ping(ctx)` 和 schema version 校验；如该进程仍支持独立运行，继续复用 `platformdb.Module` 或等价 migration lifecycle。

证据/触发条件：`cmd/mcp-orch` 只注册 pool close lifecycle；`internal/platform/db/module.go` 的连接和 migration 在 lifecycle OnStart 执行。

### P1-4 — Linux 包缺少 model registry，可能导致 `mcp-orch` 启动失败

位置：`scripts/package_linux.sh:82`

问题：Linux 包没有复制 `models.yaml`，`run.sh` 也没有设置 `SUPER_DOLPHIN_MODEL_REGISTRY`。

影响：干净 Linux 解包运行时，`mcp-orch` 构造 model registry 可能找不到模型注册表，导致 sidecar 启动失败，编排/MCP tools 不可用。若 Linux 不在本次 release scope，应在 release plan 中显式标为 out of scope。

建议：像 macOS 一样复制 `cmd/mcp-orch/tools/modelregistry/models.yaml` 到包内，并在 `run.sh` 中设置 `SUPER_DOLPHIN_MODEL_REGISTRY="$here/models.yaml"`；增加 Linux package guard test。

证据/触发条件：Linux 脚本只复制 binaries/migrations/postgres/env；macOS 已复制 model registry 并设置 registry env。

## P2 — 发布前应修

### P2-1 — 启动失败清理复用已取消 context，可能泄漏 embedded PostgreSQL

位置：`internal/platform/db/module.go:314`

问题：启动失败清理复用了 `OnStart` 的 `ctx` 去停止 embedded PostgreSQL。

影响：如果失败原因是 startup context 超时或取消，`embeddedpg.Stop(ctx, ...)` 会继承 canceled context，`pg_ctl stop` 不会可靠执行，导致启动失败后 PostgreSQL 进程泄漏。

建议：失败清理使用独立 shutdown context，例如基于 `context.WithoutCancel(ctx)` 和 shutdown timeout 构造 stop context，再调用 `embeddedpg.Stop`。

证据/触发条件：`failAfterEmbeddedStart` 直接传入 startup `ctx`；`embeddedpg.Stop` 最终用该 context 执行 `exec.CommandContext`。当 migration 或 schema version 校验因 `ctx.Err()` 失败时，stop 命令会被取消。现有测试只覆盖未取消 context。

### P2-2 — thread start 参数未读取 canonical `codexModelProvider`

位置：`internal/provider/codexapp/support.go:309`

问题：`threadStartParams.ModelProvider` 只读 `"modelProvider"`，没有读取 canonical 的 `codexModelProvider`。

影响：自定义或非默认 `codexModelProvider` 不会传给 Codex `thread/start`。默认 relay 场景是否实际失败取决于 Codex config 默认值，但该路径仍会造成 provider 路由不一致风险。

建议：读取顺序应包含 `contract.CodexModelProviderKey`，再兼容 `"modelProvider"`；补测试覆盖“只提供 `codexModelProvider` 时 `threadStartParams.ModelProvider` 必须非空”。

证据/触发条件：默认 identity 写入的是 `codexModelProvider`；`buildThreadStartParams` 读不到它。

### P2-3 — 用户可写 PATH 位于系统路径前，内部 shell wrapper 可能执行用户目录中的 `sh`

位置：`internal/platform/runtimeenv/runtimeenv.go:146`, `internal/provider/codexapp/process_unix.go:55`

问题：打包环境把 `~/.local/bin`、`~/.npm-global/bin`、`~/bin` 放到 `/bin` 前面；Codex spawn wrapper 使用 `exec.Command("sh", "-c", ...)`，会按 PATH 解析 `sh`。

影响：如果用户目录里有 `sh` shim 或恶意脚本，打包 app 启动 Codex app-server 时会执行它，而不是系统 `/bin/sh`。这是同用户 PATH 污染风险，低于跨用户权限边界问题，但仍应修复。

建议：内部 shell wrapper 使用绝对路径 `/bin/sh`；或将用户 bin 放到系统路径之后，并把“找 codex”和“找内部解释器”分开处理。

证据/触发条件：runtime env 把用户 bin 插到 bundled bin 之后、系统 bin 之前；process wrapper 使用相对 `"sh"`。

### P2-4 — tool error classifier 删除 DB/schema 分类后会误报为 LSP unavailable

位置：`internal/mcpserver/common/tool_error_envelope.go:135`

问题：DB schema / task invalid input 相关 classifier 被删除后，未匹配错误默认返回 `lsp_unavailable` 且 `retryable=true`。

影响：`task_*` 或 `mcp-orch` DB schema 缺失会被误报成“语言服务器启动中”，掩盖真实打包/迁移问题，降低 fail-fast 可诊断性。

建议：恢复专用 classifier，例如 `database_schema_missing`、`invalid_input`；或在 task/orch 调用点用 `CodedToolError` 明确 code。

证据/触发条件：`relation "task_dags" does not exist`、`column ... does not exist`、非法 transition 等不再命中专用分类，会落到 LSP fallback。

### P2-5 — 新安装无偏好时未默认 model/effort

位置：`cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:442`

问题：新安装/无偏好时，只默认 active provider，没有默认 model/effort；`startThread()` 在 provider model/effort pref 缺失时发送空值。

影响：Provider Settings UI 显示默认 `gpt-5.5/xhigh`，但首次直接开 Codex thread 会依赖 Codex/provider 隐式默认值，packaged app 默认配置不确定。

建议：在 `effectiveModel` / `effectiveEffort` 计算中使用 `getProviderDefaultConfig(modelProvider)` fallback，并补“只有 `settings.provider.active=codex`、无 model/effort pref”的测试。

证据/触发条件：uistate 只默认 active provider；当前逻辑无 provider default fallback；provider start params 接收空 model/effort。

### P2-6 — 业务启动和线程启动逻辑与打包/Codex 基础设施耦合偏高

位置：`internal/app/app.go:156`, `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:429`, `scripts/package_macos.sh:253`

问题：当前开箱即用改动把多个基础设施细节穿透到业务启动和 UI 线程启动路径：desktop preflight 直接处理 Codex relay bootstrap；thread start helper 直接组装 `codexHome`、`codexInstanceKey`、`codexModelProvider`、LSP defaults 等 provider identity 细节；打包脚本直接复制/决定 Codex CLI、model registry、relay `.env` 等运行时契约。

影响：这会让“开箱即用”策略、provider identity、打包资产布局和业务线程启动互相影响。后续如果调整内部中转站、Codex 内置策略、provider home 策略或 LSP defaults，容易同时改 frontend、app bootstrap、provider、scripts 和 docs，增加回归面。

建议：短期不要求大重构，但应收敛契约边界：

- 后端提供单一的 packaged runtime/provider defaults 解析结果，前端只传用户明确选择或 override，不直接拼装打包默认 identity。
- Codex bootstrap、relay credential、内置 Codex CLI 路径、model registry 路径集中到一个 runtime manifest/config 解析层，app preflight 和打包脚本只消费同一契约。
- 打包脚本中保留复制动作，但避免成为业务默认值的唯一来源；关键 env/key/path 名称应由后端 contract 或 manifest 测试锁定。

证据/触发条件：`internal/app/app.go` 的 desktop preflight 直接读取 relay env 并调用 Codex bootstrap；`thread-actions-helpers.js` 在业务线程启动时组装 Codex identity 和 LSP tool defaults；`package_macos.sh` 决定 Codex CLI 是否内置、缺失时是否转运行期下载。

## P3 — 非阻塞清理项

### P3-1 — macOS 验证脚本用当前 Go env 推导 bundle 平台

位置：`scripts/verify_packaged_app_macos.sh:12`

问题：验证脚本用当前 `go env GOOS/GOARCH` 推导 PostgreSQL bundle 平台，而不是从 `.app` 内容推导。

影响：交叉架构验证或环境残留 `GOARCH` 时会检查错误目录；若 bundle 内有多个平台目录，还可能验证错目标。

建议：从 `Contents/Resources/postgres/*` 枚举平台目录，或提供显式 `--platform` 参数；必要时用 `lipo` / `file` 校验 `agent-terminal` 架构。

证据/触发条件：用 `GOARCH=amd64` 验证 arm64 bundle 时会查 `postgres/darwin-amd64`。

### P3-2 — Linux 打包文档遗漏必需 env 且包含本机绝对路径

位置：`docs/packaging/embedded-postgres.md:92`

问题：Linux 打包文档没有列出脚本必需 env，同时示例硬编码了本机 worktree 路径。

影响：按文档执行会在 `package_linux.sh` 中 fail-fast；非本机读者也会被 `/Users/ai/Desktop/...` 路径误导。

建议：用 `<repo-root>` 或 `git rev-parse --show-toplevel` 风格说明替代绝对路径。relay credential 文档必须依赖 P0-2 的最终安全方案：可以指导注入公开、低权限、可撤销的 bootstrap/client 凭据；如果该值仍是 privileged secret，文档不得指导 release packaging 注入它。

证据/触发条件：文档 Linux 章节只设置 PostgreSQL dist；脚本实际强制要求 relay env。

## 已删除的首轮 finding

删除项：原 `Finding 9 — 前端发送 partial Codex identity，干净 packaged app 首次建线程可能失败`。

删除原因：交叉评审核对当前代码后确认该结论不成立。前端确实不发送空 `codexHome`，但后端 `prepareStartSessionRequest` 会在 identity 解析前选择 app-managed home 并通过 `withDefaultCodexIdentity` 补齐完整 identity；现有 `thread-store-codex-default-home.test.js` 也锁定“不发送 codexHome”的行为。

## 验证状态

当前评审是只读评审，不代表改动已验证可发布。

已记录的子 agent 验证：

- `go test ./internal/platform/embeddedpg ./internal/app -count=1` 通过。
- `go test ./internal/platform/db -run 'TestRegisterLifecycle' -count=1` 通过。
- `go test ./internal/platform/embeddedpg ./internal/platform/db ./internal/app` 在 `internal/platform/db` 的 `TestPromptTemplateRuntimeMetadataMigrationUpdatesDAGDesignerAndRollbackRestores` 失败；首轮评审判断像 prompt seed 内容问题，仍需合入前明确归因或关闭。
- `./scripts/test_with_guard.sh ./internal/platform/runtimeenv ./internal/provider/codexapp ./internal/mcpserver/common ./internal/platform/rpc ./cmd/mcp-lsp ./cmd/mcp-ida -count=1` 通过。
- `./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1` 通过。
- UI/scripts/docs 范围未运行 frontend test/build/package 验证。

合入前至少还需要：

- affected Go package tests with guard。
- packaged runtime/provider defaults 的契约测试，覆盖 app bootstrap、frontend start payload、provider request 三者边界。
- frontend `node scripts/size-guard.cjs`、`npx vitest run`、`npm run build`。
- macOS/Linux package guard 或等价 packaged app smoke test。
- embedded PostgreSQL 首启、失败清理、重复实例、权限预置场景测试。

## 首轮子 agent 覆盖范围

### 子 agent 1：embedded PostgreSQL / DB lifecycle

覆盖：

- `internal/platform/embeddedpg/**`
- `internal/platform/db/module.go`
- `internal/platform/db/embedded_postgres_lifecycle_test.go`
- `internal/app/app.go`
- `internal/app/desktop_preflight_test.go`
- `cmd/agent-terminal/main.go`

### 子 agent 2：runtime / provider / MCP

覆盖：

- `internal/platform/runtimeenv/**`
- `internal/provider/codexapp/**`
- `cmd/mcp-orch/**`
- `cmd/mcp-lsp/main.go`
- `cmd/mcp-ida/main.go`
- `internal/mcpserver/common/tool_error_envelope*`
- `internal/platform/rpc/**`

### 子 agent 3：UI / scripts / docs

覆盖：

- `cmd/agent-terminal/frontend/vue-app/**`
- `internal/module/uistate/**`
- `internal/contract/config.go`
- `internal/platform/config/**`
- `scripts/package_*.sh`
- `scripts/verify_packaged_app_macos.sh`
- `scripts/build_relocatable_postgres_macos.sh`
- `Makefile`
- `docs/packaging/**`
- codemap 变更一致性

## 建议修复顺序

1. P0 合入硬阻塞：P0-2 明确 relay 随包凭据的安全属性，确保 privileged secret 不进入发布包；P0-1 Codex CLI 随包内置并校验，运行期下载只能作为安全 fallback。
2. P1 发布阻塞：P1-1 embedded PostgreSQL 权限；P1-2 ownership/single-instance gate；P1-3 `mcp-orch` DB/schema OnStart fail-fast；P1-4 Linux model registry（若 Linux 属于本次交付范围）。
3. P2 发布前质量项：P2-1 cleanup context；P2-2 canonical `codexModelProvider`；P2-3 shell wrapper 绝对路径；P2-4 tool error 分类；P2-5 默认 model/effort；P2-6 收敛业务启动/UI 与打包 runtime 的契约边界。
4. P3 文档/验证体验项：P3-1 macOS 验证脚本平台推导；P3-2 Linux 文档在 P0-2/P1-4 的最终脚本契约确定后更新。
