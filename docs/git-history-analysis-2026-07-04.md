# Git 历史提交统计分析报告

生成日期：2026-07-04

统计范围：执行 `git fetch --all --prune --tags` 后的本地全部 refs

核心口径：`git log --all` 统计所有可达唯一提交；`git log main` 单独统计主线

## 1. 执行摘要

Super-Dolphin 的 Git 历史从 2026-03-19 开始，主线在 2026-03-19 到 2026-07-03 之间累计 6520 个提交；全 refs 到 2026-07-04 累计 6619 个唯一提交。全历史只有 99 个提交尚未进入 `main`，因此主线基本覆盖了绝大多数历史活动。

整体演进呈现五个阶段：

| 阶段 | 主线提交数 | 主线变更行数 | 主要特征 |
|---|---:|---:|---|
| 2026-03 | 217 | 514,811 | V3 骨架、迁移起点、核心架构和 store/orchestration 奠基 |
| 2026-04 | 1,924 | 2,785,278 | 功能爆发期，skills/provider/codemap/docs 大量演进 |
| 2026-05 | 1,664 | 1,280,100 | MCP 编排、provider、桌面 UI 和集成分支集中推进 |
| 2026-06 | 2,618 | 2,604,292 | 最高活跃期，merge、风险修复、前端替换、代码地图和守卫治理集中发生 |
| 2026-07 | 97 | 32,346 | 收敛期，以 fix/docs/merge 为主，集中在风险阻断、Codex/provider、LSP 和守卫 |

最重要的结论：

- 历史不是线性的产品发布史，而是多代理、多分支、高频合并的工程活动史。
- `main` 已包含 98.5% 的全部可达提交，分支外溢不大，但分支上存在一个 403,424 行、2,297 文件的超大提交，合并前必须单独审查。
- 变更热点高度集中在 `docs/doc`、`cmd/agent-terminal`、`internal/module`、`frontend-app/src`、`cmd/mcp-orch` 和 provider/platform/store 相关目录。
- JSON 与生成型文档占据大量 churn，尤其 `docs/doc/codemap/ai-index.json` 单文件主线变更 1,356,512 行，显著放大历史噪音。
- 主线有 1,222 个 merge 提交，占 18.7%；重复 merge subject、重复修复 subject 和多 root 历史说明仓库曾长期采用并行 lane 集成。
- 没有任何 Git tag，当前无法用 tag 锚定发布、里程碑或可回溯基线。

## 2. 范围与拓扑

| 指标 | 全 refs | main |
|---|---:|---:|
| 唯一提交数 | 6,619 | 6,520 |
| 时间范围 | 2026-03-19 至 2026-07-04 | 2026-03-19 至 2026-07-03 |
| merge 提交 | 1,237 | 1,222 |
| merge 占比 | 18.7% | 18.7% |
| root 提交 | 3 | 3 |
| 变更行数 add+del | 7,730,898 | 7,216,827 |
| 提交规模 p50 | 113 行 | 113 行 |
| 提交规模 p95 | 2,359 行 | 2,290 行 |
| 单提交最大变更 | 403,424 行 | 132,860 行 |
| 单提交最大文件数 | 2,297 | 1,174 |

当前主线指针：

| ref | 短 hash |
|---|---|
| `main` | `9e3947baa325` |
| `origin/main` | `9e3947baa325` |

标签情况：无 tag。

root 提交：

| hash | 日期 | 作者 | subject |
|---|---|---|---|
| `9dc38d52` | 2026-03-19 | `lihah111222333-cloud` | `Initial commit` |
| `52f36b09` | 2026-03-19 | `hai` | `Initial commit` |
| `1238963f` | 2026-03-19 | `hai` | `chore: V3 project skeleton — migration from go-agent-v2` |

这说明早期历史不是单一 root 线性导入，而是至少三条初始历史后来被合并。

## 3. 分支与 refs

刷新后共有 79 个本地可见 heads/remotes refs。按分支名前缀统计：

| 前缀 | refs 数 |
|---|---:|
| `codex` | 43 |
| `fix` | 6 |
| `integration` | 6 |
| `work` | 3 |
| `瘦身` | 3 |
| `main` | 2 |
| `feat` | 2 |
| `feature` | 2 |
| `pr` | 2 |
| `upload` | 2 |

分支专属提交共 99 个，其中最近的是：

| hash | 日期 | 作者 | 类型 | subject |
|---|---|---|---|---|
| `262d5b13c0ef` | 2026-07-04 | L4place | feat | `feat: 添加 agentic e2e 业务链路发现` |
| `3e351316dab5` | 2026-06-17 | Toolbridge Test | chore | `chore(merge): sync main into optimize-code` |
| `f6710f08bf46` | 2026-06-17 | xhxhxxh | chore | `同步代码地图当前结构` |
| `f434a6872238` | 2026-06-16 | xhxhxxh | unclassified | `架构：完成 sidecar 迁移与模块治理` |
| `1c0039e17f25` | 2026-06-16 | xhxhxxh | unclassified | `提交剩余代码改动` |

分支专属集合的最大提交是 `1c0039e17f25`，403,424 行、2,297 文件。它没有进入 `main`，但如果未来准备合入，需要作为单独风险项审查。

## 4. 作者与身份分布

以下统计是 Git author identity，不等同于真实个人。仓库历史中有大量代理、测试身份和本地机器邮箱。

主线按 author identity：

| author | email | 提交数 | 占比 |
|---|---|---:|---:|
| Toolbridge Test | `toolbridge@test` | 960 | 14.7% |
| Toolbridge Test | `xiaoxiaotest9527@gmail.com` | 922 | 14.1% |
| hai | `mima0000@M3-pro-36G.local` | 896 | 13.7% |
| hai | `mima0000@DMMAC.local` | 778 | 11.9% |
| ai | `ai@aideMacBook-Pro.local` | 628 | 9.6% |
| xiaoxiaotest9527 | `xiaoxiaotest9527@gmail.com` | 380 | 5.8% |
| ai01 | `ai01@f666.com` | 300 | 4.6% |
| ai03 | `ai03@f666.com` | 264 | 4.0% |
| ai01 | `ai01@f666.com@ubuntu01.f666.com` | 260 | 4.0% |
| xiaoxiaotest9527-bit | `xiaoxiaotest9527@gmail.com` | 250 | 3.8% |

主线按 email 聚合：

| email | 提交数 | 占比 | 活跃时间 | 昵称样本 |
|---|---:|---:|---|---|
| `xiaoxiaotest9527@gmail.com` | 1,780 | 27.3% | 2026-04-13 至 2026-06-14 | Test Hardening Agent, Toolbridge Test, mac, xiaoxiaotest9527 |
| `toolbridge@test` | 960 | 14.7% | 2026-04-26 至 2026-05-12 | Toolbridge Test |
| `mima0000@M3-pro-36G.local` | 896 | 13.7% | 2026-05-14 至 2026-06-30 | hai |
| `mima0000@DMMAC.local` | 778 | 11.9% | 2026-03-21 至 2026-05-14 | hai |
| `ai@aideMacBook-Pro.local` | 628 | 9.6% | 2026-04-24 至 2026-06-11 | ai |
| `ai01@f666.com` | 300 | 4.6% | 2026-05-22 至 2026-06-23 | ai01 |
| `ai03@f666.com` | 264 | 4.0% | 2026-04-24 至 2026-06-22 | ai03 |
| `ai01@f666.com@ubuntu01.f666.com` | 260 | 4.0% | 2026-05-30 至 2026-06-09 | ai01 |

建议后续补 `.mailmap`，否则作者统计、责任归属和贡献趋势会持续失真。

## 5. 提交类型

主线 subject 类型统计：

| 类型 | 提交数 | 占主线比例 |
|---|---:|---:|
| fix | 1,507 | 23.1% |
| merge | 1,162 | 17.8% |
| feat | 1,101 | 16.9% |
| docs | 890 | 13.7% |
| unclassified | 598 | 9.2% |
| refactor | 360 | 5.5% |
| chore | 312 | 4.8% |
| test | 222 | 3.4% |
| risk | 40 | 0.6% |
| perf | 38 | 0.6% |
| revert | 34 | 0.5% |

观察：

- `fix + merge + feat + docs` 占主线约 71.5%，说明仓库长期处于快速迭代和持续集成状态。
- `unclassified` 有 598 个，主要来自中文非规范前缀或领域前缀。未来若要自动生成 release notes，需要统一 subject 规范或建立中文前缀映射。
- `test` 只有 222 个，但大量 fix/chore 提交可能包含测试改动；仅凭 subject 判断测试投入会低估。

## 6. 主题分布

主题是基于 subject 关键字的启发式分类，不是源码 AST 归属。

主线主题统计：

| 主题 | 命中提交数 |
|---|---:|
| other | 1,576 |
| provider-codex-claude | 1,075 |
| architecture-docs | 1,055 |
| mcp-orch-orchestration | 972 |
| frontend-ui | 914 |
| guards-tests | 763 |
| skills | 510 |
| mcp-lsp | 467 |
| storage-sqlite-store | 447 |
| packaging-release | 272 |
| observability-trace | 239 |
| security-failfast-risk | 212 |

解读：

- provider/Codex/Claude、架构文档、MCP orchestration、前端 UI 是四个最高频演进面。
- 2026-07 的主题明显转向风险阻断、provider/Codex、LSP 和守卫，说明近期不再以大功能展开为主，而是在收口稳定性和运行时安全。
- `other` 仍最高，说明 subject 对机制和模块归属的表达不够稳定。

## 7. 月度演进

### 2026-03：V3 初始化和架构迁移

主线 217 个提交，514,811 行 churn。主要类型是 `refactor`、`feat`、`docs`、`chore`，主题集中在 orchestration、guard/test、storage/sqlc。

代表性变化：

- V3 project skeleton，从 go-agent-v2 迁移开始。
- ADR-001/002/003 建立 provider convergence、jrpc2、fx/run/stateless 等基础选择。
- store/sqlc、provider、uistate、orchestration 等模块快速成型。

### 2026-04：功能和文档爆发

主线 1,924 个提交，2,785,278 行 churn，是第一个高峰。主要类型是 `feat` 586、`fix` 398、`docs` 358。

代表性变化：

- skills/provider/Codex/Claude 相关机制大量推进。
- codemap、AI index、架构文档反复生成和重建，带来巨大 JSON/Markdown churn。
- MCP、cron、memory、prompt、thread 等业务能力展开。

### 2026-05：集成与 UI 迁移

主线 1,664 个提交，1,280,100 行 churn。`fix` 430、`feat` 326、`merge` 308、`docs` 250。

代表性变化：

- MCP orchestration 和 provider 持续推进。
- Wails/React 迁移、frontend-app、客户端状态和测试开始成为热点。
- merge 数量明显上升，说明多分支并行集成加剧。

### 2026-06：最高活跃与治理期

主线 2,618 个提交，2,604,292 行 churn，是提交数最高月份。`merge` 798、`fix` 624、`docs` 236。

代表性变化：

- 前端架构切换和旧 Vue 删除：多个 100k 行级别提交。
- archtest、guard、hook、CodeTrust 风格审查和风险修复密集出现。
- comments/codemap/skill mirror/agent docs 等治理性改动大量进入主线。
- merge 提交集中，历史里出现多次重复合并 subject。

### 2026-07：风险收敛期

主线截至 2026-07-03 有 97 个提交，32,346 行 churn。类型为 `fix` 41、`docs` 24、`merge` 22、`chore` 8、`test` 2。

代表性变化：

- Codex 原生工具、动态工具禁用、scoped surface、resume 配置等 fail-fast 问题修复。
- LSP TypeScript 大纲、RPC 字段守卫、生产风险复核缺口修补。
- 本地 2026-07-04 分支新增 `agentic e2e` 业务链路发现，尚未进入 main。

## 8. 目录热区

主线按目录 churn 排序：

| 目录 | 提交数 | 文件事件 | add | del | churn |
|---|---:|---:|---:|---:|---:|
| `docs/doc` | 364 | 1,052 | 1,100,356 | 852,540 | 1,952,896 |
| `cmd/agent-terminal` | 676 | 7,940 | 570,073 | 569,274 | 1,139,347 |
| `internal/module` | 1,481 | 12,428 | 555,191 | 174,001 | 729,192 |
| `docs/other` | 205 | 1,567 | 322,150 | 251,010 | 573,160 |
| `frontend-app/src` | 575 | 2,904 | 261,021 | 103,970 | 364,991 |
| `cmd/mcp-orch` | 864 | 5,996 | 255,331 | 77,497 | 332,828 |
| `internal/platform` | 772 | 4,615 | 197,641 | 53,857 | 251,498 |
| `internal/provider` | 803 | 4,391 | 154,867 | 45,733 | 200,600 |
| `internal/store` | 307 | 2,140 | 95,746 | 35,169 | 130,915 |
| `cmd/mcp-lsp` | 407 | 2,555 | 100,138 | 27,727 | 127,865 |
| `internal/archtest` | 465 | 1,121 | 58,699 | 21,824 | 80,523 |
| `scripts` | 239 | 793 | 56,227 | 6,857 | 63,084 |

判断：

- `docs/doc` 是最大 churn 来源，尤其代码地图和索引生成物。
- `cmd/agent-terminal` 同时包含桌面 host、旧前端遗留和 embed 相关历史，因此 churn 很高。
- `internal/module` 提交数最高，说明业务域变化比单次生成文件更持续。
- `cmd/mcp-orch`、`internal/provider`、`internal/platform` 是核心运行时演进面。

## 9. 文件类型与热点文件

主线按扩展名 churn：

| 类型 | 提交数 | 文件事件 | churn |
|---|---:|---:|---:|
| JSON | 536 | 827 | 2,315,607 |
| Go | 3,377 | 36,892 | 2,006,521 |
| JS | 1,035 | 8,178 | 1,261,985 |
| Markdown | 约 1,663 | 约 7,908 | 约 702,848 |
| JSX | 438 | 1,713 | 252,167 |
| CSS | 298 | 626 | 227,964 |
| `package-lock.json` | 48 | 50 | 86,106 |
| CSV | 4 | 304 | 59,928 |
| TSV | 60 | 156 | 50,560 |
| Python | 48 | 164 | 44,204 |
| SQL | 346 | 813 | 32,662 |

Markdown 近似值来自 `.md` 与带引号路径解析分桶的合并。

主线热点文件：

| 文件 | 提交数 | churn | 备注 |
|---|---:|---:|---|
| `docs/doc/codemap/ai-index.json` | 274 | 1,356,512 | 最大单文件 churn，生成索引主噪音源 |
| `docs/.obsidian/plugins/realclaudian/main.js` | 4 | 417,892 | 大体积插件/构建类文件 |
| `docs/doc/ai-index.json` | 4 | 264,528 | 历史 AI index |
| `ai-index.json` | 4 | 264,528 | 历史根目录 AI index |
| `docs/doc/codemap/capability-contract/capability_manifest.json` | 4 | 195,260 | 能力清单生成物 |
| `cmd/agent-terminal/frontend/vue-app/data/en-zh-dict.json` | 8 | 118,144 | 旧前端大字典 |
| `frontend-app/src/styles.css` | 126 | 55,148 | 新 UI 样式热点 |
| `frontend-app/src/App.jsx` | 132 | 50,916 | 新 UI 根组件热点 |
| `frontend-app/src/pages/chat/ChatPage.jsx` | 142 | 30,966 | Chat 主页面热点 |
| `frontend-app/src/App.test.jsx` | 221 | 28,812 | 前端测试热点 |
| `frontend-app/src/entities/client/model/useClientStore.js` | 177 | 26,797 | 客户端状态热点 |

## 10. 大提交与风险点

主线最大变更主要集中在 revert、删除旧前端、生成索引和归档/治理：

| hash | 日期 | 作者 | churn | 文件数 | subject |
|---|---|---|---:|---:|---|
| `7bdf6a95e476` | 2026-06-28 | hai | 132,860 | 697 | `Revert "fix: 修复 archtest 代码审查 P1/P2/P3 问题"` |
| `9d4783fc3fac` | 2026-06-28 | lihah111222333-cloud | 132,860 | 697 | `Revert "fix: 修复 archtest 代码审查 P1/P2/P3 问题"` |
| `5d963e232467` | 2026-06-28 | hai | 124,329 | 570 | `chore: 删除旧 Vue 前端` |
| `87c9337ba5cd` | 2026-06-28 | hai | 124,329 | 570 | `chore: 删除旧 Vue 前端` |
| `f999208dd05a` | 2026-06-29 | hai | 119,449 | 564 | `前端: 删除旧前端文件` |
| `d48b1df6f3c3` | 2026-06-29 | hai | 119,449 | 564 | `前端: 删除旧前端文件` |
| `45565cb5a79c` | 2026-06-11 | ai01 | 112,266 | 24 | `回滚 PR31 误提交内容` |
| `2331d9a6ac6c` | 2026-06-11 | ai01 | 112,266 | 24 | `回滚 PR31 误提交内容` |
| `df705d2f0b71` | 2026-03-21 | hai | 97,161 | 487 | `原子推送` |
| `0e8cb221ce99` | 2026-03-21 | hai | 97,161 | 487 | `原子推送` |

按文件数排序，最大主线提交：

| hash | 日期 | 作者 | 文件数 | churn | subject |
|---|---|---|---:|---:|---|
| `51700a9ddecb` | 2026-06-26 | hai | 1,174 | 36,699 | `chore: 完成后端注释治理` |
| `f042a2903120` | 2026-06-26 | hai | 1,174 | 36,699 | `chore: 完成后端注释治理` |
| `88446880ea73` | 2026-06-14 | Toolbridge Test | 884 | 6,622 | `chore: 补充函数级中文注释并整理存储分层` |
| `84f2a3e66af8` | 2026-06-14 | Toolbridge Test | 884 | 6,622 | `chore: 补充函数级中文注释并整理存储分层` |
| `7bdf6a95e476` | 2026-06-28 | hai | 697 | 132,860 | `Revert "fix: 修复 archtest 代码审查 P1/P2/P3 问题"` |

风险解读：

- 多个大提交以成对形式出现，说明同一逻辑变更可能通过不同 lane 或重复集成进入历史。
- 旧前端删除、回滚和生成文件更新贡献了大量 churn，不应直接等同于业务复杂度。
- 单提交 p95 是 2,290 行，但最大提交达到 132,860 行；未来应对超过 p95 的提交强制拆分或附带审查说明。

## 11. 重复与历史噪音

高频重复 subject：

| subject | 次数 |
|---|---:|
| `Merge remote-tracking branch 'origin/main'` | 38 |
| `Merge branch 'yxf20260606' into 'main'` | 12 |
| `Merge branch 'codex/fix-codex-minimal-effort-tools' into 'main'` | 10 |
| `chore: atomic push` | 9 |
| `Merge branch 'codex/feature-integration-20260622' into 'main'` | 6 |
| `Merge branch 'codex/feature-integration-20260617' into 'main'` | 6 |
| `docs(codemap): refresh ai index` | 6 |

完全近似重复的例子：

| 日期 | subject | churn | 文件数 | 次数 |
|---|---|---:|---:|---:|
| 2026-06-08 | `Merge branch 'yxf20260606' into 'main'` | 0 | 0 | 12 |
| 2026-06-13 | `Merge branch 'codex/fix-codex-minimal-effort-tools' into 'main'` | 0 | 0 | 8 |
| 2026-05-14 | `Merge remote-tracking branch 'origin/main'` | 0 | 0 | 8 |
| 2026-06-28 | `fix: 修复 archtest 代码审查 P1/P2/P3 问题` | 304 | 11 | 4 |
| 2026-06-18 | `修复: remote launcher 注册 control RPC` | 275 | 3 | 4 |

这类重复不会破坏 Git 一致性，但会降低历史可读性，也会影响“提交数”等过程指标。

## 12. 当前历史质量评价

### 强项

- 主线覆盖度高：`main` 吸收了 6,520 / 6,619 个唯一提交。
- 核心领域变更可见：module、mcp-orch、provider、platform、store、frontend-app 均有充分历史。
- 风险治理在 2026-06 以后明显增强：guard、archtest、fail-fast、LSP diagnostics、RPC 字段守卫、provider-native skills 都有密集修复记录。
- 文档和 codemap 投入非常高，仓库可导航性优于一般快速迭代项目。

### 问题

- 无 tag，缺少发布和里程碑锚点。
- author identity 混杂，代理身份、个人邮箱、本地机器邮箱未统一。
- merge 和重复 subject 过多，历史审计需要额外去重。
- 生成文件 churn 过大，尤其 AI index/capability manifest/codemap JSON。
- subject 规范混杂，英文 Conventional Commit、中文前缀、领域前缀并存。
- 大提交尾部很重，回滚、删除、生成和批量注释治理容易掩盖真实业务变更。

## 13. 建议

1. 建立 tag 策略：至少为重大阶段补充 `v0.x`、`migration-*`、`ui-react-cutover-*`、`sqlite-cutover-*` 等轻量 tag。
2. 增加 `.mailmap`：把 `hai`、`Toolbridge Test`、`ai*`、本地机器邮箱和测试邮箱按真实角色或代理角色归一。
3. 制定 subject 映射：保留中文可读性，但统一 `修复:`、`文档:`、`测试:`、`维护:` 与 Conventional Commit 的统计映射。
4. 对超过 p95 的提交设门槛：主线超过 2,290 行或 30 文件时，应在提交说明中写明类型、原因和验证。
5. 生成物隔离：codemap/AI index/capability manifest 更新尽量独立提交，不和业务修复混在一起。
6. 分支合并前检查 branch-only 大提交：尤其 `1c0039e17f25` 这种 400k 行级别提交，不应直接合入。
7. 降低重复 merge 噪音：优先使用 fast-forward 或 squash 策略处理纯同步分支，保留真正需要的 merge commit。
8. 用月度报告替代裸提交数 KPI：当前提交数受代理、重复合并和生成物影响太大，目录 churn、风险修复和测试覆盖变化更有解释力。

## 14. 可复现命令

主要命令：

```bash
git fetch --all --prune --tags
git rev-list --all --count
git rev-list main --count
git rev-list --all --merges --count
git rev-list main --merges --count
git log --all --format='...' --numstat
git log main --format='...' --numstat
git for-each-ref --format='...' refs/heads refs/remotes
git tag --list
```

统计说明：

- churn 定义为 `additions + deletions`。
- merge 提交通常无 numstat diff，因此提交数统计和 churn 统计的解释边界不同。
- subject 主题分类使用关键词启发式，适合趋势判断，不适合作为严格模块归属。
- 文件名中含空格、引号或 rename 语法时，少量路径可能被 Git 文本输出转义，目录/扩展名统计会有轻微噪音，但不影响主要结论。
