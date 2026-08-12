# 14 远程 CI / ECI / ImageCache 单路径

## 阅读边界

当前事实以 `docs/契约/remote-ci-eci-imagecache-contract.md`、源码和同包测试为准。历史计划、迁移报告和归档材料只用于来源追溯，不得覆盖 Accepted 契约。

## 入口与唯一执行路径

- `cmd/super-dolphin-gate/main.go`：Gate CLI 总入口；远程命令进入 run、hook、materializer、manifest installer 与 worker。
- `cmd/super-dolphin-gate/remote_run.go`：读取 strict 配置和 SQLite accepted baseline，构造 normal/calibration 运行并输出权威账本。
- `cmd/super-dolphin-gate/remote_provision_generation_one.go`：仅由 normal run/hook 在空 singleton 时消费外部 generation-one strict receipt；不是独立 provision command。
- `.githooks/pre-commit`：绑定 exact staged tree 的本地代码守卫入口，不启动 remote CI。
- `.githooks/pre-push`：绑定 exact pushed ref update 的唯一 Git remote ECI 门禁入口。

## Coordinator、传输与 worker

- `internal/devtools/remoteci/coordinator.go`：PASS lookup 后只规划 MISS，并无仓库并发上限地创建阿里云 ECI shards。
- `internal/devtools/remoteci/coordinator_request.go`：把 accepted snapshot、strict bootstrap/current request、source/work/temp 卷与 worker argv 投影到 ECI request。
- `internal/devtools/remoteci/source_materializer.go`：生成唯一 v2 thin Git bundle、synthetic base/transport commit 与 strict source manifest。
- `internal/devtools/remoteci/worker_supervisor.go`：ECI shard PID1 supervisor；实际 gate 执行仍由 `internal/devtools/gate/executor.go` 唯一拥有。
- `internal/devtools/remoteci/human_timing_ledger.go`：只从 SQLite authority 渲染人类可读 timing ledger。

## 契约、SQLite 与阿里云边界

- `internal/devtools/cicontract/contract.go`：远程 CI provider、并发、资源、timing、retention 与 forbidden legacy capability 的代码 owner。
- `internal/devtools/gate/ledger_store_sqlite.go`：duration ledger SQLite authority 的打开、读取与 CAS 边界。
- `internal/devtools/gate/remote_baseline_generation_one.go`：accepted baseline singleton 的唯一生产 INSERT 路径。
- `internal/devtools/alicloud/eci/client.go`：阿里云 ECI container group 生命周期；正常 shard 必须绑定 accepted ImageCache snapshot。
- `internal/devtools/alicloud/eci/image_cache.go`：ImageCache 只读 Describe/验证，不提供仓库内 writer。
- `internal/devtools/alicloud/oss/client.go`：source/request/report 的内容寻址 OSS 传输，不是状态权威。

## 配置、启动器与架构守卫

- `config/remote-ci/aliyun.json`：strict remote run 配置样例；原始 registry/agent 凭据不得持久化其中。
- `scripts/git_with_remote_ci_credentials.sh`：从受信凭据存储取得短期 GHCR 凭据并以环境继承执行 Git。
- `scripts/init_remote_ci_local.sh`：安装本地配置、SQLite 路径与 credential-aware Git alias，不创建 ImageCache。
- `internal/archtest/remote_ci_eci_imagecache_contract_guard_test.go`：静态拒绝本地/第二 executor、旧 cache authority、并发上限和仓库内 refresh writer。
- `internal/archtest/remote_ci_retired_legacy_artifacts_guard_test.go`：拒绝已退役 JSON/cache/bundle artifact 复活。

## 定位顺序

1. 先读 Accepted 契约与 `internal/devtools/cicontract/contract.go`。
2. 从 `cmd/super-dolphin-gate/remote_run.go` 跳到 `internal/devtools/remoteci/coordinator.go`。
3. 按问题选择 request/source transport、worker/executor、SQLite authority 或 ECI/OSS client。
4. 使用 LSP definitions、references、call hierarchy 与 diagnostics 核对生产调用链，再读同包测试和架构守卫。
