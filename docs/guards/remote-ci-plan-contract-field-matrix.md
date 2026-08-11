# Remote CI 计划契约字段矩阵

本矩阵是 `remote-ci-eci-imagecache-contract.md` 的可复查守卫映射。它不增加协议版本，也不提供兼容解码；缺字段、旧版本、未知字段或身份越界都必须在当前边界 fail-fast。

| 契约材料 | 唯一 owner / producer | 直接消费者与持久化投影 | 身份边界 | 守卫与证据 |
| --- | --- | --- | --- | --- |
| `WorkloadExecutionPlan` schema 10、`AlgorithmID`、`ObjectiveDigest` | `internal/devtools/cicontract/contract.go`；gate 仅 alias/调用 owner | `gate/workload_model.go` 生成；`workload_planning.go` 的 `ValidateStored` 校验；`plan_digest` 进入 request、worker report、SQLite run 与 receipt | schema/algorithm/objective 属于计划内容；不直接进入 PASS identity | `cicontract.ValidateWorkloadPlanContract`；`TestRemoteCIPlanContractFieldChainGuard`；AST 与 reflection 字段集合必须相同 |
| `PlanningPolicyDigest` | `cicontract.WorkloadPlanningPolicyDigest` | 计划生成、stored-plan validation 与 `WorkloadExecutionPlan.digest()`；下游通过 `plan_digest` 绑定 | policy 变化必须使计划摘要变化并拒绝旧计划 | `WorkloadPlanningPolicyMaterial.Validate`、digest validation 与 plan field guard |
| `EstimationPolicyDigest` / `PlanningContext` | `cicontract.WorkloadEstimationPolicyMaterial`；gate 只投影 context | plan producer、stored validation、canonical plan digest；report/receipt/SQLite 通过 `plan_digest` 绑定 | 估时 policy 只属计划/执行账本，不进入 PASS identity | canonical material digest 校验；动态 context literal coverage；plan field guard |
| `WorkloadPackingEvidence` | gate producer；D-CPAP policy 常量由 cicontract 持有 | `validateWorkloadPackingEvidence`、`ValidateStored` 与 canonical plan digest；不作为独立 PASS 输入 | 每个 resource tier 的 solver/lower-bound/planned/gap 见证属于计划内容 | packing evidence validator、动态 JSON field coverage、plan digest consumer matrix |
| PASS identity domain | `cicontract.WorkloadPassIdentityDomain = pass-identity/v2` | `WorkloadPassIdentitySHA256`；SQLite `ci_workload_pass_evidence` identity columns | 仅 workload/execution/input/environment digest；禁止 tree/commit/worktree | `TestRemoteCIIdentityAndFingerprintDomainsHaveSingleOwners`；`TestRemoteCIPassIdentityTreeExclusionGuard` |
| input fingerprint domain | `cicontract.WorkloadInputFingerprintDomain = input-fingerprint/v2`，schema 4 | remoteci fingerprint producer 与 source replay candidate lookup | source tree 只用于重算 input fingerprint；不得以 tree 直接命中 PASS | domain owner guard、source replay validation、natural-MISS tests |
| PASS environment | `cicontract.WorkloadPassEnvironmentSchemaVersion = remote-workload-pass-environment/v10` | `remoteWorkloadEnvironmentDigestForGoFlags` 对完整结构 strict canonical JSON hash | schema、platform、toolchain、runtime seed、semantic env、Go flags 与 worker semantic digest 进入 environment digest | 动态 AST composite-literal 与 JSON-tag 差集；旧 v8/v9 拒绝 |
| accepted/current protocol versions | cicontract accepted `14/1/1`、current request `15`、worker manifest `2` | accepted bootstrap/current request/manifest strict decoders | unknown fields、旧版本、未来版本和协商/fallback 均拒绝；各请求 1 MiB | strict decoder AST guard、protocol version aliases、decoder red tests |
| DTO → mapper → SQLite | plan DTO 动态 JSON fields；run/identity DTO 显式 projection | request mapper 使用 `plan_digest`；`ci_runs` 保存 plan/catalog/tree audit；PASS 表保存 identity component digests 与 origin tree | tree/source commit 只属于 run audit、catalog observation、PASS origin proof，不属于 PASS lookup identity | `contracts_test.go` 通过 reflection 动态注册字段；SQLite schema projection tests；plan consumer matrix |
| retained reused proof / physical schema v16 | `cicontract.DurationLedgerSQLiteSchemaVersion`、`RetainedWorkloadPassProofsTable` 与 additive index registry | reused consumer result → `ci_retained_workload_pass_proofs`；consumer job/workload/identity、direct origin、strict execution JSON、canonical evidence SHA | proof 是 retained consumer 的 immutable 辅助投影，不是第八 retention root、第二 authority 或 fallback；仅 current+prev2 consumer 可消费 | Accepted schema block、cicontract/arch dynamic table/index registry、v15→v16 count/rollback/backfill migration tests、four-generation retention proof test |
| DTO → report → receipt | worker report 绑定 request `PlanDigest`；authority receipt 保存 `CandidateTreeSHA`，finalizer 将 receipt set 绑定到同一 run record | report decoder/validator、`CheckReceiptRecord` hash 与 SQLite check receipts | report/receipt 的 tree 是 candidate run audit；PASS evidence 的 origin tree 只证明来源，不改变 identity digest | report decoder binding guard、receipt hash validation、receipt finalizer plan binding、tree exclusion guard |
| `RemoteCIExecutionScope` / v15 side table | `RemoteCIExecutionScope` constructs catalog-ordered scope; `cicontract.RemoteRunExecutionScopesTable` registers the only table | `RemoteCIRunRecord.Scope` is loaded/written through `ci_remote_run_execution_scopes` only | legacy/full scope has no row; subset alone persists selected IDs/typed JSON/digest/count. The digest excludes tree/path/commit/token and is not PASS identity, local namespace or owner attestation | v15 schema/migration guard, cicontract table registration, `TestRemoteCIExecutionScopeV15AcceptedContract` and its second-table/ci_runs-column/unknown-table/local-alias counterexamples |

## 关键不变量

1. 计划字段新增时，producer AST、Go reflection、JSON marshal 与动态 field registration 必须同时出现；不得维护第二份手工字段数组。
2. `plan_digest` 是 report、request、SQLite run 和 receipt 对完整计划（包括 packing evidence 与两个 policy digest）的唯一跨边界绑定；下游不能把某一个字段的字符串存在误认为已完成 mapper 覆盖。
3. `SourceTreeSHA`、`CandidateTreeSHA`、commit/worktree 只进入 run audit、catalog observation 或来源重放证明；`WorkloadPassIdentitySHA256` 的 canonical payload 不得包含它们。
4. 所有严格 decoder 都必须拒绝未知字段、尾随 JSON 与不接受的版本；cicontract 版本 alias 是唯一 owner。
5. 物理 schema v16 由唯一 `cicontract` version owner、Accepted schema block 与 dynamic table/index registry 共同冻结：v14→v15 只新增 remote execution-scope side table/indexes，v15→v16 只新增 consumer-owned immutable retained-proof table、必要 indexes 与严格 backfill；不得 ALTER/rewrite 既有 authority、向 `ci_runs`/evidence 加列、另建 truth source，或把 auxiliary projection 升格为第八 retention root/第二 authority。

对应架构测试：

- `TestRemoteCIPlanContractFieldChainGuard`
- `TestRemoteCIStrictPlanAndProtocolDecoderGuard`
- `TestRemoteCIEnvironmentV10AndDynamicFieldsGuard`
- `TestRemoteCIPassIdentityTreeExclusionGuard`
- `TestRemoteCIExecutionScopeV15AcceptedContract`
