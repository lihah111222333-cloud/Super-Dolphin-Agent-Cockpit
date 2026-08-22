# 远程 CI 四节点容量执行单路径契约

状态：Accepted

适用范围：独立的 super-dolphin-remote-ci 二进制、internal/devtools/remoteportable、internal/devtools/heteroci、internal/devtools/heterociowner，以及 V3 和 WJ target adapter。本文是跨仓库共用的生产合同；目标仓库不复制执行器、权威库或调度策略。

契约身份：remote-ci-four-node/v1
请求协议：remote-ci-run-request/v1（外部 operation/locator binding）
目标清单协议：remote-ci-portable-manifest/v1（target tree repository-owned static manifest）
绑定摘要域：remote-ci-run-binding/v1（external locator + raw blob + actual engine facts）
校验结果协议：remote-ci-validation-result/v1
结果协议：remote-ci-result/v1（仅 execute terminal）
事件协议：remote-ci-event/v1
远程权威：remote-ci-postgresql-primary/v1
唯一执行池：四台节点，Ubuntu-01、Ubuntu-02、Windows-01、Windows-02
协调端：macOS 只编排、提交请求、读取状态和展示结果，不执行任何 workload。

本文冻结远程 CI 的唯一生产边界。缺失字段、身份漂移、容量不足、日志不完整和清理不确定均必须立即失败；不得用默认值、隐式排队、隐式重试或备用执行路径掩盖错误。

<!-- remote-ci-four-node-contract:begin -->

## 1. 独立二进制与跨仓库身份

super-dolphin-remote-ci 是独立构建、独立发布、独立运行的二进制。V3 与 WJ 只提供 target 描述和 workload，不编译、嵌入或替代该二进制。

每次请求必须显式绑定：

- engine：composition 从独立 binary/runtime 计算的二进制 SHA-256、协议 ABI 摘要、工具链摘要和能力；协议 ABI 摘要必须由单一 composite owner 绑定 portable、runtime、probe/planner、node batch、workflow binding、event/receipt 的 schema 常量与 reflect wire descriptors，任一字段或 schema 漂移都必须改变摘要；
- target：repository_id、object_format、exact commit_sha、exact tree_sha；
- workload：manifest content/raw blob 摘要、profile、capacity policy 摘要和 operation_id；
- authority：本合同的 contract_id 与 PostgreSQL authority identity。

engine identity 与 target identity 属于不同域。相同 engine 二进制可以服务 V3 和 WJ；target 仓库、commit、tree、workload 或 manifest ABI 改变时，必须产生新的 target binding digest。不得从 cwd、HEAD、branch、默认仓库、宿主工作树或可执行文件位置推断任一 identity，也不得要求 target 仓库含有某个 Gate 路径。

## 2. Portable manifest

唯一 repository-owned workload/policy input 是 target tree 内的 portable manifest；external run request 只提供 operation、repository locator、exact commit/tree/path/raw blob digest，不复制 workload 内容或 engine facts。manifest 使用 canonical JSON，最大 1 MiB；decoder 和 encoder 共同拒绝 unknown、duplicate、null、missing、trailing、非 canonical bytes、过期 schema、零值 digest、无效 Git object、绝对宿主路径、符号链接路径和未绑定的临时目录。

manifest 必须声明：

- schema_version、contract_id；operation_id 属于外部 request binding，不能写入 repository-owned manifest；
- engine static policy：required_protocol_abi_digest、required_toolchain_digest 和 required engine capabilities；actual binary/protocol/toolchain facts 由 composition 从正在运行的独立 binary 与 runtime 计算，不能由 manifest 或 descriptor 冒充；
- target repository_id、object_format 和 repo-relative worktree mount；exact commit_sha、tree_sha、manifest_path 与 manifest_blob_sha256 只由外部 binding 显式携带，不能写入 target tree manifest；
- workload 的稳定 unit_id、命令摘要、target platform、runtime capability、CPU/内存需求、历史估时和 execution digest；validate 必须从 bound exact tree 派生完整 workload catalog，按 canonical wire fields 排序并输出 catalog_digest、workload_count、linux_workload_count、windows_workload_count proof；
- Go catalog 的 modules 与 excluded_nested_modules 必须绑定 exact tree：composition 枚举全部 go.mod，逐项核对声明 module path；excluded_nested_modules 必须排序唯一、对应声明模块下未声明的 nested go.mod。未声明且未排除的 nested go.mod、stale/重复/包含 declared module 的排除项均 fail-fast，生成 package 不得穿透排除 subtree；
- profile、capacity_policy_digest、required node capabilities 和日志策略。

repository-owned manifest 不携带自身 raw blob SHA-256、operation_id、actual engine identity、commit_sha/tree_sha 或 manifest_path，避免不可实现的自引用和重复事实；外部 binding 必须携带 operation_id、repo-relative manifest_path 与 manifest_blob_sha256，并从 exact target commit/tree blob 原始字节重算后逐字段比对。Bind 只能接受 composition 注入且已计算的 actual engine facts；这些 facts 必须进入 domain-separated binding digest，并与静态 engine policy exact 比对。若实现增加 content digest，只能使用省略 digest 字段后的 domain-separated canonical digest，不能命名或冒充 raw blob SHA-256。宿主绝对 manifest、宿主绝对 worktree、环境变量发现、默认路径和第二 loader 均拒绝。manifest 不携带凭据，不携带 PostgreSQL authority generation，不携带可执行器地址。

## 3. PostgreSQL remote authority

远程 PostgreSQL read-write primary 是 operation、manifest binding、capacity snapshot、reservation、execution lease、start/finish、terminal result、event sequence 和 receipt 的唯一权威。owner 使用 heteroci_owner schema 与 heteroci_owner_writer role，receipt 使用 heteroci_receipt schema 与 heteroci_receipt_writer role；两域事务独立，不得双写、交叉提交或互相降级。

所有 mutation 只能进入已验证的 read-write primary，使用 SERIALIZABLE transaction、canonical owner scope 的 transaction advisory lock 和 operation_id + request_digest 的不可变 journal。synchronous_commit=remote_apply；standby、只读副本、本地副本、本地 SQLite、JSON 文件和内存状态都不能成为读写权威。连接失败、commit 结果不确定、journal 缺失或 digest 冲突必须返回稳定错误并阻断，不得以内存 plan 继续执行。workflow request、topology plan request 和 owner plan journal 必须逐字段保留并 exact readback `external_binding_digest`、`manifest_blob_sha256`、`runtime_policy_digest` 三项 strict `sha256:` 摘要；它们分别绑定 actual engine/external locator、manifest raw blob 和 runtime policy，不能折叠、改名或冒充 `manifest_digest`，缺失即拒绝。

PG 记录必须逐字段 exact readback。reservation、lease、request aggregate terminal result 和 event sequence 都必须带 manifest_digest、plan_digest、operation_id、executor_binding_digest、node_set_digest 与 authority generation；实际 node_id、executor_kind、shard/batch/unit digest 和逐节点终态只出现在按 node_id 排序的 shards 中。过期 owner、旧 epoch、重复 operation 或 stale digest 必须拒绝。事务提交后才发布对外状态；失败路径仍写 bounded error code 和清理事实，不写 raw credential、宿主绝对路径或底层错误树。

## 4. 固定四节点执行池

执行池恰好四台 live node：

| node_id | platform | 共享规则 |
| --- | --- | --- |
| Ubuntu-01 | ubuntu | 只读 object store；读取共享增量 cache publication layer；private worktree/overlay |
| Ubuntu-02 | ubuntu | 只读 object store；读取共享增量 cache publication layer；private worktree/overlay |
| Windows-01 | windows | 独立 repository、worktree、cache、overlay 和 temp root |
| Windows-02 | windows | 独立 repository、worktree、cache、overlay 和 temp root |

每个 node_id、repository_id、Git common-dir、worktree root、cache root、toolchain digest、probe identity 和 boot identity 都必须稳定且可验证。节点清单必须恰好包含两台 Ubuntu 和两台 Windows；重复、缺失、离线、能力声明与 live probe 不一致均 fail-fast。macOS 不得出现在执行节点清单。

Ubuntu 的只读 object store 按 repository_id 分 namespace；共享增量 cache publication layer 只保存经 SHA-256 验证的 immutable blob 和 path-to-digest manifest。节点只能在 private overlay 和 private worktree 中写入，发布必须在跨节点锁内完成 manifest、size、digest、generation 的 exact readback 后原子更新 ready pointer。节点不能共享可写 worktree、普通可写 cache、临时目录或未验证的 partial generation。

Windows 节点不共享 object store、repository、worktree、cache、manifest 或 temp root。即使执行同一个 target，也必须使用不同的物理根和不同的 cache generation。所有节点产生的结果标记为 LOCAL_NON_AUTHORITATIVE，只有 PostgreSQL remote authority 可以发布 remote receipt。

## 5. 通用容量分片与 CapacityBlocker

所有 workload 使用同一个 deterministic capacity sharding planner。planner 先按 workload 的 platform、runtime capability、CPU、memory、estimated cost 和 node live capacity 形成可审计的 shard plan，再在 PostgreSQL 事务内预留容量。不同平台可以有不同 capability predicate，但不能各自复制一套权威调度算法。

一次请求只允许一次 fresh snapshot、一次 reservation 和一次 executor binding。容量不足、节点失联、规格不匹配、reservation 冲突或 work unit 无法装箱时，必须返回 CapacityBlocker；CapacityBlocker 必须包含稳定 unit_id、target platform、required capability、reason code 和可重放的 plan/manifest digest，并且不得泄露 endpoint、凭据或宿主路径。

容量不足是终态错误：不得等待空闲容量，不得加入隐式队列，不得自动重试，不得切换备用节点集合，不得转发到外部执行池，不得双跑或投机复制。executor 一旦 start，代码错误、超时、OOM、工具失败或节点失联均属于本次执行失败；不得迁移、重试、fallback 或再次执行同一 operation_id。

## 6. Observable logs

stdout 对每个可解析调用只能输出一个 bounded canonical JSON object。validate 只输出 named/canonical remote-ci-validation-result/v1（schema_version、status=validated、contract_id、authority=LOCAL_NON_AUTHORITATIVE、execution_pool_contract、operation_id、target facts、manifest digests、catalog_digest 与精确 workload/platform counts、完整 actual engine facts 和 binding facts），validate 的 stderr 必须为空；未取得 PostgreSQL authority generation 前不得输出 remote-ci-event/v1。validate 不得冒充 terminal result 或声称已选择节点；只有 execute terminal 才输出 remote-ci-result/v1。terminal result 是一次 request 的唯一 aggregate，至少包含 status、operation_id、request_digest、manifest_digest、plan_digest、executor_binding_digest、node_set_digest、started_at、finished_at、exit_code、cleanup_state，以及按 node_id 排序且携带实际 node/executor/digest/终态/清理事实的 shards；CapacityBlocker 在未产生 plan 时 shards 必须为空并携带稳定 code。

stderr 只允许 JSONL remote-ci-event/v1。每行必须包含 operation_id、manifest_digest、plan_digest、executor_binding_digest、node_set_digest、严格递增 seq、RFC3339 时间、固定 stage、state、稳定 code 和按 node_id 排序的 shard 投影；request_validation、manifest_binding、capacity_snapshot 在 reservation 前使用空 shards，reservation 及后续阶段必须与 terminal aggregate 的实际 shard set exact 一致，CapacityBlocker 全链保持空 shards。允许阶段固定为 request_validation、manifest_binding、capacity_snapshot、reservation、materialize、execute、cleanup、terminal。禁止自由文本、凭据、token、绝对路径、raw command、raw provider response 和底层 error string。日志 channel 写失败必须阻断并在 PG 中保留 bounded failure code。

PG 只保存可重放的 result/event digest、结构化字段和有限 error code；人类日志可以脱离 PG 展示，但不得反向成为 authority。terminal result 只有在 reservation release、private overlay 清理、worktree 状态和 event sequence 全部得到证明后才能发布。

## 7. 唯一生命周期与验收

唯一生命周期如下：

1. macOS 编排端提交显式 portable manifest；
2. super-dolphin-remote-ci 严格解码并绑定 engine、target、manifest；
3. 从四节点取得一次 fresh live snapshot；
4. 在 PostgreSQL primary 内完成 deterministic plan、reservation 和唯一 executor binding；
5. 物化 target tree 到绑定节点的 private worktree/overlay；
6. 在绑定节点执行一次 workload，并写入 observable events；
7. 释放 reservation，验证 cleanup 和 terminal event，写入 PostgreSQL receipt；
8. 仅返回 canonical result。

任何步骤缺失或顺序漂移都必须 fail-fast。macOS 只负责步骤 1 和结果观察，不能作为步骤 5 或 6 的执行器。V3/WJ 只替换 target descriptor 和 manifest，不改变 lifecycle、authority、log schema 或 capacity policy。

## 8. 明确禁止的替代路径

生产实现不得引入外部供应商执行池、对象存储中转、可变镜像缓存、远程迁移、第二 PostgreSQL writer、第二 executor、本地数据库权威、JSON truth source、隐式 queue/retry/fallback、共享 raw token、共享可写 worktree、固定单平台 alias 或隐藏的 host discovery。四节点之外的执行请求必须返回 CapacityBlocker 或稳定 contract error。

合同、manifest、PG schema、日志协议和 arch/contract guard 必须在同一变更中同步；guard 必须对旧外部执行身份、隐式远程迁移/queue/retry/fallback 和第二 authority 保持 fail-fast。不得用保留旧入口、兼容字段、默认节点、静默吞错或放宽 decoder 消除失败。

<!-- remote-ci-four-node-contract:end -->

## 9. 变更与验证门禁

- 变更必须保留本文件路径，以便 AGENTS.md 和合同导航继续定位唯一入口。
- 变更必须通过 portable manifest strict round-trip、CapacityBlocker fail-first、PG authority identity/readback、四节点 topology、macOS non-executor、observable result/event 和 cross-repository V3/WJ tests。
- 任何 arch/contract diagnostics、未知字段、missing/stale registry、diff --check 或定向测试失败都视为阻断；不得报告为 partial pass。
- 远程 CI 独立二进制的真实执行证据必须绑定同一 engine digest、target tree、manifest digest、PG generation、node identity、reservation 和完整 result/event sequence。
