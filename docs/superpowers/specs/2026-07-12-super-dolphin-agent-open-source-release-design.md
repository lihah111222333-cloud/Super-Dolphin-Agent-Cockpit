# Super Dolphin Agent Open-Source Release Design

状态：已批准，待实现
日期：2026-07-12
范围：品牌与许可证统一、六语言 README、源码白名单导出、治理演示、开源上手与社区发行面

## 1. 背景

当前仓库已经具备多代理编排、MCP、LSP、DAG、session、memory、provider-native skills、codemap、project-map、capability contract、Archtest 与本地 gate 等工程能力，但现有对外发行面仍存在以下阻断：

1. 根 README 使用 `Super Agent v3`，Go module 使用 `github.com/anthropic-ai/super-agent-v3`，远端仓库、环境变量与产品内名称又使用 Super Dolphin，品牌和代码身份不一致。
2. 根 `LICENSE` 是 Unlicense，README 却声明 `Proprietary — Anthropic, Inc.`，无法形成清晰、可信的开源许可边界。
3. README 没有稳定的多语言入口、产品演示和直接解释治理差异的首屏叙事，Quick Start 仍包含 `<repo-url>` 占位符。
4. 私有开发历史包含 archive、内部问题记录、迁移计划、代理工作证据和 handoff 材料，不应因公开产品源码而一并发布。
5. 缺少可重复验证的源码公开策略、导出收据、演示、社区文档和公开 CI 门禁。

本设计建立“私有开发仓 + 干净公开发行仓”的双仓模式。当前仓库继续作为开发真源；公开仓库只接收由确定性白名单导出器从已提交源码生成并验证的候选内容。

## 2. 已批准决策

### 2.1 产品身份

- 产品名统一为 **Super Dolphin Agent**。
- 英文定位统一为 **AI-native software governance and multi-agent development control plane**。
- 规范公开仓库与 Go module 路径统一为 `github.com/lihah111222333-cloud/super-dolphin-agent`。
- 二进制名和环境变量继续使用 `super-dolphin` 与 `SUPER_DOLPHIN_*`，避免破坏已有用户配置和运行时数据目录。
- 活跃源码、测试、生成器、公开文档和生成物中的 `github.com/anthropic-ai/super-agent-v3` 必须迁移到规范 module path。
- 公开发行候选不得包含 `Anthropic, Inc.`、旧 module path、`Super Agent v3` 或把当前产品描述为 go-agent-v2 迁移项目的文案。

### 2.2 许可证

- 采用标准 Apache License 2.0 全文替换当前 Unlicense。
- 新增 `NOTICE`，版权主体使用 `Super Dolphin Agent contributors`。
- README、公开策略、社区文档和发行检查只引用 `Apache-2.0`，不维护第二份许可结论。
- 发现 Unlicense、Proprietary 或 Anthropic 许可声明时，公开导出和 verify 必须失败。

### 2.3 语言

- `README.md`：English，默认入口和语义基线。
- `README.zh-CN.md`：简体中文。
- `README.ja.md`：日本語。
- `README.ko.md`：한국어。
- `README.es.md`：Español。
- `README.de.md`：Deutsch。

六个 README 均为人工维护的自然语言文档。命令、路径、环境变量、规则 ID 和生成 marker 不翻译；动态事实由生成器统一刷新，不能手写六份可能漂移的数字。

### 2.4 发布动作边界

本次只做到“可公开发布”：完成代码、文档、许可证、导出器、示例、演示和验证，不创建公开 GitHub 仓库，不修改远端仓库名称，不推送公开分支，不创建 tag 或 GitHub Release。任何外部发布动作必须在导出候选完成后再次取得用户明确授权。

## 3. 目标

完成后应满足：

1. 当前开发仓能够从一个确定 Git commit 生成干净、可复查、带哈希收据的公开发行候选。
2. 新增文件默认不能进入公开候选；公开范围由一个版本化、机器校验的策略显式声明。
3. 公开候选具有一致的品牌、module path、Apache-2.0 许可和六语言 README。
4. 新用户能通过每个平台的一条 bootstrap 命令完成依赖检查、构建和最小 smoke test。
5. `make demo-governance` 能在临时目录中运行真实治理逻辑，产生可复现的 RED→GREEN 证据。
6. 社区能够通过文档、issue/PR 模板、security policy、公开 CI 和导出验证理解并验证项目。
7. 导出器、公开策略、演示和验证命令本身进入公开候选，使公开仓能够验证自己的发行边界。

## 4. 非目标

- 不把当前私有仓库直接改为 public。
- 不重写、清洗或公开当前私有 Git 历史。
- 不自动创建、重命名、推送或配置 GitHub 仓库。
- 不生成或发布尚未验证的平台安装包、签名包、badge、tag 或版本号。
- 不把 docs archive、内部问题记录、代理运行证据和迁移执行历史包装成公开文档。
- 不把源码公开导出职责塞进现有 `cmd/super-dolphin-release-manifest`；该命令继续只负责二进制更新 artifact 的签名 manifest。
- 不自动安装系统软件、不写全局 Codex/Claude 配置、不启动后台守护进程。
- 不把本次工作扩大成产品 UI 重设计或治理引擎通用化重构。

## 5. 方案选择

### 5.1 采用：双仓白名单导出

私有开发仓是开发真源；公开候选由版本化策略驱动的导出器生成。策略默认拒绝未分类路径，导出器只读取已提交 Git 对象，生成精确文件收据，并在输出目录重新执行 verify。

选择原因：

- 新增内部目录不会因遗漏黑名单而被自动公开。
- 私有开发历史与公开发行历史可以保持物理分离。
- 每次公开内容变化都有策略 diff、测试和收据证据。
- 符合仓库 fail-fast、单一事实源和生成物责任边界。

### 5.2 不采用：黑名单过滤

“复制全部再排除敏感目录”会让新增路径默认公开。它无法证明未来文件已经被分类，不符合默认拒绝要求。

### 5.3 不采用：只开源治理内核

单独抽取 codemap、capability contract 和 Archtest 会降低发布复杂度，但会失去桌面产品、多代理运行时、MCP/LSP/DAG 与治理闭环的完整演示。本次保持完整产品发行候选，但通过白名单严格收窄公开材料。

## 6. 总体架构

```text
private development repository
  + committed Git HEAD
  + release/open-source-policy.json
  + release/open-source-export.schema.json
             |
             v
cmd/super-dolphin-source-export
             |
             v
internal/devtools/sourceexport
  - policy validation
  - Git object enumeration
  - path and content admission
  - deterministic copy
  - receipt generation
  - exported-tree verification
             |
             v
empty external output directory
  - public source tree
  - OPEN_SOURCE_EXPORT.json
             |
             v
verify + gitleaks + repo-native gates
             |
             v
human-reviewed publication candidate
```

源码导出与二进制发布 manifest 是两个独立边界：前者决定哪些源码可以公开，后者决定发布 artifact 的更新签名和完整性。两者不能共用含糊的 manifest 类型或命令模式。

## 7. 组件与文件职责

### 7.1 `release/open-source-policy.json`

公开边界唯一规范输入，包含：

- `schema_version`
- `canonical_product_name`
- `canonical_repository`
- `canonical_module_path`
- `license_spdx`
- `required_root_files`
- `allow_rules`
- `deny_rules`
- `forbidden_identities`
- `required_readmes`
- `required_readme_sections`
- `forbidden_file_names`

策略使用仓库相对、slash-normalized 路径。未知字段、重复规则、空规则、绝对路径、`..`、反斜线、无效 glob、allow/deny 冲突和没有覆盖任何当前文件的必需规则均为错误。

`allow_rules` 只覆盖已批准公开面；未匹配任何 allow rule 的路径返回 `UNCLASSIFIED_PATH`。`deny_rules` 是对白名单内部敏感子树的额外硬拒绝，不能用于把全仓默认改成允许。

### 7.2 `release/open-source-export.schema.json`

固定策略 JSON Schema，供编辑器、CI 和导出器共同校验。Go 解析器仍必须拒绝未知字段，不能只依赖外部 schema 工具。

### 7.3 `internal/devtools/sourceexport`

聚焦的开发工具包，职责包括：

- 解析并严格校验公开策略。
- 通过 Git 命令读取指定 commit 的 tracked tree，不遍历工作区寻找候选文件。
- 拒绝 submodule、symlink、非普通 blob、路径逃逸和大小写冲突。
- 对每个 tracked path 应用 deny、allow 和内容策略。
- 将 blob 写入不存在或为空的外部目录。
- 规范化文件权限，只保留普通文件和已批准脚本的 executable bit。
- 生成稳定排序的文件 SHA-256 收据。
- 对现有导出目录重新计算并验证全部事实。
- 返回稳定错误码、目标路径和可操作原因。

包不拥有 Git push、GitHub API、tag、release、凭据或网络发布职责。

### 7.4 `cmd/super-dolphin-source-export`

可测试 CLI，提供：

```text
super-dolphin-source-export export --repo <abs> --commit <sha> --policy <path> --out <abs>
super-dolphin-source-export verify --dir <abs> --policy <path>
```

正式 `export` 必须要求：

- `--repo` 是可信 Git worktree 根。
- `--commit` 解析为单一 commit。
- 工作区和 index 干净；否则返回 `SOURCE_DIRTY`，避免用户误判未提交变更已被导出。
- `--out` 不在仓库根内，且不存在或为空。

CLI 不提供 `--force`、`--skip-checks`、`--ignore-dirty` 或遇错继续模式。

### 7.5 `OPEN_SOURCE_EXPORT.json`

导出候选根目录的机器可读收据，包含：

- receipt schema version
- public project identity
- source commit SHA
- policy SHA-256
- deterministic file count
- 每个文件的相对路径、模式、大小和 SHA-256

不写私有仓绝对路径、用户名、机器名、生成时间或其他导致相同输入产生不同输出的字段。verify 必须拒绝缺失、多余、篡改、模式变化或哈希不一致的文件。

### 7.6 Makefile

新增：

```text
make open-source-export OUT=/absolute/path COMMIT=<sha>
make open-source-verify DIR=/absolute/path
make demo-governance
make bootstrap
```

`open-source-export` 在 Go CLI 成功后对候选运行 verify 和 gitleaks。缺少 gitleaks 时正式公开导出失败并输出精确安装指令；单元测试不依赖本机 gitleaks。

## 8. 公开白名单

### 8.1 允许的主要内容

- 产品源码：`cmd`、`internal`、`pkg`、`frontend-app`
- 数据与构建：`sql`、`migrations`、必要的 `third_party`
- 项目规范技能：`.agents/skills`
- 当前架构事实：`docs/契约`、`docs/架构`、`docs/decisions`、`docs/adr`
- AI 导航：`docs/doc/codemap`
- 公开说明：`docs/open-source`、`docs/demo`、`examples`
- 构建与验证：`scripts`、`Makefile`、公开 `.github` 配置
- 根发行文件：六语言 README、`AGENTS.md`、Go/npm module 与锁文件、Apache-2.0、NOTICE、community files 和必要启动脚本

白名单必须继续细分不应公开的生成结果、fixture 和内部维护脚本；“主要内容”不是把整个顶层目录无条件放行的替代品。

### 8.2 永不公开的内容

- `docs/archive`
- `docs/cc`
- `docs/before`
- `docs/plans`、`docs/superpowers/specs` 和内部迁移/执行计划
- `.agent`、`.agnet`、工作流状态、DAG 运行证据、handoff 与 report
- `.worktrees`、`.workspace`、`.codex` 本地配置和构建缓存
- 数据库、WAL、日志、用户 memory、凭据、provider 私有 home 与本机配置
- 未跟踪文件、ignored 文件和 Git 历史

## 9. 品牌与 module path 迁移

活跃代码面中的 module path 迁移是一次原子变更：

1. 修改 `go.mod` module declaration。
2. 机械替换活跃 Go 源码、测试、脚本、生成器、公开 fixture 和当前生成文档中的旧 import path。
3. 刷新 codemap、project-map、capability contract 和由 module path 派生的生成物。
4. 运行旧身份扫描，确认公开候选零命中。
5. 历史 archive 与明确不公开的内部计划不因本次目标被改写；公开导出策略确保它们不能进入候选。

产品环境变量与数据库 home 不随 module path 改名。兼容性身份必须是代码显式需要的运行兼容项，不能作为 README 或公开品牌出现。

## 10. 六语言 README

六个 README 保持同一章节顺序和稳定 section marker：

1. 语言切换
2. 一句话定位
3. 产品截图与三分钟演示
4. Why this is not another agent framework
5. 核心治理闭环
6. 五分钟 Quick Start
7. 支持的 provider 与平台
8. 架构概览
9. 可复现验证
10. Roadmap、贡献、安全和许可证

根 README 的首屏必须直接回答：

- Super Dolphin Agent 是什么。
- 它治理的对象是什么。
- 它与只会执行任务的 agent framework 有何不同。
- 用户如何在五分钟内看到真实结果。

README 不再以内部目录树和迁移历史作为首屏。详细架构进入 `docs/open-source/ARCHITECTURE.md`，治理事实进入 `docs/open-source/GOVERNANCE.md`。

### 10.1 一致性 guard

新增测试必须验证：

- 六个文件全部存在且语言导航互相闭合。
- 稳定章节 marker、命令块数量和必需内部链接一致。
- module path、产品名、license SPDX 和 Quick Start 命令一致。
- 命令、路径、环境变量没有被翻译或改写。
- 动态统计只存在于生成 marker 内，并由同一生成器刷新所有 README。
- 禁止占位符、旧身份和失效相对链接。

guard 不宣称自动判断翻译语义质量；翻译内容由人工审阅，结构和机器事实由 guard 负责。

## 11. Bootstrap 与五分钟上手

提供：

```bash
./scripts/bootstrap.sh
```

```powershell
.\scripts\bootstrap.ps1
```

每个平台脚本执行相同阶段：

1. 识别仓库根和平台。
2. 检查 Go、Node、npm、Codex CLI、gopls、tsserver 与 typescript-language-server。
3. 验证版本和认证前置条件；缺失时输出精确安装命令并失败。
4. 使用锁文件安装前端依赖。
5. 构建前端 embed、Go 主程序和当前 worktree 的 LSP sidecar。
6. 运行最小 smoke test。
7. 输出唯一的桌面启动命令。

脚本不得自动安装系统包、修改全局配置、复用其他 worktree binary、吞掉失败或启动长驻进程。重复运行必须是幂等的。

## 12. 三分钟治理演示

`make demo-governance` 在临时目录运行，不修改用户工作区：

1. 创建最小 Go module fixture。
2. 写入业务 module 直接依赖 store 的违规版本。
3. 调用真实 backend boundary registry/evaluator，要求捕获指定 rule ID、文件和行号。
4. 将 fixture 改为通过 contract port 依赖。
5. 重新运行同一治理逻辑并要求零违规。
6. 输出稳定的 `demo-report.json`，包含 RED 违规、修复摘要和 GREEN 结果。

如果 RED 阶段未失败、命中了错误规则、没有精确位置、GREEN 仍有违规或报告不稳定，演示命令返回非零。README 只展示由实际命令产生并经测试锁定的输出，不手写虚构日志。

`docs/demo/governance-walkthrough.md` 解释场景、命令、预期证据和如何在真实仓库中理解同类违规。公开 CI 每次运行该演示，防止示例腐烂。

## 13. 社区发行面

新增：

- `CONTRIBUTING.md`
- `SECURITY.md`
- `CODE_OF_CONDUCT.md`
- `CHANGELOG.md`
- `SUPPORT.md`
- `NOTICE`
- `docs/open-source/ROADMAP.md`
- `docs/open-source/ARCHITECTURE.md`
- `docs/open-source/GOVERNANCE.md`
- `docs/open-source/RELEASE_CHECKLIST.md`
- `.github/ISSUE_TEMPLATE/*`
- `.github/pull_request_template.md`
- `.github/workflows/open-source-gates.yml`

约束：

- `SECURITY.md` 使用 GitHub Private Vulnerability Reporting 作为私密报告入口，不要求尚不存在的安全邮箱。
- `CHANGELOG.md` 从 `[Unreleased]` 开始，不伪造历史 release。
- Roadmap 区分已实现、正在验证和计划能力，不把计划写成现状。
- issue/PR 模板要求复现、验证命令、日志脱敏和治理影响面。
- 不添加无法证明、依赖尚未存在公开 workflow 或 release 的 badge。

## 14. 公开 CI

`.github/workflows/open-source-gates.yml` 至少执行：

1. 品牌、module path、Apache-2.0 和六语言 README 一致性 guard。
2. source-export policy/schema 单元测试。
3. 对当前 commit 生成临时公开候选并执行 verify。
4. gitleaks 对导出候选执行扫描。
5. `make demo-governance`。
6. 现有 Go、frontend-app 和 AI-maintenance gates 中适合公开 runner 的确定性子集。

公开 CI 不替代私有开发仓的本地 commit/push gates。两者验证不同边界：私有 gate 保护开发真源，公开 CI 证明发行候选可重复构建且没有公开策略漂移。

## 15. 失败模型

导出工具返回稳定错误码、相对目标和原因：

- `POLICY_INVALID`
- `UNCLASSIFIED_PATH`
- `FORBIDDEN_PATH`
- `FORBIDDEN_IDENTITY`
- `LICENSE_MISMATCH`
- `MODULE_PATH_MISMATCH`
- `SYMLINK_REJECTED`
- `CASE_COLLISION`
- `OUTPUT_NOT_EMPTY`
- `SOURCE_DIRTY`
- `SECRET_SCAN_FAILED`
- `EXPORT_RECEIPT_MISMATCH`

全部错误使命令非零退出。禁止跳过、警告后继续、静默排除未分类文件或自动放宽策略。

## 16. 安全边界

- 只读取已提交 Git blob；不从文件系统递归发现公开候选。
- 拒绝 submodule、symlink、设备文件、FIFO 和非普通 Git entry。
- 输出前验证目标真实路径不在源仓内部，拒绝通过 symlink 父目录逃逸。
- 检查 Unicode/大小写规范化冲突，确保 macOS、Linux、Windows checkout 结果一致。
- 文件写入使用受限权限和同目录临时文件；失败时删除未完成候选，不保留看似可发布的半成品。
- 内容扫描至少覆盖旧身份、本机绝对路径、高风险文件名和许可证冲突；正式导出再通过 gitleaks 扫描秘密。
- 日志不得打印文件内容、凭据、环境变量值或私有绝对路径；错误中只使用仓库相对路径。
- receipt 使用稳定排序和 SHA-256，verify 拒绝任何未登记文件。

## 17. 测试设计

### 17.1 Source export 单元测试

- 有效最小策略和临时 Git repo 成功导出。
- 未分类文件、deny 内嵌套文件和未知策略字段失败。
- 绝对路径、`..`、无效 glob、空 allow 和冲突规则失败。
- symlink、submodule、非普通 mode 和大小写冲突失败。
- 旧身份、错误 module path、错误 license 和禁止文件名失败。
- 非空输出目录和位于源仓内的输出目录失败。
- 工作区或 index dirty 时正式导出失败。
- receipt 稳定排序；缺失、多余、篡改和 mode 变化均使 verify 失败。
- 写入中途失败不会留下可被 verify 接受的候选。

### 17.2 README 与 community guard

- 六语言 README 全部存在。
- 语言导航、章节 marker、命令块、链接和动态 marker 对齐。
- 品牌、仓库 URL、module path、license SPDX 和 Quick Start 无漂移。
- 所有相对链接目标存在。
- 必需 community 文件和 GitHub 模板存在且不含占位符。

### 17.3 Governance demo

- RED fixture 被指定 rule 拒绝并带精确位置。
- 修复后的 GREEN fixture 通过。
- 将修复撤销后测试再次失败，以证明回归锁真实有效。
- report JSON 在相同输入下逐字节稳定。

### 17.4 集成验证

```bash
go test ./internal/devtools/sourceexport ./cmd/super-dolphin-source-export -count=1
go test ./scripts -run 'OpenSource|ReadmeI18N|GovernanceDemo' -count=1
make demo-governance
make open-source-export OUT=/tmp/super-dolphin-agent-public COMMIT=$(git rev-parse HEAD)
make open-source-verify DIR=/tmp/super-dolphin-agent-public
make codemap-check
make project-map-check
make capcontract-check
make guard
go vet ./...
( cd frontend-app && npm run lint && npm test && npm run build )
```

所有修改的 Go 和前端源文件还必须通过 worktree-local LSP diagnostics；Error、Warning、Information 和 Hint 均视为待处理项。

## 18. 验收标准

- 产品名、仓库 URL、Go module、README 和 Apache-2.0 许可一致。
- 六语言 README 具有闭环语言导航、相同机器事实和有效链接。
- 当前 commit 能生成一个默认拒绝、无禁止目录、无旧身份、无未登记文件的公开候选。
- `OPEN_SOURCE_EXPORT.json` 能发现候选中的任何增删改或 mode 漂移。
- `make demo-governance` 产生真实、稳定的 RED→GREEN 证据。
- bootstrap 在缺失依赖时 fail-fast，在满足依赖时完成构建和 smoke test，不修改全局状态。
- community docs、GitHub templates 和公开 CI 文件齐全且不含占位符。
- gitleaks、source verify、focused tests、codemap/project-map/capcontract checks、guard、vet 和 frontend lint/test/build 均有新鲜通过证据。
- 当前私有主工作区的无关 dirty 文件未被修改、stage 或带入提交。
- 没有创建、重命名或推送公开仓库，没有创建 tag 或 Release。

## 19. 实施顺序

1. 原子迁移品牌、许可证和 module path，并刷新受管生成物。
2. 建立 sourceexport policy/schema、Go 包、CLI、Makefile 接线和 RED→GREEN 测试。
3. 建立治理演示和稳定报告。
4. 重写英文 README，再完成五种翻译和结构 guard。
5. 补齐 bootstrap、公开架构/治理说明和 community 文件。
6. 增加公开 CI 与 release checklist。
7. 从干净 commit 生成候选，运行完整验证并人工复查导出边界。

每一步都必须保持可验证；共享 module path 和生成物迁移先串行完成，之后才能并行处理互不重叠的翻译、community docs 和 demo 文档。
