# 远程 CI ECI ImageCache 单路径契约

状态：Accepted
适用范围：`cmd/super-dolphin-gate`、`internal/devtools/remoteci`、`internal/devtools/gate`、`internal/devtools/alicloud/{eci,oss}`、`scripts/refresh_remote_ci_imagecache.sh`、Git hooks
目标平台：`linux/amd64`

代码契约唯一 owner：`internal/devtools/cicontract`
契约身份：`remote-ci-aliyun-eci-imagecache/v5`
正常执行路径：`sqlite-correctness-authority-live-verified-refresh-imagecache-aliyun-eci-shards/v3`
唯一执行与验收提供方：`aliyun-eci/v1`
首代供给路径：`normal-run-hook-configured-aliyun-eci-generation-one-strict-receipt-bootstrap/v1`
候选缓存刷新路径：`script-oss-handoff-aliyun-eci-offline-imagecache-runtime/v2`
候选缓存刷新成功间隔：`24h`
唯一数据源：`duration-ledger-sqlite/v1`
accepted baseline JSON schema：`13`
非权威缓存材料 schema：`remote-ci-cache-material/v1`
非权威缓存材料 authority：`non_authoritative_material`

本文冻结远程 CI 的唯一生产架构。后续实现、重构、子代理任务和代码审查必须以本契约为边界；历史计划、旧迁移说明和已删除实现不得覆盖本文。

本契约不禁止非权威的宿主开发红绿测试。宿主入口唯一为 `scripts/test_with_guard.sh --host-test <light|medium>`：只接受一个精确 Go package、一个精确 top-level `Test` selector、`-count=1` 和有界 timeout；light 不接受 build tag 且 timeout 不超过 120 秒，medium 只可额外使用 `-tags=e2e` 且 timeout 不超过 600 秒。入口必须在执行前后对实际 CPU busy 百分比采样并读取可用内存证据：低负载（CPU busy 小于 50% 且可用内存不少于 25%）或中负载（CPU busy 小于 70% 且可用内存不少于 15%）均可运行 light/medium；CPU busy 达到 70%、可用内存低于 15%、无法取证或其他高负载必须 fail-fast 并转 ECI。不得使用 load average/逻辑 CPU 比值替代 CPU busy，因为 runnable 与 I/O wait 不等于实际 CPU 占用。宿主执行固定限制 light=`GOMAXPROCS=2,-p=1`、medium=`GOMAXPROCS=4,-p=2`，结果必须标记 `LOCAL_NON_AUTHORITATIVE`，不得写入 SQLite authority、PASS evidence、release receipt 或冒充 ECI。宿主编译缓存必须由 `scripts/local_go_cache.sh` 按本机 Go 二进制、`asm/cgo/compile/link` 工具闭包、目标平台、架构级别、CGO/C 工具链与编译策略生成身份；同一设备的 linked worktree 共享该身份的持久 `GOCACHE`，每次运行使用独立 `GOTMPDIR`。每台设备只读取自己的 Git common-dir 邻接缓存与私有状态 JSON；发现协议可跨设备复用，但对象物料、绝对路径和状态文件不得跨设备传输或进入 Git。race、benchmark、fuzz、整包/递归包、多 selector、超过 medium 上限、重型代码门禁和未知负载仍只允许 `super-dolphin-gate test` 经 ECI 执行；不得 remote-to-local fallback。

## 1. 不可变设计目标

1. 基准运行环境由一代已接受的阿里云 ECI ImageCache 提供。其不可变镜像必须包含：
   - 单一 `internal/devtools/godistribution/go-distribution.lock` 中锁定的 Go 1.26.5；Linux/amd64 远程构建必须调用 `ValidateRemoteCIAsset`，三平台映射只能保留在该 TSV，且不得新增下载或网络 fallback；
   - Go module cache 与 Go build cache；
   - 前端锁定依赖、`node_modules`、Vite dependency optimizer cache 与 Playwright 浏览器；
   - Gate、gopls、sqlc、sqruff、ripgrep、Node、npm、Git、Make 等锁定工具；
   - `/opt/super-dolphin-gate/source-baseline.git`，其中仅含 `RunnerBaseTree` 的完整 tree/blob closure 与一个确定性的无 parent baseline commit。
2. normal CI 仅在 accepted singleton 为空时消费配置中的 strict ECI generation-one receipt，在实时复核阿里云 ECI 事实后原子首写 correctness baseline 与账本元数据；singleton 非空后继续只读该 SQLite identity。`pre-push` 从 exact pushed tree 非阻塞启动唯一隔离 dispatcher；`scripts/refresh_remote_ci_imagecache.sh` 创建有限期非权威 ImageCache 加速物料且不读写 SQLite。normal run 必须严格读取 OSS current receipt、绑定 accepted OCI base，并实时 Describe 精确复核 region、Ready cache ID/name/image/snapshot 后，才把本次执行镜像和 snapshot 切换到刷新层；accepted generation、runner correctness identity、PASS 与历史权威均保持 SQLite 不变。
3. 空 accepted singleton 的唯一初始化入口是 normal run/hook 的配置回执 bootstrap；独立 `provision-generation-one` 命令不得存在。仓库只验证并原子写入该 receipt，绝不创建 ImageCache 或候选。
4. 多个 agent 可以并发触发 Git hook，多个 remote CI job 可以并发运行，且所有可分片的前端、后端、normal、e2e 和 race 工作负载按历史耗时动态规划并无上限并发执行。不得设置全局 hook 锁、active-job lock、semaphore、`errgroup.SetLimit`、`max_shards`、`max_concurrency`、admission cap 或共享原始 token；同一 worktree 的 Git `index.lock` 只是 Git index 一致性边界，绝不是 CI 并发上限。
5. 所有远程 CI 动作、镜像构建和 cache-prime 的唯一执行环境都是阿里云 ECI container group，并由代码契约身份 `aliyun-eci-only-no-github-runner/v1` 锁定。候选编译、测试、校准、首代内容检查、权威耗时观测、Go/前端缓存层生成和最终 OCI 镜像构建都必须在 ECI 内执行；BuildKit 若被使用，也只能作为 ECI 容器内的构建进程。GitHub-hosted 或 self-hosted Actions runner 不得承载远程 CI、编译、测试、cache-prime 或镜像构建；独立产品发布 workflow 不签发远程 CI 结论，不属于本契约执行面。Docker、Docker Desktop、Kubernetes、其他云及本地容器同样不得加工 CI 镜像物料或作为失败 fallback。ECI 镜像物料若携带 manifest，只允许使用 `schema_version=remote-ci-cache-material/v1`、`authority=non_authoritative_material` 与 `seed_steps`，禁止使用 `checks`、generation-one 或 receipt 命名；物料日志仍不得写入 `provision_checks`、不得生成 authoritative receipt。
6. 单个可拆测试预计超过 100 秒时必须继续拆分或优化；不可再拆的原子 workload 可以超过目标，但必须在账本中明确记录和告警。shard 在 `Running` 满 100 秒时只实时发出一次“目标超限”告警，绝不得为此取消、中断、kill 或标记失败；完成后 `test_body` 或 `total` 超过 100 秒仍只写结构化告警，权威回执可以保持 PASS。
7. 校准/基准运行使用固定且被回执绑定的 CPU、内存规格；普通 CI 仍可按 workload 资源策略选择规格。所有 CI、镜像物料构建与 cache-prime ECI container group 必须显式请求 `100 GiB` 原始临时盘，禁止依赖较小的服务默认值。

<!-- cicontract:scheduling:begin -->
所有 normal、校准与首代内容检查 ECI container group 必须使用配置中 2 到 10 个不同 vSwitch，并显式绑定每个 vSwitch 的 zone_id；集合必须覆盖至少两个可用区。CreateContainerGroup 必须把全部 ID 以阿里云原生逗号列表传入 VSwitchId，并固定 ScheduleStrategy=VSwitchRandom。禁止单 vSwitch、多个同区 vSwitch、单区失败重试、串行 fallback 或用并发上限掩盖 NoStock；调度库存等待必须作为 provider eci_wait 记录。
<!-- cicontract:scheduling:end -->
8. 每次运行必须分别记录物化、候选编译、启动、测试主体、总耗时、缓存命中/未命中及资源等待时间。
9. normal 与 calibration 均按 exact workload production-input fingerprint。只可复用同一唯一 Coordinator/SQLite authority 中当前 accepted generation 及其前两代（最近 3 代）、完整权威且仍新鲜的 PASS evidence；future 或更旧 evidence 必须 miss/fail-fast，不得复用；每个 identity 最多保留这 3 个 accepted generations。此处 fresh 仅指代际窗口、权威 passed/cleanup 状态、完整 identity 及 canonical receipt proof 全部有效；不使用 wall-clock TTL。所有相关输入不变且上述约束满足时必须直接 PASS，不重复执行。normal 与 calibration all-hit 均不创建 workload CI ECI、不执行测试，且不创建 workload shard、OSS/temp 或 calibration；部分命中只实际执行 miss。execution mode、校准/普通资源档位和资源策略只影响本次调度与耗时样本，不进入 PASS identity，因此 calibration->normal 在 correctness fingerprint 相同时必须直接复用；同一 candidate/tree/toolchain/policy 的权威 PASS 可跨 commit、push、release 场景和 agent 精确复用。每次调用仍保有独立 job/agent audit，hook、job 与 shard 无上限并发，同一个 miss 可以被并发重复执行。identity 必须绑定该 workload 的可观察源码、测试、脚本和命令、平台、真正影响结果的 policy/toolchain、runtime dependency seed，以及 exact execution semantic digest；candidate Gate/协调器源码和其编译工具链只作 run/receipt 审计，除非已经提取进该 workload 的执行语义闭包。exact Go test/benchmark fingerprint 还必须包含同包所有适用 `_test.go` 编译输入，以及目标运行时观察闭包。不得绑定 agent token、job、generation、snapshot、RunnerImage、RunnerIdentity、RunnerConfigDigest、GateBinary 或 BaselineManifest，也不得只使用 whole tree 或裸生产源码粗指纹。accepted ImageCache 的内容以外部 receipt 导入时的绑定事实为准；依赖、工具链、policy 或 workload Gate 语义变化必须使 strict PASS evidence 失效。agent token 仅是审计身份，跨 agent 可以共享等价 evidence。
	默认运行（无 `--force`）命中完整 PASS 时必须直接返回 PASS 且不创建任何 workload ECI；只有显式 `--force` 才可绕过 PASS 查询并执行所有 shardable workload。`force` 只写入 RunInput/RunResult、SQLite run/check receipt 与 calibration checkpoint 的审计身份，不进入 workload PASS identity；强制运行失败或取消不得删除、覆盖或污染既有 PASS evidence。

## 2. 唯一权威与职责

| 数据 | 唯一权威 | 禁止的替代权威 |
| --- | --- | --- |
| 已接受基准代 | duration-ledger SQLite 中的 `ci_remote_baseline_state`，并持久化 `execution_provider=aliyun-eci/v1` 与唯一 region | JSON 状态文件、DataCache、OSS manifest、环境变量 |
| 测试历史、规划样本与 strict workload PASS evidence | 同一 duration-ledger SQLite | 另一份 ledger、静态 shard 数、agent 私有账本、JSON/OSS/.pass/旧 `ci_workload_fingerprints` |
| 运行时缓存选择 | strict OSS current receipt 与实时 ECI Describe 完全一致的有限期 Ready `ImageCacheId + ImageSnapshotId + immutable image`，并绑定 accepted OCI base | tag、自动匹配、registry tag、未实时复核或已过期缓存 |
| 候选源码 | 精确 Git commit/tree/patch identity | 工作目录当前内容、未绑定临时目录 |
| 候选 Gate 编译身份 | exact tree 的真实 Go 传递编译闭包与工具链摘要 | 手工输入清单、文件时间、warm workspace binary |
| check receipt、timing、warning 与 reuse proof | 同一 duration-ledger SQLite | OSS 对象、日志、环境变量、独立数据库或本地文件 |

SQLite 状态中的 JSON 只是严格 schema 编码；它不是 JSON 文件真相源。OSS 只传输内容寻址请求、候选源码和刷新物料回执；回执只在实时 ECI readback 成功后选择加速层，绝不提供 correctness、PASS、accepted generation 或历史权威。

`ci_runs.authoritative=1` 后，该 JobID 的 core、gate/workload execution、result、shard、timing、warning、receipt 与 PASS evidence 投影全部不可变；任何 provisional 重写必须在 upsert 或删除 child rows 前 fail-fast。authority 提升前必须从同一 SQLite 回读 aggregate 与 workload execution，并对实际持久化的 `Status`、`ExitCode`、`ArgvDigest`、`LogDigest`、`ExecutionProfile`、起止时间和测试时序逐项精确匹配当前 ECI 结果；缺失、重复或漂移均不得晋级。

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
  -> 在 exact candidate tree 计算 workload production-input fingerprints
  -> 同一 SQLite authority 查询 fresh authoritative PASS；只投影 exact MISS，完整 PASS 直接结束且不创建 planner、compile group、资源、shard、OSS/temp 或 ECI
  -> 仅 MISS 读取 SQLite 历史耗时、生成 compile groups 与 LPT 分片、选择资源，并构造冻结 accepted bootstrap request 与完整 current ShardRequest；accepted bootstrap 的 gate_ids 只在 accepted encoder/identity 边界把 expansion-only `backend:nilness::go-package::<pkg>` 投影为 canonical `backend:nilness`，current request/manifest 始终保留精确 per-package IDs
  -> 所有 MISS shard 无上限并发创建 ECI container group
  -> accepted Gate 严格消费只含 canonical nilness parent 的冻结 bootstrap request 并发布临时 v1 manifest；在 exact candidate tree 内复核编译闭包并增量编译候选 Gate
  -> 候选 Gate 严格消费保留精确 per-package workload IDs 的完整 current request、交叉绑定 bootstrap 身份并原子替换为 v2 manifest；同一 shard worker 按该唯一 manifest 复用 test binary
  -> 实际执行所有 MISS，严格复用 fresh authoritative PASS hit
  -> 严格回执、资源清理、耗时与缓存证据写回同一 SQLite
```

硬约束：

- 正常 CI、首代内容检查、cache-prime 与 OCI 镜像构建只能通过阿里云 ECI API 创建 container group 执行。仓库不得提供通用 provider executor、Docker executor、Kubernetes executor、GitHub Actions executor/runner、其他云适配器或 remote-to-local fallback；GitHub 仅承载 Git 对象。只有 ECI 构建的不可变镜像被 Ready ImageCache 接受并由绑定该 snapshot 的 ECI container group 实测后，才形成可导入证据。
- 云控制面客户端只允许配置阿里云官方 `aliyun` CLI（可为其绝对路径）并只调用 ECI/OSS API；不得把该配置项改造成任意 provider executable、协议代理或第二云适配入口。该 CLI、凭据 profile 与运行主机属于受信基础设施边界，测试注入 runner 只能验证命令形状，不能形成权威远程证据。每次 CLI 调用必须同时有 context 超时与进程管道 WaitDelay；CLI 或其后代在取消后持有 stdout/stderr 不得让状态轮询、证据汇总或资源清理越过看门狗无界等待。
- Git hook invocation、remote CI job 与动态 shard 彼此独立并发；hook 不得以全局锁、active job 锁或共享 raw token 把不同 agent 串成单队列。一个 agent 的原始 token 只属于该 agent 的当前进程链，跨边界只传 digest。
- 同一 worktree 出现 Git `index.lock` 时，只按 Git 的一致性语义处理该 index 操作；不得把它扩展为 CI job、shard 或其他 worktree 的 admission/串行机制。
- 创建 ECI container group 必须传 accepted `ImageCacheSnapshotID`；不接受自动匹配、tag 或最近创建缓存作为选择依据。
- 主容器与 init 容器必须使用不可变 digest 镜像，且显式绑定同一个已接受 snapshot。
- 私有 GHCR 镜像的每个 normal shard 必须从当前 Gate 进程的 `SUPER_DOLPHIN_CI_GHCR_USERNAME` 与 `SUPER_DOLPHIN_CI_GHCR_TOKEN` 读取完整短期凭据，并映射到 ECI `ImageRegistryCredential`；server 固定为 `ghcr.io` 且必须与主/init 不可变镜像域名一致。缺失、空值、超长、域名错配必须在创建 group 前 fail-fast。原始用户名和 token 只能存在于当前进程内存和 ECI API 密文参数，不得进入 remote run config、SQLite、receipt、日志、tag、OSS、Git、命令投影或 ConfigFileVolume。
- 仓库可提供 hook 外的版本化 Git 启动器，从 GitHub CLI 的系统凭据存储取得上述 GHCR 短期凭据，并从目标仓库已验证的受信 Gate 签发当前 Git 进程链的 agent token。启动器只能通过环境 `exec git`，不得把原始凭据写入 Git 配置、文件、SQLite、日志或 argv；Git hook 与 Gate 仍不得自行签发、缓存或静默补齐凭据。目标仓库、GitHub CLI 身份、凭据对或 bootstrap 任一不可验证时必须 fail-fast。
- 禁止 `AutoMatchImageCache` 参与正常 shard 选择。
- 候选 Gate 必须在 shard 内从 exact candidate tree 增量编译；不得复用未绑定候选树的预编译二进制。
- 候选源码 transport 必须使用唯一标准 v2 `git-bundle-thin`：每次 CI 生成一个 tree 等于 SourceSpec parent/base tree、唯一 parent 等于 accepted baseline 的确定性 candidate-parent synthetic commit，再生成 tree 等于候选且唯一 parent 等于该 synthetic base 的确定性 transport commit，并以相对 baseline 的 bundle 传输；bundle header 必须恰好广告一个 baseline prerequisite 和唯一 `refs/source/materialized`。物化工作树必须同时发布只读 `refs/source/base` 指向 synthetic base commit，所有 diff/LSP changed-files 门禁只能比较该 base 到候选，禁止因 ref 缺失退化为整树扫描。禁止自包含 bundle、full fallback、raw whole repo、候选原始历史或第二 bundle/manifest 形态。
- UID 0 materializer 完成候选 checkout 后，唯一 bootstrap 必须以 `a+rX` 发布整个 `/workspace/source`（包括 `.git/objects/info/alternates`）供 UID 65532 worker 读取；worker 对该卷的挂载必须保持只读。禁止通过让 worker 使用 root、改成可写源码卷、复制第二份源码或吞掉权限错误绕过该边界。
- accepted materializer 与候选 coordinator 之间的 Gate CLI compile-closure 是跨代稳定握手：只允许根 Go module 的生产导入闭包、根 `go.mod`/`go.sum` 与 toolchain lock。Gate CLI 源码导入 repository-local `replace` module 必须 fail-fast；该类嵌套 module 的 `go.mod`/`go.sum` 只纳入 worker execution digest，禁止动态加入 Gate 握手导致已接受镜像中的 materializer 与候选算法漂移。
- source bundle `source.bundle` 与 strict manifest `source-manifest.json` 必须在同一 job 的完整内容寻址上传成功并完成摘要/字段验证后，才可按同一 SQLite authority 的 LPT 计划创建全部并发 shards；任何单个对象缺失、部分上传或验证失败都必须 fail-fast，不能先创建 shard。
 - PASS lookup 必须先于所有 MISS compile group、LPT、resource、shard、OSS/temp 与 ECI side effect；PASS workload 永不得进入 planner、compile group、资源选择、bootstrap request 或 ShardRequest，部分命中只投影 MISS。每个 MISS shard 必须在创建 ECI 前以 create-only 方式上传两个同 job、内容寻址且不可互换的对象：冻结 accepted 顶层 schema 14 + CompileGroup schema 1 bootstrap request，以及携带 canonical CompileGroup schema 2、batch plan 与 `shard_execution_manifest_digest` 的完整 current ShardRequest；任一上传失败都不得创建 ECI。accepted bootstrap `gate_ids` 只在 accepted encoder/identity 边界将 expansion-only `backend:nilness::go-package::<pkg>` 投影为 canonical `backend:nilness`；完整 current ShardRequest 和 worker manifest 必须保留精确 per-package IDs，coordinator/current worker 不得消费或执行该 projection。accepted ImageCache Gate 只可严格解码 bootstrap request，并在 gate-owned 固定路径原子发布临时 v1 manifest；候选 Gate 编译完成后必须由同一 init 容器的 `_remote-install-manifest` 严格下载完整 current request，校验两个对象的原始 SHA-256、同一 job 目录、稳定身份及旧 v1 manifest，再以 fsync + rename 原子替换固定路径为完整 v2 manifest。该 installer 只安装 manifest，不得执行测试或成为第二 executor；worker 仍是唯一 executor，argv 只能传固定 `--manifest-path /workspace/work/shard-execution-manifest.json` 与摘要，禁止长 `--gates` 列表。完整 current ShardRequest JSON（含 compile groups）与冻结 bootstrap request 各自总字节数上限均为 `1 MiB`，编码、上传前和相应 strict decoder 必须共用该上限并 fail-fast；禁止 unknown-field 宽松解码、schema 协商、兼容 fallback 或刷新 accepted ImageCache 来绕过滚动升级。
- Go/module/build cache、Node/npm、前端依赖/Vite 构建 cache、Playwright 浏览器及系统依赖必须直接读取 accepted ImageCache 的不可变镜像层。完整依赖树与构建缓存树的内容摘要、链接和写权限只能在不可变镜像构建及 ECI 内容验收时全量扫描；normal shard 只允许严格解码当前 manifest、校验固定根和 accepted 镜像只读挂载，禁止每分片、每 lane、每 workload 运行 `WalkDir`、整树摘要或逐文件权限复验。ECI request 的数据面必须且只能声明 `source-data`、`work-data`、`temp-data` 三个 EmptyDir；normal shard 只额外允许一个 init-only、只读、无凭据的 bootstrap ConfigFileVolume 绕过 ECI command/argument 字符限制，main container 不得挂载，且三个 EmptyDir 与控制投影的总卷数必须在创建前校验不超过 ECI 固定 20 卷上限。该投影不得承载源码、依赖、缓存、registry 凭据、签名 URL 或状态。禁止 FlexVolume/OSS bootstrap、`expanded-data`、DataCache、缓存卷、subPath 挂载、解压目录或逐分片复制展开。`node_modules` 与 `.vite` 必须直接链接镜像层，`.vite-temp` 才可位于分片私有工作目录；前端 `dist` 是构建结果，不得伪装成可复用 build cache 打入镜像。Go 增量编译必须只使用当前 accepted ImageCache 内唯一只读 build-cache seed 与分片私有的小型写层，不得创建 `cache-seeds` 多代目录、额外 seed 链或按 seed 代际分叉的观察字段，也不得把 seed 复制到外挂卷。Dockerfile 与 seed worker 属于必须审计的配方输入，不得进入可复用依赖内容 identity；仅配方/脚本变化而锁定依赖、工具链和模块清单不变时必须直接复用上一代 runtime 镜像，不得重跑 Go/npm/Playwright/tool seed。
- cache hit 只能加速已证明等价的输入、物化或编译；strict workload PASS reuse 只能从同一 SQLite authority 的当前 accepted generation 及其前两代（最近 3 代）完整权威 fresh evidence 取得；future 或更旧 evidence 必须 miss/fail-fast，不能复用。identity 必须覆盖该 workload 的可观察源码、测试、脚本、命令、平台、真正影响结果的 policy/toolchain、runtime dependency seed 和 exact execution semantic digest；普通/校准 execution mode、资源策略、终止预算以及协调器或 candidate Gate 的整体源码/工具链只作本次 run 审计或耗时规划，不得让等价 PASS 全局失效；exact Go test/benchmark fingerprint 必须额外包含同包所有适用 `_test.go` 编译输入和目标运行时观察闭包。cache/source-only baseline refresh 不使其失效；依赖、工具链、policy 或 workload Gate 语义变化必须使其失效。它不能省略候选物化或身份验证，不能以 whole-tree/裸生产源码粗指纹、agent token/job/generation/snapshot、RunnerImage/RunnerIdentity/RunnerConfigDigest/GateBinary/BaselineManifest、JSON/OSS/.pass 或旧 `ci_workload_fingerprints` 冒充等价。每个 reused check 必须含 canonical reuse proof SHA-256；non-reused check 禁止携带该字段；有效 PASS 必须 `passed && (executed || reused)`，同一 run 可混合 executed miss 与 reused hit。

- worker 实际消费的 `SUPER_DOLPHIN_REMOTE_EXECUTION_TIMEOUT` 只进入本次 run 审计和终止预算观测，不进入 strict workload PASS identity；agent token、job、source key 与 shard identity 仅用于审计或传输，不能阻断跨 agent 等价 PASS 复用。
## 4. normal run/hook 首代严格回执 bootstrap 边界

首代 ImageCache 的创建、发布、Ready 等待、运营权限和生命周期完全在仓库边界之外。外部 operator 只能通过阿里云 ECI container group 完成镜像构建、cache-prime 与这些云侧动作，并把无密钥 strict receipt 放入 remote run config 的 `generation_one_provision` 协议字段。唯一例外是由 `pre-push` 非阻塞调度的 `scripts/refresh_remote_ci_imagecache.sh`：它可以在本地把精确 Git 归档与 Go module download cache 闭包内容寻址上传 OSS，再启动绑定 accepted 或最近有限期候选 snapshot 的 ECI builder；源码编译、OCI 缓存层生成、临时 VPC registry 与 `CreateImageCache` 都必须在阿里云 ECI/ECI API 边界内完成。该脚本只输出无密钥、非权威、有限保留期的候选回执，不得写 SQLite、选择或晋级 accepted、清理 accepted cache，也不得使用 GitHub runner、本地 Docker、其他云、BuildKit publisher、`output_repository`、候选 reservation 或 CAS promotion。外部 operator 创建或更新需要从公网私有 registry 拉层的 ImageCache 时，必须显式给临时 ECI 绑定可用 EIP（或已验证 NAT）和该 registry 的短期凭据；凭据只能存在于 operator 当前进程与 ECI API 密文参数中，不得写入仓库、投影、remote config、receipt、SQLite 或日志。ImageCache 进入 Ready 或 Failed 后必须确认 EIP 已解绑，所有占用清理完成后再释放 EIP。刷新脚本使用 OSS 内网与临时 VPC registry，不依赖 vSwitch 访问国际网络。

### 4.1 外部 operator 回执内容

外部 operator 交给仓库的 generation-one strict receipt 必须明确绑定 execution provider=`aliyun-eci/v1`、region、每项检查唯一且真实的 ECI container group ID 与 container name、generation=1、canonical state SHA-256、唯一 Ready `ImageCacheId + ImageSnapshotId`、不可变镜像 identity、精确 source tree、Go/toolchain、policy、runtime dependency seed 与固定 CPU/内存规格。六项内容检查的每个 observation 还必须显式携带当前 normal resource policy 中的 `resource_class_id`、`resource_cpu`、`resource_memory_gib`，三者必须一致且只能是 2C/4GiB、4C/8GiB 或 8C/16GiB；这些 per-check 规格不等同于 §6 独立 calibration 4C/8GiB。`gate_build`、`normal_compile`、`e2e_compile`、`race_compile`、`frontend_build`、`dependency` 六项内容检查必须分别由绑定该 Ready snapshot 的阿里云 ECI container group 实际执行并通过，同时记录每项严格大于零的真实起止、`duration_ms`、candidate compile 观测、回执摘要和 `test_body=not_applicable`。每个 group 必须以规范 ECI tags 绑定 provider、ImageCache ID、snapshot、source tree、check 与 plan digest；同一个 group 不得复用于两项检查。

首写前仓库必须通过配置指定 region 的阿里云 ECI API 实时 `DescribeImageCaches` 与 `DescribeContainerGroups`，精确核对 cache region/Ready/snapshot/image 集合，以及每个 group 的 region、唯一 ID、`Succeeded`、实际 CPU/内存、主容器名称、不可变 runtime image、`Terminated`、exit code 0、规范 tags 和容纳 observation 的真实运行时间区间。实际 CPU/内存必须逐组精确等于 observation 的 per-check 规格；缺少或无法证明规格时必须 fail-fast。外部 operator 必须让这些终态 container groups 保持可查询，直到 normal run/hook 完成实时核验和 SQLite 原子首写；只能在首写成功后清理。缺少、提前清理、跨 region、重复、额外、非终态或任一字段漂移都必须 fail-fast。normal shard 遇到 ECI command/argument 字符限制时只允许使用 §3 冻结的单个只读控制投影。外部 operator 遇到 ECI command、argument 或 environment 字符限制时，只能使用只读 `ConfigFileVolume` 投影控制脚本、签名 URL、内容寻址 identity 或小型增量 bundle；投影不得包含依赖、构建缓存、`node_modules`、Go module/build cache、registry 凭据或任何第二状态源，也不得替代 normal shard 固定的三个 EmptyDir 数据面。只有 ECI 内的镜像构建进程可以产出 `remote-ci-cache-material/v1`、`authority=non_authoritative_material` 的候选镜像缓存物料；其 manifest 只能列 `seed_steps`，不得列 `checks`。构建日志不得转换、复制或冒充 ECI 内容检查 observations；Dockerfile、BuildKit stage 和 OCI artifact 中禁止生成 `provision_checks` 或 generation-one authoritative receipt。这些 ECI 内容检查仅证明外部首代的构建内容，绝不构成 normal CI 测试 PASS。

### 4.2 仓库唯一首写动作

normal run/hook 只验证配置 receipt 与上述阿里云 ECI 实时事实，并仅在 `ci_remote_baseline_state` 为空时，把 accepted singleton generation=1 与 `duration_ledger_meta` generation=1 在同一 SQLite 事务中原子 INSERT。相同 state digest 的并发首写 loser 可在严格重读后幂等收敛；不同 state digest、已有不同 singleton、generation 非 1、非 Ready snapshot、缺少任一绑定字段、摘要不匹配、区域漂移、元数据冲突或规格漂移必须 fail-fast 并整笔回滚。accepted state 必须继续保存 `execution_provider=aliyun-eci/v1` 与 region；后续 run/hook/calibrate 只读 accepted，不再消费或等待首代回执。

### 4.3 唯一候选刷新脚本与禁止边界

`pre-push` 只按第一个非删除 ref 的 exact pushed commit/tree 调度维护：它必须从该 pushed tree 提取 dispatcher 与刷新脚本，清除 agent token、registry 凭据与手工刷新覆盖环境，以 `nohup`、关闭 stdin、独立日志的方式启动后立即继续正常 pre-push gate，不得等待云端结果或因刷新进程存活阻塞 push。dispatcher 可以使用 Git common-dir 下的进程锁去重同时发生的维护刷新；该锁只保护候选维护，不得成为 hook、normal CI job 或 shard admission lock。

仅 `scripts/refresh_remote_ci_imagecache.sh` 可以把 exact committed tree 与本地解析的 Go module download cache 闭包上传到 OSS，由绑定 accepted 或最近有限期候选 snapshot 的 ECI 在 `GOPROXY=off` 下编译一次，并通过临时 VPC registry 生成有限期、非权威的新 ImageCache。脚本先严格读取固定 OSS scheduling receipt；上次成功刷新不超过 24 小时时必须静默跳过，严格超过 24 小时才可刷新。成功后必须同时写入内容寻址回执和固定 current pointer；v2 回执必须包含 region、cache name/ID/snapshot、同一 immutable image、accepted OCI base、生命周期与摘要。normal run 每次消费前都必须 strict decode、拒绝过期或漂移，并实时 Describe 完全一致的 Ready cache；随后显式用同一 cached image 作为主/init 容器且不编码临时 registry 凭据。回执不得作为 SQLite/PASS/accepted 权威。脚本成功后清理临时 ECI 和 registry，失败时清理候选；不得修改工作树 module 文件、依赖公网 registry 或宣称 CI PASS。

除此以外，仓库不得提供独立 `provision-generation-one` 或 refresh command、第二后台/定时 refresh executor、BuildKit publish、`output_repository`、其他 `CreateImageCache` writer、候选 reservation、lease/heartbeat/takeover、source delta 构建、CAS promotion 或 accepted/旧代 cleanup。normal run 的严格 read-only runtime selector 不是第二 executor，也不得创建、刷新或晋级缓存。

## 5. 动态分片与并发

- shard 数由 workload 历史耗时和 100 秒目标自动计算，最大只受当前可分片原子 workload 数量限制。
- Git hook invocations、remote CI jobs 与 job 内动态 shards 均无仓库并发上限；不同 agent 的 hook 不得彼此阻塞，job 不得因另一个 active job 等待，shard 不得排队等待人为 token。
- 禁止全局 hook lock、active-job lock、`max_shards`、`max_concurrency`、batch size、semaphore、`errgroup.SetLimit`、admission cap、共享 raw token 或其他人为并发上限。Git `index.lock` 仅保护同一 worktree 的 Git index 一致性，不得作为上述任一上限的实现。
- 云配额/API 限流是显式基础设施错误，不能通过静默降并发或转为本地执行来兜底。
- Go package/test、race package、Vitest 文件、Playwright spec 和其他可拆目标必须以可稳定寻址的原子 workload 入账。静态代码规模扫描 `./internal/archtest#TestCodeSizeGuard` 只在 normal gate 入账；canonical race catalog 必须排除该 exact selector，且不得因排除后回退为整个 `./internal/archtest` race 包，其他 archtest race selector 的覆盖、fingerprint 和 PASS reuse 仍须独立保留。仅供本地显式子进程调用的 Go helper 必须在源码上声明 `super-dolphin-ci: helper`，并在候选 Git tree 清单阶段排除；它不得生成 workload、PASS 指纹或进入 MISS 拆分器，执行端还必须拒绝旧/伪造 manifest 中的 helper selector。
- workload 大于 100 秒时，优先优化测试实现；无法优化且可拆时继续拆分。不可拆原因必须进入优化告警。
- normal shard 的 CPU 只能来自 `normal_classes` 中的三个精确资源档：2C/4GiB、4C/8GiB、8C/16GiB。规划器必须先读取同一 `WorkloadExecutionPlan` 内每个 workload 的账本估时并分档：`<= 5000ms` 使用 2C/4GiB，`5001..70000ms` 使用 4C/8GiB，`> 70000ms` 使用 8C/16GiB；所选 `resource_cpu` 与 `resource_memory_gib` 必须作为 plan identity 持久化并原样投影到 ECI selector，后续分片、compile group 或资源选择器禁止再按 duration 二次分类。再在每个档内部独立按 100 秒目标执行确定性 LPT，禁止把不同 CPU 档混入同一 shard，也禁止用接近 100 秒的 shard 聚合估时把整片误升到 8C。已有权威实测估值使 workload 首次升到新档、而新档尚无 exact sample 时，必须保留上一档的实测估值并使用新档规划首次执行；不得伪造新档样本、回退资源或因“无新档样本”形成不可达固定点。所有 workload kind 的 bootstrap 都固定使用 2C/4GiB；禁止额外内存档、自动内存抬升或用内存掩盖慢/大测试。观测或 OOM 在同档没有更大内存时必须 fail-fast，要求优化或拆分。
- 70 秒阈值来自当前 SQLite 权威账本测算：1243 个 workload 会生成 31 个 2C、68 个 4C、19 个 8C shard，共 118 shard/486 vCPU，最大估时 99972ms；30 秒阈值会产生 710 vCPU，不能作为当前策略。该测算只解释阈值，不冻结 workload 数、shard 数或总 vCPU；代码持续变化后只能通过重新测算、同步修改本文档与代码契约并改变 resource-policy digest 来升级阈值。
- normal 资源档、bootstrap、两个耗时阈值、headroom、downsize 样本数和独立校准规格必须进入 canonical resource-policy digest；该 digest 只用于 workload plan、duration sample、shard 资源与 run 审计，不进入 workload PASS environment identity。资源语义变化不得让 correctness fingerprint 等价的旧 PASS 全局失效；本次运行仍必须按当前策略重新规划资源。
- job 的计划 vCPU 是全部 shard 所选 vCPU 之和，仅用于账本和云配额诊断，不得转化为仓库并发上限。
- 同一 package-affinity compile group 只执行一次 `go test -c`；wire manifest 必须冻结每个 selector 的 `body_estimate_ms`、canonical `batch_plan_digest` 及 wave/batch 覆盖。archtest 每个 compile group 最多 64 个 selector、每个 ECI shard 仅一个 test-binary batch/process 并固定 `GOMEMLIMIT=3GiB`，423 个 selector 按有界组拆成约 7 个独立 CompileGroup/ECI shard 并无上限并发，允许跨 shard 增量编译但禁止跨 shard CAS；同 wave 普通 `test2json` 进程可并发，codexapp 的进程树/PID registry 独占 selector 各占串行 wave，且不得与普通 wave 重叠。worker supervisor 必须显式将 `TMPDIR` 绑定现有 `temp-data` 挂载根 `/tmp`，禁止依赖 runner image 默认环境；每个 batch 必须在该挂载根下创建唯一短 `0700` 运行根，并令 `TMPDIR`/`GOTMPDIR` 指向其子目录，结束时清理，禁止使用长 lane/batchRoot。batch 的 `HOME`、`XDG_*` 保持独立；同一 shard 的 candidate `GOCACHE` 增量层可写共享，accepted baseline seed 只读共享，每个 batch 的 metrics invocation 独立；成功 selector 的回执日志只保留最多 512 字节尾部，每个 compile group 只有首个失败 selector 保留完整 32 KiB 诊断窗口，其余 compile-group selector（包括其他 batch 的失败和 PASS）保留最多 512 字节尾部，失败 batch 也必须保留其他 batch 的结构化结果。compile timing history 只能按 `PackageTarget + SemanticKey + Platform + RunnerIdentityDigest + ToolchainDigest + ExecutionMode + ResourceClassID/CPU/Memory` 的完整 identity 查询；source tree、generation、shared input 和 artifact digest 只作证据或运行审计，不能跨 identity 混用。只有最近 3 个 accepted generation 中 authoritative、passed、cleanup complete、measured/raw 的真实 compile-group observation 可进入索引。规划必须在创建 `PlannedWorkload` 前按 package+semantic owner 做有界 fixed-point 资源选择：无 compile history 的 normal MISS 固定 small 2C/4GiB，已有权威样本升档时新档无样本则携带上一档实测值；所有 selector 共享同一 owner compile cost，compile 只计一次且不得写入 selector body。calibration 始终使用固定 4C/8GiB，不得按 compile duration 重分类。

## 6. 固定规格校准

- calibration/benchmark 必须使用独立于 `normal_classes` 的唯一固定 `calibration_resource=4C/8GiB`；calibration class ID 不得复用 normal medium。每个 ECI container group 都必须显式申请 `100 GiB` 原始临时盘。
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
| `candidate_compile` | exact candidate Gate CLI 编译 |
| `test_binary_compile` | 每个唯一 artifact key + 资源档 compile group 一次的 Go test 二进制编译，scope=`compile_group`；空账本也不得为满足 100 秒目标把同一 artifact key + 资源档拆到多个 ECI 重复编译；绑定 `same-eci-shard-worker-test-binary-compile-no-cross-shard-cas/v1`，不计入 workload startup/test_body/total |
| `startup` | worker 启动且不包含编译、测试主体 |
| `test_body` | 实际测试执行 |
| `total` | 端到端 shard/workload 耗时 |

同时必须按 workload 输出：

- 对每个计划内 workload，实际执行 miss 或以 `reused=true` 严格复用 hit；reused 必须有 canonical proof SHA-256，non-reused 禁止携带 proof。normal 与 calibration all-hit 均为 0 workload CI ECI，且不创建 workload shard、OSS/temp 或 calibration、不执行测试；部分命中只执行 miss；execution mode、资源档位和资源策略只在当前 run/shard receipt 与 duration sample 中记录，不改变等价 correctness fingerprint 的 PASS reuse。不得存在 JSON/OSS/.pass/旧 `ci_workload_fingerprints`、无完整权威 fresh evidence 的 PASS 结果缓存或跳过测试回执；
- 每次运行的 required-check 范围必须由该次已持久化 `WorkloadCatalog` 中实际计划的 workload 唯一映射并按 canonical 顺序校验；`local-fast`、`push` 等较小 profile 不得伪造或强行执行 release-only 的 e2e/race/dependency，release 目录仍必须精确覆盖 gate、normal、e2e、race、frontend、dependency 六类。SQLite 最终化、重载和 PASS evidence 摘要必须回读同一 `catalog_digest` 后执行相同范围校验，禁止把“回执自身有哪些项”当作完整性依据；
- Go private cache hit、baseline cache hit、miss、put 的状态与计数；
- 前端依赖 seed 和 Vite cache 是否验证、是否实际 materialize；
- 不适用阶段的明确 `not_applicable`，不得伪造为 0ms 或 cache miss。

<!-- cicontract:timing:begin -->
每条 measured observation 必须在同一 SQLite authority 保存真实 started_at、completed_at 与 duration_ms；统一账本分辨率是 1ms，实际为正但不足 1ms 的 worker startup/test_body 阶段必须在唯一计时生产者处量化为 1ms，禁止向下截断成表示缺失的 0ms；仅当量化后的两个串行阶段超出真实 workload total 时，生产者才可将 workload completed_at 向上规范化到恰好覆盖二者，禁止改写 started_at 或扩大到额外整数毫秒。并发 selector 的 test_body 必须以 top-level run→pause→cont→terminal 事件中的 cont 时间为起点，run→pause 排队等待不得计入 workload test_body；测试进程仍保持并发，shard/run wall time 继续保留真实起止与关键路径。raw 和 critical_path 的 duration_ms 必须严格等于按该分辨率规范化后的区间长度。interval_union 的 duration_ms 必须是全部原始 workload 子区间的精确并集：重叠只计一次、空隙不计入，禁止用最早开始到最晚结束的 envelope 冒充活跃耗时。

workload 的 startup、test_body 与 total 是 raw；shard 的 startup 与 test_body 是 workload raw 区间的 interval_union，shard/run total 是 critical_path。每个 compile group 另以 test_binary_compile raw observation 记录一次，scope=compile_group，包含 group/artifact identity、真实起止、Go cache hit/miss/put、artifact digest/size/status；该时间不得写入 workload startup/test_body/total，也不得与 candidate Gate CLI 的 candidate_compile 合并或重复计数。每个 calibration-resource shard 的 orchestration overhead 必须按 v2 accounted-interval-union 计算：从 shard total interval 中扣除 workload total、shard eci_wait/source_materialize/candidate_compile 以及 compile-group test_binary_compile 的全部 measured 区间精确并集，重叠只扣一次、间隙保留为真实编排开销；禁止用最早 workload 到最晚 workload 的 envelope 把上述已单独计量阶段重新算作 overhead。aggregate 使用 nearest-rank P95，并把 accounted duration/count、workload envelope、完整样本事实、accepted generation、snapshot 与 4C/8GiB 资源身份写入同一 SQLite authority；缺少任一必需 shard 阶段、区间越过 shard total、重复 workload/compile-group 身份或旧 v1 policy 必须 fail-fast。eci_wait 只能使用 ECI provider 返回的 CreationTime 到 materializer CurrentState.StartTime；shard total 终点必须取同一终态响应中 container-group SucceededTime/FailedTime 与唯一 worker CurrentState.FinishTime 的较晚者，两者都属于 provider lifecycle evidence。阿里云 ECI 已返回终态但 CreationTime、materializer CurrentState.StartTime、SucceededTime/FailedTime 或唯一 worker CurrentState.FinishTime 尚未同步时，只允许沿同一 Describe 路径按 PollInterval 对该分片有界重读最多 3 次；重读期间不得伪造时间、消费报告、移出 pending、取消兄弟分片或跳过清理，窗口耗尽后缺失任一项必须 fail-fast。禁止用 worker 日志、report 端点、本地请求或轮询时间替换 provider 终态；任一真实子阶段仍越过该 provider envelope 时保持 provisional NOT_VERIFIED。本地请求、轮询或日志时间不得写成权威耗时。所有 cache evidence 与阶段观测绑定，人类账本只能读取同一事务已提交的 SQLite observations。compile timing history 只能按 PackageTarget、SemanticKey、Platform、RunnerIdentityDigest、ToolchainDigest、ExecutionMode 与 ResourceClassID/CPU/Memory 完整 identity 查询；只允许最近三个 accepted generation 中 authoritative、passed、cleanup-complete、measured/raw 的真实 compile-group observation，source tree、shared input 与 artifact digest 不得跨 identity 混用。normal 无历史固定 2C/4GiB，owner fixed-point 发生在 PlannedWorkload 创建前，shared compile cost 每组只计一次且不写入 selector body；calibration 固定 4C/8GiB。
<!-- cicontract:timing:end -->

编译和测试时间不得重叠计数。缺少应有观测时必须拒绝 authoritative receipt。任何已经取得 job identity 的失败终态都必须在同一 SQLite authority 写入 non-authoritative provisional run，并原子迁移 live warning；只能保留真实完整的已测区间，未开始或缺失的阶段保持无 observation 且将缺失原因写入 bounded `error_text`，禁止以 0ms 或 `not_applicable` 伪造。

失败或取消的 overall run 永远不得升为 authoritative PASS；但在 `cleanup_complete=1` 且 accepted generation、snapshot、candidate/runner/toolchain、catalog、shard/report、execution profile、测试时序和输入/环境/执行 identity 均由同一 SQLite 投影精确绑定时，允许逐 workload 持久化并复用其中 `status=passed`、`exit_code=0` 且起止时间与时序完整的 executed evidence。任何 failed/cancelled、缺字段、分片/报告漂移或清理未完成的 workload 只能 MISS，并把跳过原因写入 bounded `error_text`；该逐 workload predicate 不得被 overall provisional 状态放宽。

100 秒是固定的 `warn_and_continue` 行为：只写入同一 SQLite authority 的结构化 warning，绝不得触发 cancel、kill、timeout failure 或 shard failure。其他独立的基础设施和安全超时不因此失效，但不得把 100 秒目标作为终止条件。

## 7.1 有界增长与淘汰

<!-- cicontract:retention:begin -->
`cicontract` 是唯一 retention 常量 owner。duration samples、shard overhead aggregates、逐分片 overhead samples、catalog observations、runs、strict workload PASS evidence 与 calibration checkpoints 七个历史根都必须绑定已验证的 accepted baseline generation；每个根写事务必须先读取同一 SQLite authority 的 accepted singleton，拒绝零值、无 authority、无效 authority 与晚于当前 accepted generation 的伪造未来代。已启动的旧 accepted generation 运行仍可在完成时写入。七个根的 distinct generation 并集按数值确定全库唯一保留集合，任何根都不得保留该集合之外的数据。每一代可包含任意数量的 workload、sample、shard、timing、receipt 或 scenario，禁止用固定行数限制代码和测试增长。SQLite 只保留最新 3 个有数据的 generation；第四个有数据代首次成功写入时，必须在同一事务内淘汰最老一代全部历史根及其 cascade 子数据。

100 秒结构化 timing warning 只能沿同一 SQLite authority 的互斥生命周期流转：ci_live_timing_warnings 只暂存仍在运行的 provider StartTime 事实，run finalizer 必须在同一事务精确吸收到 ci_run_timing_warnings 并删除对应 live 行；不得预写或伪造 ci_runs 失败终态，也不得让 live 与 final 行同时存在。live 表不是第八个历史根或第二真相源，不参与七根 generation 并集；唯一 compactor 必须按已校验 accepted singleton 的 current/current-2 数值窗口保留 active 行并清理崩溃残留。

唯一 compactDurationLedgerAuthority 只能在既有成功写事务的 commit 前同步调用，禁止 timer、goroutine、后台 GC 或第二入口。generation 按数值排序，不能用行数、时间戳或插入顺序冒充；无法证明 generation 的旧行必须 fail-fast，不能默认绑定当前代。删除旧 run 依靠 FK cascade 同步删除 requester、shard/workload、execution、timing、warning 与 receipt；删除旧 checkpoint 同步删除任意数量 scenario；catalog 内容只有在不再被保留代 observation/run 引用时才能删除。SQLite authority 必须使用 FULL auto-vacuum，让每次成功淘汰在同一提交边界自动归还空页；无生产读取者且重复保存完整 run payload 的 raw observation event 表、索引、触发器和旧 schema migration 入口均已退役，禁止恢复。accepted baseline 是当前状态 singleton，duration meta/calibration 与 query meta 不是历史代，不参与淘汰。
<!-- cicontract:retention:end -->

## 8. 明确禁止的旧实现

以下生产代码、配置和降级路径不得存在或重新接线：

- DataCache、Anchor、Delta、direct cache、zstd cache bundle；
- 本地 Docker/Docker Desktop/buildx、localci scheduler 或本地镜像 bootstrap；
- ECI 外的 Docker/BuildKit、GitHub Actions runner、Kubernetes、其他云或本地容器作为 CI executor、镜像物料加工器、首代内容检查执行器或 authoritative receipt 来源；
- 通用 provider abstraction、第二云 provider、非阿里云 ECI executor 或任何 non-ECI 执行 fallback；
- ACR 专用 auth、RAM role token、registry control-plane 或 repository 管理；ECI 返回的 opaque immutable image identity 不属于此类控制面；
- JSON baseline/ledger truth source、双写、宽松 schema 兼容或迁移回读；
- accepted ECI materializer 之外的 candidate CLI artifact builder、跨 shard test-binary CAS、第二 executor；同一 ECI shard worker 路径内的 compile-group test binary 编译是唯一允许的测试二进制编译入口；
- JSON/OSS/.pass/旧 `ci_workload_fingerprints` workload PASS 结果缓存、第二 authority，或任何未绑定同一 SQLite canonical proof 的 `reused` 返回/跳过路径；
- spot 到按量计费、remote 到 local、cache miss 到无缓存执行之外的隐式 fallback；
- 固定 shard 数、五分片限制、并发上限、全局 hook 锁、active-job lock、semaphore、admission cap 或共享 raw token；Git `index.lock` 除外，它只能作为同一 worktree 的 Git index 一致性边界，不能拿来限制 CI；
- accepted 缺失时自动全量重建；
- 除 `scripts/refresh_remote_ci_imagecache.sh` 的有限期非权威候选创建外，任何仓库内 successor refresh executor、refresh command、BuildKit publish、`output_repository`、`CreateImageCache` writer、candidate reservation、lease/CAS promotion 或 accepted/旧代 cleanup。

严格 request/receipt JSON、SQLite 中带摘要的 state JSON 以及阿里云 CLI JSON 响应属于协议编码，不属于被禁止的 JSON truth source。

## 9. 变更与验收门禁

任何触碰本文范围的改动必须同时满足：

1. LSP 定位、定义/悬停、引用/调用链、精读与 diagnostics 全部闭合；所有 severity 均处理。
2. 字段从生产者到消费者、SQLite schema、严格 JSON、回执和字段守卫同步更新。
3. 单元测试至少覆盖：外部 generation-one receipt 的字段完整性、六项 per-check normal resource class/CPU/memory 一致性、`aliyun-eci/v1` provider/region/container group/container/image/tag/exit/timing/snapshot/实际规格实时绑定、非 ECI 执行证据拒绝、generation=1/空 singleton 原子导入、重复或非 Ready 导入拒绝、独立 calibration 固定规格绑定与旧代保留，以及 baseline closure、确定性 transport commit、单 prerequisite v2 bundle 和 bundle/manifest 上传屏障。
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
| `1.2` | §1 | normal CI 仅在空 singleton 时原子首写 accepted correctness identity；pre-push 按 exact pushed tree 非阻塞刷新有限期 ImageCache，normal run 严格读取 OSS 回执并实时复核 Ready identity 后只替换执行镜像与 snapshot，绝不改写 SQLite | `runtime call graph + hook dispatch + strict receipt + live ECI verification + archtest` |
| `1.3` | §1 | hook、job 与动态 shards 无仓库并发上限；index.lock 仅保护 Git index | `cicontract concurrency policy + archtest` |
| `1.5` | §1 | 所有远程 CI 动作、镜像构建与 cache-prime 只能在阿里云 ECI 执行；禁止 GitHub Actions 或其他环境承载远程 CI | `provider identity + ECI request/receipt field guard + remote-CI workflow deletion + archtest` |
| `1.6` | §1 | 100 秒只触发一次目标超限告警；不得取消、中断、kill 或标失败 | `planner + non-terminating timing warning` |
| `1.7` | §1 | 校准运行使用独立于 normal 的固定 4C/8GiB 规格并被回执绑定；所有 ECI 执行使用 100 GiB 原始临时盘 | `request + receipt + SQLite + ECI CLI guard` |
| `1.8` | §1 | 运行以真实起止和 duration_ms 分别记录物化、编译、启动、测试、总耗时、缓存与等待；并发聚合使用精确 interval union | `receipt + SQLite timing ledger` |
| `1.9` | §1 | normal 与 calibration 默认均只复用同一 correctness-bound exact workload identity 的权威 PASS；all-hit 不创建 workload CI ECI、workload shard、workload OSS/temp 或 calibration，且不执行测试；部分命中只执行 miss；只有显式 --force 才绕过 PASS 查询并执行全部 shardable workload；execution mode、资源规格与 force 只绑定本次 run/shard/duration evidence，不阻断或污染等价 PASS | `required-check catalogue + strict reuse receipt validation + force audit + archtest` |
| `2.1` | §2 | accepted baseline、duration history、run/shard receipt 与 calibration checkpoint 只读写同一 SQLite authority | `SQLite schema + store` |
| `2.2` | §2 | correctness identity 只来自 accepted SQLite；运行时加速缓存只由 strict OSS current receipt 与实时 Describe 完全一致的 Ready ImageCache ID、name、image 和 snapshot 选择 | `SQLite state validation + strict receipt + live ECI validation + request field guard` |
| `2.3` | §2 | 候选源码和 Gate 编译分别绑定 exact Git identity 与根 module 跨代稳定传递编译闭包；Gate 禁止导入 repository-local replace module，嵌套 module metadata 只进 worker execution digest | `materializer + receipt + closure guard` |
| `2.4` | §2 | 严格 JSON 仅作协议编码，OSS refresh receipt 仅选择可实时复核的加速物料；二者均不得提供 PASS、accepted generation 或 correctness 权威 | `strict decoders + live ECI readback + archtest` |
| `2.5` | §2 | accepted state、check receipts、timing 与 warnings 只写同一个 SQLite authority | `SQLite schema + receipt store` |
| `2.6` | §2 | generation-one 只能由 normal CI 消费配置外部回执首写；唯一刷新脚本按 OSS strict receipt 的 24h 成功间隔创建有限期 ImageCache，normal run 可在 strict decode、accepted OCI base 绑定和实时 Ready 复核后消费该加速层，但不得晋级或改写 SQLite | `strict receipt boundary + accepted-base binding + live ECI guard + archtest` |
| `2.7` | §2 | 无 token 请求只返回 --agent-token=issue 与 env=issue 申请方式和实际 token 的 flag/env 使用方式；仅单一显式 issue 才签发 raw token，前两阶段均不执行 CI/ECI；git hook 无状态且只继承/验证实际 env token，跨 SQLite/OSS/ECI/log/tag/checkpoint/receipt 只传 sha256 digest | `agent-token contract + field guard + archtest` |
| `2.8` | §2 | 首次读取缺失 SQLite 时原子初始化 schema/index；normal run/hook 仅凭显式 strict ECI receipt 原子首写 baseline 与账本元数据，缺失或漂移仍 fail-fast | `SQLite initializer + generation-one bootstrap test` |
| `2.9` | §2 | authoritative JobID 的全部 SQLite 投影不可重写；晋级前必须精确回读 aggregate/workload execution 的状态、摘要、时序与执行画像 | `provisional write guard + strict aggregate/workload readback tests` |
| `2.10` | §2 | 每次阿里云 CLI 调用同时受 context 超时与进程管道 WaitDelay 约束；子孙进程持有输出管道不得使轮询、汇总或清理无界等待 | `ECI CLI process watchdog + inherited-pipe regression test` |
| `3.1` | §3 | 正常 CI 唯一路径为 accepted SQLite correctness identity 加实时验证 refresh runtime，再到 LPT 和无上限并发 ECI shards | `runtime call graph + archtest` |
| `3.2` | §3 | 正常 shard 显式绑定本次实时验证的 refresh snapshot 与同一 cached immutable image，禁止 AutoMatch 或 registry fallback | `ECI request validation` |
| `3.3` | §3 | 候选 Gate 在 exact candidate tree 内增量编译，测试二进制在同一 ECI shard worker 路径按 compile group 各编译一次；cache hit 不跳过身份验证 | `materializer + shard worker + receipt` |
| `3.4` | §3 | 工具链与依赖 correctness seed 绑定 accepted OCI base，Go/module 编译缓存可来自其有限期 refresh 镜像层；ECI request 数据面仍只有 source/work/temp 三个 EmptyDir 和单个 init-only 无凭据 bootstrap ConfigFileVolume，禁止 DataCache、缓存卷、逐 shard 展开或第二状态源 | `accepted-base binding + ECI request shape + image closure + archtest` |
| `3.5` | §3 | accepted ImageCache 在 /opt/super-dolphin-gate/source-baseline.git 提供 RunnerBaseTree 的 tree/blob closure 与确定性无 parent baseline commit；每次 CI 只上传标准 v2 git-bundle-thin，bundle 由 tree=SourceSpec parent/base tree 的 candidate-parent synthetic commit（唯一 parent=baseline）再连接 tree=候选的 transport commit，且 header 恰好一个 prerequisite、物化 refs/source/base 指向 synthetic base；严禁自包含/full fallback/raw whole repo，bundle 与 strict manifest 完整上传后才按 SQLite LPT 创建全部并发 shards | `source baseline + deterministic transport commit + strict bundle/manifest verifier + upload barrier + archtest` |
| `3.6` | §3 | accepted GHCR 镜像路径仍延迟加载固定 ghcr.io 短期凭据；实时验证的 refresh cache-only 路径要求主/init 为同一 cached immutable image 且不编码 registry 凭据，禁止访问已删除临时 registry 或回退拉取 | `explicit cache-only request + credential omission + dynamic field guard + redaction tests` |
| `3.7` | §3 | PASS lookup 先于一切规划和副作用，只有 MISS 构造并 create-only 上传同 job 内容寻址的冻结 accepted schema14/CompileGroup-v1 bootstrap request 与完整 current CompileGroup-v2 ShardRequest；accepted bootstrap 的 gate_ids 仅在 accepted encoder/identity 边界把 expansion-only backend:nilness::go-package::<pkg> 投影为 canonical backend:nilness，current request/manifest 保留精确 per-package IDs，禁止把投影扩散到 coordinator/current worker；accepted Gate 发布临时 v1 manifest，候选 Gate 编译后严格交叉校验并原子替换固定 v2 manifest，worker 保持唯一 executor，两个请求各限 1 MiB且禁止宽松解码、协商、fallback 或刷新镜像绕过升级 | `miss-only call-order guard + dual-request rolling manifest guard + request byte-limit tests` |
| `4.1` | §4 | 唯一写 accepted singleton 的路径是 normal run/hook 空库 bootstrap；首写前必须消费配置 strict receipt 并实时 Describe 阿里云 ECI cache/container groups，绑定 provider、region、唯一 group/container、Ready snapshot、immutable image、tags、零退出、真实时间、逐项 normal CPU/内存、generation=1、state SHA、源码/工具链/策略/seed 与固定规格；外部 operator 仅可用只读 ConfigFileVolume 投影控制文本和小型增量 bundle，禁止投影依赖、缓存、registry 凭据或第二状态源；公网私有 registry 的 ImageCache 临时 ECI 必须绑定 EIP 或已验证 NAT，并只在进程和 API 密文参数传短期凭据，终态后确认 EIP 解绑 | `receipt validation + live ECI API verification + SQLite INSERT + archtest` |
| `4.2` | §4 | 首写只允许空 singleton 原子 INSERT baseline 与账本元数据；同态并发幂等收敛，异态、非空、缺字段或非首代 receipt 必须 fail-fast | `strict bootstrap boundary` |
| `4.3` | §4 | pre-push 从 exact pushed tree 后台静默启动唯一 dispatcher；仅当 OSS strict 成功回执超过 24h 才在 ECI 内创建有限期非权威 ImageCache；normal run 每次 strict 读取并实时复核该物料后显式使用，维护锁只去重刷新 | `exact-tree hook dispatch + scheduling receipt + live runtime selection + maintenance lock + archtest` |
| `5.1` | §5 | shard 数只受可分片原子 workload 数量限制 | `LPT planner + archtest` |
| `5.2` | §5 | 云配额与 API 限流必须显式失败，不得静默降并发或转本地 | `runtime + archtest` |
| `5.3` | §5 | normal 按单 workload 账本估时固定选择 <=5s 2C/4GiB、5-70s 4C/8GiB、>70s 8C/16GiB，资源身份持久化进 plan 并原样投影到 ECI selector，禁止后续按 duration 二次分类；先分档再在档内 LPT；首次升档无新档 exact sample 时携带上一档权威实测估值规划新档，不伪造样本或回退资源；bootstrap 三类均固定 2C/4GiB，资源策略摘要只进入规划、duration sample 与 shard/run evidence，不进入 PASS identity；禁止额外内存档、混档污染、自动内存抬升、CPU 抬档、按 shard 聚合估时或复用旧策略 PASS，观测或 OOM 在同档没有更大内存必须 fail-fast | `tiered LPT + workload plan projection + resource selector + PASS identity tests` |
| `5.4` | §5 | CompileGroup schema v2 冻结 selector 估时、batch digest、wave/batch 覆盖与 warning；同一 package-affinity compile group 只执行一次 go test -c；archtest 每个 compile group 最多 64 个 selector、每个 ECI shard 仅一个 test-binary batch/process 并固定 GOMEMLIMIT=3GiB，423 个 selector 按有界组拆成约 7 个独立 CompileGroup/ECI shard 并无上限并发，允许跨 shard 增量编译但禁止跨 shard CAS；同 wave 普通 test2json 并发，codexapp exclusive selector 各占串行 wave；成功 selector 日志最多 512 字节、每个 compile group 首个失败日志保留完整 32KiB 窗口，其余 compile-group selector（包括其他 batch 的失败和 PASS）最多 512 字节；worker plan report framed output 与 strict decoder 累计均不得超过 1 MiB，后端 ExecutionProfile 全字段必须进入 report digest；源码 helper 声明在候选清单阶段排除，旧/伪造 manifest 的 helper/manual selector 由执行协议拒绝；worker supervisor 必须显式将 TMPDIR 绑定现有 temp-data 挂载根 /tmp，禁止依赖镜像默认环境；每个 batch 必须在该挂载根下创建唯一短 0700 运行根并令 TMPDIR/GOTMPDIR 指向其子目录，结束时清理，禁止使用长 lane/batchRoot；batch 的 HOME/XDG 独立、同 shard candidate cache 可写共享、accepted seed 只读共享、metrics 独立，planner warning 必须投影到 RunResult 与 SQLite warnings；compile timing history 只能按 PackageTarget、SemanticKey、Platform、RunnerIdentityDigest、ToolchainDigest、ExecutionMode 与 ResourceClassID/CPU/Memory 九维完整 identity 查询；只允许最近三个 accepted generation 中 authoritative、passed、cleanup-complete、measured/raw observation，source tree、shared input 与 artifact digest 不得跨 identity 混用；normal 无历史固定 small 2C/4GiB，owner fixed-point 必须在 PlannedWorkload 创建前完成，新档无样本时携带上一档实测 compile 值，shared compile cost 每组只计一次且不得写入 selector body；calibration 始终固定 4C/8GiB，不得按 compile duration 重分类 | `compile-group schema/batch/helper/warning/history arch guard + worker/planner/coordinator tests` |
| `5.5` | §5 | 除 release owner 外所有重门禁必须作为 canonical shardable workload，在 PASS lookup 后仅 MISS 进入历史耗时 LPT 与无上限 ECI 分片；最终串行 owner 只能对逐 workload 权威证据生成固定大小、版本化且防篡改的 proof root，不得重跑门禁或拼接无界子日志 | `workload catalog + miss-only planner + bounded owner proof + archtest` |
| `6.1` | §6 | 独立于 normal 的固定 4C/8GiB 校准规格贯穿 RunInput、ShardRequest、receipt、SQLite 与 checkpoint identity | `field guard + store` |
| `6.2` | §6 | 校准规格漂移或缺失必须 fail-fast，固定规格不限制 shard 并发 | `validation + archtest` |
| `7.1` | §7 | shard 与 workload 账本以统一 1ms 分辨率显式表达六个耗时阶段；compile group 另以 test_binary_compile 记录每组一次；实际为正的亚毫秒 worker 阶段记为 1ms，禁止降为缺失的 0ms | `worker timing producer + receipt + ledger renderer` |
| `7.2` | §7 | 账本证明 workload 实际 executed miss 或严格 reused hit，并记录 canonical reuse proof 与仅用于加速的 Go cache、前端 seed/Vite cache 证据 | `receipt + ledger renderer` |
| `7.3` | §7 | 不适用阶段写 not_applicable，缺失应有观测拒绝 authoritative receipt；orchestration overhead 使用 v2 accounted interval union，扣除 workload、ECI wait、源码物化、候选 Gate 编译和 test-binary 编译的 measured 区间且不重复计数 | `receipt validation + overhead schema/policy guard` |
| `7.4` | §7 | 100 秒告警固定 warn_and_continue；不得 cancel、kill 或 fail shard | `warning-action validation + archtest` |
| `8.1` | §8 | DataCache、旧 bundle、本地 Docker、ACR、JSON truth、GitHub remote-CI runner、通用/第二 provider executor、跨 shard CAS 与隐式 fallback 禁止存在；测试二进制编译只能留在既有 ECI shard worker 路径，镜像物料也必须在 ECI 构建 | `deletion + archtest` |
| `8.2` | §8 | 固定 shard 数、CI 并发上限、accepted 缺失自动重建及接入 SQLite 的 successor refresh executor 禁止存在；唯一 pre-push 后台候选刷新维护不得阻塞 push、等待刷新结果或成为第二 CI executor | `hook non-blocking boundary + script deletion + archtest` |
| `9.1` | §9 | 变更必须闭合 LSP、字段链、状态矩阵、守卫和变更面测试 | `repository gates` |
| `9.2` | §9 | 远程验收绑定同一 candidate tree、generation、snapshot、资源与完整账本 | `authoritative receipt` |
| `9.3` | §9 | 非 authoritative 结果保持 NOT_VERIFIED，warm CI 超过 100 秒继续优化 | `remote acceptance` |
| `7.5` | §7 | 七个 SQLite 历史根（含 shard overhead aggregate 与逐分片样本）写前证明 generation 已被接受，并共享全库唯一保留集合；每代行数不限，只保留最新 3 个有数据代，第四代写入与第一代全族淘汰同事务；SQLite FULL auto-vacuum 在提交时归还淘汰页，禁止无消费者的全量运行快照事件链 | `accepted-generation proof + single write-transaction compactor + FULL auto-vacuum + retired-object archtest` |
| `7.6` | §7 | 失败终态仍写 non-authoritative provisional run、迁移 live warning 且只保留真实已测区间；缺失阶段不伪造 0ms/not_applicable | `failed-run SQLite projection + receipt authority guard` |
| `7.7` | §7 | required-check 精确绑定当前持久化 workload catalog；较小 profile 不伪造 release-only 检查，release 仍覆盖完整六类 | `catalog-scoped observation + receipt + SQLite reload validation` |
| `7.8` | §7 | 阿里云 ECI 终态生命周期字段允许沿同一 Describe 路径按 PollInterval 每分片最多重读 3 次；不得伪造时间、提前消费报告、移出 pending、取消兄弟或跳过清理，窗口耗尽仍 fail-fast | `bounded terminal evidence reread + fanout drain tests + timing guard` |
<!-- cicontract:end -->
