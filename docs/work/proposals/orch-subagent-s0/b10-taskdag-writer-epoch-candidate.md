# B10 taskdag writer-epoch candidate（未接入生产）

Status: Candidate / isolated / not wired to production

本候选只定义 shared taskdag RW SQLite mutation owner 的边界合同，不表示现有
`cmd/mcp-orch/store/taskdag` 或 `internal/platform/db` 已完成 B10 修复。实现位于
`cmd/mcp-orch/store/taskdagwriterfence`，当前不由 Fx 装配、不导入 taskdag 生产路径，
也不打开或修改 RecoveryCustody 的私有 SQLite。

## 合同

* admission 必须显式给出 `schema_version=1`、正 `writer_epoch` 和规范化
  `deployment_fence`；缺失、未知版本、空白漂移、NUL 或越界一律
  `ErrInvalidAdmission`，禁止本地默认值。
* `Bootstrap` 只在调用方提供的候选数据库中创建并 seed 单例
  `taskdag_writer_fence` 行。生产 shared DB 的 migration/namespace 仍未被本候选
  触碰。
* `Open` 读取并校验 DB-resident fence，要求与 admission 完全相等；配置值不能
  覆盖数据库事实。
* `Mutate` 在业务 callback 前执行精确的 DB-resident CAS（按
  `schema_version + writer_epoch + deployment_fence` 谓词）。CAS 影响行数不是 1
  时返回 `ErrStaleWriter`，回滚整个事务且不调用 callback，因此 stale writer 的
  业务写入为 zero rows；该实例随后 sticky `ErrAlreadyFenced`。
* `Advance` 使用同样的 compare-and-swap 原子推进到严格更高 epoch；旧 expected
  token 的推进是 `ErrStaleWriter`，不能覆盖 successor 的 fence。
* transport、lease、RecoveryCustody、PostgreSQL 和 Fx 都不属于该候选的授权来源。
  未来接入时，外部 lease 只能作为 admission 前置，不能替代每个 mutation 的
  DB CAS。

## A/B subprocess 验证骨架

`TestWriterFenceSubprocessABCutover` 建立专用临时 SQLite。subprocess A 以 epoch N
打开并持有连接，父进程 B 以同一 expected token CAS bump/freeze 到 N+1；随后 A
尝试 mutation，必须得到 `stale_writer`，callback 不得运行，`mutation_probe` 行数
必须保持 0。测试还验证同一 A 被 sticky-fence 后后续 mutation 立即拒绝。

这是生成式验证骨架而非 production cutover 证明：仍需未来补齐真实 taskdag 全部
mutation owner 的枚举、OS 写权撤销/进程终止或新 namespace 隔离，以及接入后的真实
Fx/runtime/发布门禁证据。
