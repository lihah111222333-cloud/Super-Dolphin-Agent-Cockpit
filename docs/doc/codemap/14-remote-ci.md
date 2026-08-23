# 14 远程 CI / 单 Ubuntu Host + Wine Runtime 单路径

## 阅读边界

当前事实以 `docs/契约/remote-ci-eci-imagecache-contract.md`、源码和同包测试为准。历史计划、迁移报告和归档材料只用于来源追溯，不得覆盖 Accepted 契约。

## 入口与验证边界

- `cmd/super-dolphin-gate/main.go`：trusted local Gate CLI 总入口；仅保留 project-map、closure、launcher 与 worker 命令。
- `cmd/super-dolphin-remote-ci/main.go`：独立 portable descriptor validation 与唯一 production execute 组合根。
- `.githooks/pre-commit`：绑定 exact staged tree 的本地代码守卫入口，不启动 remote CI。
- `.githooks/pre-push`：绑定 exact pushed ref update 的唯一 Git remote CI 门禁入口；不启动 ECI、ImageCache 或 physical-Windows executor。

## Coordinator、传输与 worker

- `internal/devtools/remoteportable/`：portable manifest、external locator binding 与 exact target identity。
- `internal/devtools/remoteportable/core_types.go`：portable request/manifest/binding core types 与 target identity carrier。
- `internal/devtools/remoteportable/workload_catalog.go`：provider-neutral canonical workload catalog digest、平台计数和 result proof 校验；manifest-owned catalog limits 进入 exact-tree resolver。
- `internal/devtools/heteroci/`：单 Ubuntu physical host（NodeID）的 probe、一次容量快照、ubuntu_container/ubuntu_wine_container 路由与 CapacityBlocker。
- `internal/devtools/heteroci/manifest.go`：single-Ubuntu/Wine topology manifest、NodeID/runtime binding 与 PostgreSQL remote-CI topology contract。
- `internal/devtools/heterociworkflow/`：单 executor binding 的通用工作流生命周期。
- `internal/devtools/remotecicli/portable_validate.go`：validate 只输出 strict validation result、不得执行 workload；同包 `execute.go`/`postgresql_authority.go` 承担唯一 production execute 与 PostgreSQL lifecycle，validate 从 bound exact tree 派生 catalog digest 与 Linux/Wine target proof。
- `internal/devtools/remotecicli/postgresql_authority.go` 的 `executeLifecycleCoordinator` 是唯一 outer `remote-ci-event/v1` owner；Linux runner/helper stderr 使用 `remote-ci-helper-log/v1`，由 `internal/devtools/remoteruntime` bounded consume 为内部摘要，不发布到外部 event/result channel。

## 契约、PostgreSQL 与单 Ubuntu runtime 边界

- `internal/devtools/heterociowner/` 与 `internal/devtools/heterociexec/`：PostgreSQL owner/receipt 适配；NodeID 是 physical host identity，runtime/target 必须沿 ExecutionUnit→BatchWorkload→material/unit/batch digest chain exact readback。
- `internal/devtools/gateprivate/`：本地 Gate CLI 仍需的最小 exit/process/trusted-Git 边界；不包含 SQLite、旧 planner/executor 或 remote authority。

## 配置、启动器与架构守卫

- `.githooks/pre-push`：验证 pushed tree 的 trusted project-map 与 closure，不读取远程 config/ledger/token。
- `.githooks/trusted-gate-launcher.sh`：trusted local Gate receipt 验证入口。
- `internal/archtest/heteroci_contract_header_guard_test.go`：锁定 Accepted 单 Ubuntu/Wine/PG-only 边界与 no physical-Windows/no ECI 约束。
- `internal/archtest/heteroci_single_ubuntu_wine_capacity_contract_guard_test.go`：锁定单 physical host、共享一次 snapshot/capacity/reservation 与 logical runtime binding。

## 定位顺序

1. 先读 Accepted 契约与 `internal/devtools/remoteportable/`、`internal/devtools/heteroci/`。
2. 从 `cmd/super-dolphin-remote-ci/main.go` 跳到 `internal/devtools/remotecicli/portable_validate.go`。
3. 按问题选择 portable binding、heteroci single-host routing/workflow、PostgreSQL owner/receipt 或 trusted local Gate closure。
4. 定义或影响面不明时使用 LSP `structure` / `xref`，修改后用 diagnostics 核对，再读同包测试和架构守卫。
