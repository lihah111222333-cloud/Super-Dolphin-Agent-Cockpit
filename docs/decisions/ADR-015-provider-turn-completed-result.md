# ADR-015 v4.1：codex/claude provider TurnCompleted.Result 补完（C1 + C2）

> 状态：✅ Accepted（v4.1 reviewer 二审通过 + 2026-05-12 实装落地）| 日期：2026-05-12 | 决策者：项目维护者
>
> **实装落地说明**（2026-05-12 W-C1 + W-C2 worker 并行实装）：
> - **C1 codex 累加器**：4 commit `2a392e61`/`5dd5486e`/`026a9cce`/`3c115ebf`。新建 `internal/provider/codexapp/turn_output_accumulator.go` + `session_approval.go` sniff + `event_map.go` 4 字段补完 + 3 处 cleanup hook + 2 个单测文件（10 文件 / +716 / -17）。贴近 v4.1 §2.1 描述；grep 揭出 2 处实际与 ADR 描述偏差已修（事件 method 字面量 3 种 + encodePayload 函数不存在）。
> - **C2 claude e2e 脚手架**：2 commit `b2220bb7`/`fe5572b0`。实测揭出 §2.4 情况 A（CLI 不截断，3KB gotLen=4509 纯 ABC）；8KB/16KB 未拿到证据（模型拒绝 / timeout）立 H12 follow-up。
> - **文档漂移修**：`4a2cba3f` 修正 §2.1 + §2.2 + §4 共 4 处 `failTurns` 文件名（`factory.go:178-188` → `session_dispatch.go:182-196`）。
> 相关：C-A 实施计划 §2.1 + §2.2（`docs/plans/dag-lifecycle-c-a-implementation.md`）/ F1.x lifecycle 审计 §8.1 实证 #1+#2（`docs/design/F1-lifecycle-audit-2026-05-12.md`）/ ADR-016（C3 auto stop，配套独立 ADR）/ ADR-017（A1 DAG subscriber 消费 ev.Result）
> 编号说明：ADR-015 编号被 v1/v2/v3 占用过，v1-v3 从未 git-tracked 已删；本 v4 是基于 C-A 路径的全新决策记录。

## 1. 背景

C-A 实施计划阶段 C 的核心前置：**让 codex / claude 两侧 provider 在 TurnCompleted 事件中正确携带 child agent 的完整回复内容**。

当前缺陷（4 轮实证已核）：

- **codex 端**（`internal/provider/codexapp/event_map.go:171-177`）：构造 TurnCompleted 时**只填 4 个字段**（Success / Error / Status / Reason），Result/Summary/Message/StopReason **全是零值**。真实 agent 回复内容流转在 `TurnOutputDelta` 流式事件里（stream="message" + Delta）。
- **claude 端**（`internal/provider/claudecli/event_map.go:130`）：用 `dataString(raw.Data, "result")` 直读 raw JSON 的 `result` 字段，provider 层**不截断**。但 claude CLI 二进制（`--output-format stream-json`）实际 result 字段对长内容的截断行为**代码层无法核**。

没有 Result 字段携带完整回复 → DAG agent 节点下游消费方（A1 subscriber + A2 outputs）拿不到真产出 → multi-agent-node DAG 链路断链 → M3 验收硬阈值（≥ 10 节点 DAG 跑通）无法达成。

## 2. 决策

### 2.1 C1（codex）— session 层 TurnOutputDelta 累加器

**挂载点**（v4.1 reviewer 修正）：`internal/provider/codexapp/session_approval.go:306-328 onNotification()` 入口，**在 `shouldSuppressTurnEvent`（line 317-321）检查之前**。

> **v4 错误回顾**：v4 草案把挂载点放在 `session_dispatch.go:13-33 dispatch()`。Explorer A 漏看了 `session_approval.go:317-321` 在 `s.dispatch(raw)` 之前已经做了 suppress：被抑制的 `turn/completed` 直接 return（line 320），**根本走不到 dispatch**。同时 `session_dispatch.go:140 forceCompleteTurn` 自己合成 turn/completed 走 dispatch 但 payload 没 result，累加器在合成场景下也漏触发。v4.1 改为 onNotification 入口前 sniff，覆盖所有路径。

**多点清理 hook**（v4.1 新增 — 避免 buffer 泄漏）：

| 位置 | 清理时机 | 原因 |
|---|---|---|
| `session_dispatch.go:103-118 takeTurn` | 取走 turn 句柄时 | 正常完成路径 |
| `session_dispatch.go:182-196 failTurns` | 异常退出所有 turn | shutdown 路径 |
| `recovery.go:303-316 applyReplayedTurn` | replay 时旧 turn-id `delete(s.turns, snapshot.providerID)` 同步清旧 buffer | provider 自身 replay 会分配新 turn-id（line 296 `turn/start`），旧 turn-id 的 buffer 必须显式清，否则泄漏到 shutdownSession |

**实现路径**：

```go
// session.go 新增字段
type session struct {
    // ... 现有字段
    turnOutputAccumulator map[string][]string  // per-turn-id buffer（key = turn UUID）
    accumulatorMu         sync.Mutex           // 独立 mutex（不复用 s.mu 避免死锁）
}

// session_approval.go onNotification() 入口（在 line 317 suppress 检查之前）：

// (a) sniff TurnOutputDelta 流并累加 — 在 suppress 之前避免漏抓
if isTurnOutputDeltaEvent(raw.Method) {
    payload := decodePayload(raw.Data)
    turnID := payloadTurnID(payload)
    stream := stringValue(payload, "stream")
    delta := stringValue(payload, "delta", "content")
    // 只累加 stream="message"（agent 回复内容；reasoning/stdout 不进 Result）
    if turnID != "" && delta != "" && stream == "message" {
        s.appendTurnOutputDelta(turnID, delta)  // 内含 mutex + 1MB 硬 cap 检查
    }
}

// (b) TurnCompleted 时 merge 回填 payload — 同样在 suppress 之前
if isTurnTerminalEvent(raw.Method) {
    payload := decodePayload(raw.Data)
    turnID := payloadTurnID(payload)
    if merged, truncated := s.consumeTurnOutputAccumulator(turnID); merged != "" {
        payload["result"] = merged
        if truncated {
            payload["truncated"] = true  // v4.1 硬 cap 标志（详 §2.2）
        }
        raw.Data = encodePayload(payload)
    }
}
```

**清理 hook 实现**（在三处加 `s.dropTurnOutputAccumulator(turnID)`）：

```go
// session_dispatch.go:103-118 takeTurn 末尾
func (s *session) takeTurn(providerID string) *turnHandle {
    s.mu.Lock(); defer s.mu.Unlock()
    h := s.turns[providerID]
    delete(s.turns, providerID)
    s.dropTurnOutputAccumulator(providerID)  // v4.1 新增
    return h
}

// session_dispatch.go:182-196 failTurns 内每条 turn 清理
// recovery.go:303-316 applyReplayedTurn 删除旧 providerID 时清理
```

**`event_map.go:171-177 TurnCompleted` 改动具化**（v4.1 — 明列新增 4 字段）：

```go
// 当前 (v4 之前)：只填 4 字段
return turndto.TurnCompleted{
    TurnHeader: header,
    Success:    turnTerminalSuccess(eventType, payload),
    Error:      stringValue(payload, "error", "message", "reason"),
    Status:     stringValue(payload, "status"),
    Reason:     stringValue(payload, "reason"),
}

// v4.1 改为：新增 4 字段读 payload（payload 已由 session 累加回填）
return turndto.TurnCompleted{
    TurnHeader: header,
    Success:    turnTerminalSuccess(eventType, payload),
    Error:      stringValue(payload, "error", "message", "reason"),
    Status:     stringValue(payload, "status"),
    Reason:     stringValue(payload, "reason"),
    // === v4.1 新增 ===
    Result:     stringValue(payload, "result"),      // 由 session 累加回填
    Summary:    stringValue(payload, "summary"),     // codex 若提供
    Message:    stringValue(payload, "message"),     // codex 若提供
    StopReason: stringValue(payload, "stop_reason"), // codex 若提供
}
```

**DTO 字段位**已存在（`internal/dto/turn/event.go:11-21`，无 DTO 改动）。

### 2.2 C1 拍板决定（v4.1 修订）

| 拍板项 | 决定 | 理由 |
|---|---|---|
| **挂载点** | `session_approval.go:306-328 onNotification` 入口前（在 suppress 检查之前）| v4.1 修正：v4 错放 dispatch；suppress 路径绕过 dispatch；forceCompleteTurn 合成 turn/completed 时 payload 无 result。onNotification 是真正的唯一入口 |
| **累加什么 stream** | 仅 `stream="message"` | reasoning / stdout 不属于 agent 回复内容；下游 DAG subscriber 需要 agent 最终回答；reasoning 仍通过 TurnOutputDelta 订阅，不丢（详 §7 Q1） |
| **buffer 内存上限** | **1MB 硬 cap + truncated=true flag**（v4.1 修正） | v4 软上限语义模糊；高频事件（每 token / 每 chunk）需明确边界。硬 cap 超限即停止累加 + payload 标 truncated=true 让下游知道内容不完整 |
| **释放策略** | TurnCompleted 触发立即清空对应 turn-id | turn 终态是明确点；lazy / TTL 增复杂度无收益 |
| **多 turn 并发** | per-turn-id map（key = providerID UUID）+ 自带 mutex | providerID 由 codex CLI 通过 `turn/start` RPC 返回（`session.go:300-308`），同 session 全局唯一；多 goroutine（tool call / approval）可能并发 onNotification |
| **session 生命周期清理** | `session_dispatch.go:182-196 failTurns` 内对每条 turn 调 `dropTurnOutputAccumulator(turnID)` | failTurns 是 shutdownSession 的子步骤；每条 turn 句柄清空时同步清 buffer |
| **正常 turn 完成清理** | `session_dispatch.go:103-118 takeTurn` 末尾调 `dropTurnOutputAccumulator(providerID)` | turn 句柄从 map 删除时同步清 buffer，避免 turn 完成后 buffer 残留 |
| **recovery 路径清理**（v4.1 新增） | `recovery.go:303-316 applyReplayedTurn` 删除 `snapshot.providerID` 时调 `dropTurnOutputAccumulator` | provider 自身 replay 会分配新 turn-id（line 296）；旧 providerID 的 buffer 必须显式清，否则泄漏到 shutdownSession |
| **跨 turn 复用 turn-id** | 不可能 | takeTurn 已 delete + recovery 路径已 delete + applyReplayedTurn 分配新 providerID；buffer 不会被新 turn 复用 |

### 2.3 C2（claude）— 实测验证 + 必要补完

claude 路径有不确定性：代码层假设 CLI 完整输出（streamEvent.Result 是 string 无大小限制），但**无 fixture 或注释佐证** CLI 二进制对长 result（>1KB）的真实行为。

**两段式落地**：

#### 步骤 0：e2e 实测验证（落码前必做）

依据 Explorer B 调研：项目有 `image_block_e2e_test.go` (`//go:build e2e_vision`) + `dream_executor_manual_test.go` (`//go:build manual`)，但**没有针对 TurnCompleted.Result 长内容的专项 e2e**。

**新建测试**：`internal/provider/claudecli/turn_result_long_e2e_test.go`（`//go:build e2e_claude`），范式参照 `image_block_e2e_test.go`：

- 真启 claude CLI（依赖环境 + API key + 网络稳定性）
- 让 spawned agent 回复一段 3KB / 8KB / 16KB 的字符串（用 prompt 强制生成）
- 抓取 TurnCompleted.Result 长度 + 内容 hash 比对
- 验证：完整 vs 截断

**测试运行命令**：`go test -tags=e2e_claude ./internal/provider/claudecli/ -run TestTurnResultLong_NotTruncated -v -count=1`

#### 步骤 1：分支决策（基于步骤 0 实测结果）

**情况 A：CLI 不截断（result 字段完整携带）**

- C2 落地 = **0 行 provider 代码改动**（仅保留新建的 e2e 测试作为 regression guard）
- 工程量：~80 行（e2e 测试 + fixture）

**情况 B：CLI 截断（result 是 preview / 长度截断）**

- 复用 C1 的**接口语义**（per-turn 累加器 + 在终态合并回填 payload），但**不能复用代码** — Explorer B 揭示 claude 走 CLI stdout JSONL（不是 codex 的 websocket JSON-RPC），事件传输层不同构
- claude 接入点：`session_events.go:32-34 applyRaw()`（逐行调用 decodeClaudeLine 的中央分发器）
- 累加什么：`assistant:message_delta` 事件（`factory.go:155-160`）的 `delta` 字段（每 content block 一个，不是每 token）
- session 字段：`session.go:57 activeTurn` 是单活跃 turn 不是 map — 累加器可以挂在 turnHandle 上而不是 session.turns map，结构更简洁
- 工程量：~150-200 行 + ~80 行 e2e

### 2.4 C2 拍板决定

| 拍板项 | 决定 | 理由 |
|---|---|---|
| **e2e 测试基础设施** | 新建 `turn_result_long_e2e_test.go` (`//go:build e2e_claude`) | 现有 vision / dream e2e 框架不覆盖长 result；e2e_claude tag 与现有 e2e_vision tag 平行 |
| **实测内容长度** | 3KB / 8KB / 16KB 三档 | 跨越 ADR-006 4KB cap + M3 硬阈值「> 4KB」+ 超 H7 summarization 阈值 |
| **实测时机** | ADR-015 v4 起草完成后、C2 实施前 | 决定情况 A vs 情况 B 分支 |
| **情况 A 落地** | 0 行 provider 代码 + 仅保留 e2e 作 regression guard | claude CLI 行为如果合契约就不要徒增代码 |
| **情况 B 接入点** | `session_events.go:32-34 applyRaw()` | 中央分发器，与 codex `session.dispatch()` 角色对称 |
| **情况 B 累加器位置** | 挂在 `session.go:57 activeTurn turnHandle` 上 | claude 是单活跃 turn 模型，无需 per-session map |

## 3. 与上下游的边界（v4.1 修订）

### 3.1 与上游订阅消费方的边界（**意外利好**）

v4.1 reviewer 揭出：`ev.Result` 已经在被消费但当前 codex 侧零值。具体证据：

| 消费方 | 位置 | 当前行为 |
|---|---|---|
| `hook_consumer.go:301 handleTurnCompleted` | `report := platformshared.FirstTrimmed(ev.Result, ev.Summary, ev.Message)` | codex 侧 ev.Result/Summary/Message 全是零值 → fallback 到空字符串 |
| `notify/turn.go:209 buildTurnCompletedBody` | 同上 FirstTrimmed 逻辑 | 同上 |
| `cmd/mcp-orch/orchestration/service.go:269 TurnCompleted 订阅` | 调用 `handleTurnCompletedEventWithCtx` → `svc.CompleteTurn(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)` | 不消费 Result，仅 agent runtime 状态推进 |

**含义**：C1 落地即激活 hook_consumer + notify 已有的消费链路（用户 UI 立刻看到 codex agent 的回复 report），不需要任何 service 层 / hook_consumer 改动。

### 3.2 与 C-A 计划其他阶段的边界

| 阶段 | 边界 |
|---|---|
| **C3（auto stop）** | 由 ADR-016 单独决定。C3 决策点正交于 C1/C2（C1/C2 改 provider 内部累加，C3 改 service 调用层 + stop API + threadID→agentID 反查路径），合并起草不增加效率反而切换 context |
| **A1（DAG subscriber）** | ADR-017 定。subscriber 消费 ev.Result，依赖 C1+C2 的输出格式契约：完整字符串 / UTF-8 / 不带终止符 |
| **A2（F1.3 outputs 重做）** | ADR-018 定。A2 处理真实输出物化、`node.result` / sharedfile 写入策略与 ADR-006 4KB cap；不做通用 jsonb merge / `_handshake` / 隐式 fallback。C1/C2 仅保证 ev.Result 字段完整可用 |

## 4. 落地范围（v4.1 工程量上调）

> **v4.1 reviewer 修订**：v4 估算 C1 ~220 行被 reviewer B 揭出"严重偏低"。基于 F1.5（spawning_thread_id 改造，commits `f111c12b` +255、`edc22076` +502、`2c2e0044` +290 等共 ~1100 行 / 8 commit）历史实证，类似规模的"加字段 + sqlc / store + executor 接线 + 单测 + 并发测"工程量翻倍。v4.1 把 C1 / C2-情况B 估算上调 ~50%。

### 4.1 C1 工程量（codex，v4.1 修订）

| 改动 | 文件 | 行数（v4.1）|
|---|---|---|
| session.go 新增 `turnOutputAccumulator` map + mutex 字段 + 3 个辅助函数（appendDelta / consumeAccumulator / dropAccumulator）| `internal/provider/codexapp/session.go` | +35 |
| session_approval.go onNotification 加 sniff + merge 回填（v4.1 改 dispatch → onNotification）| `internal/provider/codexapp/session_approval.go:306-328` | +60 |
| 辅助函数 `isTurnOutputDeltaEvent` + payload helper（复用 event_map.go 已有 stream 分类逻辑，避免双份漂移） | `internal/provider/codexapp/factory.go` 或新 helper.go | +20 |
| event_map.go 改 TurnCompleted 构造加 4 字段读取 | `internal/provider/codexapp/event_map.go:171-177` | +15 |
| 多点清理 hook：takeTurn + failTurns + applyReplayedTurn | `session_dispatch.go:103-118` + `session_dispatch.go:182-196` + `recovery.go:303-316` | +30 |
| 单测 `session_accumulator_test.go`（覆盖 sniff / merge / 三处清理 / suppress 绕过场景）| 新建 | +150 |
| 并发测 `session_accumulator_concurrent_test.go`（多 goroutine 同 turn 不同 stream 交错；recovery 复用场景）| 新建 | +90 |

**C1 小计（v4.1）**：**~320-380 行 / 3-4 commit**（v4 估算 ~220 行偏低 ~150 行）

### 4.2 C2 工程量（claude，v4.1 修订）

| 阶段 | 改动 | 行数（v4.1）|
|---|---|---|
| 步骤 0 e2e 基础设施 | `internal/provider/claudecli/turn_result_long_e2e_test.go`（新建，`//go:build e2e_claude` tag） | ~80 |
| **情况 A** | provider 代码 0 行 + **单测兜底**（v4.1 新增 — CI 不跑 e2e_claude 时的 regression guard） | 0 + ~30 单测 |
| **情况 B** | session_events.go 加 applyRaw sniff + turnHandle 累加器字段 + event_map 改（v4.1 上调：turnHandle 改动入侵已有结构，多处持锁路径需同步） | **~220-280** |
| **情况 B** 单测 | 新建 session 累加器单测 + 并发测 | +120 |

**C2 小计**（v4.1）：
- **情况 A**：~110 行 / 1-2 commit（e2e + 单测兜底）
- **情况 B**：**~420-480 行 / 3-4 commit**（v4 估算 ~310-360 偏低 ~110 行）

### 4.3 总计（v4.1 修订）

- **C1 + C2（情况 A）**：**~430-490 行 / 4-6 commit**
- **C1 + C2（情况 B）**：**~740-860 行 / 6-8 commit**

### 4.4 与 C-A 实施计划工程量数字同步

C-A 计划 v2.1 §1 总览表 C1 行写 ~250 行，§9 写 C1 ~250 行；ADR-015 v4.1 修订到 ~320-380 行。**ADR-015 落码前需同步 C-A 计划工程量数字**（避免 reviewer C2 类型的漂移）。

### 4.5 commit 粒度（v4.1 — prefer-small-commits）

按 reviewer 建议拆 4 commit：

1. `feat(codex/session): 加 turnOutputAccumulator + 3 个 helper 函数`
2. `feat(codex/session_approval): onNotification 加 sniff + merge 回填 payload`
3. `feat(codex/event_map): TurnCompleted 加 Result/Summary/Message/StopReason 字段读取`
4. `feat(codex): takeTurn + failTurns + applyReplayedTurn 清理 hook + 单测 + 并发测`

## 5. 验收（v4.1 强化）

### 5.1 单测

- C1：mock TurnOutputDelta 序列 + TurnCompleted → 断言 ev.Result = 拼接结果
- C1 并发：多 goroutine 并发 onNotification 同 turn-id → 断言无 race / 累加结果可重复
- C1 边界（v4.1 改硬 cap）：单 turn buffer 超 1MB → **`truncated=true` flag 写入 payload** + log warn + 后续 delta 丢弃（不 panic）
- C1 suppress 场景（v4.1 新增）：suppress 路径绕过 dispatch 时，sniff 仍在 onNotification 入口前触发 → 断言累加器仍正常工作
- C1 recovery 场景（v4.1 新增）：mock `applyReplayedTurn(oldID → newID)` → 断言旧 turn-id buffer 被清，新 turn-id 累加器全新
- C2：mock claude streamEvent → 断言 dataString 读取行为不变

### 5.2 端到端

#### C1
mcp-orch 启 codex spawned agent + first_turn = "回复你好" → 观察事件 bus 上 TurnCompleted.Result == "你好"

#### C2 — e2e_claude build tag（v4.1 明文策略）

**CI 策略**（v4.1 新增 — 与 `e2e_vision` 相同的本地 manual gate 模型）：
- `e2e_claude` tag 是**本地 manual gate**（项目搜不到 `.github/workflows` 跑 e2e_* tag 的证据）
- 不进 CI；落码 commit message 写明"本地手测命令 + 期望输出"作为 manual verification protocol
- C2 情况 A 的 regression guard 由**单测**（§4.2 +30 行）提供，不依赖 e2e

**测试设计**（v4.1 — 改进自 reviewer 建议）：

```go
// 测试运行命令：
// go test -tags=e2e_claude ./internal/provider/claudecli/ -run TestTurnResultLong -v -count=1

func TestTurnResultLong_3KB(t *testing.T) {
    // prompt 强制配方：让 claude 输出确定长度字符串
    prompt := `Output exactly this string and nothing else: ` + strings.Repeat("A", 3072)

    result := runClaudeAgent(t, prompt)

    // 断言（v4.1 — hash 前缀 + 长度容差，不依赖精确长度）
    require.Greater(t, len(result.Result), 3000, "result 应至少 3000 字符")
    require.Less(t, len(result.Result), 4000, "result 上限容差 4000 字符")

    // hash 前 1024 字节防截断：完整 A x 1024 的 hash
    expectedPrefix := sha256.Sum256([]byte(strings.Repeat("A", 1024)))
    actualPrefix := sha256.Sum256([]byte(result.Result[:1024]))
    require.Equal(t, expectedPrefix, actualPrefix, "前 1024 字节内容必须完整")
}

// 同理 _8KB / _16KB 三档
```

**失败回归路径**：
- 若 3KB 通过 / 8KB 失败 → claude CLI 在某长度截断 → 走情况 B（落 ~220-280 行 + ~120 行单测）
- 若 16KB 通过 → 情况 A 落地（仅 e2e + ~30 行单测兜底）

### 5.3 manual verification protocol（落 main 必须包含的 commit message body）

```
本地手测命令：
  cd internal/provider/claudecli
  CLAUDE_API_KEY=xxx go test -tags=e2e_claude -run TestTurnResultLong -v -count=1

期望输出：
  PASS: TestTurnResultLong_3KB (xx ms)
  PASS: TestTurnResultLong_8KB (xx ms)
  PASS: TestTurnResultLong_16KB (xx ms)
```

## 6. 不做的事

- **不**改 transport 层（`transport.go` / `transport_process.go`）— Explorer A 已确认 transport 是单连接复用不绑 turn
- **不**累加 reasoning / stdout stream — 那不是 agent 回复内容
- **不**做 backpressure / drop policy — 1MB 软上限即可，超限场景需要 H 阶段 H7 summarization 介入
- **不**改 unified dispatcher — payload 回填在 provider 内部完成，对外接口稳定
- **不**改 service 层 / hook_consumer — TurnCompleted 现有订阅链路（`service.go:269` + `event_relay.go:50-56`）不需要任何调整
- **不**碰 C3 auto stop — 独立 ADR-016 处理

## 7. 开放问题与决策项（v4.1 整理）

### 7.1 已升为决策项（v4.1 — reviewer 修订）

- **D1（原 Q1，升决策项）**：reasoning 流不算 agent 回复 — 这是**设计意图**。
  - 用户场景：DAG subscriber 消费的是 agent 最终回答（落进下游节点 inputs.from_nodes），reasoning 是 codex 模型的思考过程
  - reasoning 内容**仍通过 TurnOutputDelta(stream="reasoning") 完整流出**给其他订阅者（UI / hook_consumer），**不丢失**
  - ev.Result 不含 reasoning 是设计取舍，不是疏漏
  - 落码时通过端到端测验证 reasoning 与 message 流的分离

- **D2（原 Q2，升决策项）**：codex turn-id 复用场景已明确：
  - 真实路径：`recovery.go:303-316 applyReplayedTurn` 在 `delete(s.turns, snapshot.providerID)` 时**provider 层自己**调 `turn/start`（line 296）分配新 providerID
  - 旧 providerID 的累加器 buffer 必须**显式清**（v4.1 §2.1 加 hook）
  - 不存在"复用同一 turn-id 起新 turn"的场景

### 7.2 落码时已确认

- **Q3（已确认）**：CI 环境**不跑** `e2e_claude` tag（v4.1 §5.2 已确认 — 与 `e2e_vision` 同样是本地 manual gate）。落 main 时 commit message 必须包含 manual verification protocol（§5.3）。
- **Q4（已对齐）**：spawned agent multi-turn 场景下，ev.Result 每个 turn 独立（v4.1 §2.2 决定）；A1 subscriber（ADR-017）按"first turn 完成 = node 完成"消费，与本决策一致。

## 8. 变更记录

- 2026-05-12 v4.1（reviewer 二审修订）：吸收两份独立 reviewer 反馈（8 P0 + 3 P1 + 2 P2/P3）：
  - **A-P0-1**：挂载点改为 `session_approval.go:306-328 onNotification` 入口前（在 suppress 检查之前）— v4 错放 dispatch 因为 Explorer A 漏看了 `session_approval.go:317-321 shouldSuppressTurnEvent` 在 dispatch 之前 + `forceCompleteTurn` 合成 turn/completed 走 dispatch 但无 result
  - **A-P0-2**：Q2 turn-id 复用路径纠正 — `recovery.go:303-316 applyReplayedTurn` 是 provider 层自己 `turn/start`，不是 mcp-orch 分配；加 buffer 清理 hook 防泄漏
  - **A-P0-3**：event_map.go §2.1 改动具化 — 明列新增 Result/Summary/Message/StopReason 4 字段引用
  - **A-P0-4**：1MB **软上限改硬 cap** + 超限 `truncated=true` flag 进 payload；后续 delta 丢弃避免热路径 unbounded 写入
  - **B-P0-1**：C1 工程量 ~220 → **~320-380 行 / 3-4 commit**（基于 F1.5 历史实证 commits `f111c12b` +255、`edc22076` +502 等 ~1100 行 / 8 commit）
  - **B-P0-2**：C2 情况 A 加单测兜底 ~30 行（CI 不跑 e2e_claude，单靠 e2e regression guard = 0）
  - **B-P0-3**：C2 情况 B ~150-200 → **~220-280 行**（claude turnHandle 改动入侵已有结构）
  - **B-P1-5**：§3 边界纠正意外利好 — `hook_consumer.go:301` + `notify/turn.go:209` 已消费 ev.Result（codex 侧零值），C1 落地即激活上游链路
  - **A-P1-5/A-P1-6/B-P2-7**：§5.2 e2e_claude CI 策略明文 + prompt 强制配方（`strings.Repeat("A", 3072)`）+ hash 前缀 + 长度容差断言
  - **A-P2-8**：Q1 reasoning 升决策项 D1（设计意图明文）
  - **B-P3-8**：commit 粒度拆 4（§4.5 明列）
- 2026-05-12 v4 初稿：基于 C-A 实施计划 §2.1 + §2.2 v2.1 拍板项 + Explorer A/B 调研结果（含 1 处事实层错误 + 工程量低估）。
