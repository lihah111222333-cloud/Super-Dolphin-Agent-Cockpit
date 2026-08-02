# 远程 CI ECI ImageCache 单路径契约

状态：Accepted
适用范围：`cmd/super-dolphin-gate`、`internal/devtools/remoteci`、`internal/devtools/gate`、`internal/devtools/alicloud/{eci,oss}`、Git hooks
目标平台：`linux/amd64`

代码契约唯一 owner：`internal/devtools/cicontract`
契约身份：`remote-ci-eci-imagecache/v1`
正常执行路径：`accepted-sqlite-imagecache-snapshot-eci-shards/v1`
后台刷新路径：`sqlite-lease-successor-imagecache-cas/v1`
唯一数据源：`duration-ledger-sqlite/v1`

本文冻结远程 CI 的唯一生产架构。后续实现、重构、子代理任务和代码审查必须以本契约为边界；历史计划、旧迁移说明和已删除实现不得覆盖本文。

## 1. 不可变设计目标

1. 基准运行环境由一代已接受的阿里云 ECI ImageCache 提供。其不可变镜像必须包含：
   - 单一 `internal/devtools/godistribution/go-distribution.lock` 中锁定的 Go 1.26.5；Linux/amd64 远程构建必须调用 `ValidateRemoteCIAsset`，三平台映射只能保留在该 TSV，且不得新增下载或网络 fallback；
   - Go module cache 与 Go build cache；
   - 前端锁定依赖、`node_modules`、Vite dependency optimizer cache 与 Playwright 浏览器；
   - Gate、gopls、sqlc、sqruff、ripgrep、Node、npm、Git、Make 等锁定工具。
2. 正常 CI 只读取已接受代，不同步构建或刷新基准。刷新在后台运行；候选代 Ready、内容验证和原子晋级完成前，所有 CI 继续使用旧代。
3. 多个用户、设备或 agent 同时触发刷新时，只允许一个 owner 执行。抢占、节流、恢复和晋级均由同一个 duration-ledger SQLite authority 裁决。
4. 每两小时最多开始一个新的刷新 attempt。活动租约过期后的接管属于同一个 attempt，不得额外生成一层。
5. 所有可分片的前端、后端、normal、e2e 和 race 工作负载按历史耗时动态规划并并发执行。不得设置 shard 数量上限或 coordinator 并发上限。
6. 单个可拆测试预计超过 100 秒时必须继续拆分或优化；不可再拆的原子 workload 可以超过目标，但必须在账本中明确记录和告警。shard 在 `Running` 满 100 秒时只实时发出一次“目标超限”告警，绝不得为此取消、中断、kill 或标记失败；完成后 `test_body` 或 `total` 超过 100 秒仍只写结构化告警，权威回执可以保持 PASS。
7. 校准/基准运行使用固定且被回执绑定的 CPU、内存规格；普通 CI 仍可按 workload 资源策略选择规格。
8. 每次运行必须分别记录物化、候选编译、启动、测试主体、总耗时、缓存命中/未命中及资源等待时间。
9. 正常 CI 的 Gate、normal、e2e、race、frontend 与 dependency checks 每次都必须全量执行并在同一 authority 回执中观察 PASS；cache 只能加速输入、物化或编译，绝不得跳过任何必跑检查。后台 refresh 只记录真实执行的 `gate_build`、`normal_compile`、`e2e_compile`、`race_compile`、`frontend_build` 与 `dependency` 操作，且必须明确 `test_body=not_applicable`，不得声称任何测试 PASS。

## 2. 唯一权威与职责

| 数据 | 唯一权威 | 禁止的替代权威 |
| --- | --- | --- |
| 已接受基准代 | duration-ledger SQLite 中的 `ci_remote_baseline_state` | JSON 状态文件、DataCache、OSS manifest、环境变量 |
| 刷新 owner/attempt/候选状态 | 同一 SQLite 中的 refresh lease/job 记录 | `.refresh.lock`、进程内 mutex、设备本地 JSON |
| 测试历史与规划样本 | 同一 duration-ledger SQLite | 另一份 ledger、静态 shard 数、agent 私有账本 |
| 运行时缓存选择 | 已接受状态中的 `ImageCacheId + ImageSnapshotId` | tag、自动匹配、registry tag、最近创建缓存 |
| 候选源码 | 精确 Git commit/tree/patch identity | 工作目录当前内容、未绑定临时目录 |
| 候选 Gate 编译身份 | exact tree 的真实 Go 传递编译闭包与工具链摘要 | 手工输入清单、文件时间、warm workspace binary |
| refresh delta identity、check receipt、timing、warning | 同一 duration-ledger SQLite | OSS 对象、日志、环境变量、独立数据库或本地文件 |

SQLite 状态中的 JSON 只是严格 schema 编码；它不是 JSON 文件真相源。OSS 只传输内容寻址的请求、候选源码和回执；它不是缓存或状态权威。

## 3. 唯一正常 CI 路径

```text
Git hook / remote run
  -> 读取并验证 SQLite accepted BaselineState
  -> 固定 accepted ImageSnapshotId
  -> 读取 SQLite 历史耗时并生成 LPT 分片
  -> 所有计划内 shard 无上限并发创建 ECI container group
  -> 在 exact candidate tree 内复核编译闭包并增量编译 Gate
  -> 每次全量执行 Gate、normal、e2e、race、frontend 与 dependency checks
  -> 严格回执、资源清理、耗时与缓存证据写回同一 SQLite
```

硬约束：

- `BaselineState.ImageCacheID` 只用于生命周期审计和删除；创建 ECI container group 必须传 `ImageCacheSnapshotID`。
- 主容器与 init 容器必须使用不可变 digest 镜像，且显式绑定同一个已接受 snapshot。
- 禁止 `AutoMatchImageCache` 参与正常 shard 选择；自动层复用只允许出现在创建下一代 ImageCache 时。
- 候选 Gate 必须在 shard 内从 exact candidate tree 增量编译；不得复用未绑定候选树的预编译二进制。
- cache hit 只能加速已证明等价的输入、物化或编译，不能省略候选物化、身份验证或任何 Gate/normal/e2e/race/frontend/dependency check。

## 4. 唯一后台刷新路径

```text
正常 CI 非阻塞触发
  -> SQLite 原子 TryClaim
  -> 唯一 detached refresher
  -> 从 accepted immutable image + accepted snapshot 构建 successor
  -> 发布 successor immutable image digest
  -> ECI CreateImageCache(AutoMatchImageCache=true, Flash=true)
  -> Describe/Wait Ready
  -> 校验 cache ID、snapshot ID、镜像 digest、候选身份
  -> SQLite lease-token + accepted-state 双 CAS
  -> 新代成为 accepted
  -> 延迟/可恢复地回收旧代
```

### 4.1 两小时节流与跨用户抢占

- 正常 CI 只能做非阻塞 `TryClaim`；busy 或未到两小时立即继续 CI，禁止等待刷新。
- normal run 的配置校验不得要求 refresh 发布仓库或 builder 镜像；这些字段缺失或非法时，仅 detached refresh 在任何 source/build/ECI 操作前 fail-fast 并写失败状态，已接受旧代继续服务。
- 新 attempt 的 `last_attempt_at` 与上一个新 attempt 至少相隔两小时。
- claim 必须绑定：随机 token、当前 accepted generation、accepted state SHA-256、目标 generation、获取时间和 lease expiry。
- 活动 lease 未过期时，任何其他 owner 都不得构建、创建 ImageCache 或晋级。
- lease 过期后允许新 owner 接管同一个 attempt。接管者必须先读取已记录 candidate ID，继续验证或清理，不能盲目创建第二个候选。
- 执行者必须 heartbeat。失去 lease、token 不匹配、accepted state 已变化或 heartbeat 失败时，必须阻断晋级。
- `.refresh.lock`、`flock` 或本地进程锁不得承担跨用户权威；相关旧实现必须删除。
- 所有设备必须指向同一个可提供 SQLite 正确锁语义的 authority。若 authority 是设备私有副本或不保证 SQLite 锁语义的文件系统，必须 fail-fast，不能宣称跨设备互斥。

### 4.2 增量刷新

- 已存在 accepted generation 时，禁止全量重建。
- refresh source transfer 必须是相对当前 accepted generation 及其 accepted source snapshot 的增量；禁止每次发送完整 closure、完整 workspace 或任何等价全量上下文。
- 缺失 accepted generation、accepted source snapshot 或 delta identity 时必须 fail-fast；不得静默退回全量传输、完整 closure 传输或 workspace fallback。
- 云端必须从 accepted snapshot 加该 delta 重建，并在晋级前验证完整目标 Git tree identity 与真实传递编译 closure；只验证 delta 自身不足以晋级。
- accepted source snapshot 的唯一 worker 路径由 `internal/devtools/cicontract` 定义：root 为 `/opt/super-dolphin-gate/source-snapshot/root`，manifest 为 `/opt/super-dolphin-gate/source-snapshot/manifest.json`；bind/core/embed/worker 不得复制、派生或覆盖这些路径。
- refresh worker 的非权威人类日志前缀唯一为 `REMOTE_CI_REFRESH_CHECK_PASS=`，由 `cicontract.RefreshCheckLogPrefix` 持有；bind/core/embed/worker 只消费该常量，禁止自定义或兼容前缀。它不得使用 normal required-check PASS 命名，且不能作为晋级权威；晋级必须依赖绑定 source tree、accepted snapshot、refresh build-check 计划、真实执行状态、耗时与回执摘要的结构化 observation。
- successor 必须以 accepted immutable image 为 parent，并显式使用 accepted snapshot；只刷新变化的源码、依赖和构建缓存层。
- 身份未变化时不创建新 ImageCache，不增加 generation；只记录本次检查结果。
- 缺失 accepted Ready ImageCache 时，仓库内自动刷新必须 fail-fast；不得偷偷进入首代全量 bootstrap。
- 云端由 accepted snapshot 加 delta 重建的 canonical build context 只包含构建所需闭包；本地只传 delta。禁止传输或复制整个 workspace、完整 closure、`node_modules`、本地 build cache 或其他设备私有目录。
- OSS 临时对象必须位于 `source_prefix/oci-builds/<job-id>/`，由 job identity 绑定并在完成后清理；不得另建 `baseline_prefix` 第二命名空间。

### 4.3 ImageCache 与镜像发布边界

- ECI ImageCache 是运行时缓存选择和加速权威。
- `CreateImageCache` 消费不可变 OCI 镜像引用；它不接收运行中容器文件系统，也不替代 successor 镜像内容发布。
- successor 的通用 OCI 发布目标只是 ImageCache 的内容输入，不得成为正常 CI 的第二执行路径或缓存选择权威。
- 禁止任何 ACR 专用代码、字段、鉴权、角色、registry access 或域名。输出仓库、builder worker image 和 accepted parent image必须拒绝阿里云 ACR host。
- builder worker 必须在 ECI 内运行；禁止本地 Docker daemon、Docker Desktop、宿主 BuildKit/buildx 或本地镜像导入。
- 若未配置可用的非 ACR 不可变 OCI 发布边界，刷新必须 fail-fast，不能退回 ACR、DataCache、OSS tar 或本地 Docker。

### 4.4 原子晋级与清理

允许的状态转换：

| From | To | 必要条件 |
| --- | --- | --- |
| `idle` | `claimed` | 两小时到期且 SQLite 原子 claim 成功 |
| `claimed` | `unchanged` | 输入身份与 accepted 完全一致；不创建新 cache、不增加 generation |
| `claimed` | `building` | token/lease/accepted identity 仍有效 |
| `building` | `cache_preparing` | successor immutable digest 已严格验证 |
| `cache_preparing` | `ready_validated` | ECI 返回唯一 Ready cache、snapshot 和预期镜像 |
| `ready_validated` | `promoted` | 同一事务验证 lease token 与旧 accepted state 后 CAS 成功 |
| `promoted` | `retiring` | 旧代进入持久化、可重试的延迟清理 |
| `cleanup_pending` | `retiring` | SQLite 原子重试认领成功，accepted successor generation/state SHA 仍匹配且 retiring ID 不是当前 accepted cache |
| `retiring` | `idle` | 唯一 cleanup token owner 成功删除 retiring ImageCache |
| `retiring` | `cleanup_pending` | 删除失败被持久化；accepted successor 保持已晋级 |
| 任意候选态 | `failed` | 记录失败并保留旧 accepted state |

- promotion 之前的失败必须删除或持久化待清理候选，且不得修改旧 accepted state。
- promotion 必须在一个 SQLite 事务中同时校验 refresh token/lease、旧 accepted generation/state digest，并写 successor 与 terminal job 状态。
- CAS loser 只能清理自己的候选，不能删除 accepted cache。
- 旧 cache 只能在 successor 已晋级且旧代不再可能被新 CI 选择后回收。
- 旧代删除失败必须记录为 cleanup pending 并重试；不得把已成功晋级的结果回滚或误报成“未晋级”。
- 进程在 CreateImageCache 后崩溃时，candidate name/ID 必须已写入 SQLite，以便接管者 Describe/Wait/Delete，禁止遗留无主资源。

## 5. 动态分片与并发

- shard 数由 workload 历史耗时和 100 秒目标自动计算，最大只受当前可分片原子 workload 数量限制。
- 禁止 `max_shards`、`max_concurrency`、batch size、semaphore、`errgroup.SetLimit` 或其他人为并发上限。
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

- `executed=true`；每个计划内 workload 都必须实际执行，不存在 `reused`、PASS 结果缓存或跳过测试的回执；
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
`cicontract` 是唯一 retention 常量 owner。duration samples、catalog observations、runs 与 calibration checkpoints 四个历史根都必须绑定已验证的 accepted baseline generation；每个根写事务必须先读取同一 SQLite authority 的 accepted singleton，拒绝零值、无 authority、无效 authority 与晚于当前 accepted generation 的伪造未来代。refresh 只能逐代晋级，因此已启动旧代运行仍可在完成时写入。四个根的 distinct generation 并集按数值确定全库唯一保留集合，任何根都不得保留该集合之外的数据。每一代可包含任意数量的 workload、sample、shard、timing、receipt 或 scenario，禁止用固定行数限制代码和测试增长。SQLite 只保留最新 3 个有数据的 generation；第四个有数据代首次成功写入时，必须在同一事务内淘汰最老一代全部历史根及其 cascade 子数据。

唯一 compactDurationLedgerAuthority 只能在既有成功写事务的 commit 前同步调用，禁止 timer、goroutine、后台 GC 或第二入口。generation 按数值排序，不能用行数、时间戳或插入顺序冒充；无法证明 generation 的旧行必须 fail-fast 或经显式迁移，不能默认绑定当前代。删除旧 run 依靠 FK cascade 同步删除 requester、shard/workload、execution、timing、warning、delta 与 receipt；删除旧 checkpoint 同步删除任意数量 scenario；catalog 内容只有在不再被保留代 observation/run 引用时才能删除。accepted baseline 与 refresh lease 是当前状态 singleton，duration meta/calibration、query meta 和源码枚举的 schema migration registry 不是历史代，不参与淘汰。
<!-- cicontract:retention:end -->

## 8. 明确禁止的旧实现

以下生产代码、配置和降级路径不得存在或重新接线：

- DataCache、Anchor、Delta、direct cache、zstd cache bundle；
- 本地 Docker/Docker Desktop/buildx、localci scheduler 或本地镜像 bootstrap；
- ACR 专用 auth、RAM role token、registry access、repository 配置或域名；
- JSON baseline/ledger truth source、双写、宽松 schema 兼容或迁移回读；
- candidate CLI artifact builder、candidate test-binary builder、第二 executor；
- workload PASS 结果缓存、`reused` 返回或任何以历史通过结果跳过本次测试的路径；
- spot 到按量计费、remote 到 local、cache miss 到无缓存执行之外的隐式 fallback；
- 固定 shard 数、五分片限制、并发上限；
- accepted 缺失时自动全量重建；
- 两套 refresh command、两套 promotion writer 或绕过 SQLite lease 的人工刷新路径。

严格 request/receipt JSON、SQLite 中带摘要的 state JSON 以及阿里云 CLI JSON 响应属于协议编码，不属于被禁止的 JSON truth source。

## 9. 变更与验收门禁

任何触碰本文范围的改动必须同时满足：

1. LSP 定位、定义/悬停、引用/调用链、精读与 diagnostics 全部闭合；所有 severity 均处理。
2. 字段从生产者到消费者、SQLite schema、严格 JSON、回执和字段守卫同步更新。
3. 单元测试至少覆盖：并发 claim 唯一性、两小时节流、过期接管、stale token、候选崩溃恢复、Ready 校验、CAS loser、旧代保留、cleanup pending。
4. 架构守卫静态拒绝第 8 节列出的旧生产路径和并发上限。
5. 前后端 lint/test/build、Go 定向测试、archtest、code guard 与 `git diff --check` 通过。
6. 远程验收必须绑定同一个候选 tree、accepted ImageCache generation/snapshot、固定资源证据和完整耗时账本。
7. 非 authoritative 候选验证只能标记 `NOT_VERIFIED`；不能用本地 PASS、缓存命中或资源刷新完成替代远程 CI 速度验收。
8. 目标是正常前后端 CI 在可比较的 warm accepted generation 上 100 秒内完成；超过目标必须以账本定位并继续优化。

修改本契约必须在同一变更中更新相关架构守卫和测试。任何临时兼容、降级或第二路径都必须直接拒绝，不允许以 TODO 保留在生产入口。

## 10. 文档与代码的 1:1 映射

下面的标记块由 `cicontract.CanonicalMarkdown` 定义，并由架构测试逐字比对。正文负责解释设计，代码规则 ID 负责让生产校验、SQLite 状态机、回执守卫和静态守卫共享同一个 owner；任一侧单独修改都会使门禁失败。

<!-- cicontract:begin -->
| ID | 章节 | 代码约束 | 执行层 |
| --- | --- | --- | --- |
| `1.1` | §1 | 基准镜像消费单一 Go 1.26.5 锁，并包含锁定工具、Go module/build cache 与前端依赖/构建 cache | `build closure + runtime manifest + archtest` |
| `1.2` | §1 | 正常 CI 只读 accepted generation 且不依赖 refresh 发布配置；后台刷新缺配置或执行期间仍使用旧代 | `runtime + SQLite` |
| `1.3` | §1 | 跨用户刷新只允许一个 SQLite lease owner | `SQLite transaction` |
| `1.4` | §1 | 每两小时最多开始一个新 refresh attempt，过期接管沿用同一 attempt | `SQLite lease` |
| `1.5` | §1 | 前后端 normal/e2e/race 按历史耗时动态分片且无仓库并发上限 | `planner + archtest` |
| `1.6` | §1 | 100 秒只触发一次目标超限告警；不得取消、中断、kill 或标失败 | `planner + non-terminating timing warning` |
| `1.7` | §1 | 校准运行使用固定且被回执绑定的 CPU 与内存规格 | `request + receipt + SQLite` |
| `1.8` | §1 | 运行以真实起止和 duration_ms 分别记录物化、编译、启动、测试、总耗时、缓存与等待；并发聚合使用精确 interval union | `receipt + SQLite timing ledger` |
| `1.9` | §1 | 正常 CI 每次全量执行 Gate、normal、e2e、race、frontend 与 dependency checks；refresh 只回执真实 build/cache-seed/dependency 操作且不得声称 tests PASS | `required-check catalogue + refresh receipt validation` |
| `2.1` | §2 | accepted baseline、refresh lease/attempt、duration history、run/shard receipt 与 calibration checkpoint 只读写同一 SQLite authority | `SQLite schema + store` |
| `2.2` | §2 | 运行时缓存只由 accepted ImageCache ID 与 snapshot ID 选择 | `state validation + ECI request` |
| `2.3` | §2 | 候选源码和 Gate 编译分别绑定 exact Git identity 与真实传递编译闭包 | `materializer + receipt` |
| `2.4` | §2 | 严格 JSON 仅作协议编码，OSS 仅作内容寻址传输，二者均非权威 | `strict decoders + archtest` |
| `2.5` | §2 | accepted、lease、delta identity、check receipts、timing 与 warnings 只写同一个 SQLite authority | `SQLite schema + receipt store` |
| `2.6` | §2 | source snapshot root、manifest 与 refresh-only 人类日志前缀只由 cicontract 定义，bind/core/embed/worker 只消费该 owner | `canonical constants + archtest` |
| `3.1` | §3 | 正常 CI 唯一路径为 accepted SQLite 到 LPT 到无上限并发 ECI shards | `runtime call graph + archtest` |
| `3.2` | §3 | 正常 shard 显式绑定 accepted snapshot，禁止 AutoMatch 选择 | `ECI request validation` |
| `3.3` | §3 | 候选 Gate 在 exact candidate tree 内增量编译，cache hit 不跳过身份验证 | `materializer + receipt` |
| `4.1` | §4 | 唯一后台链为 TryClaim、successor、CreateImageCache、Ready、双 CAS 与延迟清理 | `runtime + SQLite state machine` |
| `4.2` | §4 | 刷新必须 heartbeat；失去 token、lease 或 accepted identity 时禁止晋级 | `SQLite CAS` |
| `4.3` | §4 | 已有 accepted generation 时只允许增量刷新，缺失 accepted 时禁止自动全量 bootstrap | `refresh validation` |
| `4.4` | §4 | ImageCache 是运行缓存权威，非 ACR OCI digest 仅是 CreateImageCache 内容输入 | `builder protocol + config validation` |
| `4.5` | §4 | 晋级前失败保留旧代；晋级后旧代清理失败进入 cleanup pending | `SQLite state machine` |
| `4.6` | §4 | 身份未变化时不创建 ImageCache、不增加 generation，只把检查结果记为 unchanged | `SQLite state machine` |
| `4.7` | §4 | refresh source 只传相对 accepted generation/source snapshot 的 delta；缺 snapshot 必须 fail-fast | `transfer-mode validation + archtest` |
| `4.8` | §4 | 云端必须从 accepted snapshot 加 delta 重建并验证完整目标 Git tree 与编译 closure | `rebuild receipt validation` |
| `4.9` | §4 | 检查 marker 只用于人类日志；晋级必须验证绑定 tree、snapshot、plan、执行、耗时与摘要的结构化 observation | `builder receipt validation + archtest` |
| `5.1` | §5 | shard 数只受可分片原子 workload 数量限制 | `LPT planner + archtest` |
| `5.2` | §5 | 云配额与 API 限流必须显式失败，不得静默降并发或转本地 | `runtime + archtest` |
| `6.1` | §6 | 固定校准规格贯穿 RunInput、ShardRequest、receipt、SQLite 与 checkpoint identity | `field guard + store` |
| `6.2` | §6 | 校准规格漂移或缺失必须 fail-fast，固定规格不限制 shard 并发 | `validation + archtest` |
| `7.1` | §7 | shard 与 workload 账本显式表达六个耗时阶段及其作用域 | `receipt + ledger renderer` |
| `7.2` | §7 | 账本证明 workload 实际 executed，并记录仅用于加速的 Go cache 与前端 seed/Vite cache 证据 | `receipt + ledger renderer` |
| `7.3` | §7 | 不适用阶段写 not_applicable，缺失应有观测拒绝 authoritative receipt | `receipt validation` |
| `7.4` | §7 | 100 秒告警固定 warn_and_continue；不得 cancel、kill 或 fail shard | `warning-action validation + archtest` |
| `8.1` | §8 | DataCache、旧 bundle、本地 Docker、ACR、JSON truth、第二 executor 与隐式 fallback 禁止存在 | `deletion + archtest` |
| `8.2` | §8 | 固定 shard 数、并发上限、自动全量重建与第二 refresh writer 禁止存在 | `deletion + archtest` |
| `9.1` | §9 | 变更必须闭合 LSP、字段链、状态矩阵、守卫和变更面测试 | `repository gates` |
| `9.2` | §9 | 远程验收绑定同一 candidate tree、generation、snapshot、资源与完整账本 | `authoritative receipt` |
| `9.3` | §9 | 非 authoritative 结果保持 NOT_VERIFIED，warm CI 超过 100 秒继续优化 | `remote acceptance` |
| `7.5` | §7 | 四个 SQLite 历史根写前证明 generation 已被接受，并共享全库唯一保留集合；每代行数不限，只保留最新 3 个有数据代，第四代写入与第一代全族淘汰同事务 | `accepted-generation proof + single write-transaction compactor + archtest` |
<!-- cicontract:end -->
