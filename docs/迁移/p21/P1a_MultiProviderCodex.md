# P1a: 多 Provider Codex 实例

## 目标

利用现有统一 Codex app-server 架构，通过显式的实例 identity 三元组（`codexHome` / `codexInstanceKey` / `codexModelProvider`）隔离接入 GLM-5.1、Qwen3.6、DeepSeek 等兼容 OpenAI 接口的模型；隔离边界是 app-server 进程，不是 thread/router。

## 现状校准

- `thread/start` 已有 `modelProvider` 与通用 `config` 透传，但 top-level `modelProvider` 仍承担 `codex|claude` provider 选择语义，不能复用成实例路由字段。
- `internal/provider/codexapp/module.go` 的 `ServerManager` 仍是单 shared app-server；只靠 session 级配置无法做到 `CODEX_HOME` 隔离。
- `internal/provider/codexapp/driver.go` 仍会缓存单个 `serverURL`；若不改成 start / resume 时按 identity 动态选择，driver 依然会退化成单实例。
- `internal/provider/codexapp/history_rollout.go` 仍通过 `os.UserHomeDir()` + `.codex/sessions/...` 搜 rollout，历史恢复会串到 legacy home。
- `internal/provider/codexapp/transport_process.go:224-245` 的真实本地启动路径目前还没有 `cmd.Env` 注入；文档伪码不能假装这件事已经存在。
- `ResumeSessionRequest` 当前只有 `ConfigOverride`，没有通用 `Config` 透传；`internal/provider/unified/session_resolver.go:127-136` 的 auto-resume 也是先经 binding lookup 再恢复 session，因此本期应把实例 identity 持久化到 binding，而不是指望 resume 时临时塞 generic config。

## 推荐改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 实例键解析 | `internal/provider/shared/config_helpers.go` | 新增 `ResolveCodexIdentity(config map[string]any) (..., error)`，严格解析 `codexHome/codexInstanceKey/codexModelProvider`，并返回对应 sentinel error |
| 启动载荷透传 | `internal/module/thread/start_session_helpers.go` | 继续复用 `ModelProvider + Config` 链路，但实例选择只能从 `req.Config` 的专用 key 读取，不新增 router 字段 |
| app-server 池化 | `internal/provider/codexapp/module.go` | 将单例 `ServerManager` 改为按 `instanceKey` 管理的池，每个 key 对应一个本地 app-server 进程 |
| 会话建连 | `internal/provider/codexapp/{driver.go,session.go}` | `StartSession/ResumeSession` 按 identity 取对应 server URL；不要在 `newDriver()` 冻结单个 `serverURL` |
| 环境注入 | `internal/provider/codexapp/transport_process.go` | `spawnLocal()` 显式注入 `CODEX_HOME=<path>`，不要把 home 拼进 shell 字符串 |
| Rollout 历史 | `internal/provider/codexapp/history_rollout.go`、`session.RolloutPath()`、`internal/platform/historyjsonl/*` | rollout 搜索目录必须从选中的 `codexHome` 反推，不能再写死 legacy home |
| 恢复链路 | `internal/store/binding/*`、`internal/provider/unified/session_resolver.go`、`internal/module/thread/*` | **拍板选择 binding 持久化为主**：扩 `agent_provider_binding` 身份字段，不把 resume 扩成 generic config |
| 设置链路 | 前端 settings / launch payload | 冻结 active codex instance 的设置存储与发包路径，把 identity 三元组注入 `payload.config.*` |

## identity 与缺失硬报错

- `codexHome`、`codexInstanceKey`、`codexModelProvider` 共同构成 codex instance identity；任一缺失都必须返回 `ErrCodexHomeRequired`、`ErrCodexInstanceKeyRequired`、`ErrCodexModelProviderRequired` 这类 sentinel error。
- `codexHome` 与 `codexInstanceKey` 必须一一绑定：同一 key 不能在不同请求里解析到不同 home；同一 home 被多个 key 指向时要么声明 alias，要么直接报错。
- **`codexHome` 统一 canonicalize**：解析前必须先做 home 展开（如 `~ -> os.UserHomeDir()`）与 `os.ExpandEnv`，再执行 `filepath.Clean` + `filepath.EvalSymlinks`；得到 canonicalized realpath 后再参与 pool 去重与一一绑定检测，避免同一物理目录被 `$HOME/.codex-providers/glm`、`/Users/demo/.codex-providers/glm` 与保留 symlink 的别名当成多个实例。
- legacy default-home 只能通过显式 feature flag / env opt-in 打开，例如 `CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1`；**禁止**把它写进默认解析链。
- `payload.config.*` 不能是完全开放 raw map：对 identity 三元组必须规定严格 key 名、兼容策略和 malformed payload 的 fail-fast 行为；unknown key、type mismatch、unsupported alias/provider/value 都必须硬报错。
- 持久化到 binding 的 `codex_home` 必须是 canonicalized realpath，而不是用户原始输入字符串。

## 环境注入伪码

```go
// 目标态伪码；当前真实 spawn 在 transport_process.go:224-245，尚无 cmd.Env 注入，
// 实施时需同步补上。
func (p *ServerPool) get(config map[string]any) (*ServerManager, error) {
    ident, err := shared.ResolveCodexIdentity(config)
    if err != nil {
        return nil, err
    }
    mgr := p.ensure(ident.InstanceKey)
    return mgr.startWithHome(ident.Home)
}

func (m *ServerManager) spawnLocal(home string) (*exec.Cmd, error) {
    cmd := exec.Command("sh", "-c", "ulimit -n ...; exec codex app-server --listen ...")
    cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
    return cmd, nil
}
```

## 恢复与持久化约束

- 本期拍板：**binding 持久化 instance identity 为主**。`agent_provider_binding` 扩字段保存 `codex_home` / `codex_instance_key` / `codex_model_provider`，作为 auto-resume 的权威恢复面。
- `ResumeSessionRequest` **不扩 generic Config**；现有 `ConfigOverride` 只保留给 runtime override，不承接实例 identity。若未来需要显式 resume override，另立提案处理。
- `internal/provider/unified/session_resolver.go:127-136` 当前是先 `GetByAgentID` 再 `autoResumeSession()`；因此 identity 丢在 binding 上，才能让重启后的 auto-resume 继续命中同一实例。
- `history_rollout.go`、`session_history.go` 与 `internal/platform/historyjsonl` 的 home 解析必须消费同一份 binding identity；否则 session 恢复对了，历史仍会串目录。
- `provider` 字段仍保持 `codex|claude` 这类 provider 语义；不要把它扩成 `codex:qwen` 这种混合字段。

## approvalPolicy=never 陷阱

- `approvalPolicy=never` 不等于全部自动通过。`internal/platform/rpc/approval_support.go:204-209` 当前只会在 `Kind=request_user_input` 且 policy 为 `never` 时自动批准。
- 因此 P1a v1 不能把多实例后台运行描述成统一无审批；必须白名单 provider / sandbox / tool 组合，并把超出白名单的场景直接拒收。

## 必测项

- `start -> binding 持久化 -> 进程重启 -> auto-resume -> history fallback` 必须验证仍命中同一实例与同一 `codexHome`。
- identity 三元组缺任一字段时必须命中硬报错，而不是 silent fallback。
- canonicalize 必须验证 `$HOME/...`、绝对路径与 symlink alias 最终落到同一 canonicalized realpath；`~` 若未被 home 展开必须直接报错，不能静默当普通字符串。
- `payload.config.*` 必须验证 unknown key / unsupported alias-provider / 错误类型都 fail fast。
- external `CODEX_APP_SERVER_URL` 模式必须验证不会被本地 manager / 二次选址覆盖。
- `approvalPolicy=never` 必须验证只对白名单 provider / sandbox / tool 组合放行；不能被误解为所有审批都自动通过。
