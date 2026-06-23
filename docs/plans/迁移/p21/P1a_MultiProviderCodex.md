# P1a: 多 Provider Codex 实例

## 目标

利用现有统一 Codex app-server 架构，通过显式的实例 identity 三元组（`codexHome` / `codexInstanceKey` / `codexModelProvider`）隔离接入 GLM-5.1、Qwen3.6、DeepSeek 等兼容 OpenAI 接口的模型；隔离边界是 app-server 进程，不是 thread/router。

## 2026-06-23 适用性评估

结论：本方案方向仍适用于当前项目，但原始“推荐改动清单”里的大部分底层工作已经落地，不应重复实施。当前代码已经有严格的 Codex identity 解析、按 identity 隔离的 ServerPool、`CODEX_HOME` 环境注入、binding 持久化和不可变约束，以及 DAG agent node 的完整 identity 注入。

当前剩余的最小闭环是 managed `launch_agent` / `orchestration_launch_agent` 路径：`internal/platform/toolbridge/handler_launch_args.go` 的 `injectManagedLaunchArgs` 只继承 `codex_model_provider`，没有继承 `codex_home` 与 `codex_instance_key`。这会让从 GLM/Qwen/DeepSeek 等独立 Codex home 启动的子代理丢失完整实例身份。

本轮开发范围只做这一处闭环：

- 从父 binding 注入 `codex_home`、`codex_instance_key`、`codex_model_provider`。
- 保持现有 `setArgStringIfMissing` 语义，调用方显式传入的 launch args 不被覆盖。
- 补齐 toolbridge 相关测试，验证注入完整 identity 且不覆盖显式值。

非本轮范围：

- 不实现模型网关、计费路由、token pool 或管理后台。
- 不重做 ServerPool、binding migration、history rollout、环境白名单等已落地基础设施。
- 不改变 `provider` 字段的 `codex|claude` 语义。

详细设计见 `docs/superpowers/specs/2026-06-23-p1a-codex-identity-inheritance-design.md`。

## 原始现状校准（历史）

以下内容保留为方案形成时的背景材料。当前实现状态以上面的“2026-06-23 适用性评估”和源码为准。

- `thread/start` 已有 `modelProvider` 与通用 `config` 透传，但 top-level `modelProvider` 仍承担 `codex|claude` provider 选择语义，不能复用成实例路由字段。
- `internal/provider/codexapp/module.go` 的 `ServerManager` 仍是单 shared app-server；只靠 session 级配置无法做到 `CODEX_HOME` 隔离。
- `internal/provider/codexapp/driver.go` 仍会缓存单个 `serverURL`；若不改成 start / resume 时按 identity 动态选择，driver 依然会退化成单实例。
- `internal/provider/codexapp/history_rollout.go` 仍通过 `os.UserHomeDir()` + `.codex/sessions/...` 搜 rollout，历史恢复会串到 legacy home。
- `internal/provider/codexapp/transport_process.go:224-245` 的真实本地启动路径目前还没有 `cmd.Env` 注入；文档伪码不能假装这件事已经存在。
- `ResumeSessionRequest` 当前只有 `ConfigOverride`，没有通用 `Config` 透传；`internal/provider/unified/session_resolver.go:127-136` 的 auto-resume 也是先经 binding lookup 再恢复 session，因此本期应把实例 identity 持久化到 binding，而不是指望 resume 时临时塞 generic config。

## 原始推荐改动清单（历史）

以下清单中的多项已经在当前代码中落地。继续开发时应先对照源码与测试确认现状，再选择剩余最小缺口。

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 实例键解析 | `internal/provider/shared/config_helpers.go` | 新增 `ResolveCodexIdentity(config map[string]any) (..., error)`，严格解析 `codexHome/codexInstanceKey/codexModelProvider`，并返回对应 sentinel error |
| 启动载荷透传 | `internal/module/thread/start_session_helpers.go` | 继续复用 `ModelProvider + Config` 链路，但实例选择只能从 `req.Config` 的专用 key 读取，不新增 router 字段 |
| app-server 池化 | `internal/provider/codexapp/{module.go,server_pool.go [NEW]}` | 将单例 `ServerManager` 改为按 canonicalized `codexHome` 管理的池；pool 不设置容量上限，只保留 **空闲 shutdown（默认 30 min）**、**spawn 失败指数退避（base 5s / cap 2m）** |
| 会话建连 | `internal/provider/codexapp/{driver.go,session.go}` | `StartSession/ResumeSession` 按 identity 取对应 server URL；不要在 `newDriver()` 冻结单个 `serverURL` |
| 环境注入 | `internal/provider/codexapp/transport_process.go` | `spawnLocal()` 走 **allowlist-style env**：先把继承环境裁剪到 `PATH/HOME/USER/LANG/LC_*/TZ/SSL_CERT_*/TMPDIR` 等白名单，再**显式**追加 `CODEX_HOME=<canonicalized path>`；禁止 `append(os.Environ(), ...)` 直通宿主，避免宿主 `CODEX_*` 变量串到 child |
| Rollout 历史 | `internal/provider/codexapp/history_rollout.go`、`session.RolloutPath()`、`internal/platform/historyjsonl/*` | rollout 搜索目录必须从选中的 `codexHome` 反推，不能再写死 legacy home |
| 恢复链路 | `internal/store/binding/*`、`internal/provider/unified/session_resolver.go`、`internal/module/thread/*`、`migrations/0048_binding_codex_identity.sql`（P1b=0045、P3=0046、P0 candidate=0047 后的下一个可用编号；实施时按 `ls migrations/` 再次校准） | **拍板选择 binding 持久化为主**：扩 `agent_provider_binding` 身份字段 `codex_home / codex_instance_key / codex_model_provider`，并**同步扩展 `prevent_agent_provider_binding_rebind()` trigger** 把三个新字段也纳入 immutable 检查（和 `provider` / `provider_thread_id` 同级别）；不把 resume 扩成 generic config |
| 设置链路 | 前端 settings / launch payload | 冻结 active codex instance 的设置存储与发包路径，把 identity 三元组注入 `payload.config.*` |

## identity 与缺失硬报错

- `codexHome`、`codexInstanceKey`、`codexModelProvider` 共同构成 codex instance identity；任一缺失都必须返回 `ErrCodexHomeRequired`、`ErrCodexInstanceKeyRequired`、`ErrCodexModelProviderRequired` 这类 sentinel error。
- `codexHome` 与 `codexInstanceKey` 必须一一绑定：同一 key 不能在不同请求里解析到不同 home；同一 home 被多个 key 指向时要么声明 alias，要么直接报错。
- **`codexHome` 统一 canonicalize**：解析前必须先做 home 展开（如 `~ -> os.UserHomeDir()`）与 `os.ExpandEnv`，再执行 `filepath.Clean` + `filepath.EvalSymlinks`；得到 canonicalized realpath 后再参与 pool 去重与一一绑定检测，避免同一物理目录被 `$HOME/.codex-providers/glm`、`/Users/demo/.codex-providers/glm` 与保留 symlink 的别名当成多个实例。
- legacy default-home 只能通过显式 feature flag / env opt-in 打开，例如 `CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1`；**禁止**把它写进默认解析链。
- `payload.config.*` 不能是完全开放 raw map：对 identity 三元组必须规定严格 key 名、兼容策略和 malformed payload 的 fail-fast 行为；unknown key、type mismatch、unsupported alias/provider/value 都必须硬报错。
- 持久化到 binding 的 `codex_home` 必须是 canonicalized realpath，而不是用户原始输入字符串。
- **`codexHome` 目录不存在时直接 `ErrCodexHomeNotFound`**：`EvalSymlinks` 遇 `ENOENT` 必须 fail fast；**禁止**自动 `mkdir -p`，也禁止 fallback 到默认 home。目录应由 host / installer 预先准备。
- **binding identity 三字段一旦写入即不可变**：`agent_provider_binding.codex_home / codex_instance_key / codex_model_provider` 必须加入 `prevent_agent_provider_binding_rebind()` trigger 的 immutable 列表；若业务要切换实例，只允许"新建 agent + 新 binding"，不允许原地 UPDATE。测试必须覆盖 UPDATE 这三列会被 trigger 拒绝。

## 环境注入伪码

```go
// 目标态伪码；当前真实 spawn 在 transport_process.go:217-264，尚无 cmd.Env 注入，
// 实施时需同步补上 allowlist env + 池化 + spawn backoff。
var codexEnvAllowlist = []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"}

func (p *ServerPool) get(ctx context.Context, config map[string]any) (*ServerManager, error) {
    ident, err := shared.ResolveCodexIdentity(config) // 已在阶段 0 冻结
    if err != nil {
        return nil, err
    }
    // ident.Home 已 canonicalized；不存在必须 ErrCodexHomeNotFound
    mgr, err := p.getOrSpawn(ident) // 含 idle shutdown + spawn backoff
    if err != nil {
        return nil, err // 可能为 ErrSpawnBackoff
    }
    return mgr.startWithHome(ctx, ident.Home)
}

func (m *ServerManager) spawnLocal(ctx context.Context, home string) (*exec.Cmd, error) {
    cmd := exec.CommandContext(ctx, "sh", "-c", "ulimit -n ...; exec codex app-server --listen ...")
    cmd.Env = buildAllowlistedEnv(codexEnvAllowlist, map[string]string{"CODEX_HOME": home})
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
- binding immutable trigger：验证对 `codex_home / codex_instance_key / codex_model_provider` 任一列做 UPDATE 必被 trigger 拒绝；旧字段（agent_id / provider / provider_thread_id）行为不回退。
- `codexHome` 目录不存在：验证启动期 / resolve 期都返回 `ErrCodexHomeNotFound`，**不**自动 mkdir，也**不** fallback 到默认 home。
- ServerPool 生命周期：验证无容量上限；空闲 manager 在 idle 超时后被回收；spawn 失败命中指数退避，短时间内重复 get 不会连打底层进程。
- allowlist env：验证 `cmd.Env` 不含宿主 `CODEX_XXX` / `PATH` 以外未声明变量；`CODEX_HOME` 必定由 pool 注入为 canonicalized realpath。
