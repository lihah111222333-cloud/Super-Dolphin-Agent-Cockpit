# Step 12-13：CI/CD、部署、回滚、监控和故障处理

## Step 12：CI/CD、部署、回滚流程

### 远程 CI 与发布边界

远程 CI 没有 GitHub runner。候选编译、测试、cache-prime、镜像构建、分片执行和权威耗时观测全部由阿里云 ECI container group 完成，唯一规范见 `docs/契约/remote-ci-eci-imagecache-contract.md`。`.github/workflows/release.yml` 仅用于手动原生发布 rollout，不签发远程 CI 结论，也不属于远程 CI 执行路径；真实发布仍必须消费 release profile 的 coordinator 授权。

| Workflow | 权威入口 | 目的 |
| --- | --- | --- |
| 阿里云 ECI API | accepted ImageCache -> 并发 ECI shards | 唯一远程 CI、缓存镜像与耗时账本执行面 |
| `release.yml` | manual native rollout + authoritative ECI release scenario | 受保护的 macOS/Windows 原生发布证据，不作为 CI receipt |

### 本地打包

| 平台 | 脚本 | 说明 |
| --- | --- | --- |
| Windows | `scripts/package_windows.ps1` | 构建 zip 或 installer，打包 peer binaries、runtime manifest、SQLite migrations、Codex、ffmpeg 等 |
| macOS | `scripts/package_macos.sh` | 构建 app bundle 和 DMG，可签名和 notarize |
| Linux | `scripts/package_linux.sh` | 构建 tar.gz 包，包含 runtime manifest 和工具依赖 |

Windows 示例：

```powershell
.\scripts\package_windows.ps1 -Artifact all
```

发布前应运行对应 `scripts/verify_packaged_app_*`，确认 manifest、bundled executables、LSP tools、Codex runtime、SQLite migrations 和前端资源一致。

### GitHub Release

发布脚本：

```powershell
bash ./scripts/publish_github_release.sh --dry-run
```

真实发布前检查：

1. `VERSION` 和 tag 语义明确。
2. Release assets 已由 package 脚本生成。
3. 更新签名 key、manifest、checksum 完整。
4. `gh auth status` 有目标仓库 Release 权限。
5. `--dry-run` 无错误后再执行真实发布。
6. 真实发布脚本会在 `gh release create` 前执行 `./scripts/ci_truth_image_gate.sh release`；该入口只能调用当前 `super-dolphin-gate remote run --scenario full --entrypoint release`，并以当前 HEAD 的 release profile 获得真实 ECI 通过结果。缺少受信 CLI、真相镜像证据或 gate 结果都会立即失败。

### 回滚策略

| 层 | 回滚方式 | 注意事项 |
| --- | --- | --- |
| 应用包 | 回退到上一版本 installer、zip、DMG 或 tar.gz | 确认 runtime manifest 和 bundled tools 一起回退 |
| 前端资源 | 使用上一版本包内资源 | 新 UI 开发态不要误判为发布态 |
| 数据库 | 停止应用后恢复已验证的同版本 SQLite 文件备份 | 没有自动 down migration；恢复后核对 `schema_migrations` 和启动 preflight |
| Provider 配置 | 恢复用户目录或配置备份 | 不要覆盖用户 secrets |
| 本地任务状态 | 备份后修复 `task_*`、thread 或 binding 表 | 保留审计和故障记录 |

## Step 13：监控、日志、告警、故障处理

### 指标

HTTP server 会挂载：

```text
/metrics
```

读取到的指标来源包括：

- `internal/platform/metrics`
- `cmd/mcp-orch/orchestration/metrics`
- `pkg/dagmetrics`
- `pkg/dreammetrics`
- `pkg/skillmetrics`

指标覆盖示例：

- bootstrap heartbeat failure
- report queue dropped
- reconnect attempts
- DAG dispatch failed
- DAG retry alert 和 retry overflow
- host tool calls
- skill trim corruption fallback
- enrich failure

### 日志

| 来源 | 路径或入口 |
| --- | --- |
| 后端启动日志 | `.tmp/run-new-ui-desktop/backend.log` |
| 前端启动日志 | `.tmp/run-new-ui-desktop/frontend.log` |
| 通用 logger | `pkg/logger`、`internal/app/app.go` |
| UI HTTP logging | `internal/ui/wails/http_logging.go` |
| Observability RPC | `internal/module/observability/rpc.go` |
| 数据库日志表 | `system_logs`、`bus_exception_logs`、`audit_events` |

### 本地 ELK

已读取到本地 ELK 配置：

- `deploy/elk/docker-compose.yml`
- `deploy/elk/logstash/pipeline/super-dolphin.conf`
- `deploy/elk/README.md`
- `scripts/elk-local.ps1`

启动：

```powershell
.\scripts\elk-local.ps1 start
```

默认本地端口：

- Elasticsearch：`127.0.0.1:9200`
- Kibana：`127.0.0.1:5601`

Logstash 读取 `.tmp/**/*.log`，索引建议为 `super-dolphin-logs-*`，时间字段为 `@timestamp`。

### 故障处理流程

1. 固定现场：记录 commit、branch、启动命令、环境变量、端口、时间和用户动作。
2. 检查进程和端口：确认是否有旧进程占用 `4511/4512/8092/5173/5175`。
3. 检查 `/metrics`：判断后端是否已启动，还是 UI 单独失败。
4. 检查后端日志：优先找 fatal、migration、database、provider、RPC handler 错误。
5. 检查前端日志和浏览器控制台：确认 Vite、React 和 RPC 调用错误。
6. 检查数据库：确认 `schema_migrations`、thread、DAG、cron 相关状态是否一致。
7. 检查 provider：确认 Codex/Claude 登录态、binary、workspace 和权限。
8. 检查 mcp-orch/mcp-lsp：确认 peer binary 可执行，工具调用有明确错误。
9. 复现最小化：把问题缩小到单个 RPC、单个模块或单个 SQL。
10. 修复后补充回归测试、smoke 记录或故障文档。

### 常见故障入口

| 症状 | 优先检查 |
| --- | --- |
| UI 白屏 | Vite 日志、浏览器控制台、`frontend-app` build/lint |
| `/metrics` 不通 | 后端日志、端口占用、数据库连接、启动脚本前置检查 |
| 数据库迁移失败 | `internal/platform/db/sqlite/migrations`、`schema_migrations`、SQLite 备份版本和启动 preflight |
| turn 无响应 | thread/turn RPC、provider binding、provider CLI 登录态 |
| DAG 卡住 | `task_dag_runs`、`task_dag_nodes`、mcp-orch 日志、retry 指标 |
| cron 不触发 | `cron_jobs`、timezone、enabled、next run、cron worker 日志 |
| LSP 工具失败 | `cmd/mcp-lsp`、language server binary、workspace root |
| 发布包启动失败 | runtime manifest、bundled executables、verify packaged app 脚本 |
