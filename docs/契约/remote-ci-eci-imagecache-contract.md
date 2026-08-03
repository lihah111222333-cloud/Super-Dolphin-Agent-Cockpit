# 远程 CI ECI ImageCache 单路径契约

状态：Accepted
适用范围：`cmd/super-dolphin-gate`、`internal/devtools/remoteci`、`internal/devtools/gate`、`internal/devtools/alicloud/{eci,oss}`、Git hooks
目标平台：`linux/amd64`

代码契约唯一 owner：`internal/devtools/cicontract`
契约身份：`remote-ci-aliyun-eci-imagecache/v2`
正常执行路径：`sqlite-generation-one-bootstrap-or-accepted-imagecache-snapshot-aliyun-eci-shards/v2`
唯一执行与验收提供方：`aliyun-eci/v1`
首代供给路径：`normal-run-hook-configured-aliyun-eci-generation-one-strict-receipt-bootstrap/v1`
唯一数据源：`duration-ledger-sqlite/v1`
accepted baseline JSON schema：`13`
非权威缓存材料 schema：`remote-ci-cache-material/v1`
非权威缓存材料 authority：`non_authoritative_material`

本文冻结远程 CI 的唯一生产架构。后续实现、重构、子代理任务和代码审查必须以本契约为边界；历史计划、旧迁移说明和已删除实现不得覆盖本文。

## 1. 不可变设计目标

1. 基准运行环境由一代已接受的阿里云 ECI ImageCache 提供。其不可变镜像必须包含：
   - 单一 `internal/devtools/godistribution/go-distribution.lock` 中锁定的 Go 1.26.5；Linux/amd64 远程构建必须调用 `ValidateRemoteCIAsset`，三平台映射只能保留在该 TSV，且不得新增下载或网络 fallback；
   - Go module cache 与 Go build cache；
   - 前端锁定依赖、`node_modules`、Vite dependency optimizer cache 与 Playwright 浏览器；
   - Gate、gopls、sqlc、sqruff、ripgrep、Node、npm、Git、Make 等锁定工具；
   - `/opt/super-dolphin-gate/source-baseline.git`，其中仅含 `RunnerBaseTree` 的完整 tree/blob closure 与一个确定性的无 parent baseline commit。
2. normal run/hook 仅在 accepted singleton 为空时消费配置中的 strict ECI generation-one receipt，在实时复核阿里云 ECI 事实后原子首写 baseline 与账本元数据；singleton 非空后只读取 SQLite 中已接受代的明确 `ImageCacheId + ImageSnapshotId`。它不构建、刷新、发布或晋级基准，仓库内不存在 successor refresh executor。
3. 空 accepted singleton 的唯一初始化入口是 normal run/hook 的配置回执 bootstrap；独立 `provision-generation-one` 命令不得存在。仓库只验证并原子写入该 receipt，绝不创建 ImageCache 或候选。
4. 多个 agent 可以并发触发 Git hook，多个 remote CI job 可以并发运行，且所有可分片的前端、后端、normal、e2e 和 race 工作负载按历史耗时动态规划并无上限并发执行。不得设置全局 hook 锁、active-job lock、semaphore、`errgroup.SetLimit`、`max_shards`、`max_concurrency`、admission cap 或共享原始 token；同一 worktree 的 Git `index.lock` 只是 Git index 一致性边界，绝不是 CI 并发上限。
5. 远程 CI 的唯一执行与验收提供方是阿里云 ECI。所有形成 CI 回执或验收结论的候选编译、测试、校准、首代内容检查及权威耗时观测必须运行在阿里云 ECI container group 内，并绑定明确 region、container group、Ready `ImageCacheId + ImageSnapshotId`。Docker、Docker Desktop、BuildKit、GitHub Actions、Kubernetes、其他云或本地容器只能在仓库边界外准备不可变 OCI 输入物料；其中为生成 Go/前端缓存层而执行的 compile-only/cache-prime 命令也只是物料加工，绝不是 CI 执行或检查。物料若携带 manifest，只允许使用 `schema_version=remote-ci-cache-material/v1`、`authority=non_authoritative_material` 与 `seed_steps`，禁止使用 `checks`、generation-one 或 receipt 命名。它们的构建、测试或日志一律不是 CI、不得写入 `provision_checks`、不得生成 authoritative receipt，也不得作为 ECI 失败时的执行 fallback。
6. 单个可拆测试预计超过 100 秒时必须继续拆分或优化；不可再拆的原子 workload 可以超过目标，但必须在账本中明确记录和告警。shard 在 `Running` 满 100 秒时只实时发出一次“目标超限”告警，绝不得为此取消、中断、kill 或标记失败；完成后 `test_body` 或 `total` 超过 100 秒仍只写结构化告警，权威回执可以保持 PASS。
7. 校准/基准运行使用固定且被回执绑定的 CPU、内存规格；普通 CI 仍可按 workload 资源策略选择规格。
8. 每次运行必须分别记录物化、候选编译、启动、测试主体、总耗时、缓存命中/未命中及资源等待时间。
9. normal CI 默认计算每个 exact workload 的 production-input fingerprint。只可复用同一唯一 Coordinator/SQLite authority 中当前 accepted generation 及其前两代（最近 3 代）、完整权威且仍新鲜的 PASS evidence；future 或更旧 evidence 必须 miss/fail-fast，不得复用；每个 identity 最多保留这 3 个 accepted generations。此处 fresh 仅指代际窗口、权威 passed/cleanup 状态、完整 identity 及 canonical receipt proof 全部有效；不使用 wall-clock TTL。所有相关输入不变且上述约束满足时必须直接 PASS，不重复执行。normal all-hit 不创建 workload CI ECI、不执行测试，且不创建 workload shard、OSS/temp 或 calibration；部分命中只实际执行 miss；calibration 永不复用。每次调用仍保有独立 job/agent audit，hook、job 与 shard 无上限并发，同一个 miss 可以被并发重复执行。identity 必须绑定可观察源码、测试、脚本和命令、平台、policy/toolchain、candidate Gate 源码/工具链与版本、runtime dependency seed；exact Go test/benchmark fingerprint 还必须包含同包所有适用 `_test.go` 编译输入，以及目标运行时观察闭包。不得绑定 agent token、job、generation、snapshot、RunnerImage、RunnerIdentity、RunnerConfigDigest、GateBinary 或 BaselineManifest，也不得只使用 whole tree 或裸生产源码粗指纹。accepted ImageCache 的内容以外部 receipt 导入时的绑定事实为准；依赖、工具链、policy 或 Gate 语义变化必须使 strict PASS evidence 失效。agent token 仅是审计身份，跨 agent 可以共享等价 evidence。

## 2. 唯一权威与职责

| 数据 | 唯一权威 | 禁止的替代权威 |
| --- | --- | --- |
| 已接受基准代 | duration-ledger SQLite 中的 `ci_remote_baseline_state`，并持久化 `execution_provider=aliyun-eci/v1` 与唯一 region | JSON 状态文件、DataCache、OSS manifest、环境变量 |
| 测试历史、规划样本与 strict workload PASS evidence | 同一 duration-ledger SQLite | 另一份 ledger、静态 shard 数、agent 私有账本、JSON/OSS/.pass/旧 `ci_workload_fingerprints` |
| 运行时缓存选择 | 已接受状态中的 `ImageCacheId + ImageSnapshotId` | tag、自动匹配、registry tag、最近创建缓存 |
| 候选源码 | 精确 Git commit/tree/patch identity | 工作目录当前内容、未绑定临时目录 |
| 候选 Gate 编译身份 | exact tree 的真实 Go 传递编译闭包与工具链摘要 | 手工输入清单、文件时间、warm workspace binary |
| check receipt、timing、warning 与 reuse proof | 同一 duration-ledger SQLite | OSS 对象、日志、环境变量、独立数据库或本地文件 |

SQLite 状态中的 JSON 只是严格 schema 编码；它不是 JSON 文件真相源。OSS 只传输内容寻址的请求、候选源码和回执；它不是缓存或状态权威。

首次读取尚不存在的 SQLite authority 时，先原子创建 current schema 及其查询索引。normal run/hook 仅在配置显式携带完整 strict ECI generation-one receipt 时，才可在实时验证后以单个事务首写 accepted baseline 与 generation=1 账本元数据；缺少回执、字段或云事实漂移必须 fail-fast，禁止生成默认状态、generation=0 或自动全量重建。

### 2.1 Agent token 身份与密文边界

- 阶段一：既未带 flag 也未带环境变量的请求只返回机器可读 `guidance`，其中同时提供 `--agent-token=issue` 与 `SUPER_DOLPHIN_CI_AGENT_TOKEN=issue` 的申请方式；不得生成或返回 token/digest，并且 `execute_ci=false`，不得创建 CI、ECI、SQLite run、OSS 对象或任何执行回执。
- 阶段二：仅当 flag 或唯一环境变量之一显式等于 `issue` 时，才生成并立即返回原始 `agent_token`、`agent_token_digest` 与后续 retry argv；它同样 `execute_ci=false` 且 fail-closed。并发 issue 必须得到不同 token。flag 与环境变量同时存在（即使值相同）必须拒绝。
- 阶段三：仅后续请求在 flag 或唯一环境变量之一携带与先前摘要匹配的同一原始 token 时，才是同一 agent 且可以执行 CI。空 token、错配 token、非规范摘要或将 `issue` 与实际 token 混用必须 fail-fast。
- Git hook 无状态：token 由调用 Git 的 agent 任务上下文或长生命周期 shell 持有。hook 只能继承并验证实际 `SUPER_DOLPHIN_CI_AGENT_TOKEN`，不得自动签发、缓存、串联或将 token 放入 argv；无 token 时只能返回阶段一的结构化申请与使用说明并 fail-closed。agent 必须在 hook 外单独 issue，再以实际 token 环境变量重试 Git。
- 原始 token 只可返回给发起 issue 的调用 agent，并仅在该 agent 任务上下文或长生命周期 shell，以及随后继承它的当前 Git/hook/Gate 进程链内存中短暂存在。唯一 flag 是 `--agent-token`，唯一环境变量是 `SUPER_DOLPHIN_CI_AGENT_TOKEN`；不得保留 `RequesterFingerprint` / `requester_fingerprint` 的类型、CLI、表或 JSON 字段路径。为了 fail-fast，入口可仅识别其旧 flag/环境变量并立即报“retired”，不得把它们解析、保存、回显或映射为 token。
- 禁止在 repo、`.git/`、SQLite、共享文件、Git config、Keychain 或其他持久化媒介缓存原始 token。多个 agent 必须各自 issue；只有显式复用同一实际 token 才是同一身份。
- SQLite、OSS、ECI request/tag、日志、checkpoint、worker report、RunInput、ShardRequest、RunResult 与 authoritative receipt 只能保存或传递 `sha256:` 格式的 `agent_token_digest`，不得保存、回显、序列化或派生传递原始 token。每个边界都必须校验 digest；同一 run/shard/report/receipt 的 digest 不一致时必须拒绝。

## 3. 唯一正常 CI 路径

```text
Git hook / remote run
  -> 读取 SQLite accepted BaselineState；若 singleton 为空则消费配置 strict ECI receipt、实时复核并原子首写 generation=1
  -> 重新读取并验证 accepted BaselineState
  -> 固定 accepted ImageSnapshotId
  -> 读取 SQLite 历史耗时并生成 LPT 分片
  -> 所有计划内 shard 无上限并发创建 ECI container group
  -> 在 exact candidate tree 内复核编译闭包并增量编译 Gate
  -> 计算 exact workload production-input fingerprints，并实际执行所有 miss、严格复用 fresh authoritative PASS hit
  -> 严格回执、资源清理、耗时与缓存证据写回同一 SQLite
```

硬约束：

- 正常 CI 与首代内容检查只能通过阿里云 ECI API 创建 container group 执行。仓库不得提供通用 provider executor、Docker executor、Kubernetes executor、GitHub Actions executor、其他云适配器或 remote-to-local fallback；OCI 发布方式不属于 CI 执行面，只有被 ECI Ready ImageCache 接受并由绑定该 snapshot 的 ECI container group 实测后才形成可导入证据。
- 云控制面客户端只允许配置阿里云官方 `aliyun` CLI（可为其绝对路径）并只调用 ECI/OSS API；不得把该配置项改造成任意 provider executable、协议代理或第二云适配入口。该 CLI、凭据 profile 与运行主机属于受信基础设施边界，测试注入 runner 只能验证命令形状，不能形成权威远程证据。
- Git hook invocation、remote CI job 与动态 shard 彼此独立并发；hook 不得以全局锁、active job 锁或共享 raw token 把不同 agent 串成单队列。一个 agent 的原始 token 只属于该 agent 的当前进程链，跨边界只传 digest。
- 同一 worktree 出现 Git `index.lock` 时，只按 Git 的一致性语义处理该 index 操作；不得把它扩展为 CI job、shard 或其他 worktree 的 admission/串行机制。
- 创建 ECI container group 必须传 accepted `ImageCacheSnapshotID`；不接受自动匹配、tag 或最近创建缓存作为选择依据。
- 主容器与 init 容器必须使用不可变 digest 镜像，且显式绑定同一个已接受 snapshot。
- 禁止 `AutoMatchImageCache` 参与正常 shard 选择。
- 候选 Gate 必须在 shard 内从 exact candidate tree 增量编译；不得复用未绑定候选树的预编译二进制。
- 候选源码 transport 必须使用唯一标准 v2 `git-bundle-thin`：每次 CI 生成一个 tree 等于候选、唯一 parent 等于 accepted baseline commit 的确定性 transport commit，并以相对 baseline 的 bundle 传输；bundle header 必须恰好广告一个 baseline prerequisite 和唯一 `refs/source/materialized`。禁止自包含 bundle、full fallback、raw whole repo、候选原始历史或第二 bundle/manifest 形态。
- source bundle `source.bundle` 与 strict manifest `source-manifest.json` 必须在同一 job 的完整内容寻址上传成功并完成摘要/字段验证后，才可按同一 SQLite authority 的 LPT 计划创建全部并发 shards；任何单个对象缺失、部分上传或验证失败都必须 fail-fast，不能先创建 shard。
- Go/module/build cache、Node/npm、前端依赖/Vite 构建 cache、Playwright 浏览器及系统依赖必须直接读取 accepted ImageCache 的不可变镜像层。ECI request 必须且只能声明 `source-data`、`work-data`、`temp-data` 三个 EmptyDir；禁止 FlexVolume/OSS bootstrap、`expanded-data`、DataCache、缓存卷、subPath 挂载、解压目录或逐分片复制展开。`node_modules` 与 `.vite` 必须直接链接镜像层，`.vite-temp` 才可位于分片私有工作目录；前端 `dist` 是构建结果，不得伪装成可复用 build cache 打入镜像。Go 增量编译必须使用镜像内只读 build-cache seed 与分片私有的小型写层，不得把 seed 复制到外挂卷。Dockerfile 与 seed worker 属于必须审计的配方输入，不得进入可复用依赖内容 identity；仅配方/脚本变化而锁定依赖、工具链和模块清单不变时必须直接复用上一代 runtime 镜像，不得重跑 Go/npm/Playwright/tool seed。
- cache hit 只能加速已证明等价的输入、物化或编译；strict workload PASS reuse 只能从同一 SQLite authority 的当前 accepted generation 及其前两代（最近 3 代）完整权威 fresh evidence 取得；future 或更旧 evidence 必须 miss/fail-fast，不能复用。identity 必须覆盖可观察源码、测试、脚本、命令、平台、policy/toolchain、candidate Gate 源码/工具链与版本和 runtime dependency seed；exact Go test/benchmark fingerprint 必须额外包含同包所有适用 `_test.go` 编译输入和目标运行时观察闭包。cache/source-only baseline refresh 不使其失效；依赖、工具链、policy 或 Gate 语义变化必须使其失效。它不能省略候选物化或身份验证，不能以 whole-tree/裸生产源码粗指纹、agent token/job/generation/snapshot、RunnerImage/RunnerIdentity/RunnerConfigDigest/GateBinary/BaselineManifest、JSON/OSS/.pass 或旧 `ci_workload_fingerprints` 冒充等价。每个 reused check 必须含 canonical reuse proof SHA-256；non-reused check 禁止携带该字段；有效 PASS 必须 `passed && (executed || reused)`，同一 run 可混合 executed miss 与 reused hit。

- worker 实际消费的 `SUPER_DOLPHIN_REMOTE_EXECUTION_TIMEOUT` 必须以 `time.Duration.String()` 规范化后进入 strict workload PASS identity；agent token、job、source key 与 shard identity 仅用于审计或传输，不能阻断跨 agent 等价 PASS 复用。
## 4. normal run/hook 首代严格回执 bootstrap 边界

首代 ImageCache 的创建、发布、Ready 等待、运营权限和生命周期完全在仓库边界之外。外部 operator 可以完成这些云侧动作并把无密钥 strict receipt 放入 remote run config 的 `generation_one_provision` 协议字段，但不得通过本仓库的命令、worker、BuildKit、registry 配置、候选 reservation 或 CAS promotion 驱动云侧供给。

### 4.1 外部 operator 回执内容

外部 operator 交给仓库的 generation-one strict receipt 必须明确绑定 execution provider=`aliyun-eci/v1`、region、每项检查唯一且真实的 ECI container group ID 与 container name、generation=1、canonical state SHA-256、唯一 Ready `ImageCacheId + ImageSnapshotId`、不可变镜像 identity、精确 source tree、Go/toolchain、policy、runtime dependency seed 与固定 CPU/内存规格。`gate_build`、`normal_compile`、`e2e_compile`、`race_compile`、`frontend_build`、`dependency` 六项内容检查必须分别由绑定该 Ready snapshot 的阿里云 ECI container group 实际执行并通过，同时记录每项严格大于零的真实起止、`duration_ms`、candidate compile 观测、回执摘要和 `test_body=not_applicable`。每个 group 必须以规范 ECI tags 绑定 provider、ImageCache ID、snapshot、source tree、check 与 plan digest；同一个 group 不得复用于两项检查。

首写前仓库必须通过配置指定 region 的阿里云 ECI API 实时 `DescribeImageCaches` 与 `DescribeContainerGroups`，精确核对 cache region/Ready/snapshot/image 集合，以及每个 group 的 region、唯一 ID、`Succeeded`、主容器名称、不可变 runtime image、`Terminated`、exit code 0、规范 tags 和容纳 observation 的真实运行时间区间。外部 operator 必须让这些终态 container groups 保持可查询，直到 normal run/hook 完成实时核验和 SQLite 原子首写；只能在首写成功后清理。缺少、提前清理、跨 region、重复、额外、非终态或任一字段漂移都必须 fail-fast。Docker/BuildKit/GitHub Actions 或其他 OCI 发布环境只能产出 `remote-ci-cache-material/v1`、`authority=non_authoritative_material` 的候选镜像缓存物料；其 manifest 只能列 `seed_steps`，不得列 `checks`。其构建或日志不得转换、复制或冒充这些 ECI observations；Dockerfile、BuildKit stage 和 OCI artifact 中禁止生成 `provision_checks` 或 generation-one authoritative receipt。这些 ECI 内容检查仅证明外部首代的构建内容，绝不构成 normal CI 测试 PASS。

### 4.2 仓库唯一首写动作

normal run/hook 只验证配置 receipt 与上述阿里云 ECI 实时事实，并仅在 `ci_remote_baseline_state` 为空时，把 accepted singleton generation=1 与 `duration_ledger_meta` generation=1 在同一 SQLite 事务中原子 INSERT。相同 state digest 的并发首写 loser 可在严格重读后幂等收敛；不同 state digest、已有不同 singleton、generation 非 1、非 Ready snapshot、缺少任一绑定字段、摘要不匹配、区域漂移、元数据冲突或规格漂移必须 fail-fast 并整笔回滚。accepted state 必须继续保存 `execution_provider=aliyun-eci/v1` 与 region；后续 run/hook/calibrate 只读 accepted，不再消费或等待首代回执。

### 4.3 明确禁止的仓库内 writer

仓库不得提供独立 `provision-generation-one` 或 refresh command、后台/定时 refresh executor、BuildKit publish、`output_repository`、`CreateImageCache` writer、候选 reservation、lease/heartbeat/takeover、source delta 构建、successor 选择、镜像发布、CAS promotion 或旧代 cleanup。首写完成后 accepted ImageCache 仅由 normal CI 读取；任何后续供给或替换都必须作为新的外部 operator 交接设计，不能复活本仓库 writer。

## 5. 动态分片与并发

- shard 数由 workload 历史耗时和 100 秒目标自动计算，最大只受当前可分片原子 workload 数量限制。
- Git hook invocations、remote CI jobs 与 job 内动态 shards 均无仓库并发上限；不同 agent 的 hook 不得彼此阻塞，job 不得因另一个 active job 等待，shard 不得排队等待人为 token。
- 禁止全局 hook lock、active-job lock、`max_shards`、`max_concurrency`、batch size、semaphore、`errgroup.SetLimit`、admission cap、共享 raw token 或其他人为并发上限。Git `index.lock` 仅保护同一 worktree 的 Git index 一致性，不得作为上述任一上限的实现。
- 云配额/API 限流是显式基础设施错误，不能通过静默降并发或转为本地执行来兜底。
- Go package/test、race package、Vitest 文件、Playwright spec 和其他可拆目标必须以可稳定寻址的原子 workload 入账。
- workload 大于 100 秒时，优先优化测试实现；无法优化且可拆时继续拆分。不可拆原因必须进入优化告警。

## 6. 固定规格校准

- calibration/benchmark 必须使用配置中唯一、明确、固定的 CPU 和内存规格。
- 固定规格必须进入 RunInput、ShardRequest、执行回执、SQLite run record 和 calibration checkpoint identity。
- 任一 shard 规格漂移、缺少规格证据或 checkpoint 规格不一致时必须 fail-fast。
- calibration 仍可并发分片；“固定规格”不等于限制 shard 数或并发数。

## 7. 精确耗时与缓存账本

每个 shard/workload 的结构化回执和人类可读账本至少包含：

所有阶段以同一 SQLite 的 `TimingObservation` 保存 `job_id`、scope、shard identity、可选 workload、phase、真实起止、`duration_ms`、measurement、reason、aggregation 与 cache evidence。`not_measured` 不是权威状态；不适用必须是 `not_applicable` 并包含 reason。人类账本只能读取已提交的同一 observations，不能从内存结果或日志重新推断。

| 指标 | 含义 |
| --- | --- |
| `eci_wait` | ECI 创建到可运行/终态等待 |
| `source_materialize` | 候选源码与 cache seed 物化 |
| `candidate_compile` | exact candidate Gate/测试编译 |
| `startup` | worker 启动且不包含编译、测试主体 |
| `test_body` | 实际测试执行 |
| `total` | 端到端 shard/workload 耗时 |

同时必须按 workload 输出：

- 对每个计划内 workload，实际执行 miss 或以 `reused=true` 严格复用 hit；reused 必须有 canonical proof SHA-256，non-reused 禁止携带 proof。normal all-hit 为 0 workload CI ECI，且不创建 workload shard、OSS/temp 或 calibration、不执行测试；部分命中只执行 miss；calibration 永不复用。不得存在 JSON/OSS/.pass/旧 `ci_workload_fingerprints`、无完整权威 fresh evidence 的 PASS 结果缓存或跳过测试回执；
- Go private cache hit、baseline cache hit、miss、put 的状态与计数；
- 前端依赖 seed 和 Vite cache 是否验证、是否实际 materialize；
- 不适用阶段的明确 `not_applicable`，不得伪造为 0ms 或 cache miss。

<!-- cicontract:timing:begin -->
每条 measured observation 必须在同一 SQLite authority 保存真实 started_at、completed_at 与 duration_ms；raw 和 critical_path 的 duration_ms 必须严格等于真实区间长度。interval_union 的 duration_ms 必须是全部原始 workload 子区间的精确并集：重叠只计一次、空隙不计入，禁止用最早开始到最晚结束的 envelope 冒充活跃耗时。

workload 的 startup、test_body 与 total 是 raw；shard 的 startup 与 test_body 是 workload raw 区间的 interval_union，shard/run total 是 critical_path。eci_wait 只能使用 ECI provider 返回的 CreationTime 到 materializer CurrentState.StartTime，shard total 终点只能使用 provider terminal time；本地请求、轮询或日志时间不得写成权威耗时。所有 cache evidence 与阶段观测绑定，人类账本只能读取同一事务已提交的 SQLite observations。
<!-- cicontract:timing:end -->

编译和测试时间不得重叠计数。缺少应有观测时必须拒绝 authoritative receipt。

100 秒是固定的 `warn_and_continue` 行为：只写入同一 SQLite authority 的结构化 warning，绝不得触发 cancel、kill、timeout failure 或 shard failure。其他独立的基础设施和安全超时不因此失效，但不得把 100 秒目标作为终止条件。

## 7.1 有界增长与淘汰

<!-- cicontract:retention:begin -->
`cicontract` 是唯一 retention 常量 owner。duration samples、catalog observations、runs、strict workload PASS evidence 与 calibration checkpoints 五个历史根都必须绑定已验证的 accepted baseline generation；每个根写事务必须先读取同一 SQLite authority 的 accepted singleton，拒绝零值、无 authority、无效 authority 与晚于当前 accepted generation 的伪造未来代。已启动的旧 accepted generation 运行仍可在完成时写入。五个根的 distinct generation 并集按数值确定全库唯一保留集合，任何根都不得保留该集合之外的数据。每一代可包含任意数量的 workload、sample、shard、timing、receipt 或 scenario，禁止用固定行数限制代码和测试增长。SQLite 只保留最新 3 个有数据的 generation；第四个有数据代首次成功写入时，必须在同一事务内淘汰最老一代全部历史根及其 cascade 子数据。

100 秒结构化 timing warning 只能沿同一 SQLite authority 的互斥生命周期流转：ci_live_timing_warnings 只暂存仍在运行的 provider StartTime 事实，run finalizer 必须在同一事务精确吸收到 ci_run_timing_warnings 并删除对应 live 行；不得预写或伪造 ci_runs 失败终态，也不得让 live 与 final 行同时存在。live 表不是第六个历史根或第二真相源，不参与五根 generation 并集；唯一 compactor 必须按已校验 accepted singleton 的 current/current-2 数值窗口保留 active 行并清理崩溃残留。

唯一 compactDurationLedgerAuthority 只能在既有成功写事务的 commit 前同步调用，禁止 timer、goroutine、后台 GC 或第二入口。generation 按数值排序，不能用行数、时间戳或插入顺序冒充；无法证明 generation 的旧行必须 fail-fast 或经显式迁移，不能默认绑定当前代。删除旧 run 依靠 FK cascade 同步删除 requester、shard/workload、execution、timing、warning 与 receipt；删除旧 checkpoint 同步删除任意数量 scenario；catalog 内容只有在不再被保留代 observation/run 引用时才能删除。accepted baseline 是当前状态 singleton，duration meta/calibration、query meta 和源码枚举的 schema migration registry 不是历史代，不参与淘汰。
<!-- cicontract:retention:end -->

## 8. 明确禁止的旧实现

以下生产代码、配置和降级路径不得存在或重新接线：

- DataCache、Anchor、Delta、direct cache、zstd cache bundle；
- 本地 Docker/Docker Desktop/buildx、localci scheduler 或本地镜像 bootstrap；
- Docker/BuildKit/GitHub Actions/Kubernetes/其他云或本地容器作为 CI executor、首代内容检查执行器或 authoritative receipt 来源；
- 通用 provider abstraction、第二云 provider、非阿里云 ECI executor 或任何 non-ECI 执行 fallback；
- ACR 专用 auth、RAM role token、registry control-plane 或 repository 管理；ECI 返回的 opaque immutable image identity 不属于此类控制面；
- JSON baseline/ledger truth source、双写、宽松 schema 兼容或迁移回读；
- candidate CLI artifact builder、candidate test-binary builder、第二 executor；
- JSON/OSS/.pass/旧 `ci_workload_fingerprints` workload PASS 结果缓存、第二 authority，或任何未绑定同一 SQLite canonical proof 的 `reused` 返回/跳过路径；
- spot 到按量计费、remote 到 local、cache miss 到无缓存执行之外的隐式 fallback；
- 固定 shard 数、五分片限制、并发上限、全局 hook 锁、active-job lock、semaphore、admission cap 或共享 raw token；Git `index.lock` 除外，它只能作为同一 worktree 的 Git index 一致性边界，不能拿来限制 CI；
- accepted 缺失时自动全量重建；
- 任何仓库内 successor refresh executor、refresh command、BuildKit publish、`output_repository`、`CreateImageCache` writer、candidate reservation、lease/CAS promotion 或旧代 cleanup。

严格 request/receipt JSON、SQLite 中带摘要的 state JSON 以及阿里云 CLI JSON 响应属于协议编码，不属于被禁止的 JSON truth source。

## 9. 变更与验收门禁

任何触碰本文范围的改动必须同时满足：

1. LSP 定位、定义/悬停、引用/调用链、精读与 diagnostics 全部闭合；所有 severity 均处理。
2. 字段从生产者到消费者、SQLite schema、严格 JSON、回执和字段守卫同步更新。
3. 单元测试至少覆盖：外部 generation-one receipt 的字段完整性、`aliyun-eci/v1` provider/region/container group/container/image/tag/exit/timing/snapshot 实时绑定、非 ECI 执行证据拒绝、generation=1/空 singleton 原子导入、重复或非 Ready 导入拒绝、固定规格绑定与旧代保留，以及 baseline closure、确定性 transport commit、单 prerequisite v2 bundle 和 bundle/manifest 上传屏障。
4. 架构守卫静态拒绝第 8 节列出的旧生产路径和并发上限，并覆盖多 agent hook、多 job、动态 shard 无上限并发、仓库内 ImageCache writer 删除及 Git `index.lock` 非 admission 语义。
5. 前后端 lint/test/build、Go 定向测试、archtest、code guard 与 `git diff --check` 通过。
6. 远程验收必须绑定同一个候选 tree、accepted ImageCache generation/snapshot、固定资源证据和完整耗时账本。
7. 非 authoritative 候选验证只能标记 `NOT_VERIFIED`；不能用本地 PASS、缓存命中或外部 ImageCache 供给完成替代远程 CI 速度验收。
8. 目标是正常前后端 CI 在可比较的 warm accepted generation 上 100 秒内完成；超过目标必须以账本定位并继续优化。

修改本契约必须在同一变更中更新相关架构守卫和测试。任何临时兼容、降级或第二路径都必须直接拒绝，不允许以 TODO 保留在生产入口。

## 10. 文档与代码的 1:1 映射

下面的标记块由 `cicontract.CanonicalMarkdown` 定义，并由架构测试逐字比对。正文负责解释设计，代码规则 ID 负责让生产校验、SQLite 状态机、回执守卫和静态守卫共享同一个 owner；任一侧单独修改都会使门禁失败。

<!-- cicontract:begin -->
| ID | 章节 | 代码约束 | 执行层 |
| --- | --- | --- | --- |
| `1.1` | §1 | 基准镜像消费单一 Go 1.26.5 锁，并包含锁定工具、Go module/build cache 与前端依赖/构建 cache | `build closure + runtime manifest + archtest` |
| `1.2` | §1 | normal run/hook 仅在空 singleton 时消费配置 strict ECI receipt 原子首写；非空后只读 accepted explicit snapshot，且不存在 successor refresh executor | `runtime call graph + archtest` |
| `1.3` | §1 | hook、job 与动态 shards 无仓库并发上限；index.lock 仅保护 Git index | `cicontract concurrency policy + archtest` |
| `1.5` | §1 | 远程 CI 的唯一执行与验收提供方是阿里云 ECI；其他环境只能生成 remote-ci-cache-material/v1 且 authority=non_authoritative_material 的缓存物料，不能执行或证明 CI | `provider identity + ECI request/receipt field guard + archtest` |
| `1.6` | §1 | 100 秒只触发一次目标超限告警；不得取消、中断、kill 或标失败 | `planner + non-terminating timing warning` |
| `1.7` | §1 | 校准运行使用固定且被回执绑定的 CPU 与内存规格 | `request + receipt + SQLite` |
| `1.8` | §1 | 运行以真实起止和 duration_ms 分别记录物化、编译、启动、测试、总耗时、缓存与等待；并发聚合使用精确 interval union | `receipt + SQLite timing ledger` |
| `1.9` | §1 | normal all-hit 不创建 workload CI ECI、workload shard、workload OSS/temp 或 calibration，且不执行测试；部分命中只执行 miss，calibration 永不复用 | `required-check catalogue + strict reuse receipt validation + archtest` |
| `2.1` | §2 | accepted baseline、duration history、run/shard receipt 与 calibration checkpoint 只读写同一 SQLite authority | `SQLite schema + store` |
| `2.2` | §2 | 运行时缓存只由 accepted ImageCache ID 与 snapshot ID 选择 | `state validation + ECI request` |
| `2.3` | §2 | 候选源码和 Gate 编译分别绑定 exact Git identity 与真实传递编译闭包 | `materializer + receipt` |
| `2.4` | §2 | 严格 JSON 仅作协议编码，OSS 仅作内容寻址传输，二者均非权威 | `strict decoders + archtest` |
| `2.5` | §2 | accepted state、check receipts、timing 与 warnings 只写同一个 SQLite authority | `SQLite schema + receipt store` |
| `2.6` | §2 | generation-one 只能由 normal run/hook 消费配置中的外部严格 ECI receipt；仓库内代码不得创建或晋级 ImageCache | `strict receipt boundary + archtest` |
| `2.7` | §2 | 无 token 请求只返回 --agent-token=issue 与 env=issue 申请方式和实际 token 的 flag/env 使用方式；仅单一显式 issue 才签发 raw token，前两阶段均不执行 CI/ECI；git hook 无状态且只继承/验证实际 env token，跨 SQLite/OSS/ECI/log/tag/checkpoint/receipt 只传 sha256 digest | `agent-token contract + field guard + archtest` |
| `2.8` | §2 | 首次读取缺失 SQLite 时原子初始化 schema/index；normal run/hook 仅凭显式 strict ECI receipt 原子首写 baseline 与账本元数据，缺失或漂移仍 fail-fast | `SQLite initializer + generation-one bootstrap test` |
| `3.1` | §3 | 正常 CI 唯一路径为 accepted SQLite 到 LPT 到无上限并发 ECI shards | `runtime call graph + archtest` |
| `3.2` | §3 | 正常 shard 显式绑定 accepted snapshot，禁止 AutoMatch 选择 | `ECI request validation` |
| `3.3` | §3 | 候选 Gate 在 exact candidate tree 内增量编译，cache hit 不跳过身份验证 | `materializer + receipt` |
| `3.4` | §3 | 工具链、依赖、浏览器与缓存只直读 accepted ImageCache 镜像层；ECI request 只能有 source/work/temp 三个 EmptyDir，禁止 FlexVolume/OSS bootstrap、expanded-data、DataCache、缓存卷、subPath 或逐 shard 复制展开；node_modules 与 Vite cache 必须链接镜像层，dist 不得冒充 cache；Dockerfile/seed worker 只作配方审计且不得改变依赖内容 identity，依赖不变必须复用上一代 runtime 镜像 | `ECI request shape + stable dependency identity + image closure + executor seed behavior + archtest` |
| `3.5` | §3 | accepted ImageCache 在 /opt/super-dolphin-gate/source-baseline.git 提供 RunnerBaseTree 的 tree/blob closure 与确定性无 parent baseline commit；每次 CI 只上传标准 v2 git-bundle-thin，bundle 由 tree=候选、唯一 parent=baseline 的确定性 transport commit 相对 baseline 生成且 header 恰好一个 prerequisite；严禁自包含/full fallback/raw whole repo，bundle 与 strict manifest 完整上传后才按 SQLite LPT 创建全部并发 shards | `source baseline + deterministic transport commit + strict bundle/manifest verifier + upload barrier + archtest` |
| `4.1` | §4 | 唯一写 accepted singleton 的路径是 normal run/hook 空库 bootstrap；首写前必须消费配置 strict receipt 并实时 Describe 阿里云 ECI cache/container groups，绑定 provider、region、唯一 group/container、Ready snapshot、immutable image、tags、零退出、真实时间、generation=1、state SHA、源码/工具链/策略/seed 与固定规格 | `receipt validation + live ECI API verification + SQLite INSERT + archtest` |
| `4.2` | §4 | 首写只允许空 singleton 原子 INSERT baseline 与账本元数据；同态并发幂等收敛，异态、非空、缺字段或非首代 receipt 必须 fail-fast | `strict bootstrap boundary` |
| `4.3` | §4 | 仓库内禁止 refresh command、BuildKit publish、output_repository、CreateImageCache writer、candidate reservation 与 CAS promotion | `deletion + archtest` |
| `5.1` | §5 | shard 数只受可分片原子 workload 数量限制 | `LPT planner + archtest` |
| `5.2` | §5 | 云配额与 API 限流必须显式失败，不得静默降并发或转本地 | `runtime + archtest` |
| `6.1` | §6 | 固定校准规格贯穿 RunInput、ShardRequest、receipt、SQLite 与 checkpoint identity | `field guard + store` |
| `6.2` | §6 | 校准规格漂移或缺失必须 fail-fast，固定规格不限制 shard 并发 | `validation + archtest` |
| `7.1` | §7 | shard 与 workload 账本显式表达六个耗时阶段及其作用域 | `receipt + ledger renderer` |
| `7.2` | §7 | 账本证明 workload 实际 executed miss 或严格 reused hit，并记录 canonical reuse proof 与仅用于加速的 Go cache、前端 seed/Vite cache 证据 | `receipt + ledger renderer` |
| `7.3` | §7 | 不适用阶段写 not_applicable，缺失应有观测拒绝 authoritative receipt | `receipt validation` |
| `7.4` | §7 | 100 秒告警固定 warn_and_continue；不得 cancel、kill 或 fail shard | `warning-action validation + archtest` |
| `8.1` | §8 | DataCache、旧 bundle、本地 Docker、ACR、JSON truth、通用/第二 provider executor 与隐式 fallback 禁止存在；非 ECI 构建日志不得冒充 CI receipt | `deletion + archtest` |
| `8.2` | §8 | 固定 shard 数、并发上限、自动全量重建及任何仓库内 successor refresh executor 禁止存在 | `deletion + archtest` |
| `9.1` | §9 | 变更必须闭合 LSP、字段链、状态矩阵、守卫和变更面测试 | `repository gates` |
| `9.2` | §9 | 远程验收绑定同一 candidate tree、generation、snapshot、资源与完整账本 | `authoritative receipt` |
| `9.3` | §9 | 非 authoritative 结果保持 NOT_VERIFIED，warm CI 超过 100 秒继续优化 | `remote acceptance` |
| `7.5` | §7 | 五个 SQLite 历史根写前证明 generation 已被接受，并共享全库唯一保留集合；每代行数不限，只保留最新 3 个有数据代，第四代写入与第一代全族淘汰同事务 | `accepted-generation proof + single write-transaction compactor + archtest` |
<!-- cicontract:end -->
