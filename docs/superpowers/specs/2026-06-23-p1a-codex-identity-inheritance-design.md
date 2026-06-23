# P1a Codex Identity 继承闭环设计

## 目标

评估 `docs/plans/迁移/p21/P1a_MultiProviderCodex.md` 是否仍适用于当前项目，并把可执行范围收敛到当前代码尚未闭环的一处缺口：`orchestration_launch_agent` / managed `launch_agent` 从父 Codex 线程启动子代理时，必须继承完整的 Codex instance identity。

完整 identity 包含：

- `codex_home`
- `codex_instance_key`
- `codex_model_provider`

## 当前判断

P1a 的总体方向适用于当前项目：用显式 Codex identity 三元组隔离不同 OpenAI-compatible 模型实例，而不是把 `provider` 字段扩成 `codex:qwen`、`codex:deepseek` 这类混合语义。

但 P1a 文档里的大部分“待实现”后台基础设施已经落地，不能按原文重新实施。当前代码已经具备：

- `internal/contract/codex_identity.go` 的严格 identity 解析、canonicalize 和 sentinel error。
- `internal/provider/codexapp/server_pool.go` 的按 identity 隔离池化、无容量上限、spawn backoff 和 release 清理。
- `internal/provider/codexapp/pool_spawner.go` / `pool_spawn_cmd.go` 的 `CODEX_HOME` 注入、环境白名单和 `model_provider` 配置传递。
- `migrations/0048_binding_codex_identity.sql`、`migrations/0070_binding_provider_thread_first_write.sql` 以及 store 测试覆盖 binding identity 字段和不可变约束。
- `internal/platform/toolbridge/handler_dag_launch_test.go` 已覆盖 DAG agent node 注入完整 Codex identity。

## 剩余缺口

`internal/platform/toolbridge/handler_launch_args.go` 的 `injectManagedLaunchArgs` 当前只从父 binding 注入：

- `parent_id`
- `provider`
- `model`
- `effort`
- `codex_model_provider`

它没有注入 `codex_home` 和 `codex_instance_key`。因此通过 managed `launch_agent` / `orchestration_launch_agent` 启动的子代理可能只继承模型 provider 名称，却丢失具体 Codex home 和实例 key，导致后续路由退回错误实例或无法按 binding 恢复。

DAG 路径已经注入完整 identity，所以本轮不改 DAG 实现，只用它作为期望行为参照。

## 设计

在 `injectManagedLaunchArgs` 中继续沿用现有 `setArgStringIfMissing` 行为，补齐从父 binding 注入的两项 identity：

- `codex_home` 取 `binding.CodexHome`
- `codex_instance_key` 取 `binding.CodexInstanceKey`
- `codex_model_provider` 继续取 `binding.CodexModelProvider`

显式传入的 launch args 优先级不变：如果调用方已经提供这些字段，不覆盖。这样保留当前可测试行为，也避免让父上下文强行覆盖显式选择。

不新增依赖、不新增配置开关、不改数据库 schema、不改 provider router、不改 DAG 结构。

## 测试

需要更新现有 toolbridge 测试：

- 扩展 `handler_launch_args_test.go`，验证三项 Codex identity 都会被注入。
- 扩展同文件“不覆盖已有参数”的测试，验证三项 identity 都保留调用方显式值。
- 更新 `handler_test.go` 中 `TestToolBridge_OrchestrationLaunchInheritsParentContextFromProviderThread` 的期望参数，要求包含 `codex_home`、`codex_instance_key` 和 `codex_model_provider`。

可选检查：

- 若动态 Codex tool surface 测试已经能从 binding 走到 managed launch 参数，则补一条同级断言；如果会造成重复覆盖，则不新增。

## 验证

Go 文件改动后先跑单文件守卫：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\platform\toolbridge\handler_launch_args.go
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\platform\toolbridge\handler_launch_args_test.go
```

最后跑受影响包：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/platform/toolbridge -count=1
```

## 非目标

- 不实现模型网关、计费、token pool 或管理后台。
- 不重做 P1a 已落地的 ServerPool、binding migration、history rollout 或环境白名单。
- 不改变 `provider` 字段的 `codex|claude` 语义。
- 不引入默认 home fallback；缺失 identity 仍应 fail fast。

## 风险

主要风险是参数名兼容：当前 launch args 用 snake_case，而 contract config 用 camelCase。managed launch 路径既有测试已经使用 `codex_model_provider`，因此本轮继续使用 snake_case，与 DAG exec 注入保持一致。

另一处风险是覆盖语义：如果父线程 identity 与调用方显式 identity 不一致，本设计保留调用方显式值。后续如果需要禁止跨实例子代理，应另立策略并在 launch 边界做校验。
