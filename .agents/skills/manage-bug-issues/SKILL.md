---
name: manage-bug-issues
description: 管理项目 Bug Issue 台账时使用：报告、检索、分诊、获得修复授权、写入修复结果、关联 Git 证据，或查询 Excel Issue、事件和证据记录时触发。
---

# Bug Issue 台账操作手册

将 Excel 工作簿视为 Bug 的长期事实源。通过 `.agents/skills/manage-bug-issues/scripts/issue_ledger.py` 查询或写入它；人和 AI 都不得直接编辑 `.xlsx`、使用电子表格库，或以其他脚本绕过台账。脚本、工作簿、命令或校验任一不可用时，立即停止并报告错误；不得猜测、补默认值、手工改表或静默降级。

## 规范路径

- 项目台账的唯一规范位置是项目根目录下的 `ISSUE.xlsx`，即 `<项目根>/ISSUE.xlsx`。
- 从项目根目录运行时，每次命令都显式传入 `--workbook ISSUE.xlsx`。
- 技能文档引用的相对资源必须以本 `SKILL.md` 所在目录 `.agents/skills/manage-bug-issues/` 为基准解析；例如技能内的 `scripts/issue_ledger.py` 解析为 `.agents/skills/manage-bug-issues/scripts/issue_ledger.py`。禁止默认按项目根目录解析技能相对资源。
- 从项目根目录执行命令时，台账脚本使用可移植的项目相对路径 `.agents/skills/manage-bug-issues/scripts/issue_ledger.py`。标准命令前缀为 `python3 .agents/skills/manage-bug-issues/scripts/issue_ledger.py --workbook ISSUE.xlsx`。
- 不得将脚本、工作簿或其他技能资源的本机绝对路径持久化到台账、Issue、Event、Evidence、说明或配置。
- `ISSUE.xlsx` 不存在时立即停止并报告；不得到项目外、旧目录或用户目录中搜索候选工作簿，也不得自动创建替代台账。

```bash
python3 .agents/skills/manage-bug-issues/scripts/issue_ledger.py \
  --workbook ISSUE.xlsx \
  <子命令及参数>
```

## 运行边界

- 每次调用都显式传入 `--workbook ISSUE.xlsx`。该项目相对路径是唯一允许的持久化定位；不得把机器绝对路径写入工作簿、Issue、Events、Evidence、说明或配置。
- 将本机 checkout 仅作为本次命令的运行时映射传入：`--repo-path <repo_id>=<本机绝对路径>`。映射只用于定位 Git，不得持久化。
- `Repositories` 只保存稳定身份，例如 `repo_id`、`canonical_url` 和项目定义的稳定元数据；不得保存 checkout 路径、用户名、主机名或其他机器特有信息。
- 保持 Excel 的受控表结构。不新增工作表、列、公式、手工筛选结果或未定义字段。
- 先运行 `python3 .agents/skills/manage-bug-issues/scripts/issue_ledger.py --help` 和目标子命令的 `--help`，以该版本暴露的命令、必填参数、枚举和输出为准。不要臆造子命令、字段名或状态名。

## 三类记录的职责

| 原语 | 意图 | 前置条件 | 写入规则 |
| --- | --- | --- | --- |
| `Issue` | 保存某个 Bug 的当前快照：身份、现状、责任、状态和结论。 | 有足以区分该 Bug 的复现/症状和受影响范围。 | 只由脚本创建或更新；每次变更必须留下相应 Event。 |
| `Event` | 保存不可变审计轨迹：谁在何时发现、分诊、获授权、开始、完成、阻塞或撤销了什么。 | 已有目标 Issue，且本次动作与事实相符。 | 仅追加；绝不编辑、删除、重排或复用历史 Event。 |
| `Evidence` | 只保存 Issue 与 Git 事实之间的映射。 | 有明确的 `repo_id`，并可由运行时 `--repo-path` 验证 Git 对象。 | 只写 `repo_id`、canonical full commit hash，以及脚本允许的可选仓库相对路径和 locator；不写分支、PR、比较范围、日志、截图、机器路径或推测。 |
| `Repository` | 标识稳定的 Git 仓库身份，供 Evidence 解析。 | 有稳定 `repo_id` 和 `canonical_url`。 | 不持久化本机路径；调用时才用 `--repo-path` 提供映射。 |

将 Issue 当前值与 Event 历史分开理解：Issue 可更新，Event 永远不可变；Evidence 不替代 Issue 结论，也不替代 Event 审计。

## 强制工作流

对人或 AI 发现的每个疑似 Bug，按以下顺序操作：

1. **先报告。** 用脚本创建或登记初始 Issue，并追加“发现/报告”Event。记录可验证的症状、复现条件、影响、发现者和时间；未知值明确标为未知，不得编造。
2. **再检索。** 用脚本检索相同症状、组件、错误签名、关联仓库和历史 Event。读取候选 Issue 的当前快照与完整事件历史，判断是同一问题、重复报告、回归还是独立问题。
3. **分诊并写回。** 将检索结论、重复关系、优先级/影响判断和下一步写入 Issue；同时追加分诊 Event。重复项只能按脚本支持的关联方式关联，不能抹除其历史。
4. **请求并记录授权。** 在任何代码、配置、数据或部署修复之前，取得明确的修复授权。将授权人、授权范围、目标 Issue 和时间写入 Event，并仅按脚本允许的迁移使 Issue 到达 `READY_TO_FIX`。没有授权只能调查和报告，不能修复。
5. **在授权范围内修复。** 仅处理获授权的 Issue、仓库和范围。修复前后都以脚本读取台账；不要把 Git 提交本身当作已写回的台账记录。
6. **无论结果都写回。** 成功、失败、无法复现、撤销、部分修复或受阻，均用脚本更新 Issue 当前结论、追加结果 Event，并在存在 Git 事实时写入 Evidence。
7. **回读校验。** 用脚本重新查询目标 Issue、相关 Events 和 Evidence，确认快照、追加事件、状态、授权和 Git 映射一致后才报告完成。

不要因发现了相关问题而跳过“先报告”。不要先改代码、后补 Issue；这会破坏授权和审计顺序。

## 状态与授权

仅使用以下既定枚举和脚本允许的可达迁移；不要自造中文状态、别名或未定义的捷径：

```text
REPORTED → TRIAGED → CONFIRMED → INVESTIGATING → READY_TO_FIX → FIXING → READY_TO_VERIFY → CLOSED

REOPENED / DEFERRED / DUPLICATE / MERGED / INVALID / CANNOT_REPRODUCE / WONT_FIX
只能按脚本定义从适用状态进入或离开。
```

- 只从当前状态迁移到脚本允许的下一状态；迁移失败即停止，不要直接改 Excel 绕过约束。
- 每次状态迁移都追加 Event，写明事实、操作者、时间和理由。
- 仅当明确授权已写入 Event 且 Issue 到达 `READY_TO_FIX` 后，才进入 `FIXING`。授权只覆盖记录范围；范围扩大、目标仓库变化、风险上升或新问题出现时，重新取得授权。
- `CLOSED` 前必须到达 `READY_TO_VERIFY` 并写入验证结论；若修复产生 Git 变更，还必须有已验证的 Evidence 映射。没有 Git 变更的结论也要写明原因，不能伪造 Git 证据。
- `DEFERRED`、`DUPLICATE`、`MERGED`、`INVALID`、`CANNOT_REPRODUCE` 与 `WONT_FIX` 都必须写回 Issue 并追加 Event；它们不是可以省略审计的失败路径。

## Git Evidence

- 先通过显式 `--repo-path <repo_id>=<本机绝对路径>` 让脚本定位仓库，再由脚本验证 Git 对象和仓库身份。
- 将每个 Evidence 关联到一个 Issue 和一个稳定 `repo_id`；写入 canonical full commit hash，必要时写入仓库相对路径和 locator。
- 不把分支、PR、比较范围、本机绝对路径、终端输出、截图、对话摘录、未推送的猜测或人工复制的 Git 信息写入 Evidence。仓库相对路径不是机器路径，且只能在脚本支持时写入。
- 证据验证失败、仓库映射缺失、`repo_id` 与 canonical URL 不一致、Git 对象不存在或工作树不符合脚本要求时，停止并报告；不要替换为“看起来合理”的值。

## 旁支 Bug 与范围控制

修复过程中发现的旁支 Bug 只能通过脚本新建一个 Issue，并追加其发现 Event；不得塞入当前 Issue、修改当前 Issue 的症状以容纳它，或在未授权下顺手修复。

为新 Issue 重复完整流程：报告、检索历史、分诊、取得该 Issue 的授权、修复、结果写回和回读校验。只有脚本提供的显式关联可用于表达两个 Issue 的关系。

## 每次调用的最低检查

1. 确认当前目录是项目根，且已显式传入 `--workbook ISSUE.xlsx`。
2. 确认操作是通过 `.agents/skills/manage-bug-issues/scripts/issue_ledger.py`，不是直接读写 `.xlsx`。
3. 对 Git 相关操作确认 `repo_id`、`canonical_url` 与本次 `--repo-path` 映射齐全；不持久化映射路径。
4. 检查脚本输出、退出状态和返回的 Issue/Event/Evidence 标识。任何错误、警告、缺字段、冲突、校验失败或意外空结果都停止并报告。
5. 写操作后立即回读目标记录，确认 Event 已追加而非覆盖、Issue 快照已更新且 Evidence 仅含允许的 Git 映射。

将命令输出、Issue ID、Event ID、Evidence ID、授权缺口和阻塞原因如实报告给请求方。没有完成回读校验，不得声称 Bug 已登记、已修复、已关闭或台账已更新。
