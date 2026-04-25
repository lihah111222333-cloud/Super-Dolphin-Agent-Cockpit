# P0b Step 3：skill_evaluator

## 目标

为 collector 排出的 `Trajectory` 提供启发式判定：返回 `EvaluationVerdict{Eligible, Reason}`，决定是否进入 LLM 提炼队列。完全 stateless、纯函数、不读库不联网，输出对同一输入确定。

## 前置依赖

- Step 2 已交付 `Trajectory` 类型（见 `step-2-trajectory-collector.md`）。
- 不依赖 Step 1 / Step 4：evaluator 不读 candidate store、不调 DreamExecutor。

## 文件清单

### 新建

| 路径 | 说明 |
|---|---|
| `internal/module/turn/skill_evaluator.go` | `Evaluator` 接口 + `DefaultEvaluator` 启发式实现。 |
| `internal/module/turn/skill_evaluator_test.go` | table-driven 验收。 |

### 修改

| 路径 | 说明 |
|---|---|
| `internal/module/turn/module.go` | `fx.Provide(NewDefaultEvaluator)`。 |

## 契约

```go
package turn

type EvaluationVerdict struct {
    Eligible bool
    Reason   string
}

type Evaluator interface {
    Evaluate(t Trajectory) EvaluationVerdict
}

type DefaultEvaluator struct {
    MinToolCalls int // 推荐默认 2
    MaxToolCalls int // 0 表示不限；防止把超长 trajectory 提炼成噪声
}

func NewDefaultEvaluator() *DefaultEvaluator {
    return &DefaultEvaluator{MinToolCalls: 2}
}

func (e *DefaultEvaluator) Evaluate(t Trajectory) EvaluationVerdict { /* ... */ }
```

## 启发式规则（按序判定，先命中先返回）

引用 P0 §"启发评估器"：判断成功、tool call 次数、diff / 结果质量、无人工拒批。

1. **terminal**：`t.TerminalState != "completed"` → ineligible，`Reason="non_completed_terminal"`。
2. **success**：`t.Success != nil && *t.Success == false` → ineligible，`Reason="completion_marked_failure"`。`t.Success == nil` 视作允许（未标注成功 ≠ 失败）。
3. **tool count 下限**：`len(t.ToolCalls) < MinToolCalls` → ineligible，`Reason="tool_calls_below_min"`。
4. **tool count 上限**：`MaxToolCalls > 0 && len(t.ToolCalls) > MaxToolCalls` → ineligible，`Reason="tool_calls_above_max"`。
5. **至少一个 tool 完成且未失败**：所有 `ToolCalls[i].Failed == true` → ineligible，`Reason="all_tool_calls_failed"`。
6. **已知失败模式**：`TerminalState ∈ {"interrupted","aborted"}` 已被规则 1 拦下；额外检测 `Reason` 字段中的 `"recursion_limit"` / `"context_exhausted"` 子串（observation 层会写入此类 reason），命中 → ineligible，`Reason="known_failure_mode"`。
7. 全部通过 → eligible，`Reason="ok"`。

## 实施约束

- 完全 stateless：不持有锁、goroutine、文件句柄、DB 连接。
- 输出确定性：同一 `Trajectory` 输入多次 `Evaluate` 必须返回相同 verdict（包括 `Reason`）。
- 不读 candidate store / 不调 LLM / 不联网（P0 §"关键实现约束"：本层只做启发判定，LLM 提炼在 extractor 内做）。
- 不修改入参 `Trajectory`（不新增字段、不重排 ToolCalls）。
- `Reason` 字段是稳定字符串枚举（用于 metric label），新增取值时同步更新测试。

## 验收标准

### 必测项（建议测试名）

- `TestEvaluator_TableDriven`：覆盖所有 7 条规则的边界（最少一行 case 一条规则）。
- `TestEvaluator_InterruptedTrajectoryIsIneligible`：`TerminalState="interrupted"` → ineligible（即使其他字段都看似 ok）。
- `TestEvaluator_AbortedTrajectoryIsIneligible`：同上对 `aborted`。
- `TestEvaluator_BelowMinToolCallsIsIneligible`：完成 + 成功，但 ToolCalls 数 < `MinToolCalls`。
- `TestEvaluator_AllToolCallsFailedIsIneligible`：`len > 0` 但所有 `Failed=true`。
- `TestEvaluator_NilSuccessTreatedAsEligible`：`Success=nil` + 其他条件满足 → eligible。
- `TestEvaluator_DeterministicAcrossRuns`：同一输入跑两遍，verdict 完全一致。

### 命令

```bash
go test ./internal/module/turn/ -run Evaluator
```

## 已知风险 / 反模式

- **把 evaluator 做有状态**（缓存历史决定）：会破坏可测性与确定性。
- **在 evaluator 里调 LLM "二次确认"**：评估必须廉价，LLM 调用归 extractor。
- **吞掉 `Reason`**：metric / 审计需要稳定枚举；不要把所有失败合成一个 `"ineligible"`。
- **依赖 ToolCalls 顺序**：评估只看集合特征（数量、是否全 failed），不要假设 `ToolCalls[0]` 是某个特定调用。