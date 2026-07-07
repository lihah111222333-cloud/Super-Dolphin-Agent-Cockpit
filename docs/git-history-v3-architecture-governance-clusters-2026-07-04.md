# V3 架构治理版本聚类分析

生成日期：2026-07-04

分析对象：`main` 历史提交

核心问题：用提交密度聚类区分“上一版 V3”和“当前版 V3”，重点比较两者在架构治理上的差异。

## 1. 结论先行

仓库没有 tag，因此这里的“上一版 V3 / 当前版 V3”不是发布语义，而是按 `main` 提交密度和架构治理提交密度推断出的工程阶段。

聚类后的切分点是 2026-06-26：

| 推断版本 | 日期范围 | 提交数 | 日均提交 | 架构治理提交 | 治理占比 | 判断 |
|---|---:|---:|---:|---:|---:|---|
| 上一版 V3 | 2026-03-19 至 2026-06-25 | 5,761 | 58.2/day | 3,993 | 69.3% | 架构搭建与能力扩张期 |
| 当前版 V3 | 2026-06-26 至 2026-07-03 | 759 | 94.9/day | 570 | 75.1% | 架构治理收敛与强约束期 |

当前版 V3 的实际形成过程分两段：

| 子阶段 | 日期范围 | 提交数 | 日均提交 | 治理提交 | 治理占比 | 特征 |
|---|---:|---:|---:|---:|---:|---|
| 当前版成型高密度簇 | 2026-06-26 至 2026-06-30 | 662 | 132.4/day | 488 | 73.7% | 大规模治理、回滚、删除旧前端、技能镜像、代码地图契约 |
| 当前版低 churn 收尾 | 2026-07-01 至 2026-07-03 | 97 | 32.3/day | 82 | 84.5% | 风险修复、fail-fast、RPC/LSP/Codex 守卫 |

一句话概括：

上一版 V3 的架构治理是“把 V2 的大对象拆开并搭出新边界”；当前版 V3 的架构治理是“把边界变成强制契约、事实源和 fail-closed 运行时策略”。

## 2. 聚类方法

用 `main` 的日粒度提交统计做密度聚类：

- 总提交密度：每日 commit 数。
- 架构治理提交密度：subject 或路径命中架构治理关键词/目录的每日 commit 数。
- 架构治理关键词：`arch`、`architecture`、`archtest`、`codemap`、`contract`、`guard`、`fail-fast`、`risk`、`mcp`、`lsp`、`provider`、`skill`、`store`、`sqlite`、`rpc`、`toolbridge`、`trace`、`风险`、`阻断`、`守卫`、`契约`、`治理` 等。
- 架构治理路径：`docs/adr`、`docs/契约`、`docs/doc/codemap`、`internal/archtest`、`internal/contract`、`internal/platform`、`internal/provider`、`internal/store`、`cmd/mcp-*`、`.agents/skills`、`.githooks`、`scripts/` 等。

检测到的高密度治理窗口：

| 窗口 | 提交数 | 治理提交数 | 解释 |
|---|---:|---:|---|
| 2026-04-14 | 104 | 56 | 早期集中集成 |
| 2026-04-19 至 2026-04-20 | 206 | 138 | prompt/skill/fail-fast 与回滚修复 |
| 2026-04-24 至 2026-04-30 | 1,166 | 814 | codemap、skills、provider、MCP、架构文档爆发 |
| 2026-05-10 至 2026-05-15 | 874 | 644 | orchestration/provider/UI 集成 |
| 2026-06-01 至 2026-06-08 | 1,038 | 626 | 自动化、打包、MCP、前端集成 |
| 2026-06-13 至 2026-06-18 | 420 | 278 | sidecar、模块治理、风险修复 |
| 2026-06-26 至 2026-06-30 | 662 | 488 | 当前版 V3 成型治理簇 |

选择 2026-06-26 作为当前版起点的原因：

- 它是最后一个持续高密度治理簇的起点。
- 该簇直接包含当前架构治理的关键动作：archtest P1/P2/P3、旧 Vue 删除、`.agents` 技能镜像、代码地图生成契约、MCP lifecycle、fail-fast、风险修复。
- 2026-07-01 至 2026-07-03 虽然总提交密度下降，但治理占比升到 84.5%，是该簇的收敛尾声。

## 3. 上一版 V3：架构搭建与能力扩张

上一版 V3 覆盖 2026-03-19 至 2026-06-25。

### 3.1 治理形态

上一版 V3 的治理主要是构建型治理：

- 从 V2 的 God Object / `Server` 聚合模式迁移到 V3 的 fx 模块化装配。
- 建立 `cmd/agent-terminal`、`cmd/mcp-orch`、`cmd/mcp-lsp`、`internal/module`、`internal/platform`、`internal/provider`、`internal/store` 等大边界。
- 通过 ADR、codemap、契约文档和 archtest 开始把经验规则文档化。
- 大量能力仍处于“边做边定边界”的阶段，提交类型里 `feat`、`fix`、`docs`、`merge` 同时很高。

数据侧特征：

| 指标 | 数值 |
|---|---:|
| 提交数 | 5,761 |
| 日均提交 | 58.2 |
| 架构治理提交 | 3,993 |
| 治理提交占比 | 69.3% |
| merge 占比 | 17.0% |
| add+del churn | 5,953,193 |
| 单提交平均 churn | 1,033.4 |

类型分布：

| 类型 | 提交数 |
|---|---:|
| fix | 1,348 |
| feat | 1,075 |
| merge | 952 |
| docs | 780 |
| refactor | 346 |
| chore | 268 |
| test | 196 |

### 3.2 主要治理边界

上一版 V3 已经有清晰方向，但仍偏“搭建”：

- `fx` 是工厂和生命周期装配基础，避免继续手写大对象装配。
- `internal/module/*` 承担业务领域，`internal/platform/*` 承担底层能力，`internal/provider/*` 承担 Claude/Codex provider adapter。
- MCP sidecar 开始拆出 `cmd/mcp-orch`、`cmd/mcp-lsp`，但生命周期、tool owner、control plane、toolbridge 之间还在持续收敛。
- `internal/store/*` 和 sqlc 封装开始隔离持久化，但 store / module / toolbridge 的事实源边界仍有后续治理空间。
- 前端仍存在旧前端和新 `frontend-app` 切换期，`cmd/agent-terminal` 历史 churn 很高。

上一版的路径热区：

| 路径 | 架构治理文件事件 |
|---|---:|
| `internal/module` | 9,325 |
| `cmd/mcp-orch` | 5,178 |
| `cmd/agent-terminal` | 4,326 |
| `internal/provider` | 3,737 |
| `internal/platform` | 3,596 |
| `cmd/mcp-lsp` | 2,236 |
| `internal/store` | 1,835 |
| `frontend-app/src` | 1,228 |
| `docs/doc` | 886 |
| `internal/archtest` | 847 |

### 3.3 治理弱点

上一版 V3 的治理弱点不是“没有架构”，而是“架构规则还没完全变成硬边界”：

- 大量文档和 codemap 反复刷新，治理知识偏解释性，自动拦截能力还在增长。
- 大提交和回滚明显，说明不少治理仍靠后验修复。
- 多个 owner 边界仍在演进，例如 tool lifecycle、skill mirror、provider surface、MCP control plane。
- 旧前端仍影响仓库结构和构建心智，前端目标形态还没完全收敛到 `frontend-app`。

代表性大治理提交：

| hash | 日期 | churn | subject |
|---|---|---:|---|
| `02234d05231d` | 2026-04-24 | 124,294 | `refactor(codemap): optimize ai-index.json generation — 4.9MB → 131KB (-97%)` |
| `9436f970e886` | 2026-04-25 | 122,631 | `docs(codemap): 全量文档更新 + ai-index 重建 + README 刷新` |
| `9fa7772d82cc` | 2026-04-20 | 120,599 | `revert(module/prompt): 恢复 SkillCatalogProvider... + 补 Bug 1a fail-fast` |
| `45565cb5a79c` | 2026-06-11 | 112,266 | `回滚 PR31 误提交内容` |
| `df705d2f0b71` | 2026-03-21 | 97,161 | `原子推送` |

## 4. 当前版 V3：强约束与事实源治理

当前版 V3 覆盖 2026-06-26 至 2026-07-03。

### 4.1 治理形态

当前版 V3 的治理主要是约束型治理：

- 从“建立模块边界”转为“边界违规直接阻断”。
- 从“文档说明 owner”转为“owner/store/contract/toolbridge 消费关系明确落地”。
- 从“工具列表隐藏即可”转为“ListTools filtering 和 direct-call deny 必须同时存在”。
- 从“兼容和兜底”转为“缺失配置、未知状态、无法解析身份全部 fail-fast / fail-closed”。
- 从“旧 UI 与新 UI 迁移期”转为“当前前端只认 `frontend-app`，旧 Vue 删除”。

数据侧特征：

| 指标 | 数值 |
|---|---:|
| 提交数 | 759 |
| 日均提交 | 94.9 |
| 架构治理提交 | 570 |
| 治理提交占比 | 75.1% |
| merge 占比 | 31.6% |
| add+del churn | 1,263,634 |
| 单提交平均 churn | 1,664.9 |

当前版形成高峰在 2026-06-26 至 2026-06-30；2026-07-01 至 2026-07-03 的提交数下降，但治理占比提升到 84.5%，说明从“大规模结构治理”切到了“小批量风险收口”。

### 4.2 当前版的治理关键点

#### 4.2.1 MCP lifecycle owner 明确化

ADR 0003 接受后，per-tool lifecycle 的治理边界变得很硬：

| 位置 | 当前版职责 |
|---|---|
| `internal/module/mcp_server` | 持久 lifecycle owner |
| `internal/store/mcpserver` | lifecycle 持久化 owner，封装 SQLite |
| `internal/contract/mcp_control.go` | 跨模块 DTO 与窄接口 |
| `internal/platform/toolbridge` | 只消费 policy reader，不维护事实源 |
| `cmd/mcp-*` / `internal/platform/mcpcontrol` | 工具执行面和活体 control plane，不拥有桌面主进程 per-tool lifecycle |

这和上一版的最大区别是：toolbridge、MCP peer、前端本地状态都不能成为 lifecycle 事实源。

#### 4.2.2 Fail-fast 从原则升级为治理硬约束

当前版强调：

- 缺 workspace/server/tool 身份必须报错。
- 未知 state 不得解释成 `enabled`。
- store 读取失败不能静默展示或调用。
- direct-call deny 不能只依赖 ListTools 隐藏。
- 修复类提交需要同提交回归测试或可执行验收。

这表示治理目标从“系统尽量工作”转成“系统不能在不确定状态下继续工作”。

#### 4.2.3 MCP 双通道边界明确

当前契约把 MCP 拆成两条通道：

| 通道 | 传输 | 职责 | 禁止 |
|---|---|---|---|
| 工具执行通道 | `stdio` | MCP manifest、tools/list、tool call、tool response | register、heartbeat、approval restore、shutdown control |
| 生命周期管理通道 | `jrpc2` TCP | `ctl/*` 注册、lease、heartbeat、config push、approval、hook、report | tool call 执行 |

上一版已经在拆 sidecar，当前版则把 sidecar 的 control plane 和 tool plane 做了强隔离。

#### 4.2.4 技能与 provider mirror 收敛

当前版将 canonical skill truth 收敛到 `.agents/skills`，provider mirror 是生成物。治理含义：

- `.agent/skills` 变成历史路径，不再作为入口。
- Codex 直接读项目 canonical root。
- 手工技能自镜像要阻断，不能静默暴露。
- provider-native mirrors 不再是事实源。

相关提交密度信号集中在 2026-06-29 至 2026-07-03：

- `chore: 跟踪 agents 技能镜像`
- `docs: 统一技能入口为 agents`
- `fix: 阻断 Codex 手工技能自镜像`
- `fix: 同步 Codex 权限设置到 app-server`

#### 4.2.5 前端治理收敛

当前版通过删除旧 Vue 前端和旧前端文件，把 UI 架构边界收敛到 `frontend-app`：

- 旧 Vue 前端不再作为编辑、验证、构建目标。
- `cmd/agent-terminal/web-dist` 只作为 embed 构建产物同步目录。
- 当前 UI 风险修复主要围绕 React/Vite 新 UI 和 provider/runtime 交互。

代表提交：

| hash | 日期 | churn | subject |
|---|---|---:|---|
| `5d963e232467` | 2026-06-28 | 124,329 | `chore: 删除旧 Vue 前端` |
| `f999208dd05a` | 2026-06-29 | 119,449 | `前端: 删除旧前端文件` |

## 5. 架构治理差异总表

| 维度 | 上一版 V3 | 当前版 V3 |
|---|---|---|
| 治理目标 | 从 V2 迁移并搭出模块边界 | 把边界变成强制契约和阻断策略 |
| 提交密度 | 日均 58.2，长期高活跃 | 日均 94.9，短期治理爆发后收敛 |
| 治理占比 | 69.3%，架构建设与功能并行 | 75.1%，7 月尾部达到 84.5% |
| 提交类型 | `feat/fix/docs/refactor` 更均衡 | `merge/fix/docs/risk/archtest/guard` 更突出 |
| 模块边界 | 建立 `module/platform/provider/store/cmd` 大边界 | 强化 owner、contract、store、policy reader 的事实源边界 |
| MCP | sidecar 和工具能力持续拆分 | stdio 工具面与 jrpc2 `ctl/*` 生命周期面强隔离 |
| Tool lifecycle | owner 和持久化边界仍在演进 | `mcp_server` owner + `store/mcpserver` + toolbridge 只读 policy |
| Provider/Skill | discovery/mirror 机制持续迭代 | `.agents/skills` 成 canonical truth，mirror 是生成物 |
| 前端 | 新旧前端迁移并存，UI churn 高 | 旧 Vue 删除，当前唯一前端是 `frontend-app` |
| 错误处理 | 已引入 fail-fast，但仍有后验修复 | fail-fast/fail-closed 成为默认治理策略 |
| 守卫 | archtest、guard、codemap 逐步增长 | LSP diagnostics、RPC 字段、Codex 工具、scope 路由等细粒度守卫 |
| 风险模式 | 大提交、回滚、生成物 churn 多 | 大清理后进入小批量风险缺口修补 |

## 6. 当前版相对上一版的架构治理升级

### 升级 1：从“模块化”到“所有权事实源”

上一版已经拆模块，但当前版进一步要求每类状态有唯一 owner。典型例子是 MCP tool lifecycle：

- 写入 owner：`internal/module/mcp_server`
- 持久化 owner：`internal/store/mcpserver`
- 消费者：`internal/platform/toolbridge`
- 非 owner：`cmd/mcp-*`、`internal/platform/mcpcontrol`、frontend、本地内存 map

这降低了跨层重复判断和状态漂移。

### 升级 2：从“工具可见性”到“调用安全边界”

上一版更容易把工具治理理解为 ListTools 可见性；当前版明确 ListTools 隐藏不是安全边界，direct-call deny 必须独立存在。

这对 Codex/Claude/MCP provider 尤其关键，因为动态工具面和 direct call 可以绕过简单列表过滤。

### 升级 3：从“兼容优先”到“fail-closed 优先”

当前版的架构治理不再把缺失数据解释成默认允许：

- 缺 workspace root：阻断。
- server/tool 无法解析：阻断。
- lifecycle row 缺失或状态未知：不能解释为 enabled。
- store 错误：不能静默展示或调用。

这是安全治理和可观测治理的共同基础。

### 升级 4：从“文档治理”到“守卫治理”

上一版文档和 codemap 占比很高，解释性治理强。当前版仍保留文档，但更强调可执行守卫：

- `internal/archtest`
- `.githooks`
- `scripts/test_with_guard.sh`
- LSP diagnostics 作为必须处理项
- RPC 字段守卫
- Codex 原生工具禁用项校验
- scoped surface / resume 配置畸形阻断

治理开始从“告诉开发者怎么做”变成“错误路径进不去”。

### 升级 5：从“多前端迁移态”到“单前端目标态”

上一版的 UI 架构治理仍受旧 Vue、Wails embed、React/Vite 新 UI 并存影响。当前版通过删除旧前端，把源码事实源压缩到 `frontend-app`，减少了审查和验证路径分叉。

## 7. 当前版仍然暴露的治理风险

1. 当前版的 merge 占比达到 31.6%，高于上一版 17.0%。这说明当前版虽然治理更硬，但集成形态仍然很重。
2. 当前版高密度簇包含大量回滚和删除提交，churn 高，不宜直接用行数衡量业务进展。
3. 2026-06-26 至 2026-06-30 的大规模治理后，仍在 2026-07-01 至 2026-07-03 追加多轮风险缺口修复，说明第一轮治理不是一次性闭环。
4. 无 Git tag，版本边界只能靠提交密度推断，不利于以后复盘和回滚。
5. author identity 混杂仍会影响“谁引入治理变化 / 谁修复治理缺口”的责任归因。

## 8. 建议

1. 为 2026-06-26 前后补一个架构治理里程碑 tag，例如 `v3-governance-cutover-20260626`。
2. 对当前版治理簇做二次审查，重点看回滚、大删除、`.agents` mirror、MCP lifecycle、Codex tool policy 是否各自能独立验证。
3. 把 ADR 0003 的实施门槛拆成可勾选验收项，避免 toolbridge filtering 早于 owner/store/backfill readiness。
4. 把 fail-fast 规则和 LSP diagnostics 规则绑定到 pre-push/CI，而不是只依赖人工审查。
5. 为所有生成物提交建立单独路径规则：codemap/ai-index/capability manifest 不应与业务修复混提交。
6. 建立 `.mailmap`，否则架构治理责任统计会持续失真。
7. 后续报告应同时看提交密度和“守卫失败率/回归修复密度”，单看 commit 数无法区分建设和返工。

## 9. 可复现口径

核心命令：

```bash
git log main --date=iso-strict --format='...' --numstat
git rev-list main --count
git rev-list main --merges --count
```

分类说明：

- “架构治理提交”是启发式分类，依据 subject 关键词和改动路径，不是严格 AST 归属。
- 当前版起点 `2026-06-26` 来自提交密度聚类，不是官方版本号。
- `2026-07-04` 的分支提交 `feat: 添加 agentic e2e 业务链路发现` 尚未进入 `main`，未纳入当前版主线统计。
