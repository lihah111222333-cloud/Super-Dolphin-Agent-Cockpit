# Dual-Provider MCP 启动配置 + 工具过滤预设（v3 修订版 r5）

> **给 Claude:** 必须使用 @执行计划 逐任务实现此计划。

> **修订历史:**
> - r1: 砍掉 `config/*` RPC 编排，改 config.toml 直写
> - r2: 修正 3 个阻断项（manifest seam / env 丢值 / TOML parser）
> - r3: 限定本地 app-server only + reload 后等 ready 栅栏 + Phase 2 降级
> - **r4: 修正最后 2 个阻断项** — ① manifest seam 显式扩 `startParams` + `startSessionConfig()` 补传 `config` 字段；② ready 栅栏改为 session 级 watcher，reload 前注册
> - **r4→r5: 修复 watcher 并发安全** — ① OnStartupStatus 幂等+非阻塞（finished 标志 + non-blocking send）；② session.mcpWatcher 用 mcpWatcherMu 保护读写

**目标:** 保持 `dto.MCPManifest` 作为 Claude/Codex 唯一的上层 MCP 模型。两个 provider 各用原生配置完成 MCP server 注入。

**架构:**
```
┌──────────────────────────────────────────────────────────────┐
│  Layer 1: 配置注入（Phase 1）                                  │
│  MCPManifest → provider 原生配置                               │
│  Claude: 写 JSON → --mcp-config（已有）                        │
│  Codex:  写 config.toml → reload → session watcher 等 ready   │
│          ↑ 仅限本地 app-server（transport.local == true）      │
├──────────────────────────────────────────────────────────────┤
│  Layer 2: 工具过滤预设（Phase 2）                              │
│  返回 BeforeDecision 的预设模板 + hooks 接线文档               │
└──────────────────────────────────────────────────────────────┘
```

**技术栈:** Go、BurntSushi/toml、Codex `config/mcpServer/reload` + `mcpServer/startupStatus/updated` 事件

---

## 范围 / 非目标

### 本期范围

**Phase 1 — 配置注入（本地 app-server only）**
- 扩 `startParams` + `StartRequest` + `startSessionConfig()` 补传 MCP 所需字段
- Codex driver 从 `req.Config` 构造 manifest（复用 Claude 模式）
- 写 config.toml（TOML parser 合并）→ reload → session watcher 等 ready → thread/start
- 仅本地 app-server（`transport.local == true`），外部模式 warn 跳过

**Phase 2 — 工具过滤预设模板（仅模板，不接线）**
- `ReviewerDecision()` / `WorkerDecision()` / `FullAccessDecision()` 返回 `mcp.BeforeDecision`
- 文档注释说明 hooks 接线方式，实际接线留下期

### 明确非目标
- 不支持外部 app-server 的 MCP 注入
- 不做 Phase 2 hooks 运行时接线
- 不做 ResumeSession 注入
- 不在 `thread/start` 传 undocumented `mcp*`
- 不覆盖用户已有 MCP key

### 延后事项
- 外部 app-server MCP 注入（需 server-side 控制面）
- Phase 2 hooks 运行时接线（subscribe + callback + scope）
- ResumeSession 注入
- 受管 MCP key cleanup / rollback

---

## 阻断项修复方案（r4 核心）

### 修复 A1: manifest seam 显式闭合

**问题:** `ManifestContext` 需要 `Env/AutoApprove/BinaryDir`，但 `startParams` / `StartRequest` / `startSessionConfig()` 都没有这些字段。

**修复:** 在 `startParams` 增加 `Config json.RawMessage` 透传字段，`startSessionConfig()` 把它解码合并进 config map。

**影响文件（4 个）:**

```
internal/module/thread/rpc_types.go   — startParams 加 Config 字段
internal/module/thread/rpc.go         — newStartHandler 传递 Config
internal/module/thread/contract.go    — StartRequest 加 Config 字段
internal/module/thread/start_session.go — startSessionConfig() 合并 Config
```

**具体改动:**

```go
// rpc_types.go — startParams 加字段
type startParams struct {
    // ... 已有字段 ...
    Personality string          `json:"personality,omitempty"`
    Prompt      string          `json:"-"`
    Config      json.RawMessage `json:"config,omitempty"` // +++ 通用配置透传
}

// contract.go — StartRequest 加字段
type StartRequest struct {
    // ... 已有字段 ...
    Personality string
    Config      map[string]any // +++ 通用配置透传
}

// rpc.go — newStartHandler 传递
func newStartHandler(svc Service) handler.Func {
    return rpc.StrictHandler(func(ctx context.Context, p startParams) (any, error) {
        result, err := svc.Start(ctx, StartRequest{
            // ... 已有字段 ...
            Personality: p.Personality,
            Config:      decodeConfigMap(p.Config), // +++ 解码透传
        })
        // ...
    })
}

// start_session.go — startSessionConfig 合并
func startSessionConfig(req StartRequest) map[string]any {
    cfg := map[string]any{}
    // ... 已有 putConfigString 调用 ...

    // +++ 合并通用 Config（env/auto_approve/binary_dir 等从这里来）
    for k, v := range req.Config {
        if _, exists := cfg[k]; !exists {
            cfg[k] = v
        }
    }

    if len(cfg) == 0 {
        return nil
    }
    return cfg
}
```

**验证:** 改完后 `req.Config` 会包含前端/orchestration API 传入的 `env`、`auto_approve`、`binary_dir` 等任意字段。
Claude driver 已有的 `stringMap(req.Config["env"])` 等取值逻辑自动生效，Codex 侧复用同一模式。

---

### 修复 A2: ready 栅栏改为 session 级 watcher

**问题:** `transport` 只有一个 ReadLoop handler，没有 subscriber 机制。`waitMCPReady(t *transport)` 的设计不成立——事件会在 reload response 返回前被 `onNotification` 吃掉。

**修复:** 在 `session` 上增加 `mcpReadyWatcher`，在 reload **之前**注册，`onNotification()` 里回调。

**设计原理:**
```
时序（修复后）:

1. newSession()      → startReadLoop() → 事件循环开始运转
2. 注册 watcher      → session.mcpReadyWatcher = newMCPReadyWatcher(names)
3. config/mcpServer/reload RPC
4. app-server 重载配置，启动 MCP servers
5. app-server 发送 mcpServer/startupStatus/updated 事件
6. ReadLoop → onNotification() → 检查 mcpReadyWatcher → 通知 ready channel
7. watcher.Wait() 返回 → 继续 thread/start
```

**关键时序保证:** watcher 在 step 2 注册，早于 step 3 的 reload。
即使事件在 reload response 返回前到达，`onNotification` 也会正确路由给 watcher。

**影响文件（3 个）:**

```
internal/provider/codexapp/mcp_config.go  — mcpReadyWatcher 定义 + writeCodexMCPConfig
internal/provider/codexapp/session.go     — 加 mcpReadyWatcher 字段 + onNotification 回调
internal/provider/codexapp/driver.go      — StartSession 编排
```

**具体设计:**


```go
// mcp_config.go — watcher 定义

// mcpReadyWatcher tracks MCP server startup status events.
// Thread-safe: OnStartupStatus called from read-loop goroutine,
// Wait called from StartSession goroutine.
type mcpReadyWatcher struct {
    expected map[string]bool   // name -> ready?
    done     chan error
    mu       sync.Mutex
    finished bool              // terminal completion 幂等保护
}

func newMCPReadyWatcher(names []string) *mcpReadyWatcher {
    expected := make(map[string]bool, len(names))
    for _, n := range names {
        expected[n] = false
    }
    return &mcpReadyWatcher{expected: expected, done: make(chan error, 1)}
}

// OnStartupStatus is called from session.onNotification for startupStatus events.
// Idempotent: after first terminal completion (all-ready / failed / cancelled),
// subsequent calls are no-ops. Uses non-blocking send to never block read loop.
func (w *mcpReadyWatcher) OnStartupStatus(name, status string) {
    w.mu.Lock()
    defer w.mu.Unlock()
    if w.finished {
        return // 幂等：已完成，忽略后续事件（含重复 ready/failed）
    }
    if _, tracked := w.expected[name]; !tracked {
        return
    }
    switch status {
    case "ready":
        w.expected[name] = true
        if w.allReady() {
            w.finish(nil)
        }
    case "failed", "cancelled":
        w.finish(fmt.Errorf("mcp server %s status: %s", name, status))
    }
}

// finish sends result to done channel exactly once (non-blocking).
// Must be called with w.mu held.
func (w *mcpReadyWatcher) finish(err error) {
    if w.finished {
        return
    }
    w.finished = true
    select {
    case w.done <- err:
    default:
    }
}

func (w *mcpReadyWatcher) allReady() bool {
    for _, ready := range w.expected {
        if !ready { return false }
    }
    return true
}

// Wait blocks until all servers ready, any failure, or context deadline.
func (w *mcpReadyWatcher) Wait(ctx context.Context) error {
    select {
    case err := <-w.done:
        return err
    case <-ctx.Done():
        return fmt.Errorf("mcp ready timeout: %w", ctx.Err())
    }
}
```

```go
// session.go — 加字段 + 线程安全 accessor + onNotification 回调

type session struct {
    // ... 已有字段 ...
    mcpWatcherMu sync.Mutex       // +++ 保护 mcpWatcher 的并发读写
    mcpWatcher   *mcpReadyWatcher // +++ nil 表示不监听
}

// setMCPWatcher 线程安全地注册/清理 watcher。
// 由 StartSession goroutine 调用。
func (s *session) setMCPWatcher(w *mcpReadyWatcher) {
    s.mcpWatcherMu.Lock()
    s.mcpWatcher = w
    s.mcpWatcherMu.Unlock()
}

// getMCPWatcher 线程安全地获取 watcher 快照。
// 由 read-loop goroutine（onNotification）调用。
func (s *session) getMCPWatcher() *mcpReadyWatcher {
    s.mcpWatcherMu.Lock()
    w := s.mcpWatcher
    s.mcpWatcherMu.Unlock()
    return w
}

func (s *session) onNotification(method string, params json.RawMessage) {
    s.noteReadActivity()
    if s.shouldSuppressTurnEvent(method, params) {
        return
    }
    raw := dto.RawProviderEvent{EventType: method, Data: params}
    method = strings.TrimSpace(method)

    // +++ MCP ready watcher 回调（在 dispatch 之前，确保不漏事件）
    // 先取 watcher 快照（线程安全），再回调（不持有 session 锁）
    if w := s.getMCPWatcher(); w != nil && isMCPStartupStatus(method) {
        name, status := extractStartupStatus(params)
        w.OnStartupStatus(name, status)
    }

    if !isApprovalBridgeMethod(method) || s.approvals == nil {
        s.dispatch(raw)
    }
    // ... 后续 switch 不变 ...
}

func isMCPStartupStatus(method string) bool {
    return method == "mcpServer/startupStatus/update" ||
           method == "mcpServer/startupStatus/updated"
}

func extractStartupStatus(params json.RawMessage) (name, status string) {
    var p struct {
        Name   string `json:"name"`
        Status string `json:"status"`
    }
    json.Unmarshal(params, &p)
    return p.Name, p.Status
}
```

```go
// driver.go — StartSession 编排（使用线程安全 accessor）

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
    s, err := newSession(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals)
    if err != nil {
        return nil, err
    }
    s.setRuntimeConfig(req.Config)
    s.setApprovalPolicy(resolveApprovalPolicy(req.Config))

    // === Phase 1: MCP 注入（仅本地 app-server） ===
    if s.transport.local {
        manifest := dto.BuildManifest(dto.ManifestContext{
            AgentID:     strings.TrimSpace(req.AgentID),
            CWD:         strings.TrimSpace(req.CWD),
            ThreadCaps:  cloneCaps(codexCapabilities),
            BinaryDir:   mcpResolveBinaryDir(req.CWD, req.Config),
            Env:         mcpStringMap(req.Config["env"]),
            AutoApprove: mcpConfigStringSlice(req.Config, "auto_approve", "autoApprove"),
        })
        managedNames := managedBinaryNames(manifest)
        if len(managedNames) > 0 {
            // 1. 注册 watcher（在 reload 之前！线程安全）
            watcher := newMCPReadyWatcher(managedNames)
            s.setMCPWatcher(watcher)

            // 2. 写 config.toml
            configPath := resolveCodexConfigPath()
            if err := writeCodexMCPConfig(configPath, manifest, req.CWD); err != nil {
                s.setMCPWatcher(nil)
                shared.LogIgnoredError(d.logger, "force stop on mcp write error", s.ForceStop())
                return nil, fmt.Errorf("mcp config write: %w", err)
            }

            // 3. Reload
            reloadCtx, cancel := withTimeout(ctx, 10*time.Second)
            _, reloadErr := s.transport.Call(reloadCtx, "config/mcpServer/reload", nil)
            cancel()
            if reloadErr != nil {
                s.setMCPWatcher(nil)
                shared.LogIgnoredError(d.logger, "force stop on mcp reload error", s.ForceStop())
                return nil, fmt.Errorf("mcp reload: %w", reloadErr)
            }

            // 4. 等 ready（30s 超时）
            readyCtx, readyCancel := withTimeout(ctx, 30*time.Second)
            readyErr := watcher.Wait(readyCtx)
            readyCancel()
            s.setMCPWatcher(nil) // 清理 watcher（线程安全）
            if readyErr != nil {
                shared.LogIgnoredError(d.logger, "force stop on mcp ready error", s.ForceStop())
                return nil, fmt.Errorf("mcp ready: %w", readyErr)
            }
        }
    }

    // === thread/start（MCP 工具已可用） ===
    result, err := startRemoteThread(ctx, s.transport, req)
    if err != nil {
        shared.LogIgnoredError(d.logger, "force stop failed on start error", s.ForceStop())
        return nil, err
    }
    s.setThreadID(result.threadID)
    if result.model != "" {
        s.setRuntimeConfigValue("model", result.model)
    }
    if result.cwd != "" {
        s.setRuntimeConfigValue("cwd", result.cwd)
    }
    if port := extractPort(s.transport.serverURL); port > 0 {
        s.setRuntimeConfigValue("port", port)
    }
    d.reportRuntime(s.agentID)
    return s, nil
}
```


---

## 配置生命周期（完整闭环）

```
StartSession (本地 app-server)
  │
  ├─ 1. newSession()                → spawnLocal() + startReadLoop()
  │                                    事件循环开始运转
  │
  ├─ 2. s.setMCPWatcher(watcher)   → 注册 watcher（reload 之前！）
  │
  ├─ 3. writeCodexMCPConfig()       → TOML parser 合并受管段
  │
  ├─ 4. config/mcpServer/reload RPC → 通知 app-server 从磁盘重载
  │
  ├─ 5. onNotification() 收到 startupStatus/updated
  │     → mcpWatcher.OnStartupStatus() 更新状态
  │     → all ready → watcher.done channel 放行
  │
  ├─ 6. mcpWatcher.Wait() 返回      → 清理 watcher
  │
  └─ 7. startRemoteThread()         → thread/start（MCP 工具已可用）
```

**时序保证:** watcher 在 step 2 注册，早于 step 4 的 reload。
即使 ready 事件在 reload response 返回前到达（step 5 早于 step 4 完成），
`onNotification` 也会正确路由给 watcher，不会漏事件。

---

## 只读参考

### 当前仓库
- **共享上层模型:**
  - `internal/dto/provider/manifest.go:17-60` — MCPBinary / MCPManifest / ManifestContext / BuildManifest
- **Claude 路径（Codex 参照）:**
  - `internal/provider/claudecli/driver.go:61-84` — 从 req.Config 构造 ManifestContext
- **Codex 路径:**
  - `internal/provider/codexapp/driver.go:91-115` — StartSession（注入点）
  - `internal/provider/codexapp/session.go:57-89` — newSession → startReadLoop
  - `internal/provider/codexapp/session.go:246-264` — onNotification（watcher 挂载点）
  - `internal/provider/codexapp/recovery.go:327-329` — runReadLoop 用 s.onNotification
  - `internal/provider/codexapp/transport.go:161-174` — newTransport（local 判定）
  - `internal/provider/codexapp/transport.go:306-336` — spawnLocal（t.local = true）
  - `internal/provider/codexapp/event_map.go:49-62` — logCodexMCPStartupStatus
- **上层 seam（需修改）:**
  - `internal/module/thread/rpc_types.go:23-36` — startParams（加 Config 字段）
  - `internal/module/thread/contract.go:34-48` — StartRequest（加 Config 字段）
  - `internal/module/thread/rpc.go:90-135` — newStartHandler（传递 Config）
  - `internal/module/thread/start_session.go:245-261` — startSessionConfig（合并 Config）
- **hooks 模型（Phase 2）:**
  - `internal/dto/mcp/hook.go:25-34` — BeforeDecision

---

## 代码预算

| 文件 | 目标 LOC | 硬上限 LOC | 约束 |
|---|---:|---:|---|
| **Phase 1 — 上层 seam** | | | |
| `internal/module/thread/rpc_types.go` | 2 | 5 | 加 Config 字段 |
| `internal/module/thread/contract.go` | 2 | 5 | 加 Config 字段 |
| `internal/module/thread/rpc.go` | 3 | 8 | 传递 Config + decodeConfigMap |
| `internal/module/thread/start_session.go` | 5 | 10 | 合并 Config map |
| **Phase 1 — Codex 注入** | | | |
| `internal/provider/codexapp/mcp_config.go` | 120 | 180 | TOML 合并 + watcher + helpers |
| `internal/provider/codexapp/mcp_config_test.go` | 130 | 200 | ≤10 个测试 |
| `internal/provider/codexapp/session.go` | 10 | 15 | mcpWatcher 字段 + onNotification 回调 |
| `internal/provider/codexapp/driver.go` | 30 | 45 | manifest + 编排流程 |
| `internal/provider/claudecli/transport_config_test.go` | 10 | 25 | guard test |
| `go.mod` / `go.sum` | — | — | BurntSushi/toml |
| **Phase 2** | | | |
| `internal/provider/toolfilter/presets.go` | 40 | 70 | 预设模板 |
| `internal/provider/toolfilter/presets_test.go` | 50 | 80 | ≤5 个测试 |

---

## 任务列表

### 任务 0: 扩上层 seam 补传 Config

修改 4 个文件，让 `thread/start` RPC 的任意 `config` 字段能透传到 provider 的 `req.Config`。

### 任务 1: TOML 写入器 + BurntSushi/toml 依赖

创建 `mcp_config.go`，用 TOML parser 做结构化合并。

### 任务 2: mcpReadyWatcher + session 回调

在 session 上加 watcher 字段，`onNotification` 里回调 startupStatus 事件。

### 任务 3: StartSession 编排（本地 app-server only）

构造 manifest → 注册 watcher → 写 config.toml → reload → wait ready → thread/start。

### 任务 4: Guard test

两个 provider 的注入不越界测试。

### 任务 5: 工具过滤预设模板

`ReviewerDecision()` / `WorkerDecision()` / `FullAccessDecision()` 返回 `mcp.BeforeDecision`。

---

## 最终验证

```bash
go test ./internal/module/thread -count=1
go test ./internal/provider/codexapp -count=1
go test ./internal/provider/claudecli -count=1
go test ./internal/provider/toolfilter -count=1
```

---

## 交付检查单

### Phase 1: 上层 seam
- [ ] `startParams` 加 `Config json.RawMessage`
- [ ] `StartRequest` 加 `Config map[string]any`
- [ ] `newStartHandler` 传递 Config
- [ ] `startSessionConfig()` 合并 Config（已有字段优先，不覆盖）
- [ ] 现有 thread/start 测试不 break

### Phase 1: Codex 注入
- [ ] `writeCodexMCPConfig` 用 TOML parser 结构化合并
- [ ] env 用 `[mcp_servers.<name>.env] KEY = "VALUE"` 子表
- [ ] 显式 `type = "stdio"`
- [ ] mcpReadyWatcher 在 reload **之前**注册（时序保证）
- [ ] `onNotification` 回调 watcher（`startupStatus/update` + `updated` 都接受）
- [ ] watcher.Wait() 等所有受管 server ready / failed / timeout
- [ ] mcpReadyWatcher.OnStartupStatus 幂等+非阻塞（finished 标志 + non-blocking send）
- [ ] session.mcpWatcher 用 mcpWatcherMu 保护并发读写（setMCPWatcher/getMCPWatcher）
- [ ] 仅本地 app-server（`transport.local == true`）
- [ ] 外部 app-server warn 跳过
- [ ] 注入失败 fatal error + ForceStop
- [ ] 只写受管 key，不碰用户 key
- [ ] 幂等更新

### Phase 2: 工具过滤预设
- [ ] `ReviewerDecision()` / `WorkerDecision()` / `FullAccessDecision()`
- [ ] 返回 `mcp.BeforeDecision`
- [ ] `shared_file_write` 不在 reviewer 预设
- [ ] 文档注释说明 hooks 接线方式
- [ ] 不做运行时接线（下期）

### 已知约束
- config.toml 并发 last-writer-wins（单实例限制）
- resume 吃到上次配置残留
- TOML parser 写回丢失注释/顺序
- AutoApprove 不投影到 Codex config.toml
- 外部 app-server 不注入 MCP
- Phase 2 预设不自动生效（需下期 hooks 接线）
- `config/mcpServer/reload` 是外部 app-server 能力，实施前需手动验证可用性
