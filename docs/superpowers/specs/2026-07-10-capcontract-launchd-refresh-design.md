# 跨仓库生成物本地定时刷新设计

## 目标

在 macOS 本机每 5 分钟刷新一次 `super-agent-v3` 的
`docs/doc/codemap/capability-contract/capability_manifest.json`，并刷新
`wjboot-v2` 的 `docs/guide` AI project map 与分片索引；整个链路不依赖 CI，
也不自动执行 `git add`、提交或推送。

## 方案

新增仓库脚本 `scripts/generated_artifacts_auto_refresh.sh`，提供以下命令：

- `install`：安装并启动当前用户的 LaunchAgent，安装后立即执行一次刷新。
- `uninstall`：停止并删除 LaunchAgent 配置。
- `status`：报告 LaunchAgent 是否已加载，并显示配置和日志路径。
- `run-once`：在前台执行一次刷新，便于测试和故障排查。

LaunchAgent 使用固定标签 `com.super-agent-v3.generated-artifacts-refresh`，配置写入
`~/Library/LaunchAgents/com.super-agent-v3.generated-artifacts-refresh.plist`，通过
`StartInterval=300` 每 5 分钟调用脚本的 `run-once` 命令，并设置
`RunAtLoad=true`，使登录后加载时立即刷新。

`install` 与 `run-once` 接受必填的 `--wjboot-repo PATH`；安装时把规范化后的绝对路径
固化到 plist，避免后台环境依赖当前工作目录或猜测兄弟仓库位置。

## 刷新链路

`run-once` 必须定位脚本所在仓库根目录，然后调用现有统一入口：

```text
generated_artifacts_auto_refresh.sh run-once --wjboot-repo <wjboot-v2>
  -> super-agent-v3/scripts/refresh_generated_artifacts.sh capcontract --repo <super-agent-v3>
  -> super-agent-v3 capability_manifest.json
  -> wjboot-v2/scripts/refresh_generated_artifacts.sh project-map --repo <wjboot-v2>
  -> wjboot-v2 docs/guide/AI_PROJECT_* 与 docs/guide/index/*.tsv
```

脚本只复用现有生成器，不复制扫描逻辑，也不直接拼装 JSON。生成失败时返回非零，
保留错误日志并等待下一个 5 分钟周期；禁止静默降级或生成默认清单。

## 进程、日志与安全边界

- 由 `launchd` 管理周期调度和登录后重启，不使用永久 `nohup` 循环。
- `run-once` 使用 `super-agent-v3` Git 公共目录下的互斥锁，避免同一脚本的手工执行与
  定时任务并发写入。
- 锁中记录 PID；活跃进程存在时本次运行明确失败，陈旧锁可在确认 PID 不存在后清理。
- 标准输出和错误输出写入 `~/Library/Logs/super-agent-v3/`。
- plist 中使用仓库和用户目录的绝对路径；仓库移动后必须重新执行 `install`。
- 不修改 Git index，不提交，不推送，也不启动 CI。

## 安装和卸载行为

`install` 创建所需目录和 plist，使用当前用户 GUI domain 调用 `launchctl` 加载任务。
重复安装必须幂等：先卸载同标签旧任务，再写入并加载新配置。任何 `launchctl` 失败都
返回非零并打印可执行的诊断信息。

`uninstall` 在任务未加载或 plist 不存在时仍给出明确状态；除目标 plist 外不删除用户
目录中的其他文件。日志默认保留，便于排查。

## 测试与验收

自动测试覆盖：

1. `run-once` 依次调用两个仓库的统一刷新入口，并传入正确仓库路径和刷新模式。
2. `install` 生成包含正确 label、`StartInterval=300`、`RunAtLoad=true`、日志路径和
   `run-once` 参数的 plist。
3. 重复安装不会留下重复任务。
4. `uninstall` 只移除目标任务和 plist。
5. 并发锁、陈旧锁和刷新失败均返回明确非零状态。

实机验收包括：执行 `run-once`、安装 LaunchAgent、检查 `status` 和 `launchctl print`、
确认 `make capcontract-check` 通过，确认 `wjboot-v2` project map 为严格漂移 OK，并验证
卸载后可重新安装。最终保持任务处于已安装、已运行状态。

## 非目标

- 不定时刷新 `super-agent-v3` 的文件级 codemap 或 project map。
- 不刷新 `wjboot-v2` 的 engine contract capability manifest。
- 不自动暂存、提交或推送生成物。
- 不提供 Linux systemd 或 Windows Task Scheduler 适配。
- 不替代 pre-commit、pre-push 或 CI 的只读漂移检查。
