# ADR-014：prompt_template-first 路线 / automation kind 冻结在 command_card

> 状态：✅ Accepted | 日期：2026-05-12 | 决策者：主线 | 相关：ADR-007（automation.kind 渐进路线，本 ADR 冻结其 §6 progression）、F2.0/F2.1（已 done 不撤回）、F7.1/F7.2（教 AI 挑 prompt_template）、F7.3（新增 prompt_template seed 库）
>
> 来源注：本 ADR 是 round-3（2026-05-12）独立产出，但文件随 reviewer 合并 W3 时 `git add -A` 顺手带入 commit `30f6837c`（W3 SQL/migration/F4.5/Tarjan SCC merge commit），git log 归属与实际产出起源不完全对齐。不 rebase 重写历史的原因：(1) 当前 50 commits 未 push，技术上可 rebase；(2) 但 rebase über 5 个 merge + 30+ worker commits 代价远大于“commit 归属准确”的美学收益；(3) 功能 / ADR 内容 / archtest / 状态项都正确不受影响。

## 1. 背景

ADR-007 拍板「command_card → webhook → http → shell 渐进开通」，F2.0 / F2.1 已实装 `kind=command_card` 路径。但 2026-05-12 复盘真同类项目实战发现：

| 项目 | 用户群 | "动作"机制 | shell 模板卡？ |
|---|---|---|---|
| Khoj | researcher / knowledge worker | automation = prompt + cron + schedule | ❌ |
| OpenHands | dev + power user | builtin Python tool + MCP | ❌ |
| Aider | dev + tech writer | 内置 commands + 自由 chat | ❌ |
| Bee Agent / Letta | power user | Python 工具函数 | ❌ |
| Devin / Cursor agent / Cline | dev | 内置工具 + LLM 自由调 | ❌ |
| Anthropic Skills | power user | markdown + allowed_tools | ❌ |
| **n8n / Activepieces / Zapier** | 业务用户 | node/piece SDK | ⚠️ 业务用户专用，跟我们用户群不重叠 |

**结论**：跟 Super-Dolphin 用户定位（power user 但不限编程 / 装 Claude Code CLI / cron 跑日常 AI 任务）相同的项目，**没有一家走"shell 模板 + 参数填空"的命令卡路线**。

同时代码核对发现：`agent_key`（→ prompt_template）已经是节点强字段（DB schema / config.go / ops.go 禁改字段列表），**prompt_template 路线在架构层面已经是一等公民**，但 seed 库空（`grep "INSERT INTO prompt_templates" migrations/` 零命中）。

## 2. 决策

**主路线 = prompt_template-first**：

1. AI 设计师挑 `prompt_template` 作主菜单，组装 `kind=agent` 节点；不挑 `command_card`
2. F7.1 / F7.2（AI 设计师 prompt 中英版）描述明确"教 AI 把 prompt_template 当主菜单"
3. **新增 F7.3**：prompt_template seed 库（10-15 张通用 AI 微技能，覆盖 power user 跨场景：晨报 / 文献摘要 / PR 摘要 / 周复盘 / 数据巡检 / 邮件起草 / 健康简报 / 选题整理 等）

**辅路线 = command_card 冻结现状**：

1. F2.0 / F2.1 已 done，保留在产，不撤回
2. **ADR-007 §6 progression 冻结在 command_card 档**——webhook / http / shell kind **不进 F 阶段**
3. F2.2（AutomationExecutor 处理 inputs/outputs）/ F3.1（HybridExecutor v1）/ F3.2-F3.4 全部**降级 H 阶段**，按需触发
4. 不补"命令卡安全契约 / argv 模式改造 / description sanitizer / seed 库 / 管理 UI"——上述设计在 prompt_template-first 路线下是过度工程

## 3. 推论

### 命令卡未来定位

仅作"明显不需要 LLM 推理 + 高频"动作的扩展点：
- `wait_for_approval`（如果作 DAG 编译器原语不便）
- `webhook_post` / `notify_email` 等纯 HTTP POST
- 用户/admin 自定义业务脚本（H 阶段引入管理 UI 时再开）

不再扩展为"30 张通用业务卡库"——业务集成（gmail / slack / notion / metabase ...）通过 agent 节点 + prompt_template 表达（让 Claude Code / Codex CLI 用自带的 web_fetch / bash / edit 工具完成），不通过命令卡。

### M3 demo 路径

> "AI 帮我设计每天 8 点的报告生成 DAG"
> → AI 调 `prompt_list` 看 prompt_template 菜单
> → 挑 `morning_briefer` 等 prompt_template
> → `task_dag_apply_ops` 加 `kind=agent` 节点 + `agent_key=morning_briefer`
> → DAG `trigger=scheduled` + `cron_expr=0 8 * * *`
> → cron tick 触发 → AgentExecutor → orchestration_launch_agent → 子 thread 跑 → 写 sharedfile → 完成

不需要 command_card 参与。

### 跟 ADR-007 的关系

ADR-007 状态保持 Accepted（已实装 command_card 档），但 §6 progression 表加注脚："**冻结：webhook / http / shell kind 不进 F 阶段，按 H 阶段触发条件激活；详 ADR-014**"。

## 4. 后果

**正向**：

- 工程量减约 70%：砍掉前期讨论过的"补 9-13 个命令卡 task（安全契约 / argv 改造 / seed 库 / 管理 UI / 翻译层）"
- M3 通路反而更顺：AI 调一组 MCP 工具（prompt_list / task_dag_apply_ops / list_models）一气呵成
- 跟 Claude Code / Codex CLI 原生能力契合：agent 节点本来就是包装 CLI 的，agent 节点能干啥 = CLI 能干啥（web_fetch / bash / edit / search ...）
- 跨场景天然：prompt_template 表里挂"晨报员 / 论文摘要员 / PR 评审员"等，研究 / 内容 / 分析 / dev 用户都能挑

**负向 / 取舍**：

- 每个节点跑都掏 LLM token（命令卡能省，prompt_template 不能）。但 cron 通常每天 1-N 次，单次成本可控；高频确定性动作走少量命令卡补救
- automation 节点这一档"低利用"：F2.x / F3.x 降级后，自动化只剩 command_card + 个别原语；hybrid 节点也降级 H
- 已 commit 的 F2.0 / F2.1 / ADR-007 / ADR-011 不浪费但利用率低——属于沉没成本

**风险**：

- 如果用户场景出现明显需要"shell kind / http kind"的需求（外部系统集成），H 阶段需要按 ADR-007 渐进路线重新激活
- prompt_template seed 库的质量直接决定 M3 demo 是否打动用户——F7.3 设计需要参考 Khoj / OpenHands prompt 风格

## 5. 实施 task 映射

| Task | 状态变化 |
|---|---|
| F2.0 / F2.1 | ✅ done 保留，不撤回 |
| F2.2（automation inputs/outputs）| ⏸ **降级 H 阶段** |
| F3.1（HybridExecutor v1）| ⏸ **降级 H 阶段** |
| F3.2 / F3.3 / F3.4（Hybrid v2 占位）| ⏸ **归入 H 阶段，不进 F** |
| F7.1 / F7.2（AI 设计师 prompt）| 描述补强：教 AI 挑 prompt_template |
| **F7.3（新增）prompt_template seed 库** | **新增 F 阶段 task** |

## 6. 不变的部分

- ADR-001 DAG v2 契约（agent / automation / hybrid 三 kind 节点）不变
- ADR-007 §1-§5 决策（kind 字段位、fail-fast 未知 kind）不变，仅冻结 §6 progression
- ADR-011 Hybrid 拓扑 ADR 不撤回，但 F3.x 实装推迟 H

