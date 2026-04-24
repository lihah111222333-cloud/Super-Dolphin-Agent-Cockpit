# P22.1 子任务 DAG + 并行度

> 总览：[`README.md`](README.md)  
> Findings：[`FINDINGS.md`](FINDINGS.md)  
> Gate：[`GATE_CONTRACTS.md`](GATE_CONTRACTS.md)

## 1. DAG 原则

- 每个实施 agent 控制在 **10-15 文件** write-set。
- 公共 contract / archtest 先行，模块 slice 后行；禁止多个 agent 同时改同一 package 的 module wiring。
- Phase 2 的 9+ 模块可并行，但公共 BusModule / RunnerModule helper 不可并行修改。
- P22.1 不改 P22 `JUDGEMENT_*` 与既有 findings；若需要引用，只新增 P22.1 文档或代码注释。

## 2. 子任务 DAG

```text
P22.1-P0A：BusModule subscriber group contract
P22.1-P0B：RunnerModule actor contract
P22.1-P0C：archtest skeleton + precise allowlist format
  └─ depends on P0A/P0B naming agreement

P22.1-P1A：root bridge shutdown ordering（internal/app/runner.go）
  └─ depends on P0C
P22.1-P1B：watchFXShutdown boundary（internal/app/app.go）
  └─ depends on P0C

P22.1-P2A：insight + turn/observation BusModule template
  └─ depends on P0A and P1A
P22.1-P2B：rpc push + hooks + mcpcontrol worker-as-runner
  └─ depends on P0A/P0B and P1A
P22.1-P2C：thread bus workers + observation integration check
  └─ depends on P2A template and P0B
P22.1-P2D：memory hooks scheduler/nested/teamSync migration
  └─ depends on P2B worker pattern and P1A ordering
P22.1-P2E：cachekeepalive relay/timer split
  └─ depends on P2B worker pattern
P22.1-P2F：toolbridge diff fallback subscriber ownership
  └─ depends on P2A BusModule template

P22.1-P3A：session-private runtime allowlist precision
  └─ depends on P2A..P2F final code shape
P22.1-P3B：full archtest hardening / remove temporary allowlist
  └─ depends on P3A
```

## 3. 子任务拆分表

| Node | Phase | 目标 | 前置依赖 | 预估 write-set（10-15 文件上限） | 可并行 |
|---|---:|---|---|---|---|
| P22.1-P0A | 0 | 定义 BusModule subscriber group 输入/输出 contract | 无 | `internal/platform/bus/*`、`internal/archtest/*`、契约文档 | P0B |
| P22.1-P0B | 0 | 定义 RunnerModule actor contract 与 worker adapter 模式 | 无 | `internal/platform/runner/*`、`internal/archtest/*`、契约文档 | P0A |
| P22.1-P0C | 0 | archtest skeleton：bus/runner/shutdown/session-private | P0A/P0B | `internal/archtest/*` | 否，公共守卫 |
| P22.1-P1A | 1 | root OnStop 顺序调整 | P0C | `internal/app/runner.go`、`internal/app/runner_test.go`、archtest | P1B |
| P22.1-P1B | 1 | `watchFXShutdown` owner ctx / allowlist 边界 | P0C | `internal/app/app.go`、desktop tests、archtest allowlist | P1A |
| P22.1-P2A | 2 | `insight` + `turn/observation` subscriber 迁移，做最小模板 | P0A/P1A | `internal/module/insight/*`、`internal/module/turn/observation/*`、bus tests | P2B/P2E/P2F |
| P22.1-P2B | 2 | `rpc`/`hooks`/`mcpcontrol` fanout workers 进入 RunnerModule，subscriptions 进 BusModule | P0A/P0B/P1A | `internal/platform/rpc/*`、`internal/platform/hooks/*`、`internal/platform/mcpcontrol/*` | P2A/P2E/P2F |
| P22.1-P2C | 2 | `thread` bus workers 进入 RunnerModule | P2A/P0B | `internal/module/thread/*`、必要 `internal/module/turn/*` contract tests | P2D 谨慎并行；合入串行 |
| P22.1-P2D | 2 | `memory` scheduler/nested/teamSync worker 迁移 | P2B/P1A | `internal/module/memory/*`、`internal/module/memory/*_test.go` | P2E/P2F |
| P22.1-P2E | 2 | `cachekeepalive` relay/timer split | P2B | `internal/platform/cachekeepalive/*` | P2A/P2D/P2F |
| P22.1-P2F | 2 | `toolbridge` diff fallback subscriber 迁移 | P2A | `internal/platform/toolbridge/*` | P2E/P2D |
| P22.1-P3A | 3 | session-private runtime allowlist 精确化 | P2A..P2F | `internal/archtest/*`、少量 docs | 否 |
| P22.1-P3B | 3 | 全量 archtest hardening 与临时 allowlist 回收 | P3A | `internal/archtest/*` | 否 |

## 4. 并行批次建议

### Batch 0：公共契约（串行收口）

```text
P22.1-P0A + P22.1-P0B（可并行调研，但合入前统一命名）
 -> P22.1-P0C（串行）
```

### Batch 1：根顺序与小模板

```text
P22.1-P1A || P22.1-P1B
P22.1-P2A 在 P1A 后启动，作为 BusModule 模板
```

### Batch 2：平台 fanout 与低耦合模块

```text
P22.1-P2B || P22.1-P2E || P22.1-P2F
```

### Batch 3：复杂业务模块

```text
P22.1-P2C（thread） -> P22.1-P2D（memory，可在 P2C 调研期并行，但代码合入需串行审冲突）
```

### Batch 4：allowlist 收口

```text
P22.1-P3A -> P22.1-P3B
```

## 5. write-set 冲突检查

| 冲突面 | 可能冲突节点 | 处理 |
|---|---|---|
| `internal/archtest/*` | P0C、P1A、P1B、P3A、P3B | 只允许一个 archtest owner；模块 slice 只新增 fixture 需求，不直接改公共判定 |
| BusModule helper | P0A、P2A、P2B、P2E、P2F | P0A 先冻结 API；后续 slice 只消费，不扩 API，除非回 P0A 变更单 |
| RunnerModule helper | P0B、P2B、P2C、P2D、P2E | P0B 先冻结 adapter；worker slice 不各自发明 wrapper |
| `internal/module/thread` 与 `internal/module/turn` | P2A、P2C | observation 模板只改 `turn/observation`；thread slice 合入前 rebase |
| `internal/platform/toolbridge` hidden contract | P2F 与任何 P22/P4 后续 | toolbridge P22.1 只处理 subscriber owner，不碰 handler fail-closed / hidden contract |
| `internal/module/memory` drain tests | P2D 与任何 memory 性能/检索任务 | P2D 独占 memory module wiring，其他任务后置 |

## 6. 每节点验收清单

1. 本节点触及的 F-x 在 [`FINDINGS.md`](FINDINGS.md) 中有对应销账说明。
2. `TestBusSubscriberGroup` / `TestRunnerActorOwnership` 对该节点不再需要临时豁免。
3. 相关 worker 有 bounded shutdown 测试；相关 subscriber 有 cancel 幂等测试。
4. `git diff --stat` 显示未越权到无关 package；P22 文档与 judgement 文件未被修改。
5. 若新增 allowlist，必须在 P3A 中登记精确 owner/function/shape，不允许整文件。
