# P0b Step 2：trajectory_collector

## 目标

把 `internal/module/turn/observation` 输出的 6 类事实（turn 映射、`call_id → turn_id`、`skills_selected`、token snapshot、terminal precedence、raw/typed 去重）聚合成可被 evaluator 消费的 `Trajectory` 对象。collector 只在内存中维护，turn 终止时排出快照供下游消费，不持久化。

## 前置依赖

- `internal/module/turn/observation/contract.go:1` Canonical Turn Observation Contract（已就绪）。
- `internal/module/turn/observation/subscribers.go:1` 已注册 bus 订阅，事实合并已在该层完成。
- 需要消费的事件来源：`internal/dto/turn/event.go:11`、`internal/dto/tool/event.go:46`（`ToolDiffUpdated` 仅含 `ThreadID/AgentID/CallID`，**没有** `turn_id`，必须经 observation 的 `call_id → turn_id` 表归属）。
- Step 1 schema 不是 Step 2 的前置（collector 不写库），但本步产出的 `Trajectory` 是 Step 3 / Step 4 的输入。

## 文件清单

### 新建

| 路径 | 说明 |
|---|---|
| `internal/module/turn/trajectory_collector.go` | `Collector` 接口 + 内存实现 + `RegisterTrajectorySubscribers` fx hook。 |
| `internal/module/turn/trajectory_collector_test.go` | 必测项见下文。 |

### 修改

| 路径 | 说明 |
|---|---|
| `internal/module/turn/module.go` | 通过 `fx.Provide(NewTrajectoryCollector)` + `fx.Invoke(RegisterTrajectorySubscribers)` 接入。 |

## 契约

```go
package turn

import "context"

// Trajectory 是一次 turn 完整生命周期的内存聚合，作为 Step 3/4 的输入。
// 不持久化；evaluator 决定是否真正提炼。
type Trajectory struct {
    TurnID         string         // provider turn id
    LocalTurnID    string         // 本地 turn id（与 TurnID 通过 observation.MapTurn 双向绑定）
    ThreadID       string
    AgentID        string
    SessionID      string         // 实施补充：collector 不收 SessionID（TurnHeader 不携带）；Step 4 extractor 负责回填
    Cwd            string         // 留空：collector 拿不到 cwd；由 Step 4 extractor 通过 ThreadID 反查后填充
    StartedAt      time.Time      // P0b Step 2 实施统一用 time.Time，与 observation.Timestamps 一致
    EndedAt        time.Time
    TerminalState  string         // "completed" | "interrupted" | "aborted" | "failed" | "stalled"
    Success        *bool          // 指针：nil=未知，避免与 false 混淆
    SkillsSelected []string       // 来自 observation.SkillsSelected
    ToolCalls      []ToolCall
    TokenUsage     *TokenSnapshot
}

type ToolCall struct {
    CallID    string
    Name      string
    Args      string
    Result    string
    Failed    bool
    Error     string         // P0b Step 2 实施补充：直接承载 ToolCallEnd.Error，便于 evaluator/extractor 直接读
    StartedAt time.Time      // P0b Step 2 实施统一用 time.Time（与 observation.Timestamps 一致）
    EndedAt   time.Time
    DiffCount int            // ToolDiffUpdated 命中归属的次数；P0 文档原本无此字段，是 Step 2 实施时为 TestTrajectoryCollector_AttributesToolDiffViaCallIDMap 测试可观察补的副作用
}

type TokenSnapshot struct {
    InputTokens         int
    OutputTokens        int
    TotalTokens         int
    ContextWindowTokens int
}

type Collector interface {
    // Snapshot 返回某 turn 当前已聚合的 Trajectory（未排出也可读，用于诊断）。
    Snapshot(turnID string) (Trajectory, bool)
    // Drain 一次性返回所有已达 terminal 的 Trajectory，并从内部状态移除。
    Drain() []Trajectory
}

// RegisterTrajectorySubscribers 是 fx.Invoke 入口：把 collector 注入 bus.subscribers。
func RegisterTrajectorySubscribers(ctx context.Context /* + bus / observation deps */) {}
```

## 实施约束

- bus callback 内**只**做 fact merge / 去重 / 入队；禁止 LLM、磁盘 IO、长阻塞（P0 §"关键实现约束"：bus callback 内只做 observation 事实合并、采样和入队）。
- terminal precedence：`interrupted > aborted > failed > stalled > completed`；一旦 `interrupted`/`aborted` 成立，不能被 late `completed` 覆盖。本层直接复用 `observation.RecordTerminal` 的判定，不重写 precedence 逻辑（P0 §"Canonical Turn Observation Contract"）。
- raw + typed 必须按 `call_id` 或 raw event id 去重一次。复用 `observation.Dedupe(DedupeKey{...})`，不要在 collector 里再造一份去重表（避免双算）。
- `ToolDiffUpdated` 缺 `turn_id` 时必须经 `observation.LookupCall(callID)` 归属；查不到归属的事件**丢弃**而非挂到任意 turn。
- token zero-event 不覆盖之前的非零 snapshot：复用 `observation.RecordTokens` 的归一逻辑，collector 只读 `observation.Tokens(localTurnID)` 的最终值；UI projection=`thread` 的 token 事件来自 `internal/provider/unified/ui_tokens.go:58` 区，可能不带 `turn_id`，**不能**当 per-turn 权威值。
- `Trajectory` **不持久化**（只在内存）；evaluator 判定 ineligible 的直接丢弃；判定 eligible 的由 Step 4 extractor 通过 runner worker 消费。
- 观念约束：`turnTracker` 不得 import 此包（由 review 阶段人工 enforce）；实测断言则覆盖：`internal/module/turn/observation` 不得 import `internal/module/turn`（单向 push：observation → collector → consumer）。引用 P0 §"关键实现约束"：observation 与 collector / extractor 之间必须是**单向 push**。
- `Drain()` 必须线程安全；典型调用方是 runner worker 周期 flush，调用频率受 runner tick 控制。

## 验收标准

### 必测项

- `TestTrajectoryCollector_DedupesRawAndTyped`：同一 `ToolCallEnd` raw event + 对应 typed event 各发一次，最终 `Trajectory.ToolCalls` 中该 call 仅一条。
- `TestTrajectoryCollector_AttributesToolDiffViaCallIDMap`：`ToolDiffUpdated` 不带 `turn_id`，但 `ToolCallBegin` 已经把 `call_id` 绑到 turn；断言 diff 被归到正确 turn。
- `TestTrajectoryCollector_InterruptedBeatsLateCompleted`：先发 `TurnInterrupted` 再发 `TurnCompleted{Success:true}`，最终 `TerminalState='interrupted'` 且 `Success` 不被改写为 true。
- `TestTrajectoryCollector_TokenZeroEventDoesNotOverwrite`：先发非零 token 事件再发 zero-event，`Trajectory.TokenUsage` 保持非零。
- `TestTrajectoryCollector_DropsToolDiffWithUnknownCall`：未先 `ToolCallBegin` 直接发 `ToolDiffUpdated` → 该事件丢弃，无幽灵 turn。
- `TestTrajectoryCollector_DrainEmptiesTerminalOnly`：未到 terminal 的 turn 不被 drain；已 terminal 的 turn 被 drain 且二次 drain 不重复返回。
- 架构断言：`TestImports_ObservationDoesNotImportTurn` 用 `go/parser.ParseDir` 读取 `internal/module/turn/observation/*.go` 的 import 段，断言其中无 `"github.com/anthropic-ai/super-agent-v3/internal/module/turn"`。

### 命令

```bash
go test ./internal/module/turn/...
```

### 集成验证

- 启动整套 fx app，跑一段含 tool call 的 turn，`Drain()` 能产出预期 `Trajectory`，并被 evaluator 消费。

## 已知风险 / 反模式

- **在 bus callback 内做长阻塞**：会拖慢 turn 主流程；任何耗时操作必须 enqueue 给 runner worker。
- **重写 terminal precedence**：observation 层已实现，collector 重写必然偏移。
- **依赖 `UITokensUpdated` 的 `Projection='thread'` 当 per-turn 权威值**：会污染 evaluator 的判定输入。
- **拿不到 `turn_id` 时回填到"最近一个 turn"**：会跨 turn 串数据，必须丢弃。
- **`Drain()` 与 `Snapshot()` 共用同一锁但持锁时间过长**：drain 时拷贝出引用即返回，避免在锁内做序列化。