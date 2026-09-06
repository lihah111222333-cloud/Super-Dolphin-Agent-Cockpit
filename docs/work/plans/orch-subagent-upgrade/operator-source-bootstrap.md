# Operator source bootstrap：独立来源 owner 与启动接线

状态：**O1/O2/O3 源码已落地；主 agent 原 host 四组功能验证 PASS，owner/entry mutation 行为 RED 与 SHA 恢复已确认。完整 ST01–ST18 目标仍 Active。** 本文只记录该增量裁决和后续接线要求，不修改总规格硬门，也不将局部来源实现计为完整首启动闭环。

## 1. 缺口与裁决

现有 CONTENT-1 的 `SourceBinding` 绑定 active task 身份链（`cmd/mcp-orch/store/content/contract.go`、`promote.go`），不能代表 standalone operator 首条启动正文。为绕过此限制而伪造 agent 或 `provider_discovery` 会混淆身份 owner，明确禁止。

新增 `cmd/mcp-orch/store/operatorsource/` 只拥有 operator 来源代际。来源 row 和 opaque identity hash **不是** transport credential、anti-rollback authority 或 source grant；也不代表 CONTENT live ref、command accepted、RESOURCE permit、custody receipt。真实传输认证仍由既有 credential/认证流程负责。`auth_context` 是配置 issuer 生成的非秘密 server-boot 标识，不是 bearer。

## 2. 持久化范围（O1）

- migration：`internal/platform/db/sqlite/migrations/138_orchestration_operator_sources.sql`。
- 查询：`cmd/mcp-orch/sql/queries/operator_source.sql`；由 sidecar `cmd/mcp-orch/sqlc.yaml` 生成，不改根产品 sqlc 配置。
- 每个 standalone state DB **全局只有一个 active source**，generation 全局单调递增；不是每个 principal/scope 各保留一个 current。
- `Activate` 使用 `BEGIN IMMEDIATE`，精确比较全局 expected generation，同事务撤销旧代并插入新代。配置 principal/scope 改变也必须撤旧源；插入失败回滚旧源撤销。
- 同 boot exact replay；revoked boot 不复活。`ResolveCurrent` 完整比对 selector 并要求全局最新 active；旧代 `Revoke` 只幂等返回对应已撤销快照，不能撤销新代。
- STRICT 表、只追加代际、只允许 active→revoked、禁止删除；读取重算完整 tuple 的域分离 SHA-256，拒绝身份损坏。时间为固定9位小数 UTC canonical 格式。
- 138 是独立全局迁移，**不依赖、不移动、不启用** `cmd/mcp-orch/store/orchestrationcommand/candidate_migrations/137_orchestration_command_submissions.sql`；号码间隙不表示137已执行。

## 3. 正式运行接线（O2/O3）

源码入口：`cmd/mcp-orch/serve.go`、`standalone_operator_source.go`、`standalone_operator_tools.go`。

```text
runServe：配置/认证 issuer → instance lock → SQLite → Activate operator source → Fx / listener
退出或启动失败：独立有界 context 精确 Revoke → close DB → release instance lock
```

listener 前完成来源持久化；清理不能依赖已经取消的启动/请求 context，清理错误须传播。旧 boot 的迟到 Close 不影响新 boot。

HTTP 所用 tool provider 在 **每次 ListTools / CallTool** 前调用 `RequireCurrent`。已有 session 保存的旧 context 不能绕过后续来源换代检查；这一入口检查**不代表原子取消已经进入下游的 in-flight 操作**。

生产 nil-issuer 便利 wrapper 已移除；测试 helper 必须显式构造身份。内层既有 task 工具过滤及其授权边界保留，不因 operator 来源存在而开放 dispatch 或越过 task 解析器。

## 4. 验证账本（主 agent 已核验）

经既有 `test_with_guard.sh --host-test` 原入口执行，未用裸 `go test` 或 staging 绕过守卫。以下 PASS 仅是本次本地功能证据；日志仍明确标记 **LOCAL_NON_AUTHORITATIVE / NOT_VERIFIED**，不代表发布、远程权威验收或完整首启动完成。

证据根目录（下表文件名均相对此目录）：

`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/operator-source`

| 包 / 测试选择器 | 结果 / 类别 | 证据日志 | archtest / 目标包耗时 |
|---|---|---|---|
| `./cmd/mcp-orch/store/operatorsource` / `TestOperatorSourceContract` | PASS / light | `owner-final.log` | 164.528s / 0.055s |
| `./cmd/mcp-orch` / `TestStandaloneOperatorBootstrapContract` | PASS / light | `bootstrap-first.log` | 172.074s / 1.700s |
| `./cmd/mcp-orch` / `TestMCPOrchServeOperatorSourceLifecycle` | PASS / medium，真实 binary | `source-lifecycle-e2e.log` | 142.727s / 0.725s |
| `./cmd/mcp-orch` / `TestMCPOrchServeOperatorSourceHTTPFence` | PASS / medium，真实 binary | `http-fence.log` | 145.463s / 0.798s |

目标包耗时取日志 `ok` 行，不冒充整个原入口总耗时。owner 覆盖真实138 SQLite、换代/replay、旧 close、CAS 无写、回滚、字段反射变异、SQL 防篡改、时间与 reopen；bootstrap 覆盖来源接线、逐请求 fence、显式身份和 Fx 图；两组真实 binary 分别覆盖 serve 生命周期与 HTTP 来源 fence。

### Mutation 行为 RED 与恢复

| 注入范围 | 证据子目录（包含 `test.log`、`result.json`） | 实际结果 |
|---|---|---|
| owner 验证分支 | `owner-20260906T050058-4acc32c2e7e149c58bfccb2bf2605ec5/` | exit 1；lifecycle、source roundtrip、direct SQL 断言出现预期行为 RED；入口总耗时184.035s |
| entry 来源/工具 fence | `entry-20260906T050523-09fa9102bfe14588ab0be30f34df4bb2/` | exit 1；旧 context 换代/关闭及 caller 字段变异断言出现预期行为 RED；入口总耗时169.278s |

两份 `result.json` 的每个文件均满足 `restoredSHA256 == originalSHA256`、`errors=[]`；主 agent 已结合真实断言日志裁决，而非仅凭 exit 1 认定守卫有效。恢复记录：

- owner `validation.go`：`ee21552b1acb277ca14a9dd63bcb2c83d33551147435ae9b4ba7ccf8c376ec84`。
- entry `standalone_operator_source.go`：`d2ff41153effe2ecae8fac923246cce501708cfb452698b909bae0a5bbd1e922`。
- entry `standalone_operator_tools.go`：`21ad6c24f8e52a3352fabb5202ca11e0b859c35f68c0556d693ba44f6e8b5740`。

此前原入口 **codeguard panic / CC 前置**失败已无 baseline 放宽地修复；这些前置失败不是业务 RED。空 SQLite 缺少138表的 missing-table 证据也仅证明 schema 缺口。上面另行列出的 mutation 才是已核验的行为 RED。本次无发布，未完成 CONTENT 首条命令链，不能把局部功能 PASS 升格为整体交付。

## 5. 必要后续与完成条件

1. **CONTENT 联合来源契约**：显式区分 operator/task source，operator 分支真实绑定本 owner，task 分支保留原 active 身份链；同步 mapper、schema、逐字段动态守卫与真实测试。不能仅传 opaque hash 当授权。
2. **首启动 ID / command 组装**：将 operator 正文来源接入实际首条启动命令链。目前尚未接通，不能宣称首条正文已有 CONTENT live ref 或 command accepted。
   当前已增加仅意图层的 `cmd/mcp-orch/orchestration/launchassembler`：它验证
   operator reservation/source/content/profile 绑定后，只写不可变 `launch_agent`
   intent；不执行 provider、accepted、dispatch 或 child identity。其 fail-fast
   contract 已通过，但真实 command-store + reservation + active CONTENT ref 的
   端到端测试和首启动 binary 链仍待完成。
3. **完整 ST01–ST18**：继续现有总规格、权威生命周期与验收要求；本增量不替代其它阶段，不解除 dispatch、RESOURCE、custody 等既有硬门。
4. 保留上述四组测试及 mutation 证据，后续修改后重跑受影响原入口，失败则定位实际可达链路；静态通过、功能通过、首启动完成与完整目标完成必须分别报告。
