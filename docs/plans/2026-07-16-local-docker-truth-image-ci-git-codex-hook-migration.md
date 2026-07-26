# 节点本地 Docker 真相镜像 CI 与 Git/Codex Hook 迁移计划

> **状态：** IMPLEMENTATION IN PROGRESS / 2026-07-26 修订为环境镜像与任务源码解耦
>
> **复核基线（2026-07-16）：** 当前 `HEAD` 与本地远端跟踪引用 `origin/main` 均为 `c2176e0dfcddf8d4f52729988fd024916800a4fd`。该 SHA 仅是文档复核快照；Task 0 开始时必须重新解析，不得把静态文档 SHA 当作执行输入。
>
> **执行边界：** Docker 安装在每个 CI 执行节点。节点从受信 Git object 实时构建并缓存 CI 环境真相镜像，但只有 CI runner、工具链、依赖锁、镜像定义或 policy 变化才允许触发镜像构建；普通产品源码、测试或文档变化只物化精确 Git tree 并注入一次性执行容器，禁止重新构建环境镜像。镜像和 BuildKit cache 不上传、不跨节点复制。4 CPU/8 GiB 是每个执行容器的上限，不是预占内存；总并发由节点 `max_active_ci_workloads` 配置决定。
>
> **目标：** 将当前分散在 Git hooks、Codex lifecycle hook、AI maintenance、代码守卫和 workflow 中的门禁迁移为统一 CLI；自动维护不可变 CI 环境真相镜像，每次 CI 从请求的精确 Git tree 物化只读源码，再按 `shards_per_job` 从该镜像创建一次性容器并行执行分片。节点以 `max_active_ci_workloads` 约束所有 Agent、build、job、service 与 shard 的总并发，超过后排队。

本文是迁移执行合同，不代表统一 CLI、本地调度器、真相镜像或远程 receipt 已经实现。最终完成状态必须绑定同一提交上的源码、测试、Docker 实测和 Hook 证据。

### 最终效果（验收口径）

1. 修改 Dockerfile、工具链锁、Gate CLI/executor、gate registry、门禁策略或镜像闭包生成器等 CI 环境真值输入后，下一次 CI 提交自动识别新的 `image_input_digest`，在接收任务的节点上实时构建候选环境真相镜像。普通产品源码、测试和文档不属于镜像输入，变化时必须复用已接受环境镜像，不得调用 BuildKit。
2. 任何被称为 CI 的执行入口只能调用统一 `submit` 网关。网关解析已接受策略，将请求的 Git object 一次物化为经过 tree 复验的私有只读源码快照，在当前节点确保环境镜像存在，再从该镜像按本次受信 `shards_per_job` 创建全新的一次性隔离容器并行执行门禁分片；禁止在宿主机直接执行 CI gate，也禁止把活动 worktree 或前一次任务的可写容器作为源码。
3. “fork 容器”在本文中严格表示从当前节点刚构建或经本地内容缓存复验的真相镜像执行 `docker create/run`，不表示克隆一个正在运行的容器。容器退出后只保留日志和 receipt，容器本身销毁。
4. 本地 Git Hook、Codex Hook、人工 CLI、release adapter 和 workflow adapter 都必须经过同一网关。任何仍直接调用 `go test`、`npm test`、`make guard` 或旧 gate wrapper 并把结果作为 CI 结论的入口，均视为迁移未完成。
5. 仓库维护版本化 `CIEntrypoint` 清单，登记所有能产生 CI 结论的 Hook、workflow、Make target、脚本和 CLI alias。架构守卫校验每个入口最终只调用 `submit`，并拒绝新增未登记入口；普通开发者手工运行测试可以存在，但不能签发或复用 CI receipt。
6. 每次独立权威 CI action 都生成新的 invocation nonce、job、container ID 和 removal proof；只允许同一 invocation 的观察者订阅同一运行，不允许用历史 passed receipt 代替新容器。

全局真相分为两个正交身份：`EnvironmentTruth = policy_sha + image_input_digest + toolchain_digest + platform`，以及 `JobSourceTruth = SourceSpec + source_tree_sha + source_bundle_digest`。普通 CI 只更新后者；只有环境真值变化才构建或晋升镜像。`NodeImageIdentity` 是环境真相在单个节点的本地物化证据，不是源码快照，也不是长期运行的“母容器”。

---

## 1. 目标与非目标

### 1.1 本计划覆盖

- 统一门禁 CLI、稳定退出码、版本化 plan/result/receipt JSON。
- 每个执行节点自动构建并按本地内容身份使用 Docker 真相镜像。
- CI 真值输入变化时自动构建候选镜像，接受后自动晋升；普通 CI 调用无需人工先执行 build。
- `shards_per_job` 与 `max_active_ci_workloads` 都由节点仓库外配置显式给出；默认可为 3，但 3 不是协议上限。
- 同一 daemon 上全部 Agent 共用一个全局 slot 池；例如 `shards_per_job=5`、`max_active_ci_workloads=20` 时，4 个 Agent 可各自启动 5 个 shard，共 20 路并行，第 5 个 Agent 排队。
- 每个 gate 容器最多 4 CPU、8 GiB 内存。
- 普通 profile 最长 10 分钟，release profile 最长 30 分钟。
- Git `pre-commit` 检查 staged/active worktree，`pre-push` 将明确 commit SHA 提交给 Docker CI。
- Codex `Stop` / `SubagentStop` 从官方 hook stdin 解析 session、turn、cwd 和 agent 身份，并将排队、失败或通过状态反馈给对应 agent。
- 同一节点相同 build singleflight、旧 SHA 取消、结构化日志、不可变 receipt 和策略晋升；CI job 本身不跨 invocation 缓存，BuildKit 缓存不跨机器复制。

### 1.2 本计划不覆盖

- 跨节点统一资源调度、Kubernetes、远程 Docker daemon 或独立 CI 集群；远程 runner 在自己的节点独立构建和执行不属于多主机统一调度。
- 用 Linux Docker 替代 macOS/Windows 原生 smoke、签名和发布验证。
- 自动修改或合并 agent 分支。
- 从不稳定 transcript 格式推断 agent 身份或 worktree。
- 把活动 worktree 的临时结果冒充 commit/merge 权威 receipt。

### 1.3 硬原则

- CI 权威输入必须是 Git object：`commit SHA`、`tree SHA` 或明确的 `base/head`，不能读取验证期间持续变化的活动目录。
- 节点本地真相镜像必须以复验过的内容身份执行；`latest` 或其他可变标签只能作为人类别名，不能进入 receipt 或缓存键。
- 候选代码不能修改裁判后立即使用自己的新裁判给自己签发绿色 receipt。
- 本地 Hook、Codex Hook、Docker runner 和 workflow 不得各自维护一份 gate 必选规则。
- 缺少身份、Git object、镜像 digest、必需 gate、evidence 或执行结果时 fail-fast，不产生绿色结果。
- queued、running、cancelled、timeout、infra_failed 和 failed 均不能显示为 passed。
- 所有 CI 执行必须经过 `submit -> scheduler 持久化/FIFO/slot -> image ensure/build -> docker create/run`；宿主机直跑只允许作为非权威开发命令，不能生成 CI receipt。
- 每个请求必须且只能携带一种 `SourceSpec`；容器源码、plan、task key 和 receipt 必须绑定同一个 job Git object，禁止从活动 worktree补读文件。环境镜像另行绑定已接受的环境输入 Git object 和 `image_input_digest`；receipt 必须同时记录这两个身份，不得要求普通 job tree 等于 image build tree。
- 权威 receipt 必须由候选容器不可访问的宿主机密钥签名；普通 JSON、日志文本或候选代码生成的校验和不构成通过证明。

---

## 2. 当前事实与迁移缺口

### 2.1 当前已有基础

- `scripts/ai_maintenance` 已提供 `plan`、`run` 和 `validate-evidence`，并具备变更面路由、runner、缓存和 push gates。
- `.githooks/pre-commit` 已固定 staged tree、使用隔离 index/临时 worktree，并检查 partial index、staged/worktree 不一致和生成物刷新。
- `.githooks/pre-push` 已要求推送对象等于当前 `HEAD`，并收集真实 push range。
- `.codex/hooks.json` 已登记 `Stop` 与 `SubagentStop`，`scripts/codex_stop_gate.sh` 已能返回 Codex 接受的 JSON。
- `.github/workflows/ci.yml` 和 SQLite release workflow 已存在，但仍手写部分领域 gate，尚未调用统一真相镜像协议。

### 2.2 当前主要缺口

- gate 路由、runner、证据命令、Hook 路径判断和 workflow 命令仍存在多个可写 owner。
- `ai_maintenance` 未提供可稳定安装的统一 gate binary 和可机器区分的退出码。
- evidence 未提供时，当前命令 gate 可以继续执行并返回成功；权威 profile 不允许这一语义。
- 仓库没有 tracked Dockerfile、镜像输入清单、镜像 digest 晋升文件或本地 3 并发调度器。
- 当前 Codex stop gate 读取 stdin 后主要匹配 `stop_hook_active`，没有严格解析官方公共字段。
- 当前 CI 结果没有同时绑定 repo、Git object、profile、policy SHA、image digest 和逐 gate 结果。

---

## 3. Codex Hook 官方契约

实现以 OpenAI 当前发布文档为契约，不从 transcript 猜字段：

- 公共 command hook stdin 包含 `session_id`、`transcript_path`、`cwd`、`hook_event_name`、`model` 和当前 `permission_mode`；turn-scoped hook 还包含 `turn_id`。
- Hook 命令以 session `cwd` 作为工作目录执行。
- `Stop` 额外包含 `stop_hook_active` 和 `last_assistant_message`。
- `SubagentStop` 额外包含 `agent_id`、`agent_type`、`agent_transcript_path`、`stop_hook_active` 和 `last_assistant_message`；其 `session_id` 是父 session ID。
- `Stop` / `SubagentStop` 在退出 0 时要求 stdout 为 JSON；`decision: "block"` 会要求 Codex 继续，并把 `reason` 作为 continuation prompt。
- Codex 当前会并发启动同一事件的多个匹配 command hook；`async` 配置已解析但尚不支持，不能依赖异步 hook handler。

参考：

- <https://developers.openai.com/codex/hooks#common-input-fields>
- <https://developers.openai.com/codex/hooks#subagentstop>
- <https://developers.openai.com/codex/hooks#stop>

### 3.1 身份键

| 场景 | 身份键 | worktree 来源 |
|---|---|---|
| 根 agent Stop | `session_id + turn_id` | hook stdin `cwd` |
| 子 agent 与父 session 共用目录 | `session_id + turn_id + agent_id` | hook stdin `cwd` |
| 子 agent 使用独立 worktree | `session_id + turn_id + agent_id` | 子 agent 在真实目录显式注册的 canonical cwd |

若子 agent 使用独立 worktree，公共 `cwd` 不能自动证明它就是子 agent 的独立目录，agent 自报参数也不能证明身份。只有实际创建该 worktree 与 agent 的受信 orchestrator 可以在 spawn 时登记：

```bash
super-dolphin-gate agent register-spawn \
  --spawn-record "$SIGNED_SPAWN_RECORD" \
  --session-id "$SESSION_ID" \
  --turn-id "$TURN_ID" \
  --agent-id "$AGENT_ID" \
  --cwd "$PWD"
```

`SIGNED_SPAWN_RECORD` 由受信 orchestrator 的仓库外签名组件在创建 agent/worktree 时产生，通过受保护 scheduler API 直接登记，不交给 agent、continuation 文本、环境变量或 transcript。它单次、短期有效，并绑定 session/turn/agent/repo/canonical cwd/parent invocation/orchestrator identity。注册时除验证 cwd 与 Git common dir 外，还必须验签并以 CAS 写入 identity mapping；同 UID 普通进程不能调用 grant issuer 或仅靠知道 agent ID 抢注。Codex 自带但未经过受信 orchestrator 创建的独立-worktree subagent 无法安全证明映射，必须 fail-fast block；共享 session cwd 的 subagent 仍直接使用官方字段。冲突、重放、过期或身份切换不回退为猜测。

---

## 4. 目标架构

```text
Git pre-commit --------------------> local-fast / staged-tree validation
Git pre-push ----------------------> submit(base_sha, head_sha, profile=push)
Codex Stop/SubagentStop -----------> resolve identity + tree -> submit/query
                                              |
                                              v
                                  super-dolphin-gate CLI
                                              |
                                              v
                                  local gate scheduler
                              queue / dedupe / cancel / receipts
                                  |       |       |
                                  v       v       v
                              Docker A Docker B Docker C
                                  \       |       /
                                   truth image digest
```

### 4.1 组件所有权

| 组件 | 唯一责任 | 不得拥有 |
|---|---|---|
| `internal/devtools/gate` | GateSpec、profile、plan、结果、证据、退出码 | Docker daemon 生命周期、Git Hook stdin |
| `cmd/super-dolphin-gate` | CLI 参数、JSON/stdout、日志/stderr、进程退出码 | 重复 gate 规则 |
| singleton scheduler daemon | 规范化 Docker daemon identity 唯一 owner、统一 FIFO、可配置全局 slot、取消、状态、签名 receipt/action grant | 领域 gate 选择规则 |
| Docker executor | clean clone/checkout、资源限制、超时、命令执行 | agent 身份推断、merge 决策 |
| Git hooks | Git stdin/index 适配和同步阻断 | 另一套 gate registry |
| Codex hooks | Codex stdin/output 适配和 agent 通知 | transcript 解析、领域 gate registry |
| image builder | 绑定 SourceSpec 的镜像输入指纹、构建和 OCI identity | 自动接受候选策略 |
| promotion controller | 受信 ref 验证、旧 receipt 验证、CAS 晋升、防回滚 | 执行候选代码、接受未签名结果 |
| trusted runner/verifier | 从已接受旧真相源计算 plan/命令闭包、直接启动并观察 gate 进程、验证候选 provenance/known-bad corpus | 信任候选 executor 自报事件 |

### 4.2 可信执行模型

普通 CI 由当前已接受策略在执行节点实时物化的 `TrustedRunnerIdentity` 执行；候选源码只作为待测 source bundle 挂入，不提供当前权威 runner。若候选修改 Gate CLI、runner 或策略，push admission 仍由旧 generation 的节点本地 runner 计算并直接启动旧 policy required gates。

候选镜像自测事件一律视为不受信输入。晋升是独立 `promotion` CI invocation：scheduler 必须原子预留两个 slot，从上一 accepted policy 的节点本地 materialization 创建新的 trusted-runner 容器，并另行创建候选测试容器；两者分别绑定 deadline、network 和 removal proof。旧真相侧 verifier 在 accepted-runner 容器内解析候选 GateSpec、计算预期 gate/命令/事件闭包，宿主 scheduler 直接观察每个进程启动、argv digest、退出码、日志 digest 和顺序，并运行固定 known-bad corpus；同时验证 BuildKit provenance 和候选 `NodeImageIdentity`。宿主 controller 只编排、观察和验签，禁止在宿主机执行 promotion gate。候选 executor 不能请求 signer 为自己上报的 `passed` 事件签章。

首次 bootstrap 使用仓库外、安装包签名的最小 runner/verifier；其二进制摘要和签名链固定在 bootstrap trust root。任何 accepted generation 都记录 `TrustedRunnerIdentity{binary_digest, signer, policy_digest}`，下一 generation 必须由上一 generation 验证。

---

## 5. CLI 与结构化协议

### 5.1 命令面

```text
super-dolphin-gate plan --profile <profile> <source-spec>
super-dolphin-gate run --job-token <opaque-single-use-token>
super-dolphin-gate image ensure --source-object <sha>
super-dolphin-gate image promote --candidate <digest> --expected-generation <n>
super-dolphin-gate submit --profile <profile> <source-spec> [--wait]
super-dolphin-gate status --job <job-id>
super-dolphin-gate wait --job <job-id>
super-dolphin-gate cancel --job <job-id>
super-dolphin-gate worktree check --cwd <path> --scope staged|worktree
super-dolphin-gate agent register-spawn --spawn-record <signed-record> --session-id ... --turn-id ... --agent-id ... --cwd ...
super-dolphin-gate codex hook
super-dolphin-gate receipt verify --input <file>
```

`submit` 是唯一权威执行入口，并且内部强制调用 `image ensure`；调用者不能传入可变 tag 或任意本地 image ID。`run` 只供 scheduler 启动容器内 executor，必须校验 scheduler 签发的一次性 job token，人工直接调用不能产生权威 receipt。`plan`、`status` 和 `receipt verify` 不执行 gate，因此不创建容器。

`image promote` 是 trusted-ref watcher 调用仓库外 PromotionController 的内部管理面，只接受有效 audience=`image.promote` ActionGrant + expected generation；普通开发者、候选代码和 CI workflow 不需要也无权手工执行晋升。正常流程必须在一次 Git push 后自动观察受信 ref 并完成或明确失败。

`<source-spec>` 是以下三种互斥形式之一，多传、少传或对象类型不符均返回退出码 12：

```text
--commit <commit_sha>
--tree <tree_sha> [--parent <commit_sha>]
--base-kind commit --base <commit_sha> --head <commit_sha> --local-ref <ref> --remote-ref <ref> --observed-remote <sha> --update-kind <kind>
--base-kind empty-tree --head <commit_sha> --local-ref <ref> --remote-ref <ref> --observed-remote <zero-sha> --update-kind create
```

生产类型 `SourceSpec` 使用 tagged union 表达 `commit`、`tree`、`range`。range 额外固定 `base_kind=commit|empty_tree`、local/remote ref、observed remote SHA 和 `update_kind=create|fast_forward|force`；每个非删除 ref update 独立生成 SourceSpec 与 invocation。受信分支默认只允许 fast-forward，force 必须使用独立 break-glass ActionGrant/key purpose 并记录旧新 tip、审批、过期和恢复；delete 不属于 CI SourceSpec，由独立 Git 管理策略处理。scheduler 首先通过 `git cat-file -e <sha>^{commit|tree}` 校验对象类型，再计算唯一 `source_tree_sha`；`image ensure`、executor、plan 和 receipt 只接收规范化后的 `SourceSpec + source_tree_sha`，不得自行重新读取 `HEAD`、index 或 cwd。

### 5.2 CI 入口收敛

| 入口 | 迁移后唯一动作 | 容器语义 |
|---|---|---|
| Git `pre-commit` | 固定 staged tree 后 `submit --profile local-fast --tree <tree> --parent <HEAD> --wait` | 从已解析 digest 新建一次性容器 |
| Git `pre-push` | 固定 base/head SHA 后 `submit --profile push --wait` | 从已解析 digest 新建一次性容器 |
| Codex Hook | 固定 agent/worktree tree 后按 hook invocation identity 创建或查询同一 invocation | 每个新 hook action 新建容器；仅重复 delivery/observer 订阅原 invocation |
| 人工 CI CLI | `submit --profile ...` | 从已解析 digest 新建一次性容器 |
| release adapter | `submit --profile release --wait` | 从 release digest 新建一次性容器 |
| workflow adapter | 在其 runner 上调用相同 `submit` 协议 | 从同一已发布 digest 新建一次性容器 |

只有同一 `invocation_id` 的重复 delivery 或 observer 可以订阅同一个正在运行的容器。task/build key 仅用于 candidate image build singleflight，不能用于 CI job 去重；两个独立 CI action 即使 source/profile 相同也必须新建容器。

### 5.3 Profile

| Profile | 输入 | 超时 | 用途 |
|---|---|---:|---|
| `local-fast` | staged/worktree tree | 10 分钟 | pre-commit 快速反馈 |
| `push` | base/head commit SHA | 10 分钟 | pre-push 权威提交门禁 |
| `remote-required` | base/head/merge candidate | 10 分钟 | 后续 required check |
| `promotion` | trusted ref 精确 tip + candidate identity | 10 分钟 | 上一 accepted policy 的节点本地 runner 验证候选并签发 promotion evidence |
| `release` | release commit SHA | 30 分钟 | release 与 packaging gates |

必须由测试证明 `local-fast` 的必需规则集合是 `push` / `remote-required` 的子集。所有非 release profile 的容器运行硬截止均为 10 分钟，release 为 30 分钟；排队时间不计入运行 deadline，但必须单独记录。deadline 在容器启动时持久化，scheduler 重启不得重置。profile 不接受任意 `--skip-required`。

### 5.4 退出码

| Code | 含义 |
|---:|---|
| 0 | 完整通过 |
| 2 | 参数、JSON 或协议错误 |
| 10 | 确定性 gate 违规 |
| 11 | evidence 不完整 |
| 12 | Git object、worktree 或 identity 不一致 |
| 13 | Docker、工具链或基础设施失败，可重试 |
| 14 | 节点本地镜像存储、BuildKit 或内部不变量错误 |
| 15 | cancelled 或被新 SHA supersede |
| 16 | timeout |

### 5.5 字段真值与守卫

生产真值源为 Go 类型上的 JSON tags。至少覆盖：

- `GatePlan`
- `JobRequest`
- `JobStatus`
- `GateResult`
- `Violation`
- `ResultReceipt`
- `GrantRequest`
- `ActionGrant`
- `SourceSpec`
- `NodeImageIdentity`
- `TrustedRunnerIdentity`
- `PromotionRecord`
- `SignedJobToken`
- `CodexHookInput`
- `CodexHookOutput`
- `AgentWorktreeRegistration`
- `CIEntrypoint`

测试必须通过反射动态枚举生产字段，并验证 JSON roundtrip、required 字段、未知枚举、缺字段、stale consumer registry 和非法互斥组合。不得用第二份手工字段数组冒充生产真值。

`CIEntrypoint` 的唯一生产 owner 是 `internal/devtools/gate/entrypoints.go`；`build/gate/ci-entrypoints.json` 由该 typed registry 单向生成并接受 drift check。清单必须逐项覆盖 `.githooks/**`、`.codex/hooks.json`、`.github/workflows/**`、`Makefile` 中的 CI targets、release scripts、PowerShell、Go/shell alias 和兼容 wrapper，并登记最终 sink。guard 使用结构化 YAML/JSON/Make/AST 解析做 inventory 差集，再以每个入口的契约 fixture 证明调用链最终到达 `submit`；新增入口、未知 wrapper 或产生权威结论却没有有效签名 receipt 时失败。

---

## 6. Git 真相与活动 worktree

### 6.1 pre-commit

`pre-commit` 保留同步本地职责：

1. 固定 staged tree SHA。
2. 拒绝 partial index 和 staged/worktree 不一致。
3. 检查与本轮 staged 输入相关的 unstaged/untracked 污染。
4. 在 staged snapshot 中刷新并精确 stage 生成物。
5. 重新执行 `git write-tree`，调用 `submit --profile local-fast --tree <tree_sha> --parent <HEAD> --wait`。

活动 worktree 结果只绑定 tree SHA；tree 变化后结果立即失效，不能复用为 commit receipt。

staged tree 可能是没有 ref 的 dangling object。宿主机 source exporter 必须在临时 bare repository 中创建 parent 固定、tree 精确相等的 synthetic commit，并生成包含完整 object closure 的 Git bundle；不得在真实仓库创建临时 ref。executor 只读导入 bundle，验证 synthetic commit 的 tree 等于请求 `tree_sha`，再 checkout 到容器私有目录。bundle、manifest 和 synthetic commit SHA 进入 job provenance；禁止挂载可写宿主仓库或退回读取活动 worktree。

### 6.2 pre-push

`pre-push` 是本地 Docker CI 的 Git 提交入口：

1. 从 Git hook stdin 解析每个非删除 ref。
2. 保持当前规则：显式 push 的 `local_sha` 必须等于当前 `HEAD`。
3. 每个 ref 独立规范化 SourceSpec：新分支使用 `base_kind=empty_tree/update_kind=create`；普通 range 使用 `base_kind=commit/update_kind=fast_forward`；无共同祖先或非快进使用 `update_kind=force` 并要求 break-glass grant。
4. 运行 commit title 与 fix-test history guard。
5. 调用完整协议：`submit --profile push --base-kind <kind> [--base <sha>] --head <local_sha> --local-ref <local_ref> --remote-ref <remote_ref> --observed-remote <remote_sha> --update-kind <kind> --wait`。若 submit 返回 queued/running，pre-push 必须在同一次 hook action 内以原 repository、invocation、job_id 发起类型化 wait；禁止退出后靠重试创建新 invocation，否则多个 Agent 会重复排队且永远无法消费已通过回执。
6. Docker CI 在临时目录执行 clean clone/checkout，并再次验证 `HEAD == requested head_sha`、base/head object 存在、工作树初始 clean。
7. job 非 passed 时阻止 push。

删除 ref 不运行代码 CI，也不能复用普通 push grant；它由独立 Git 管理策略授权。force update 默认拒绝，只有 break-glass ActionGrant 在 audience/ref/old/new SHA 全部匹配时才允许。

Docker job 完成后还要检查验证目录没有未解释漂移。生成物 check 允许命令内部临时写入，但最终结果必须明确列出 expected drift；未知 dirty 文件直接失败。

commit/range 输入由 source exporter 生成同样的只读 Git bundle；容器内必须重新计算 `HEAD`、base/head object type 和 `source_tree_sha`。因此本地 clone、worktree 与远端 runner 使用同一对象传输协议，不依赖共享宿主 `.git` 目录。

---

## 7. 真相镜像自动构建

### 7.0 首次 bootstrap

节点不存在已接受策略的本地镜像时，第一次 `submit` 仍必须自动完成初始化，不要求开发者手工执行 Docker build：

1. 从仓库外 bootstrap trust root 取得受信仓库身份、remote URL、baseline commit、bootstrap runner OCI manifest digest、公钥和最小验证器版本。
2. 仓库外签名 launcher 使用安装包内的最小宿主 verifier 校验 baseline Git object、固定构建定义和锁文件，再在当前节点启动受控 BuildKit build；不得导入或拉取预构建 gate/runtime 镜像，也不得执行 checkout 内 CLI。
3. scheduler 将 bootstrap 建模为特殊受信 invocation并取得全局 slot。BuildKit 只读取 baseline Git tree 导出的 canonical context，按当前平台实时构建 bootstrap runner；构建过程应用 4 CPU/8 GiB、network、deadline 和 provenance 合同。
4. scheduler 从新构建的本地内容身份创建全新 bootstrap-runner 容器，运行固定 fixtures、known-bad corpus、自测试、schema roundtrip 和 required gates；宿主 verifier 只编排、观察和验签，禁止在宿主机启动 gate。
5. 全部通过后原子写入 generation 1 的 `policy_sha + source_tree_sha + image_input_digest` 晋升记录，并把该节点的 `NodeImageIdentity` 作为可丢弃物化证据单独记录。
6. 当前 CI job 随后从该节点本地内容身份创建新的执行容器；用于 build 的中间容器不能直接充当 job container。

bootstrap trust root 由签名安装包写入 `~/.config/super-dolphin/gate/bootstrap-root.json`，其内容不从当前 checkout 更新；至少固定 `repo_id`、规范化 remote URL、baseline commit、Ed25519 公钥、最小 launcher/verifier/promotion controller 的二进制摘要、锁定基础镜像 digest、安装包 signer 和 verifier version。Keychain ACL 绑定仓库外 launcher/controller 的代码签名；候选容器、构建上下文、仓库 CLI 和未签名二进制均不可调用签名私钥。root/key 更新必须由旧 root 签名，记录 key epoch、not-before/not-after、revocation 和恢复流程。缺 trust root、baseline、签名、输入摘要、构建证明、测试或晋升记录时 fail-fast；不得把“本节点还没有真相镜像”作为回退到宿主机直跑或拉取预构建镜像的理由。

### 7.1 构建输入

以下路径是环境镜像闭包的初始种子，不是完整仓库源码清单：

```text
build/gate/Dockerfile
build/gate/toolchain.lock
build/gate/runtime-deps.lock
cmd/super-dolphin-gate/**
cmd/super-dolphin-gate-executor/**
internal/devtools/gate/**
internal/devtools/localci/**
```

manifest generator 从已接受环境输入 Git object 自动闭包 Dockerfile `COPY/ADD`、Gate CLI/executor 编译依赖、工具链与依赖锁和 policy，生成 canonical sorted image manifest。普通产品 `cmd/**`、`internal/module/**`、`frontend-app/src/**`、测试和文档不得进入环境镜像闭包。candidate state 和 build request 只能持久化该环境输入闭包，禁止以 base64 或其他形式嵌入完整 Git tree。source exporter 为每次 job 从请求的 `SourceSpec` 生成独立 Git bundle/manifest，节点侧只物化一次并复验 `source_tree_sha`，随后由本次 `shards_per_job` 个 shard 以只读方式共享；源码 bundle 不进入 BuildKit context。

Go 依赖层只构建锁文件绑定的节点共享 module proxy，执行容器校验 job tree 的 `go.sum` 与 runtime-proxy lock 后直接使用该只读 proxy；禁止在环境镜像生成 vendor，也禁止把 vendor 注入 job tree。为兼容已签名安装的 schema v4 bootstrap controller，`runtime-deps.lock.paths.vendor` 暂时保留为不参与构建与执行的 legacy 解码字段，不代表镜像中必须存在该目录。`cmd/agent-terminal/web-dist` 是未提交的产品构建产物：frontend build/embed 门禁必须从本次 Git tree 构建并验证真实产物；backend/race 仅为 Go 包加载注入环境镜像内固定、只读且与产品代码无关的单文件 embed 编译占位种子，禁止把它当作产品构建证明。

`image ensure` 只从上述只读 Git object/context 读取输入，不读取 cwd。它对 Git blob SHA、文件 mode、内容 SHA-256、build args、目标平台、Dockerfile frontend/BuildKit 版本、所有 `FROM` 基础镜像 digest、锁定依赖源和网络策略计算 `image_input_digest`。无法闭包命令目标、存在未声明 `COPY/ADD`、出现 symlink 越界、未锁定外部依赖或 provenance/materials 漂移时 fail-fast。

候选 build 使用受控 rootless worker/cgroup；禁止 secret mount、SSH forwarding、host network、insecure entitlement、Docker socket、宿主 credential helper和代理变量。BuildKit 缓存只存在于当前执行节点；同一节点的全部 Agent、CI invocation、runtime dependency 镜像和候选真相镜像必须复用唯一固定的持久 cache pool，禁止按源码树、镜像输入或依赖 digest 创建新 cache 目录。环境输入 digest 未变化时 `image ensure` 必须在 inspect/identity 复验后直接返回，禁止仅为了“刷新缓存”再次执行 BuildKit。只有环境输入变化时才读取并写回该共享 pool；普通业务源码变化只更新 source bundle，不访问 image build cache。缓存不得上传、导出、导入或跨节点复制，命中也不能替代锁文件、provenance 和产物身份复验。

每次 `submit` 都必须把 job 的 `source_object + source_tree_sha` 交给 scheduler；scheduler 先持久化 invocation/workload，再解析已接受 `EnvironmentTruth`。环境输入 digest 命中时直接复验并使用不可变镜像 identity，不创建 build workload；仅环境输入变化时自动排入 candidate build workload。receipt 同时写入 job source identity 与 accepted environment/image identity。相同环境 build key 可以 singleflight，但独立 CI invocation 不能复用历史 job。

### 7.2 镜像标签与 digest

`NodeImageIdentity` 记录当前节点 Docker daemon identity、本地 image ID、config digest、rootfs diff IDs、平台三元组和完整 labels。它必须绑定 `image_build_tree_sha + image_input_digest + toolchain_digest`，只在生成它的节点和 daemon 内有效。job 的 `source_tree_sha` 由 source bundle/materialization receipt 单独证明，不写成环境镜像的源码标签。

```text
human tag: super-dolphin-gate:<policy-sha-prefix>
execution authority: local image ID + daemon identity
cross-node authority: image_build_tree_sha + image_input_digest + locked build definition
labels:
  org.super-dolphin.policy-sha
  org.super-dolphin.source-tree-sha  # 兼容字段名；语义是 image_build_tree_sha，不是本次 job tree
  org.super-dolphin.image-input-digest
  org.super-dolphin.toolchain-digest
  org.super-dolphin.schema-version
```

调度器在 `docker create` 前后都按 image ID 重新 inspect，并复验 daemon identity、config、rootfs 和 labels。标签指向错误 image ID、label 缺失或 label 与请求不一致时拒绝执行。远程节点不得接受另一个节点提供的 image ID；它必须从相同 Git object 独立构建。

### 7.3 晋升规则

普通业务提交使用当前已接受 policy SHA 对应的锁定构建定义，并在执行节点确保其本地物化镜像存在。`PromotionController` 是仓库外签名组件；受信 remote URL、接受 ref（默认 `refs/heads/main`）、当前 generation 和公钥来自 bootstrap root/外部单调 state。若候选修改 CLI、gate registry、Dockerfile 或镜像输入，采用两阶段状态机：

1. 旧策略的节点本地 runner 完成候选验证并产出 ResultReceipt；scheduler 的唯一 grant issuer 基于该证据签发 audience=`git.push` 的 ActionGrant。scheduler 实时构建并验证候选镜像后将候选策略持久化为 `awaiting_trusted_ref`，但继续使用旧 generation 执行权威 CI。
2. pre-push 只原子消费本次 audience=`git.push` 的 ActionGrant 并允许 Git push；它不在远端接受前晋升。
3. scheduler 的 trusted-ref watcher 在 admission 后自动轮询/fetch 受信 remote/ref，无需第二条人工命令。它只处理该 ref 的精确 tip；若 tip 已前进，旧 pending candidate 标记 superseded，并为新 tip 重新计算 CI 输入，不得因 tip“包含”旧候选就晋升旧策略。
4. `promotion` profile 从上一 accepted policy 的节点本地镜像创建 runner 容器，对精确 tip 的固定 fixture、known-bad corpus、新旧 plan、required gate、命令闭包、退出码、结果 schema、BuildKit provenance 和候选 `NodeImageIdentity` 完成验证；verifier 只提交证据，scheduler grant issuer 基于 promotion ResultReceipt 签发 audience=`policy.promote` 的 ActionGrant。
5. 外部单调 store 以 `expected_previous_policy + expected_previous_input_digest + expected_generation` 对签名 PromotionRecord 做一次原子 authority CAS，成功后 generation 加一。PromotionRecord 只记录策略、Git object、构建输入和锁摘要，不包含可供其他节点拉取的镜像地址。
6. 当前节点把已验证的 `NodeImageIdentity` 记录为可丢弃 materialization；其他节点观察到新 generation 后，从 PromotionRecord 的精确 Git object 独立实时构建并验证自己的本地 materialization。
7. controller 在 awaiting_trusted_ref/committed/materialized 任一 crash point 重启后按外部 authority 幂等恢复；CAS 冲突、ref 回退、重复 generation、pending 超时、签名错误、构建失败或本地产物身份不一致时保持旧真相源并失败，不允许候选代码直接晋升自己。

固定 fixtures 和兼容性判定来自旧真相镜像或 bootstrap trust root，不能由候选单方面替换。镜像构建是统一 FIFO 中的 `build` workload，占一个全局 slot 且同一时间最多一个；构建期间新 job 不得插队，不能使用半构建镜像。已运行旧 generation job 可以完成，其 receipt 明确记录 `accepted_at_submit` 与 `accepted_at_finish`；generation 变化时状态为 `passed_stale_policy`，只能展示历史结果，不能授权新的 push/release action。

### 7.4 本地与远程节点实时构建边界

- 本地 Git/Codex/CLI 入口从仓库外签名 PromotionRecord 解析已接受 generation、环境输入 Git object 和 `image_input_digest`。命中本机不可变环境镜像时只复验 inputs、labels、config 和 rootfs，不运行 BuildKit；仅本机无该环境 identity 或环境输入变化时实时构建。每次 job 的 Git object 通过独立 bundle 物化，不参与镜像命中键。
- GitHub 托管 runner、自托管 runner或另一台开发机同样从精确 Git object 独立构建。禁止从 GHCR、Docker Hub、artifact、共享目录或另一节点 Docker daemon 拉取预构建 gate/runtime 真相镜像。
- 后期可用 SSH 作为远程 CI 派发传输层，但只传递签名任务、仓库身份、精确 Git OID、profile 和 deadline。远端节点必须从受信 Git remote 取得并复验该 Git object，再使用自己的 Docker daemon 实时构建和执行；SSH 不得传输源码快照、预构建镜像、OCI layout、本地 image ID 或 BuildKit cache。
- 每个节点只有一份供本节点全部仓库工作树、Agent、CI 任务和镜像共享的持久 BuildKit cache pool；完整路径由该节点的仓库外 `CandidateBuildRoot` 配置解析，只固定相对目录 `cache/shared`，不得在代码或仓库配置中写死某台设备的绝对路径。cache 路径严禁包含 worktree path、agent ID、source tree、image input 或 dependency digest。任务退出只回收 builder、OCI layout 和执行容器，不删除共享 cache。禁止 cache export/import、跨机复制和把 cache 命中作为正确性证据。节点清空缓存后必须能够从 Git object 与锁定依赖重新构建。
- 不同 CPU 架构分别构建各自平台产物；跨节点比较 `image_build_tree_sha + image_input_digest + toolchain_digest + platform + provenance materials`，job source 另比较 `source_tree_sha + bundle_digest`，不要求不同架构具有相同本地 image ID。
- receipt 同时记录 job source truth、全局环境构建真相和当前节点的 `NodeImageIdentity`。另一个节点只能信任前两者并自行物化源码和镜像，不能直接使用收到的 host path 或 image ID。

远程 runner 预置 bootstrap 公钥，只从仓库外只增不改的 PromotionRecord store 获取当前记录，验证 repo ID、trusted ref/event SHA、generation、签名、Git object 和构建输入摘要后才开始本地 build。候选 workflow 文件不能传入或覆盖 authority 输入。

GitHub 远端 required check 的唯一 owner 是组织级受保护 reusable workflow + GitHub App check publisher，二者配置不来自候选 ref。受保护 workflow 使用 GitHub OIDC identity，在 runner 上执行独立 `remote-required` submit、实时构建节点本地镜像、创建新容器并产生带 OIDC attestation 的 ResultReceipt；只有 App identity 可以发布 branch protection/merge queue 所要求的 check 名称。候选仓库 workflow、同名 job、空成功或本地签名不能产生等价 required 结论。

该模型不要求新建独立 CI 服务器：当前本地任务由本机 Docker 构建和执行；后期既可由远端 workflow，也可由受信 SSH dispatcher 将任务派发到远端 runner，但构建与 CI 容器始终在接收任务的节点本地完成。需要跨节点统一排队时才引入中央调度服务，但中央调度器也不得分发源码快照、预构建真相镜像或 BuildKit 缓存。

---

## 8. 本地调度与资源合同

### 8.1 可配置容量与固定单容器上限

```text
shards_per_job = 5
max_running_gate_containers = 20
max_active_ci_workloads = 20
container_cpus = 4
container_memory = 8 GiB
normal_timeout = 10m
release_timeout = 30m
queue_policy = FIFO
max_image_builds = 1
```

节点若要兑现配置的完整并发，应具备与 `max_active_ci_workloads × container_cpus / container_memory` 相匹配的可用资源；启动自检必须报告 daemon CPU、内存、配置上限和理论最坏值。容器的 4 CPU/8 GiB 是 cgroup 上限，不是启动时一次性预占；实际内存随 workload 使用增长。配额不足时不能静默篡改配置，应拒绝启动或要求显式调整容量或分片数。

资源合同固定的是“每个执行容器的上限”和“节点显式配置的全局 slot 数”，不固定 slot 数值。`shards_per_job` 必须处于 `1..min(max_active_ci_workloads, canonical_gate_count)`；单个 invocation 的 shard gang 大于节点容量时立即 fail-fast。修改容量配置必须产生新的配置身份与 receipt，不能仍沿用旧容量声明。

每个执行容器默认 `network=none`、非 root、`cap-drop=ALL`、`no-new-privileges`、只读 rootfs、只读 source、受限 tmpfs、固定 seccomp，并清空宿主环境、凭据和代理变量；同时设置 PID、临时磁盘和日志上限，不挂载 Docker socket，不使用 privileged，不共享可写源码、Git index、生成物目录或 `.build-cache`。确需服务依赖的 gate 必须为该 invocation 创建唯一 internal network；service container 绑定 invocation token/labels，不能与其他 job 共享 DNS/alias，并且每个 service container 也占全局 slot。receipt 记录完整 network/container 集合和 removal proof。依赖下载只发生在受控构建阶段并受 lock/digest 约束。

### 8.2 单例调度所有权

调度 key 不是 Docker context 名称，而是规范化 endpoint/TLS fingerprint 加 Docker `/info` daemon ID。多个 context alias 指向同一 daemon 时必须解析为同一 key 和同一 scheduler。第一版只支持当前 UID 独占拥有的本地 Docker daemon/socket；发现 group/world 可写 socket、共享 daemon 或无法证明唯一 owner 时拒绝启动，不把“每进程各自的容量”冒充 daemon 全局容量。

CLI、Git Hook、Codex Hook 和所有 worktree/clone 只通过仓库外 Unix socket 连接该唯一 scheduler daemon，不得在各自进程内创建私有 scheduler。socket 权限固定 `0600` 并校验 peer UID。

```text
socket: ~/.local/share/super-dolphin-gate/<daemon-identity>/scheduler.sock
state:  ~/.local/share/super-dolphin-gate/<daemon-identity>/state.db
lock:   ~/.local/share/super-dolphin-gate/<daemon-identity>/scheduler.lock
keys:   macOS Keychain / host secret store
```

daemon 以进程锁和 SQLite 原子 slot lease 管理全局容量。job/service/shard container 各占一个 slot，candidate image build session 也占一个 slot，因此任意时刻 `running job/service/shard containers + active builds <= max_active_ci_workloads`；build 仍受 `max_image_builds=1` 约束。`shards_per_job=5` 且全局容量 20 时，4 个独立 invocation 可以各占 5 个 slot 同时运行，后续 runnable workload 进入统一队列。build 只能通过 scheduler 独占的 rootless BuildKit control endpoint 启动，持久化 solve/session ID、heartbeat 和 owner lease；不得依赖无法枚举的匿名 build。启动、恢复和发放 slot 前必须通过 Docker API/BuildKit control API 对账全部受控 workload；未知或重复 owner 时 fail-fast，不再启动容器。

### 8.3 FIFO、去重与取消

每次 `submit` 先生成新的 `invocation_id + request_nonce + enqueue_seq` 并持久化依赖 DAG，再解析/构建镜像。workload 类型为 `job|build|service|shard`，共享同一单调 FIFO。等待依赖的 job 不占 slot；缺镜像时，singleflight build 继承最早依赖 invocation 的 `enqueue_seq` 和子序号 0，build 完成后唤醒依赖 job。每个 invocation 必须在启动任何 shard 前按配置原子 gang-reserve `shards_per_job + N services` 个 slot，需求大于 `max_active_ci_workloads` 立即失败；部分 create 失败时回滚全部 lease/container/network。

调度器每次选择 `enqueue_seq` 最小的 runnable DAG node；blocked head 不占 slot，也不阻止其依赖 build 运行。每个 job 的硬依赖只允许包含该 invocation 自己的 candidate build 或 service gang，不得把前一个 invocation 的 job ID 写成成功依赖；跨 invocation 顺序由单调 `enqueue_seq`、全局 slot 和同一 job 的原子 gang 控制，前序任务进入任何 terminal failure 后都不能永久阻塞后续任务。相同 enqueue_seq 按 `build -> service gang -> job` 固定排序，gang 资源不足时保持 queued 并继续检查下一个不依赖该 gang、但 enqueue_seq 更大的 runnable node，避免空闲 slot；一旦 gang 可满足，不能被后来节点持续饿死。build 使用 4 CPU/8 GiB、普通 10 分钟或 release 30 分钟的同一资源/deadline/heartbeat/kill/reconcile 合同。

任务身份字段：

```text
repo_id
invocation_id
request_nonce
enqueue_seq
base_kind
base_sha
head_sha
tree_sha
local_ref
remote_ref
observed_remote_sha
update_kind
profile
policy_sha
platform_manifest_digest
promotion_generation
platform
network_policy_digest
```

规则：

- 只有同一 `invocation_id` 的多个观察者可以成为 subscriber；两个独立 submit 即使 source/profile 完全相同，也必须生成两个 job/container。
- 历史 passed receipt 只供审计查询，不能满足新 CI action；不存在跨 invocation 绿色 job cache。
- 新 workload 一律先按 `enqueue_seq` 入队；scheduler 只按上一段的“最早 runnable DAG node、受限 bypass、gang 防饥饿”算法发放空闲 slot，不存在另一套“严格物理队首”规则。
- 同一 branch/worktree 注册新 tree/head 后，旧 queued job 标记 superseded。
- running job 只有没有其他 subscriber 时才允许取消。
- timeout、Docker daemon 重启、进程异常和取消不能生成绿色 receipt。
- scheduler 重启后，旧 running job 先进入 recovering；无法用 container ID 和 heartbeat 证明存活时标记 infra_failed，不猜测成功。
- 宿主机 watchdog 使用持久化 `started_at + deadline` 执行 timeout；普通任务 10 分钟、release 30 分钟，恢复后沿用原 deadline，并按 kill、wait、remove 顺序清理容器。

### 8.4 Token 与 receipt 信任

scheduler 为每个 job 生成随机 nonce 和短期、单次使用的 `SignedJobToken`，绑定 job ID、invocation、规范化 SourceSpec、source tree、profile、image identity、generation、deadline 和 container ID。token 由宿主机密钥签名，在 trusted runner 启动握手时原子消费；过期、重放、跨 job/container 使用均失败。候选容器永远不能读取宿主签名私钥。

容器只返回未受信的原始执行事件。trusted runner/verifier 向 scheduler 提交由其直接观察的 gate plan、进程/argv digest、事件闭包、退出状态和日志摘要；scheduler 结合 Docker inspect 生成 `ResultReceipt`，并使用对应 key-purpose 的宿主机 Ed25519 密钥签名。receipt 至少包含：repo/invocation/source object/source tree、plan/policy/runner digest、完整 `NodeImageIdentity`、promotion generation、container/network/service IDs、规范化 HostConfig/network policy、资源限制、started_at/deadline、逐 gate/evidence/log digest、最终状态、container/network removal proof、signer key ID 和 key epoch。候选生成的 JSON 或 checksum 不能成为权威 receipt。

可缓存 `ResultReceipt` 只用于审计，不授权新的权威动作。Git push、release、promotion 等消费端必须提交 typed `GrantRequest`：绑定 result receipt、invocation owner/subscriber capability、受信 adapter identity、进程 challenge、audience/action policy、remote/ref/old/new SHA 和 request nonce。

ActionGrant 使用唯一两阶段状态机：`requested -> issued(unconsumed) -> consumed | expired | revoked`。scheduler grant issuer 在独立签发事务中校验 invocation 权属和 adapter/audience，写入 `issued` 后交付 grant；受信 adapter 在外部动作前以自身 challenge 执行 CAS `issued -> consumed`，只有首次 CAS 成功返回 action authorization acknowledgment。签发崩溃、交付崩溃可幂等查询 issued grant；消费后动作失败不回退 grant，只能基于仍有效 ResultReceipt 发起新 nonce 的 GrantRequest。并发双消费只允许一个成功。跨 agent/invocation/action/ref、抢先消费、过期、重复消费、已撤销 key epoch 或 `passed_stale_policy` 均拒绝。`receipt verify` 必须验证签名、签发时/完成时 generation、字段闭包、日志摘要、key rotation/revocation 和 action audience。

---

## 9. Codex 生命周期接入

### 9.1 Hook 处理

权威 Codex Hook 只能来自系统/MDM/`requirements.toml` managed source，固定启用 `[features] hooks = true` 和 `allow_managed_hooks_only = true`，并指向签名安装包部署的仓库外绝对路径 launcher。launcher digest、代码签名、CLI digest/signer、managed hook identity 和 scheduler daemon/socket identity 共同进入激活记录。仓库内 `.codex/hooks.json` 与 `scripts/codex_stop_gate.sh` 仅作为非权威开发/兼容入口，完成态不得位于真实 Hook 信任链中，也不能与 managed hook 竞争决策。

处理流程：

1. 严格解码官方 hook JSON。
2. 校验 event 为 `Stop` 或 `SubagentStop`。
3. 校验 `session_id`、`turn_id`、`cwd` 和受控枚举 `permission_mode`；SubagentStop 额外校验 `agent_id`。permission mode 写入 subscriber、job provenance 和 receipt 审计字段。
4. managed launcher 为首次事件生成并持久化 `hook_invocation_id`；`stop_hook_active=true` 时不得创建递归 job，而是通过 `parent_stop_invocation_id` 查询父 invocation。只有父 job passed、ResultReceipt 有效且当前 tree 未变化时才能 continue；queued/running/failed/tree-changed 继续 block，terminal 无法自动恢复时进入显式 manual-resolution，不得无条件 `continue:true`。
5. 解析登记 worktree 或官方 cwd，固定当前 Git tree/HEAD。
6. 查询本次 Codex hook invocation identity 对应的 job；没有则创建新的 submit/invocation，已有则只订阅或查询该 invocation，不按 source/profile 复用历史 job。
7. 最多短等待 5 秒。
8. passed 且 tree 未变化：输出 `{"continue":true}`。
9. queued/running：输出 `decision:block`，reason 包含 job ID、队列位置和 `gate wait` 命令。
10. failed/timeout/infra_failed：输出 `decision:block`，reason 包含首个可行动违规和日志引用。

Codex 当前不支持异步 command hook，因此 Hook 不在后台静默等待。`decision:block` 会把 reason 注入 continuation prompt，agent 随后运行 `gate wait` 或修复违规。

仅依赖项目 Hook 的用户信任不能满足“任何 CI 动作必经统一入口”。迁移修改 managed requirements、launcher、CLI signer/digest 或 scheduler identity 后必须使旧激活记录失效并重新审核。完成证据必须记录真实 `/hooks` managed 来源、`hooks=true`、`allow_managed_hooks_only=true`、用户无法禁用、managed identity、launcher/CLI digest、scheduler identity，以及一次根 agent Stop 和一次 SubagentStop 的可复查事件；还必须证明 repo/user/plugin 竞争 Hook 不能覆盖或绕过门禁。仅有 JSON fixture 或脚本单测不能证明生命周期 Hook 正在运行。任何后续权威消费端还必须拒绝缺少匹配 ResultReceipt/ActionGrant 的动作，形成 Hook 配置异常时的外部兜底。

### 9.2 通知边界

通知必须绑定 subscriber：

```text
root:     session_id + turn_id
subagent: session_id + turn_id + agent_id
```

一个 job 可有多个 subscriber。一个 subscriber 断开不能取消其他 subscriber 仍需要的 job。每个 `subscriber + invocation` 使用单调 `event_seq` 和 durable outbox；job 状态与 outbox event 在同一事务提交。投递支持幂等 delivery、ack cursor、重试、断线重连 replay、terminal retention 和 subscriber capability 鉴权。scheduler/Codex 在状态落库、投递前后或 ack 前后崩溃均不能丢失、乱序或错投 terminal result。状态至少支持 queued、started、passed、failed、cancelled、timeout、infra_failed 和 acknowledged。

---

## 10. 并行执行波次

### Task 0：冻结协议和 RED

**写集：** `docs/plans`、新 gate contract tests。

1. 重新解析并记录执行时 `HEAD`、受信 remote URL/ref/SHA，禁止沿用文档静态基线。
2. 固定 SourceSpec、NodeImageIdentity、PromotionRecord、签名 token/receipt、CLI 命令、profile、退出码和 JSON schema。
3. 建立 CI entrypoint inventory RED：现有 Hook、workflow、Make target、PowerShell/Go/shell wrapper 和 Codex 脚本中的宿主机权威直跑全部被识别。
4. 建立 Codex Hook fixture：Stop、SubagentStop、permission_mode 全枚举、缺 cwd/agent_id、orchestrator spawn record 抢注/重放、stop_hook_active 的 queued/failed/passed/tree-changed、managed requirements/launcher identity 变化失效、竞争 repo/user hook 无 authority。
5. 建立 Git fixture：commit/tree/range 互斥、base_kind/ref/update kind、dangling staged tree bundle、普通 range、新分支、zero SHA、fast-forward/force break-glass、无共同祖先、dirty/partial index；delete 明确走独立 Git 策略。
6. 建立 scheduler RED：context alias/竞争单例、配置容量达到上限后下一个 workload 排队、多组冷 submit、build/tie-breaker、job+services/shards gang、部分 create rollback、统一 DAG/FIFO、旧 SHA supersede、outbox crash/replay、GrantRequest 越权、ActionGrant issuance/delivery/双消费/crash/revoke。
7. 建立 image/promotion RED：canonical context/provenance 闭包、输入变更触发 rebuild、Git object/构建上下文错配、OCI digest 错配、候选伪造全绿事件不能获签、首次 push 后 watcher 自动晋升、CAS 冲突和回滚拒绝。

**出口：** 所有缺口由确定性 RED 锁定，字段动态差集可报告准确 missing/stale。

### Task 1A：Gate 内核与 CLI

**建议写集：** `cmd/super-dolphin-gate/**`、`internal/devtools/gate/**`、必要的 command boundary registry/tests。

- 抽取 GateSpec 单一 registry。
- 实现 SourceSpec、typed CIEntrypoint registry、TrustedRunnerIdentity、GrantRequest、plan/run/evidence、ResultReceipt/ActionGrant 和稳定退出码。
- 保留旧 `scripts/ai_maintenance` 兼容入口，直到 Hook/CI 全部切换。
- 将代码守卫算法保留在现有 owner，CLI 只组合调用。

### Task 1B：镜像与 executor

**建议写集：** `build/gate/**`、`internal/devtools/localci/executor*`、镜像 contract tests。

- 实现自动输入指纹、build lock、digest/label 验证。
- 实现签名安装的仓库外 verifier/controller、trust root、root/key rotation、bootstrap-runner OCI 容器、空 image store 自动 bootstrap、固定 fixtures 和首份原子晋升记录。
- 将 `image ensure/build` 固化为 scheduler 持久化 submit 后的强制 workload 步骤，CI 输入变化自动构建候选镜像。
- 实现 PromotionController 的 awaiting_trusted_ref watcher、精确 tip 验证、promotion ResultReceipt/ActionGrant、staging publish/远端复验、generation CAS 和防回滚。
- 实现 SourceSpec bundle 导入、Git object 复验和 Docker 资源/权限/网络/超时限制。
- 普通、release timeout 分开验证。

### Task 1C：调度器

**建议写集：** `internal/devtools/localci/scheduler*`、状态存储和 scheduler tests。

- 实现基于 daemon identity 的单例、build/job/service/shard DAG FIFO、gang reservation、节点可配置全局 slot、同 invocation subscriber、durable outbox、取消/恢复、签名 token/receipt/action grant。
- 第一版队列/outbox/lease 可使用仓库已有 SQLite driver，但 PromotionRecord authority 与 Keychain generation anchor 不以该 SQLite 为真值；数据库属于 developer tooling，不进入产品 store/module。
- 不使用 Redis 作为权威状态。

Task 1A/1B/1C 可并行，但先冻结共享类型；每条 lane 不得各自复制 Job/Receipt DTO。

### Task 2：Hook 迁移

**建议写集：** `.githooks/pre-push`、`.githooks/pre-commit` 的薄适配段、签名安装包/managed requirements manifest、仓库外 launcher 源码与测试；`.codex/hooks.json`、`scripts/codex_stop_gate.sh` 只作非权威迁移兼容面。

- pre-commit 接 local-fast。
- pre-push 接 submit/wait push profile。
- 通过系统/MDM/requirements managed hook、`hooks=true`、`allow_managed_hooks_only=true` 和仓库外签名 launcher 严格解析官方 JSON。
- 验证 managed source 无法由用户/竞争 hook 覆盖，orchestrator spawn record、launcher/CLI/scheduler identity、permission_mode、Stop/SubagentStop 激活证据。
- 保留 commit-msg、history guard 和已有 staged snapshot 语义。

### Task 3：Workflow 与 release 接线

**建议写集：** `.github/workflows/*.yml`、release gate adapter/tests。

- workflow 只负责环境准备、调用统一 CLI、上传 receipt。
- 删除 workflow 中具有 CI 结论权的宿主机直跑测试步骤；所有 required job 必须从已发布 digest 新建容器。
- 接入组织级受保护 reusable workflow、OIDC attestation、唯一 GitHub App check publisher 和 branch protection/merge queue；候选同名 workflow 不具备 authority。
- 增加 `merge_group` 触发后再启用 merge queue。
- macOS/Windows 原生 smoke 只能标记 advisory，不得签发、替代或汇总为权威 CI receipt；需要成为 required 时必须另行设计受信原生执行器，不能违反本计划“所有权威 CI 从真相镜像创建容器”的目标。
- SQLite release gate 明确 required/advisory，不允许 workflow 名称掩盖非阻断结果。

### Task 4：删除重复真值

- 删除 Hook/workflow 中重复 gate 路由和命令清单。
- 旧 wrapper 只转发统一 CLI；确认无调用方后删除。
- 更新 README、hook 文档、codemap/project-map 和 capability contract。

---

## 11. 测试与验收矩阵

### 11.1 必须具备的自动测试

| 面 | 最小证明 |
|---|---|
| Git commit truth | 容器 checkout 的 HEAD 精确等于请求 head SHA，初始工作树 clean |
| SourceSpec | commit/tree/range 严格互斥；base_kind/ref/observed remote/update kind 正确；job source tree、容器源码、plan 和 receipt 完全一致，并与 environment image identity 分字段记录 |
| Active worktree | dangling staged tree 经临时 bare repo/bundle 导入；容器复算 tree SHA；tree 变化使旧结果失效；partial index 被拒绝 |
| Queue/DAG | context alias 解析同一 daemon；多组冷 submit 无死锁；build 继承最早票据；job+services/shards 原子 gang reserve；超过配置容量的 workload queued |
| Fresh container | 每个独立 submit 都有新 invocation nonce/container/removal proof；只有同 invocation observer 可订阅；历史 passed receipt 不能代替运行 |
| Resources | Docker inspect 证明 `--cpus=4`、`--memory=8g`、非 root、cap-drop、只读 rootfs/source、PID/磁盘/日志限制 |
| Network isolation | 默认 network none；每 invocation 独占 internal network/service identity；跨 job TCP/DNS 不可达；宿主凭据和 Docker socket 不可见 |
| Build isolation | 候选 Dockerfile 无 secret/SSH/host network/insecure entitlement/socket/proxy；节点外配置根下唯一 `cache/shared` 被全部工作树和镜像复用，路径不含设备、工作树或 digest 身份；只访问锁定依赖源 |
| Timeout | fresh shard/container 在 admission 后启动执行时钟：非 release 10m、release 30m；排队时间单列，源码快照准备独立 15m；候选 truth-image 构建与晋升独立 30m；重启不重置已启动的执行 deadline；超时 kill/wait/remove 且不 passed |
| Image truth | 普通源码/测试/文档变化不调用 BuildKit且复用 accepted image；runner、Dockerfile、policy、工具链或依赖锁变化才改变 image input；image context/provenance 与环境闭包一致 |
| Source injection | 每个 job 的精确 Git tree 只物化一次；`shards_per_job` 个一次性 shard 只读共享同一 snapshot；tree/bundle/manifest 任一不匹配 fail-fast |
| Trusted runner | 候选 executor 伪造全绿且未启动 required command 时，旧 verifier 通过进程/argv/log/known-bad 闭包证明并拒绝晋升 |
| Bootstrap | 空 image store 从固定 baseline Git object 实时构建 bootstrap-runner；宿主不执行 gate；篡改 checkout/CLI/verifier、未知 baseline、签名错误失败 |
| Promotion | promotion profile 从上一 accepted policy 的节点本地镜像新建 runner 容器；一次 push 后 watcher 晋升精确 tip；awaiting_trusted_ref/committed/materialized 各 crash point可恢复 |
| Cross-node build | 本地与远端 receipt 绑定同一 Git object、image input、工具链和平台；各节点独立实时构建，禁止分发预构建镜像或 BuildKit cache |
| Execution gateway | `CIEntrypoint` 清单覆盖 Hook/workflow/Make/release/wrapper；每个入口均到达 `submit`，扫描和契约测试阻止宿主机直接执行权威 gate |
| Container isolation | 不同 invocation 对应不同 container ID，容器均从 receipt 中复验过的节点本地 image ID 创建并在结束后销毁 |
| Codex Stop | 使用 session/turn/cwd；queued/failed 通过 continuation reason 通知根 agent |
| SubagentStop | 使用 agent_id；独立 worktree 未注册时 fail-fast block |
| Fields | producer 字段动态枚举，missing/stale/未知枚举/缺字段 fail-first |
| Token/Receipt | GrantRequest 越权失败；ActionGrant issued 后首次 adapter CAS consumed，签发/交付 crash 可恢复，并发双消费仅一次成功；过期/撤销失败 |
| Recovery | scheduler/Docker daemon 异常不会把不明状态恢复为 passed |
| Codex activation | requirements managed-only 生效且竞争 hook 无 authority；orchestrator spawn record 防抢注；active 查询父 invocation；Stop/SubagentStop 产生真实 job |
| Notification | subscriber capability、event_seq、durable outbox、ack/replay 正确；状态提交/投递/ack 各 crash point 不丢失或错投 terminal event |
| Remote authority | 受保护 reusable workflow 从 accepted Git object 实时构建节点本地镜像、创建容器并产 OIDC attestation；只有 GitHub App identity 能发布 required check |

### 11.2 完成门槛

只有同时满足以下条件，才能声明迁移完成：

- Git `pre-push` 对真实 commit SHA 启动本地 Docker CI，并能阻止失败 push。
- Git `pre-commit` 对 dangling staged tree 通过只读 bundle 启动容器，容器复算 tree SHA 与请求一致。
- Codex managed Stop/SubagentStop 通过仓库外签名 launcher 定位 session/agent、permission mode 与 worktree；独立 worktree 走显式注册。
- 配置 `shards_per_job=5`、`max_active_ci_workloads=20` 时，从不同 Agent、进程、worktree 和 clone 同时提交 4 个 invocation，必须观察到 20 个 shard active；第 5 个 invocation queued。job/service/shard/build 同样占 slot且遵守统一 FIFO/fairness。
- 三个冷启动 submit、job+两个 service、build singleflight 和任一 create/crash point 均无 slot/FIFO 死锁。
- 每个 gate/service 容器和 build session 的 inspect/control evidence 证明 4 CPU/8 GiB 限额。
- 普通与 release timeout 负向测试均通过。
- 真相镜像变更、构建、digest 校验与晋升闭环通过。
- canonical image context 与 BuildKit provenance 完全绑定环境输入 Git tree；job source bundle/materialization 独立绑定请求 Git tree。候选 runner 伪造全绿事件不能得到 ResultReceipt 或 audience=`image.promote` ActionGrant。
- PromotionController 在唯一一次正常 push 后由 watcher 自动观察精确受信 tip并执行 generation CAS；伪造事件/receipt、候选自签、并发覆盖和回滚负测全部失败。
- promotion 验证本身从上一 accepted policy 的节点本地镜像创建新 runner 容器；宿主 controller 不执行 gate。
- 清空本地 gate image/state 后，第一次 `submit` 无需人工 build 即可完成 bootstrap，并从晋升后的 digest 创建独立 job container。
- bootstrap 的所有 required gate 都在固定 digest 的 bootstrap-runner 容器内执行，宿主 verifier 只观察和验签。
- 修改任一登记的 CI 真值输入后，不运行人工 build 命令也能由下一次 `submit` 自动构建候选镜像。
- 仅修改普通产品源码、测试或文档时，下一次 `submit` 不启动 BuildKit；它复用已接受环境镜像、物化新 Git tree，并从该镜像按 `shards_per_job` 创建新容器并行执行门禁。
- Git Hook、Codex Hook、人工 CI、release 和 workflow required job 不存在绕过 `submit` 的宿主机权威执行路径。
- GitHub branch protection/merge queue 只接受受保护 reusable workflow + App identity 发布的容器 check；`--no-verify`、同名空 workflow 和 API ref update 不能绕过。
- 所有权威 receipt 均由候选容器不可访问的宿主机密钥签名；原生 advisory smoke 不得影响权威 CI 状态。
- ResultReceipt 不能直接授权 push/release/promotion；每个权威消费必须取得并原子消费 audience/ref/nonce 绑定的 ActionGrant。
- 同 SHA 的两个独立 CI action 各创建新容器；同一 invocation 的多个 observer 收到同一结果，不重复创建容器。
- 所有变更面 LSP diagnostics 为零；Error、Warning、Information、Hint 均已处理或记录 blocker。
- 与变更面匹配的 Go tests、Hook contract tests、Docker smoke、架构 guard、生成物 checks 全部通过。
- 工作区未混入本计划之外的用户 dirty 文件。

---

## 12. 已知风险与实施裁决

- **节点容量：** 理论最坏资源为 `max_active_ci_workloads × 4 CPU / 8 GiB`；例如 20 路完整合同要求 80 CPU/160 GiB。资源不足是启动 blocker，不通过争抢制造随机超时；容器内存按实际使用增长，不在启动时一次性预占。
- **镜像自举：** 首次 bootstrap 只信任仓库外 trust root；后续 gate 代码变更必须由旧镜像验证并经过候选镜像对比，禁止候选自签。
- **全局并发：** `max_active_ci_workloads` 个 slot 属于规范化 Docker daemon identity 的单例 daemon，不属于 context 名称或单个 worktree；build、job、service 和 shard container 共同占 slot。
- **晋升与 receipt：** 晋升必须使用受信 ref、旧签名 receipt 和 generation CAS；候选容器不能访问签名密钥。
- **子 agent worktree：** 官方 `SubagentStop` 提供 agent_id，但公共 cwd 是 session cwd；独立 worktree 必须显式登记。
- **Hook 并发：** Codex 会并发运行同事件多个匹配 hook；只有同一 hook invocation 的 observer 可以订阅同一 job，独立 CI action 不去重。
- **Hook async：** Codex 当前不支持 async command hook；采用短等待 + continuation prompt，不在 Hook 中隐式后台运行。
- **Hook 激活：** 修改 `.codex/hooks.json`、仓库外 launcher/CLI 或 scheduler identity 后必须重新确认 managed 来源和完整信任摘要；fixture 通过不等于真实 Hook 已激活。
- **平台真实性：** Linux Docker receipt 不代表 macOS/Windows smoke 通过；原生 smoke 只作 advisory，平台与完整 `NodeImageIdentity` 必须进入 task key 和 receipt。
- **活动输入：** worktree 结果只对精确 tree SHA 有效；commit push 始终重新绑定 commit SHA。

---

## 13. 首轮实施顺序

第一轮按以下顺序执行，不再新增讨论性旁路：

1. 提交本计划和协议 fixture。
2. 并行实现 Task 1A、1B、1C。
3. 集成 CLI 与 scheduler，完成 4-job/3-running Docker smoke。
4. 迁移 Codex Hook，验证根 agent 与 subagent fixture。
5. 迁移 pre-push，验证真实 commit SHA 和失败阻断。
6. 迁移 pre-commit 的 local-fast 入口，保留 staged snapshot 真值。
7. 接 workflow/release，最后删除重复规则。

计划执行中可以调整文件拆分，但不得改变 Git object 真相、3 并发、4 CPU/8 GiB、10m/30m、Codex 官方身份字段和 fail-fast 这些验收合同。
