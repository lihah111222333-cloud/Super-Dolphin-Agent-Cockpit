# P1a: 多 Provider Codex 实例

## 目标
利用现有的统一 Codex App-Server 架构，仅通过注入独立的配置目录（`CODEX_HOME`）和环境变量实现对 GLM-5.1、Qwen3.6、DeepSeek 等 100% 兼容 OpenAI 接口的模型支持。这消除并替代了 Hermes 冗余的 Provider Adapter。

## 现状校准

- `thread/start` 已有 `modelProvider` 与通用 `config` 透传，`codexapp` driver 也会把 `modelProvider` 传给远端 `thread/start`。
- 但当前 top-level `modelProvider` 同时还参与 `thread/start` 的 provider 选择，只允许 `codex|claude`；因此不能直接拿它复用成 `qwen/glm/deepseek` 这类实例键。
- 当前 `internal/provider/codexapp/module.go` 的 `ServerManager` 是单例，一个 shared app-server 服务所有 codex session；这意味着仅靠 session 级配置无法实现 `CODEX_HOME` 隔离。
- 当前 `internal/provider/codexapp/driver.go` 会在 `newDriver()` 阶段缓存单个 `serverURL`；如果不改成 start/resume 时按请求动态选择实例，哪怕底层有池化，driver 仍会退化成单实例。
- `internal/provider/codexapp/history_rollout.go` 仍把 rollout 搜索根写死为 `~/.codex/sessions/...`，即便 spawn 路径支持多 home，历史恢复也会串目录。
- `ResumeSessionRequest` 当前没有 `Config`，`unified/session_resolver.go` 的 auto-resume 也只靠 binding 恢复最小字段；如果实例信息不持久化，重启后会回到默认 `CODEX_HOME`。
- `internal/router/rule.go` 是 prompt template 分类器，不是 provider 实例路由器；这里不应承载 `CODEX_HOME` 选择逻辑。

## 推荐改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 实例键解析 | `internal/provider/shared/config_helpers.go` | 新增 `ResolveCodexHome(config map[string]any) (instanceKey, home string)`；建议使用不与 top-level `modelProvider` 冲突的 key，例如 `config.codexHome` / `config.codexInstanceKey` / `config.codexModelProvider` |
| 启动载荷透传 | `internal/module/thread/start_session_helpers.go` | 继续复用现有 `ModelProvider` + `Config` 链路，但实例选择只能从 `req.Config` 里的专用 key 读取，不新增 router 字段 |
| app-server 池化 | `internal/provider/codexapp/module.go` | 将单例 `ServerManager` 改为按 `instanceKey` 管理的池，每个 key 对应一个本地 app-server 进程 |
| 会话建连 | `internal/provider/codexapp/{driver.go,session.go}` | `StartSession/ResumeSession` 按实例键取对应 server URL；不要在 `newDriver()` 预先冻结单个 `serverURL`；runtime config 中记录选中的实例键与 home |
| 环境注入 | `internal/provider/codexapp/transport_process.go` | `spawnLocal()` 支持注入 `CODEX_HOME=<path>`；必要时把 env 作为显式入参而非写死 `os.Environ()` |
| Rollout 历史 | `internal/provider/codexapp/history_rollout.go` | rollout 搜索目录改为从选中的 `CODEX_HOME` 反推 `sessions/`，不能再写死 `~/.codex` |
| 恢复链路 | `internal/dto/provider/session.go`、`internal/provider/unified/session_resolver.go`、`internal/module/thread/*` | 持久化 `instanceKey/codexHome` 并让 resume/auto-resume 能恢复到同一实例，而不是重启后掉回默认 home |

> **关键边界**：P1a 的隔离边界是 app-server 进程，不是 thread/router。只要 `ServerManager` 还是单 shared process，多 provider `CODEX_HOME` 就不成立。

## 配置目录设计示例
```text
~/.codex-providers/
├── glm/
│   └── config.toml   (wire_api = "chat", base_url = "https://open.bigmodel.cn/api/paas/v4")
└── qwen/
    └── config.toml   (wire_api = "chat", base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1")
```

## 选择优先级建议

1. `thread/start` 显式传入 `config.codexHome`
2. 否则根据 `config.codexModelProvider` 或 `config.codexInstanceKey` 映射到预定义 home
3. 否则走默认 `~/.codex`

> 不建议把实例选择绑到当前 top-level `modelProvider`。该字段今天仍承担 `codex|claude` provider 选择语义，直接复用会与现有校验和前端发包冲突。

## 环境注入伪码

```go
func (p *ServerPool) get(config map[string]any) (*ServerManager, error) {
    instanceKey, home := shared.ResolveCodexHome(config)
    mgr := p.ensure(instanceKey)
    return mgr.startWithHome(home)
}

func (m *ServerManager) spawnLocal(home string) (*exec.Cmd, error) {
    cmd := exec.Command("sh", "-c", "ulimit -n ...; exec codex app-server --listen ...")
    cmd.Env = append(os.Environ(),
        "CODEX_HOME=" + home,
    )
    // ...
}
```

## 范围说明

- 第一阶段建议仅支持本地 app-server 模式。若用户通过 `CODEX_APP_SERVER_URL` 指向外部共享服务，无法对每个 thread 做 `CODEX_HOME` 级别隔离，应降级为 warning + default instance。
- 不建议修改 `internal/router/rule.go`。provider 选择是 runtime/session 问题，不是 prompt 分类问题。
- `driver` 选实例的时机应晚于 `req.Config` 解析；因此池查询应发生在 `StartSession/ResumeSession`，而不是 factory 或模块初始化阶段。
- 若支持外部 `CODEX_APP_SERVER_URL`，应明确其优先级。当前代码里本地 manager 很容易覆盖显式 URL；P1a 要么在 external 模式彻底跳过本地 pool/manager，要么明确说明 external 模式下禁用多实例。
- pending-launch 路径也要持久化实例信息，否则首次 `thread/start` 与后续 `SpawnIfNeeded()` 会落到不同实例。

## 恢复与持久化约束

- 仅在 `StartSession` 里按实例键选 URL 不够，resume 与 auto-resume 也必须恢复到同一实例。
- 至少要把 `codexHome/instanceKey` 写进 thread runtime config；更稳妥的方案是把实例键补到 binding 或其他持久化恢复面，供 `unified/session_resolver.go` 重建 `ResumeSessionRequest`。
- `history_rollout.go`、`session_history.go` 与 `platform/historyjsonl` 的 codex fallback 都必须消费同一份实例信息，否则恢复成功但历史读取仍会串到默认 `~/.codex`。

## 实现注意事项

- 当前本地 app-server 实际启动命令是 `codex app-server --listen ...` 的 shell wrapper，而不是文档伪码里的 `serve`；实现时应保持 `cmd.Env` 注入 `CODEX_HOME`，不要把 home 直接拼进 shell 字符串。
- pool 不是把单例 `ServerManager` 简单换成 map 就结束，还要处理 lazy start、统一 stop、tool handler 传播、peer 进程只启动一次、PID registry 统一 close。
- 若去掉 driver 级 `serverURL` 缓存，runtime report 也要改成从本次选中的 session/manager 取 URL；否则 port 与 runtime 元数据会写错。

**Hermes 源码对照点**:
- `run_agent.py:708-766` — Agent 初始化，各种 base_url 和 key 分支
- `agent/anthropic_adapter.py:296-361` — 重复造轮子的 Client builder
