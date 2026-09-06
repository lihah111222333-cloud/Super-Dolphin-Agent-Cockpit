# RecoveryCustody 独立服务候选

Status: Candidate — fail-closed, not production-ready

`cmd/mcp-recovery-custody` 是未注册、非生产编排组合的独立进程构造候选。当前 `run` 入口始终因缺少生产外部 evidence finalizer/receipt verifier 加载器而 fail-closed，因此它目前只能证明配置解析、依赖注入和生命周期测试路径，不能宣称 CLI 已可启动或已具备生产服务资格。仅测试构造器可注入外部 evidence finalizer/receipt verifier 替身来验证生命周期和 transport；即使显式 JSON 配置（authority 必须声明 `external_service: true`，不携带 product/workspace 路径）、owner-only credential 文件和独立 authority SQLite 路径均满足，生产 `run` 仍不会启动。

进程仅绑定 `127.0.0.1:<port>`（不接受通配地址、IPv6 或代理），在 `/rpc` 提供现有
`authorityhttp` JSON-RPC 的 `identity|submit|query` 合同，并提供 `/healthz`（别名
`/health`）与 `/readyz`（别名 `/ready`）。当前 service 尚未装配
`reconcilecoordinator`，因此不会由 HTTP service 自行推进 terminal resolution 到
`available|aborted`；即使测试依赖注入成功，也只有 pending/query 能力。优雅取消会先
停止 HTTP listener，再关闭 authority 数据库和内核目录租约。credential 值不会写入日志、
错误或 ready 响应。

`bootstrap: true` 只允许对空的、独立私有数据库执行一次 authority schema bootstrap；当前 authority schema revision 3 将 `external_service` mode 与 writer fence 一并持久化，已存在数据库仍由 `OpenAuthority` 做完整 schema/identity 校验。服务只校验自己的 authority parent、主库和 WAL/SHM 私有性，不读取或要求 product SQLite/workspace；authority parent 还必须通过 canonical path 的本地文件系统类型检查，已知网络/未知类型和检测失败均拒绝。Bootstrap/OpenAuthority/Takeover 使用同一 canonical path，并在同一内核目录租约内绑定已存在的 SQLite 主库/WAL/SHM 文件对象，before/after identity read-back 会拒绝同路径 rename/recreate 或 sidecar 替换；不导入 PostgreSQL、不共享产品 SQLite，也没有 mcp-orch Fx 注册或生产 adapter 接线。

配置解码器递归拒绝嵌套对象/数组的重复 JSON key，并限制深度、成员数和总字节数，避免 authority 字段被 `encoding/json` 的最后值静默覆盖。主进程当前没有可发布的外部 finalizer/verifier 加载器，`processDependencies` 因此始终拒绝启动；测试通过构造函数注入替身验证生命周期和 transport。该候选不构成 availability/abort terminal proof 或 production readiness。

构建与打包边界同样保持 candidate-only：`make build-recovery-custody-candidate` 是显式 opt-in
目标，原子生成 `bin/mcp-recovery-custody`，不会由默认 `build`、`build-peer-binaries` 或
orch runtime 自动触发。Linux/macOS 打包脚本默认不构建或复制该二进制；只有显式设置
`RECOVERY_CUSTODY_CANDIDATE_PACKAGE=1` 时，才将它作为独立资产复制到 `bin/` 并在
`runtime-manifest.json` 记录 `candidate`、`disabled` 及
`requires-external-evidence-finalizer-and-receipt-verifier` 元数据；它不进入 launcher 的
`bundled_execs`，也不被服务启用或自动启动。因为发布进程仍缺少真实外部
EvidenceFinalizer/ReceiptVerifier，完整配置下的 binary 仍在依赖检查阶段稳定失败并保持零
startup side effect；这些制品和 manifest 不能被解释为可运行服务、terminal receipt 或 release
授权。打包验证器对没有任何 `recovery_custody_candidate_*` 字段的既有 manifest 保持兼容；一旦
出现任一候选字段，则要求四项元数据成组出现、路径和可执行文件匹配，且 `status=candidate`、
`runtime=disabled`，否则 fail-closed。

服务内 reconcile 的当前接缝也只存在于 test-owned candidate：
`service_coordinator_candidate_test.go` 将已构造的 authority 收窄为
`ListActiveResolutionHandles`/`ReconcileResolution` 两个方法，并只打开已显式 Bootstrap
且与 authority 使用不同私有 SQLite 文件的 coordinator。它不调用 `Serve`/`Run`，不改变
`service.go`/`main.go` 的 production wiring；因此仍不能宣称服务会自动推进 terminal
resolution 或具备 supervisor 生命周期。
