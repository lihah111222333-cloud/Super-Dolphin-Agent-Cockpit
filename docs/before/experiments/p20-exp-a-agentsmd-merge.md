# P20 实验 A：codexapp AGENTS.md 与 baseInstructions 合并顺序

> 执行时间：2026-04-18 | 执行方式：strings 静态分析 + codex-cli 0.121.0 行为观察
> 目的：验证 L1 清单注入 `baseInstructions` 后，是否被 codex-rs 加载的 AGENTS.md 稀释或覆盖

## 方法

因为 `codex exec` CLI **不暴露** `--system-prompt` 或 `-c base_instructions=` 类 flag（baseInstructions 是 codex-rs 的 RPC 层字段，非 CLI 层），无法从 CLI 端复刻我们项目 `internal/provider/codexapp/support.go:248` 的链路。

但可以通过两个维度验证顺序语义：

### 维度 1：codex 二进制内建 system prompt 的明文规则

执行：
```bash
strings "$(which codex)" | grep -B1 -A1 "AGENTS.md"
```

命中的原文（位于 codex-rs `core/src/project_doc.rs` 附近，属于内建 base_instructions）：

> Files called AGENTS.md commonly appear in many places inside a container ...
> Their purpose is to pass along human guidance to you, the agent. ...
> Each AGENTS.md governs the entire directory that contains it and every child directory beneath that point.
> **When two AGENTS.md files disagree, the one located deeper in the directory structure overrides the higher-level file, while instructions given directly in the prompt by the system, developer, or user outrank any AGENTS.md content.**

### 维度 2：codex 二进制对 base_instructions / developer_instructions 的字段承认

```bash
strings "$(which codex)" | grep -iE "^(base_instructions|user_instructions|developer_instructions)"
# 命中：base_instructions, developer_instructions（以及 CollabAgentState.ts 中的 baseInstructions / developerInstructions TypeScript bindings）
```

说明 codex-rs 协议层**原生支持** `baseInstructions + developerInstructions` 双轨道参数，即我们 `threadStartParams.BaseInstructions` 送进去的内容会被作为"system/developer 侧的直接指令"处理。

## 结论

✅ **baseInstructions 优先级 > AGENTS.md**（由 codex 内建 prompt 文本**明文声明**，是权威结论）

语义层级（codex 自己声明的优先级）：
```
system/developer/user prompt (= 我们的 baseInstructions / developerInstructions)
        ↑ outrank ↑
AGENTS.md (deeper > higher-level in dir tree)
```

## 对 P20 Phase 的影响

| Phase | 原假设 | 实验 A 后修订 |
|---|---|---|
| **Phase 8（SkillCatalogProvider）** | L1 清单放 baseInstructions 头部还是尾部？需验证 | **任意位置皆可**，baseInstructions 整体优先级已高于 AGENTS.md。推荐放**尾部**，因为前面可能有 Phase 1 信任作用域声明 / developerInstructions；放尾部可让 L1 清单最贴近用户输入，强化注意力 |
| **Phase 9（元指令 + `<system-reminder>`）** | 每 turn 用 `<system-reminder>` 重贴防 AGENTS.md 稀释 | **从"必需"降级为"可选加固"**。主要目的变为对抗"长对话注意力衰减"而非 AGENTS.md 覆盖 |

**工作量影响**：
- Phase 8 决策简化（无需 A/B 位置测试）
- Phase 9 可选的"每 turn 重贴"可推迟到灰度观察期（miss_rate > 0.15 时再启用）

## 残留不确定性

- codex-rs 实际拼接 `baseInstructions + AGENTS.md` 的**文本物理位置**（前/后/交织）未从 strings 中直接观察到；但语义优先级明确，物理位置属实现细节，对模型行为无实质影响
- 若 codex-rs 未来版本调整，需通过 `codex exec -c` 探测或重测——建议灰度期监控 `skill_catalog_hit` 指标

## 参考

- codex 二进制：`/opt/homebrew/bin/codex`（codex-cli 0.121.0）
- 命中文件：`core/src/project_doc.rs` 附近（strings 中可见路径）
- 项目代码：`internal/provider/codexapp/support.go:248`（`startAssemblyInstructions` 产生 baseInstructions）
