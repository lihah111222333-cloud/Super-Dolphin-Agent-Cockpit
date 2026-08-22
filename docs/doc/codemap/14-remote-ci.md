# 14 远程 CI / ECI / ImageCache 单路径

## 阅读边界

当前事实以 `docs/契约/remote-ci-eci-imagecache-contract.md`、源码和同包测试为准。历史计划、迁移报告和归档材料只用于来源追溯，不得覆盖 Accepted 契约。

## 入口与验证边界

- `cmd/super-dolphin-gate/main.go`：trusted local Gate CLI 总入口；仅保留 project-map、closure、launcher 与 worker 命令。
- `cmd/super-dolphin-remote-ci/main.go`：独立 portable descriptor validation 组合根；execute 入口 fail-fast 退役。
- `.githooks/pre-commit`：绑定 exact staged tree 的本地代码守卫入口，不启动 remote CI。
- `.githooks/pre-push`：绑定 exact pushed ref update 的唯一 Git remote ECI 门禁入口。

## Coordinator、传输与 worker

- `internal/devtools/remoteportable/`：portable manifest、external locator binding 与 exact target identity。
- `internal/devtools/remoteportable/workload_catalog.go`：provider-neutral canonical workload catalog digest、平台计数和 result proof 校验。
- `internal/devtools/heteroci/`：动态节点探针、容量规划、本地非权威路由与 CapacityBlocker。
- `internal/devtools/heterociworkflow/`：单 executor binding 的通用工作流生命周期。
- `internal/devtools/remotecicli/portable_validate.go`：只输出 strict validation result；不得执行 workload；validate 先从 bound exact tree 调用 `expandBoundPortableRepositoryCatalog`，输出 catalog digest 与 Linux/Windows workload proof。

## 契约、SQLite 与阿里云边界

- `internal/devtools/heterociowner/` 与 `internal/devtools/heterociexec/`：PostgreSQL owner/receipt 适配；不由本地 Gate 充当权威。
- `internal/devtools/gate/`：trusted local Gate executor 与 SQLite-local diagnostics，不承担 remote authority。

## 配置、启动器与架构守卫

- `.githooks/pre-push`：验证 pushed tree 的 trusted project-map 与 closure，不读取远程 config/ledger/token。
- `.githooks/trusted-gate-launcher.sh`：trusted local Gate receipt 验证入口。
- `internal/archtest/heteroci_contract_header_guard_test.go`：锁定 Accepted 四节点/PG 边界与 local non-authoritative 约束。
- `internal/archtest/heteroci_four_node_capacity_contract_guard_test.go`：锁定动态探针、容量规划与单 binding 边界。

## 定位顺序

1. 先读 Accepted 契约与 `internal/devtools/remoteportable/`、`internal/devtools/heteroci/`。
2. 从 `cmd/super-dolphin-remote-ci/main.go` 跳到 `internal/devtools/remotecicli/portable_validate.go`。
3. 按问题选择 portable binding、heteroci routing/workflow、PostgreSQL owner/receipt 或 trusted local Gate closure。
4. 使用 LSP definitions、references、call hierarchy 与 diagnostics 核对生产调用链，再读同包测试和架构守卫。
