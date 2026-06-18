# Step 15-18：交接文档包、评审、修订和知识库维护

## Step 15：输出交接文档包

### 当前文档包组成

| 文档 | 覆盖内容 |
| --- | --- |
| `README.md` | 文档包索引、证据来源、未执行事项 |
| `01-handoff-goal-and-intake.md` | Step 1-2，交接目标、资产、环境、权限 |
| `02-business-requirements-agile.md` | Step 3-4、14，业务目标、需求、用户故事、Agile 流程 |
| `03-architecture-diagrams.md` | Step 5-6，上下文、容器、组件图 |
| `04-data-model.md` | Step 7，ER 图和数据字典 |
| `05-interfaces-and-sequences.md` | Step 8，核心接口和关键时序图 |
| `06-codebase-startup-quality.md` | Step 9-11，仓库结构、启动、核心流程、质量门禁 |
| `07-ci-cd-ops-incident.md` | Step 12-13，CI/CD、部署、回滚、监控、故障处理 |
| `08-delivery-review-knowledge-base.md` | Step 15-18，交付、评审、修订、长期维护 |

### 交付前检查

1. 文件均位于 `docs/ai01-docs/project-sop`。
2. 每个 Step 在 `README.md` 索引中有对应文档。
3. 每个命令、路径、模块名来自当前仓库读取结果。
4. 未实际执行的启动、测试、发布、权限检查被明确标记。
5. `git diff -- docs/ai01-docs/project-sop` 可解释且无无关改动。

## Step 16：组织交接评审会议

### 参会角色

- 当前维护者。
- 接手开发者或团队代表。
- QA 或质量负责人。
- 发布和运维负责人。
- 需要时加入产品或业务负责人。

### 建议议程

| 时间 | 议题 | 输出 |
| --- | --- | --- |
| 10 分钟 | 交接目标和范围 | 确认交接边界 |
| 15 分钟 | 业务目标、角色、核心流程 | 确认业务理解无偏差 |
| 20 分钟 | 架构、接口、数据模型 | 记录架构疑问和风险 |
| 20 分钟 | 本地启动和核心 smoke | 指派现场验证人 |
| 15 分钟 | 测试、CI/CD、发布、回滚 | 确认质量和发布责任 |
| 15 分钟 | 监控、日志、故障处理 | 确认故障分工 |
| 15 分钟 | 文档缺口和后续 Action | 形成修订清单 |

### 评审问题清单

1. 当前推荐启动入口是否仍是 `run-new-ui-desktop.sh` 和 `run-new-ui-desktop.ps1`？
2. GoLand 手动启动文档是否覆盖当前团队的 Windows/macOS 开发方式？
3. `cmd/agent-terminal/frontend` 是否还需要维护，还是仅作为 legacy bundle？
4. provider 登录态、密钥、配置和权限由谁维护？
5. 生产或发布数据库是否允许人工 rollback，审批链是什么？
6. 当前 GitHub Release 权限和签名证书是否完整？
7. CI 中 Node 20 和本地 Node 26 的差异是否接受？
8. 当前 hooksPath 指向其他 worktree 的问题是否已修复？
9. ELK 是否只是本地开发用途，是否存在正式日志平台？
10. 哪些老计划文档已废弃，哪些应成为正式知识库？

## Step 17：根据评审意见修订文档

### 修订流程

1. 把会议 Action 拆成具体文档修改项。
2. 每个修改项标注来源：评审人、日期、关联 issue 或 PR。
3. 涉及路径、命令、表名、RPC method 的内容必须重新读取代码确认。
4. 涉及启动、测试、部署的内容必须附执行结果或明确说明未执行。
5. 修订后由至少一名接手方复核。

### 修订记录模板

```markdown
## YYYY-MM-DD 修订记录

- 背景：
- 修改文件：
- 证据来源：
- 验证方式：
- 未验证项：
- 后续动作：
```

## Step 18：沉淀为长期维护的项目知识库

### 维护原则

1. 代码优先：行为描述必须以当前源代码、脚本、迁移、CI 或执行结果为依据。
2. 入口敏感：如果启动入口、UI 包、provider home、skill 路径变化，必须同步更新文档。
3. 版本敏感：Release、migration、schema version、runtime manifest 相关内容必须记录 commit 或 tag。
4. 明确过期：历史计划、迁移报告和旧测试报告不能默认当作当前事实。
5. 小步更新：每次只更新受影响文档，不重写整个文档包。

### 推荐更新触发条件

| 触发 | 必改文档 |
| --- | --- |
| 启动脚本变化 | `06-codebase-startup-quality.md` |
| UI 路由或 RPC 变化 | `05-interfaces-and-sequences.md`、`03-architecture-diagrams.md` |
| 数据库 migration 变化 | `04-data-model.md`、`07-ci-cd-ops-incident.md` |
| CI job 或 guard 变化 | `06-codebase-startup-quality.md`、`07-ci-cd-ops-incident.md` |
| 发布脚本变化 | `07-ci-cd-ops-incident.md` |
| 新 provider 或 MCP peer | `03-architecture-diagrams.md`、`05-interfaces-and-sequences.md` |
| 新故障复盘 | `07-ci-cd-ops-incident.md` |
| 交接评审完成 | `08-delivery-review-knowledge-base.md` |

### 知识库目录建议

```text
docs/ai01-docs/project-sop/
  README.md
  01-handoff-goal-and-intake.md
  02-business-requirements-agile.md
  03-architecture-diagrams.md
  04-data-model.md
  05-interfaces-and-sequences.md
  06-codebase-startup-quality.md
  07-ci-cd-ops-incident.md
  08-delivery-review-knowledge-base.md
```

后续如果需要加入真实启动证据，建议新增：

```text
docs/ai01-docs/project-sop/evidence/
  YYYY-MM-DD-startup-smoke.md
  YYYY-MM-DD-release-dry-run.md
  YYYY-MM-DD-incident-review.md
```

不要把临时日志、截图大文件、数据库 dump 或 secrets 放入文档目录。
