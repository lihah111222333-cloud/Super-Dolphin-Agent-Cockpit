# P21 Fix-01 可执行修复清单 · v2

> 修订日期：2026-04-27
> 基础版本：[`fix01.md`](./fix01.md)
> 修订动因：三方互审（安全 / 工程可行性 / 计划完整性）共识
> 主要变更：补 T-00 Phase 0 前置；补 T-07b/T-09b 闭合 F8/F12；修正所有行号；T-01 防 TOCTOU；T-05 补齐高价值密钥模式；T-06 升至 128-bit；T-17 redirect 重校验细化；T-13 明确 allow-list 语义；新增日志脱敏 T-05.1；§五验收脚本化；§六补 4 项 kill-switch；§四 T-06 序列化点 + T-13 提到第 1 波。

---

## 变更摘要（vs v1）

| 类别 | 变更 |
|---|---|
| 新增任务 | **T-00**（Phase 0 ResolveCodexIdentity 锁定，前置）<br>**T-05.1**（日志/错误链路脱敏）<br>**T-07b**（F8 archtest 锁定 trajectory_collector 只读）<br>**T-09b**（F12 SkillsChanged 跨 scope 事件 + cwd 相对化） |
| 行号修正 | T-01 `:117-142` → `:135-143`；T-05 拆为 `:43-57` 与 `:95-106`；T-06 描述明确为"取前 12 hex char = 48 bit" |
| 安全升级 | T-01 强制 callerCwd 入参；T-05 表中显式列 12+ 高价值 token 家族；T-06 升至 128-bit；T-13 明确 allow-list；T-17 必须重跑 LookupIP+isBlockedIP；新增日志脱敏 |
| 工时调整 | T-03 由 M 升至 **M+**（依赖 actor binding） |
| 工程兜底 | T-06 补 DDL ALTER COLUMN；预占迁移编号 0065/0066/0067/0068；T-19 增加 CLI 版本前置确认；T-18 明确 router 是否需新建 |
| 验收 | §五 落地为 `docs/security/p21-redteam.sh` 5 条断言；webhook payload 快照 |
| 回滚 | §六 新增 T-03/T-12/P2/T-07 各自 kill-switch |
| 排程 | §四 T-06 设为第 1 波序列化锚点；T-13 提至第 1 波与 T-12 配对评审 |

---

## 一、文档与代码偏差总览

| 主线 | 文档标注 | 代码实际 | 偏差方向 | 处理 |
|---|---|---|---|---|
| Phase 0（共享契约） | 隐含 | ⚠️ ResolveCodexIdentity 已实现，但未作为单独前置锁定 | **未列出** | T-00 显式纳入 |
| P0a | ✅ 完成 | ✅ 完成 | 一致 | — |
| P0b | ⏳ 14 项缺陷 | F1/F2/F3/F4/F6/F7/F9/F10/F11/F13/F14 待修；F5 已部分；**F8/F12 隐含未验证** | **F8/F12 漏列** | T-01~T-11 + **T-07b/T-09b** |
| P1a | ⏳ 待启动 | ✅ 已实现 | **文档落后** | 文档升级为 ✅（T-20） |
| P1b | 🔲 未启动 | ✅ 95%（缺 turn 接线 + approval 白名单） | **文档严重落后** | T-12 / T-13 |
| P2 | 🔲 未启动 | ⚠️ ~40%（配置 + SSRF 已落，平台适配器全缺） | **文档落后** | T-14 / T-15 / T-16 / T-17 |
| P3 | 🔲 阻塞 | ⚠️ ~70%（DDL/聚合/flusher 已落，缺 HTTP 路由） | **文档落后** | T-18 |
| P4 | ✅ 80% parity | ⚠️ ~75%（缺 `--append-system-prompt` + ephemeral TTL） | **文档夸大** | T-19 |
| 迁移编号 | `0064_skill_candidates.sql` | 实际为 `0064_skill_candidates.sql`；下一可用为 `0065` | 编号漂移 | T-20 修正 |

---

## 二、任务总表（按优先级）

| 优先级 | 任务 | 主线 | 标题 | 工作量 |
|---|---|---|---|---|
| **P-1（前置）** | **T-00** | Phase 0 | ResolveCodexIdentity 契约锁定 + 复用审计 | S |
| **P0（数据安全红线）** | T-01 | P0b/F2 | 审批校验 caller cwd 指纹（防 TOCTOU） | S |
| P0 | T-02 | P0b/F3 | List/Lookup 增加 repo_fingerprint 过滤 | S |
| P0 | T-03 | P0b/F4 | Reviewer 身份认证落地 | **M+** |
| P0 | T-04 | P0b/F14 | List 接口剔除 RedactedSample 字段 | XS |
| P0 | T-05 | P0b/F6 | 脱敏模式补齐至 ≥15 类（含真实 provider token） | M |
| P0 | **T-05.1** | P0b/F6 | **错误链路与日志脱敏统一过 Redactor** | S |
| P0 | T-06 | P0b/F1 | RepoFingerprint 统一并升至 128-bit | M |
| P0 | T-17 | P2 | HTTP 重定向防 DNS rebinding 重校验 | S |
| **P1（架构正确性）** | T-07 | P0b/F7 | 拆分 observation Reader/Writer | M |
| P1 | **T-07b** | P0b/F8 | **archtest 锁定 trajectory_collector 只读** | S |
| P1 | T-08 | P0b/F10 | Store 区分 not-found vs 状态冲突 | S |
| P1 | T-09 | P0b/F11 | 实现 superseded 状态机 | M |
| P1 | **T-09b** | P0b/F12 | **SkillsChanged 跨 scope 事件 + cwd 相对化** | S |
| P1 | T-10 | P0b/F9 | approved 失败后的 resume 路径 | M |
| P1 | T-11 | P0b/F13 | extractor / approval 字节限额 + backpressure | S |
| P1 | T-12 | P1b | NoopTurnSubmitter 替换为 turn.Service | M |
| P1 | T-13 | P1b | approval_policy allow-list 字段 + 校验 | M |
| **P2（功能闭合）** | T-14 | P2 | Slack webhook 适配器 | M |
| P2 | T-15 | P2 | 钉钉 webhook 适配器（含 HMAC 签名） | M |
| P2 | T-16 | P2 | 飞书 webhook 适配器（含签名） | M |
| P2 | T-18 | P3 | `dashboard/insights/list` HTTP 路由挂载 | S |
| P2 | T-19 | P4 | `--append-system-prompt` + ephemeral 5min TTL | M |
| **P3（文档同步）** | T-20 | 全局 | 更新 P21 README 状态表 + 修正迁移编号 | S |

工作量记号：XS<0.5d、S=0.5–1d、M=1–3d、M+=2–4d、L=3–5d。

迁移编号预占：
- `0065_skill_candidates_promotion.sql`（T-10：promotion_attempt_count + last_promotion_error）
- `0066_skill_candidates_repo_fp_widen.sql`（T-06：repo_fingerprint 列宽至 VARCHAR(64)）
- `0067_cron_jobs_approval_policy.sql`（T-13：approval_policy_json）
- `0068_skill_candidates_audit_retention.sql`（T-13 关联：审计表 + 90d 保留触发）

---

## 三、详细任务卡

### T-00 · Phase 0 ResolveCodexIdentity 契约锁定 + 复用审计

**问题**
`internal/provider/shared/codex_identity.go:53-75` 已实现 `ResolveCodexIdentity`，但 P21 文档把它隐含在 P1a 内，未作为 P1b cron / P3 insight / 后续 P2 通知的共享契约显式冻结。任何后续修改都可能破坏下游消费方。

**修改**
1. 在 `internal/provider/shared/codex_identity.go` 顶部加注释块：标注"Phase 0 共享契约：禁止破坏性修改；新增字段须走 ADR"。
2. 新增 `internal/provider/shared/codex_identity_contract_test.go`：契约测试，固化输入/输出黄金用例（三元组缺失 → sentinel 错误；symlink 解析；env 展开）。
3. 审计现有调用点：`internal/module/cron/service.go:248`、`internal/module/thread/lifecycle.go:352`、`internal/provider/codexapp/driver_pool_routing.go:42` — 每处加单测断言"调用方不得绕过 Resolve 直接读 codexHome 字段"。
4. 在 `docs/plans/迁移/p21/README.md` 顶部加 Phase 0 段落，明确"ResolveCodexIdentity 已冻结，T-00 之后任何修改需走 ADR"。

**验证**
- 契约测试 100% 通过。
- grep 全仓 `codexHome` 直接读用法 → 仅出现在 `codex_identity.go` 内部。

---

### T-01 · P0b/F2 审批校验 caller cwd 指纹（防 TOCTOU）

**问题**
`internal/module/skill/candidate_review.go:135-143` 的 `validateCandidateApproval` 仅判断 `repo_fingerprint` 非空，未与 caller cwd 实际计算的指纹比对。**且即使补充检查，如果 callerCwd 来自请求 ctx value 而非不可变入参，SQL 读取与指纹校验之间存在 TOCTOU 竞态**。

**修改**
1. `ApproveCandidateParams` / `RejectCandidateParams` **结构体新增不可变字段** `CallerRepoFingerprint string`（不接受 cwd 字符串，只接受已经在 RPC 入口计算好的指纹）。
2. RPC handler 入口在解析参数时调用 `repofingerprint.Compute(authenticatedCwd)` 一次，写入入参；之后所有内部调用都用此值。
3. `validateCandidateApproval` 改为 `(ctx, raw, callerFp)`：先检查 `callerFp != ""`；再 `if raw.RepoFingerprint != callerFp → ErrRepoFingerprintMismatch`（新增 sentinel）。
4. 注：本任务依赖 T-06（统一指纹算法）先合入；并发开发时使用过渡 helper。

**验证**
- 单测：`callerFp=A, candidate.RepoFingerprint=B` → `ErrRepoFingerprintMismatch`。
- 单测：相同 → 通过。
- 单测：`callerFp=""` → `ErrCallerFingerprintRequired`。
- 集成：mock 两个不同 git 仓互调审批 → 全部拒绝；并验证从读 candidate 到 validate 之间无机会改 ctx。

---

### T-02 · P0b/F3 List/Lookup 增加 repo_fingerprint 过滤

**问题**
`sql/queries/skill_candidate.sql:16-20` 的 `ListPendingSkillCandidates` 没有 `repo_fingerprint` 过滤，跨仓枚举。

**修改**
1. SQL：`WHERE status = 'pending_review' AND repo_fingerprint = $1 ORDER BY created_at DESC LIMIT $2`。
2. 重新生成 sqlc，store/服务层签名透传 caller fingerprint（与 T-01 同源，不接受 cwd 字符串）。
3. 入参强制校验：`repo_fingerprint` 必须满足 `^[0-9a-f]{32}$`（128-bit 后），否则拒绝执行 SQL（防止任何字符串注入企图）。

**验证**
- 单测：仓 A 调用 list 仅看到 A 的候选；非 hex 入参 → 拒绝。
- 集成：仓 B 看不到 A 的候选。

---

### T-03 · P0b/F4 Reviewer 身份认证（M+）

**问题**
当前 `ApprovedBy` 是 caller 自报字符串，无任何验证。即便补 ctx 抽取，若 `Source` 字段也由 caller 传入，攻击者可伪造 `source=internal_admin`。

**修改**
1. 新增 `internal/module/skill/reviewer_authn.go`：
   - `ReviewerIdentity{Email string; Source ReviewerSource}`，`ReviewerSource` 为 enum：`SourceMCPAuthenticated / SourceOAuth2GitHub / SourceLDAP / SourceCLILocal`。
   - `ReviewerAuthenticator interface { Resolve(ctx) (ReviewerIdentity, error) }`，由 RPC 中间件实现，**只从经过验证的请求上下文抽取**（mcp 鉴权层 / OAuth header 签名校验后），不读 caller 自报字段。
2. `ApproveCandidate` / `RejectCandidate` 调用前必须先 `Resolve`；落库时使用解析结果覆盖 caller 自报值。
3. 审计字段：`approved_by`、`approved_source`、`approved_at` 同步写入（`approved_source` 持久化为 enum 字符串）。
4. **Feature flag**：`SKILL_REVIEWER_AUTHN_REQUIRED`（默认 `1`），灰度期可设 `0` 临时降级；降级时仍记录 `approved_source=DEGRADED_SELF_REPORT` 便于事后审计。
5. **依赖**：本任务取决于 RPC/wails 层是否已有 actor binding。若无，需先打 RPC 中间件脚手架（额外 1-2d，故工时升至 M+）。

**验证**
- 单测：未注入 identity 的 ctx + flag=1 → 拒绝。
- 单测：caller 自报 `approved_by=root` 与 ctx identity=alice → 以 ctx 为准，audit log 记录 alice。
- 单测：flag=0 + 无 ctx → 通过但 audit `approved_source=DEGRADED_SELF_REPORT`。

---

### T-04 · P0b/F14 List 接口剔除 RedactedSample 字段

**说明**：现状 `internal/module/skill/contract.go:80-92` 的 `Candidate` 结构与 List 响应共用，唯一需要剔除的就是 `RedactedSample`（line 90）；不必引入完整 Summary/Detail 拆分。

**修改**
1. 新增 `CandidateListItem`：复制 `Candidate` 所有字段，**唯独剔除 `RedactedSample`**。
2. `ListPendingCandidates` / `LookupApproval` 响应类型改为 `CandidateListItem`。
3. 新增 `GetCandidateByID(id)` RPC，返回完整 `Candidate`（含 RedactedSample，仅供单条审阅）；该接口同样走 T-03 的 ReviewerAuthenticator。
4. 测试快照覆盖 List 序列化 JSON，断言无 `redacted_sample` 键。

**验证**
- 单测：序列化 `CandidateListItem` 无 `redacted_sample`。
- 集成：调 list endpoint 抓响应体确认。

---

### T-05 · P0b/F6 脱敏模式补齐至 ≥15 类

**问题**
`internal/module/turn/redaction.go:43-57` 仅 6 类（bearer / JWT / credential_env / http_cookie / long_hex / long_base64）。

**修改**
新增并验证以下 12+ 模式（每类一条表驱动用例，使用真实 provider 文档样例）：

| # | 类别 | 模式样例 |
|---|---|---|
| 1 | Authorization Basic | `Authorization: Basic [A-Za-z0-9+/=]{20,}` |
| 2 | x-api-key / X-Auth-Token | header 行命中 |
| 3 | password / passwd / pwd | form/query/json `password\s*[:=]\s*\S+` |
| 4 | PEM 私钥块 | `-----BEGIN (RSA\|EC\|OPENSSH\|PGP\|DSA\|ENCRYPTED) PRIVATE KEY-----[\s\S]+?-----END` |
| 5 | AWS AKID + Secret | `AKIA[0-9A-Z]{16}` + 邻近 40 字符 base64 |
| 6 | GitHub token | `(ghp\|gho\|ghs\|ghu\|github_pat)_[A-Za-z0-9_]{20,}` |
| 7 | Slack token | `xox[baprs]-[A-Za-z0-9-]{10,}` |
| 8 | Google API Key | `AIza[0-9A-Za-z_-]{35}` |
| 9 | **Anthropic** | `sk-ant-(api\|adm)\d{2}-[A-Za-z0-9_-]{20,}` |
| 10 | **OpenAI** | `sk-(proj-)?[A-Za-z0-9_-]{20,}` |
| 11 | **Stripe** | `(sk\|pk\|rk)_(live\|test)_[A-Za-z0-9]{20,}` |
| 12 | **Google Service Account JSON** | `"type"\s*:\s*"service_account"` 整块 JSON |
| 13 | **Notion / Linear** | `(secret_\|ntn_)[A-Za-z0-9]{20,}`、`lin_(api\|oauth)_[A-Za-z0-9]{20,}` |
| 14 | **Azure 连接字符串** | `(DefaultEndpointsProtocol\|AccountKey)=[A-Za-z0-9+/=]+` |
| 15 | **JDBC URL with password** | `jdbc:[^?\s]+\?[^\s]*password=[^&\s]+` |
| 16 | **`.npmrc` auth** | `_authToken=|//.+/:_password=` |
| 17 | **age / sops 头** | `-----BEGIN AGE ENCRYPTED FILE-----` 或 sops `lastmodified` 块 |

脱敏失败 fail-closed：candidate 标记 `dropped` 而非 `pending_review`，并发出 `redaction_candidate_dropped{reason}` 计数器。

**验证**
- 表驱动单测每模式正反例。
- 集成：构造含全部 17 类 secret 的 trajectory → 抽取候选体内 grep 全部 17 模式 0 命中。
- 监控：`redaction_candidate_dropped` 比率 > 5% 触发 P-2 告警。

---

### T-05.1 · 错误链路与日志脱敏（新增）

**问题**
即使数据通道脱敏到位，错误返回与日志输出仍可能在 `error.Error()` / stack trace 中泄漏 secret 片段。

**修改**
1. 新增 `internal/platform/redactlog/wrapper.go`：包装 zap/slog logger，每次落字符串前过 `Redactor.Redact()`。
2. 新增 `RedactedError` 类型：包装原始 error，`Error()` 返回脱敏后字符串。
3. RPC 顶层 recovery + error 返回路径强制经过 RedactedError。
4. 重要 audit 表（skill_candidate / cron_job_runs / session_insights）写入 message 字段前同样过 Redactor。

**验证**
- 单测：构造含 `sk-ant-XXX` 的 error，断言 logger 输出与 RPC 响应均无原文。
- 集成：故意触发 approval 失败，secret 出现在 candidate slug → 日志/响应均脱敏。

---

### T-06 · P0b/F1 RepoFingerprint 统一并升至 128-bit

**问题**
- `internal/module/turn/redaction.go:95-106` 当前实现 `hex.EncodeToString(sum[:])[:12]` = **取前 12 hex char = 48 bit**。
- 与 skill 模块若有第二实现，长度/算法不一致。
- 即便统一为 64-bit，~4B 仓时碰撞概率达 2^32（生日攻击），用于跨仓隔离边界不安全。

**修改**
1. 新建 `internal/platform/repofingerprint/fingerprint.go`，单一实现：`func Compute(cwd string) (string, error)`，返回 **完整 32 hex 字符（128-bit，SHA-256 截断）**。
2. turn / skill / cron / insight 全部改为调用该包；删除旧实现。
3. **DDL 迁移** `0066_skill_candidates_repo_fp_widen.sql`：
   ```sql
   ALTER TABLE skill_candidates ALTER COLUMN repo_fingerprint TYPE VARCHAR(64);
   -- 64 字符列宽足够容纳 32 hex 字符，预留扩展。
   ```
4. **双写过渡**：升级期间 Compute 返回 32 字符；旧 12 字符数据保留，store 层比对时若 candidate 字段长度 ≤12，视为遗留并回退到旧算法比对（仅审批前临时容忍 7 天）。
5. 一次性脚本 `scripts/migrate_repo_fingerprint.go` 在 7 天窗口内重算所有 pending candidates；窗口结束后删除回退分支。

**验证**
- 单测：相同 cwd 多模块下结果一致，长度恒为 32。
- 单测：1M 随机 cwd 样本零碰撞。
- 集成：T-01 cwd 比对在升级后仍通过；旧数据 7 天容忍期内可正常审批；之后旧数据全部被脚本重算。

---

### T-07 · P0b/F7 拆分 observation Reader/Writer

**问题**
`internal/module/turn/observation/contract.go:107-190` 单一 `Contract` 接口同时具备读写能力。

**修改**
1. 拆出 `ObservationReader`（GetByLocalTurnID / GetByCallID / Stream / Query 等只读）与 `ObservationWriter`（AttributeCall / Dedupe / WriteSnapshot）。
2. fx 装配：发布 Reader 给 trajectory_collector / insight.Flusher / extractor；Writer 仅注入 turn 内部聚合层。
3. 旧 `Contract` 标记 deprecated，下一 PR 删除。

**验证**
- 编译期：trajectory_collector / insight 模块无法 import Writer 方法。
- 自定义 vet：`build/vet_observation_split.log` 0 报警；任何越权 import 阻断编译。

---

### T-07b · P0b/F8 archtest 锁定 trajectory_collector 只读（新增）

**问题**
T-07 拆接口后，trajectory_collector 历史上仍可能残留对 observation 的写调用（F8 原始风险），需要兜底机制持续阻断回归。

**修改**
1. 移除 `internal/module/turn/trajectory_collector.go` 内任何 `AttributeCall / Dedupe / WriteSnapshot` 残留（grep 应已为 0；本任务做最终确认）。
2. 新增 `internal/archtest/trajectory_readonly_test.go`：用 `go/ast` 解析 trajectory_collector.go，扫描所有调用，禁止命中 ObservationWriter 方法集；命中即 fail。
3. 在 CI `make test` 中纳入 archtest。

**验证**
- `grep -E 'AttributeCall|Dedupe|WriteSnapshot' internal/module/turn/trajectory_collector.go` → 0。
- 故意在 collector 加一行 Writer 调用 → archtest 应失败；移除后通过。

---

### T-08 · P0b/F10 Store 区分 not-found vs 状态冲突

**问题**
`sql/queries/skill_candidate.sql:22-42` 状态守卫 UPDATE 在行不存在与状态不匹配两种情况都返回 `pgx.ErrNoRows`。

**修改**
SQL 改写为 CTE：先 SELECT 探测存在性，再带状态守卫 UPDATE，返回 `(existed bool, updated bool)`。store 层根据组合返回 `ErrCandidateNotFound` 或 `ErrCandidateStateMismatch`。

**验证**
- 单测：删除候选后 update → ErrCandidateNotFound。
- 单测：状态不符 → ErrCandidateStateMismatch。

---

### T-09 · P0b/F11 实现 superseded 状态机

**修改**
1. 新增 SQL `MarkCandidateSuperseded`：把同 `(scope, slug, repo_fingerprint)` 维度旧 pending 改为 superseded。
2. extractor 写入新 candidate 时事务内调用，确保同维度只剩一条 pending。
3. List 默认排除 superseded（已隐含）。

**验证**
- 单测：连续两次抽取 → 旧 superseded、新 pending。
- 单测：list 不返回 superseded。

---

### T-09b · P0b/F12 SkillsChanged 跨 scope 事件 + cwd 相对化（新增）

**问题**
- 同 slug 跨 scope（project / system）同时触发更新时，SkillsChanged 事件 buffer 可能丢消息。
- 事件 payload 中含绝对路径 cwd，泄漏宿主机目录结构。

**修改**
1. SkillsChanged 事件结构调整：增加 `(scope, slug)` 复合 key 字段；buffer 用 map[(scope,slug)] → 不同 scope 同 slug 不会互相覆盖。
2. cwd 字段改为 `repo_fingerprint`（128-bit hex）+ `relative_path`（相对仓根的相对路径）；不发送绝对路径。
3. 订阅方（catalog/resolver）按 `(scope, slug, repo_fingerprint)` 反查。

**验证**
- 单测：并发 project + system 各发一条同 slug 事件 → 订阅方收到 2 条，无丢失。
- 单测：事件序列化无绝对路径字段。

---

### T-10 · P0b/F9 approved 失败后的 resume 路径

**修改**
1. DDL 迁移 `0065_skill_candidates_promotion.sql`：增列 `promotion_attempt_count INTEGER DEFAULT 0`、`last_promotion_error TEXT`。
2. approve 流程：先标 approved → 调 CreateSkill；失败时记录 `last_promotion_error`、`attempt_count++`，candidate 维持 approved。
3. 新 RPC `RetryPromotion(candidate_id)`：仅 approved 状态可调，重新执行 CreateSkill；成功转 `written`。
4. 后台自动 retry 留下一期。

**验证**
- 集成：mock CreateSkill 第一次失败 → candidate 保持 approved + last_promotion_error 写入；调 RetryPromotion → 转 written。

---

### T-11 · P0b/F13 字节限额 + backpressure

**修改**
1. 配置常量：`MaxTrajectoryBytes = 256*1024`、`MaxSkillMDBytes = 64*1024`、`MaxCandidatesPerRepo = 200`。
2. extractor 入口校验 trajectory 总字节；超限丢弃 + WARN 日志（含 repo_fingerprint / size / limit）+ metric `extractor_trajectory_dropped` + span event。
3. SQL 写入前校验 skill_md 字节。
4. CreateCandidate 前 SELECT count(*) WHERE status='pending_review' AND repo_fingerprint=$1，达上限 → 拒绝并返回 `ErrCandidateQuotaExceeded`，metric `+1`。
5. SLO：`extractor_trajectory_dropped / total_extractions < 1%`。

**验证**
- 单测：超限 → 丢弃 + counter +1。
- 单测：候选数达上限 → `ErrCandidateQuotaExceeded`。

---

### T-12 · P1b NoopTurnSubmitter 替换为 turn.Service

**修改**
1. cron 模块 fx wiring：注入 `turn.Service` 适配器实现 `TurnSubmitter`。适配器层避免 cron 直接依赖 turn 包。
2. 适配器 `StartTurn(ctx, params)`：透传 dedupe_key / prompt / provider / skills / **approval_policy**（来自 T-13）。
3. 错误映射：turn 层 `ErrDuplicateDedupeKey` → cron scheduler 跳过 markRunSubmitting 直接进 observe 阶段。
4. **观测**：`cron_lease_lost{job_id}` 事件；P-1 告警若同 job 1h 内丢租 ≥3 次。
5. **依赖**：T-13 schema 必须先合入；本任务接线时直接读 approval_policy_json 列。

**验证**
- 集成：cron 触发 → cron_job_runs 出现 running → finished；turn 表对应 turn_id。
- 集成：crash 模拟（kill 在 submitting 阶段）→ 重启后 RecoverDanglingRuns 通过 dedupe lookup → 不重复提交。
- 集成：故意构造重复 dedupe_key → 适配器返回 `ErrDuplicateDedupeKey` → scheduler 走 observe 路径不报错。

---

### T-13 · P1b approval_policy allow-list

**问题**
P21 spec 要求 cron 非交互场景 approval 是白名单（allow-list），不是 blanket auto-approve。

**修改**
1. DDL `0067_cron_jobs_approval_policy.sql`：`cron_jobs` 加 `approval_policy_json JSONB NOT NULL`（无默认，必填）。
2. 内容契约（明确 allow-list 语义）：
   ```json
   {
     "provider": "codex",
     "sandbox": "strict",
     "tools_allowed": ["file.read", "file.write"]
   }
   ```
   - `tools_allowed` **是 allow-list**：不在列表的工具调用一律拒绝。
   - `tools_allowed: []`（空数组）= **拒绝所有工具**（fail-closed），不是放行所有。
   - 创建/更新 cron job 时校验：provider/sandbox 与 cron job 自身字段一致；`tools_allowed` 至少有一个或显式注明 `[]`。
3. service 层创建 cron job 时校验 policy 与 provider/skills 兼容；缺 `approval_policy_json` 拒绝创建。
4. driveJob 把 policy 透传到 turn 层；turn 层据此放行/拒绝具体工具调用，被拒的调用写 audit 记录 `tool_call_denied`。
5. 审计表保留：`SKILL_AUDIT_LOG_RETENTION=90d`，迁移 `0068` 加保留触发器。

**验证**
- 单测：缺 `approval_policy_json` → 创建拒绝。
- 单测：`tools_allowed=[]` → 任何工具调用被拒。
- 集成：`tools_allowed=["file.read"]` 但 turn 内尝试 `file.write` → 被拒 + audit `tool_call_denied`。

---

### T-14 · P2 Slack webhook 适配器

**修改**
- 新建 `internal/module/notify/platform/slack/adapter.go`：渲染 turn-completed / cron-finished / cron-failed 三种事件为 Slack `blocks` payload。
- 复用 `webhook.WebhookClient`（含 T-17 的 redirect 重校验）。
- 注册到 `platform.Registry`。
- 不需要签名（Slack incoming webhook URL 即凭据）。

**验证**
- 单测：3 种事件 payload 快照测试。
- 本地：echo http server 收一条断言 JSON 正确。

---

### T-15 · P2 钉钉 webhook 适配器

**修改**
- 新建 `internal/module/notify/platform/dingtalk/adapter.go`。
- payload：`{"msgtype":"markdown","markdown":{"title":"...","text":"..."}}`。
- 签名：HMAC-SHA256(secret, "<timestamp>\n<secret>")，URL 追加 `&timestamp=&sign=`。
- HTTPS-only。

**验证**
- 单测：HMAC 计算用钉钉官方文档样例值校验。
- 集成：mock 端点校验 query string 含 timestamp/sign。

---

### T-16 · P2 飞书 webhook 适配器

**修改**
- 新建 `internal/module/notify/platform/feishu/adapter.go`。
- payload：飞书 interactive card 或 text。
- 签名：HMAC-SHA256(secret, "<timestamp>\n<secret>")，body 追加 `timestamp` / `sign`。
- HTTPS-only。

**验证**
- 单测：payload + 签名飞书官方样例校验。

---

### T-17 · P2 HTTP 重定向防 DNS rebinding 重校验

**问题**
`internal/module/notify/platform/webhook.go` 的 `CheckRedirect` 当前仅校验 scheme，未对 redirect 目标重新解析 IP；存在 DNS rebinding bypass。IPv6 zoneid（`fe80::1%eth0`）也未显式拒绝。

**修改**
1. `http.Client.CheckRedirect`：
   ```go
   func(req *http.Request, via []*http.Request) error {
       if len(via) >= 5 { return errors.New("too many redirects") }
       if req.URL.Scheme != "https" { return ErrNonHTTPS }
       host, _, err := net.SplitHostPort(req.URL.Host)
       if err != nil { host = req.URL.Host }
       if strings.Contains(host, "%") { return ErrIPv6ZoneIDForbidden }
       ips, err := net.DefaultResolver.LookupIP(req.Context(), "ip", host)
       if err != nil { return err }
       for _, ip := range ips {
           if isBlockedIP(ip) { return ErrDisallowedAddress }
       }
       return nil
   }
   ```
2. `http.Transport.Proxy = nil` 显式禁用 env proxy。
3. CONNECT / WebSocket Upgrade：在 webhook payload 路径只用 POST，文档化"客户端不支持 CONNECT / Upgrade"。

**验证**
- 单测：mock server 跳转到 `127.0.0.1` → 拒绝。
- 单测：URL host 含 `%` → `ErrIPv6ZoneIDForbidden`。
- 单测：env `HTTP_PROXY=...` → 实际请求不走 proxy。
- 单测：5 跳后 → 拒绝。

---

### T-18 · P3 dashboard/insights/list HTTP 路由

**问题**
`internal/module/insight` service 层有 `ListRecent` / `ListByThread` 等方法，但 dashboard router 不存在。

**修改**
1. 检查现有 dashboard 路由约定（grep `dashboard/`）；若无 router 模块，先在 `internal/module/dashboard/router.go` 建脚手架，并在 fx 中装配。
2. 新增 `internal/module/insight/rpc.go`：注册 `dashboard/insights/list` 与 `dashboard/insights/get`。
3. 入参：`{thread_id?, limit, offset, before_created_at?}`；出参：分页 snapshot 列表。
4. 鉴权：复用 dashboard 现有鉴权策略；若 dashboard 无鉴权层，本任务暂用与 T-03 相同的 ReviewerAuthenticator 验证。

**验证**
- 集成：跑一轮 turn → flusher 落表 → 调 `dashboard/insights/list` 拿到 snapshot。

---

### T-19 · P4 `--append-system-prompt` + ephemeral 5min TTL

**问题**
- `internal/provider/claudecli/transport_config.go:236-241` 当前 CachedPrefix + UncachedTail 都用 `--system-prompt` 多次叠加；P21 设计要求 UncachedTail 走 `--append-system-prompt`。
- `cache_control` 未设 `ttl=300s, type=ephemeral`。
- **CLI 版本前置**：仓内全文 grep `--append-system-prompt` 0 命中，需先确认 Claude CLI 版本支持该 flag。

**修改**
1. **前置确认**：在执行修改前，验证项目锁定的 Claude CLI 版本是否支持 `--append-system-prompt`；不支持则升级 CLI 或提交 ADR 解释 fallback。
2. transport_config 区分两个 block：CachedPrefix → `--system-prompt`；UncachedTail → `--append-system-prompt`。
3. CachedPrefix 段落附 cache_control 元数据：`{type:"ephemeral", ttl_seconds:300}`，经 CLI 透传 Anthropic API。
4. 若 CLI 不支持透传：fallback 为 provider 自管 prompt cache（沿用 InputScoped + TTL=300s）；ADR 落档。
5. **Feature flag**：`PROMPT_CACHE_LEGACY=1` 一键回旧路径。

**验证**
- 抓 CLI 命令行：含且仅含一个 `--system-prompt` 与一个 `--append-system-prompt`。
- 抓 API request body：CachedPrefix cache_control.type=ephemeral ttl=300；UncachedTail 无。
- 性能：连续 2 轮 cache hit rate ≥ 90%。

---

### T-20 · 文档同步

**修改**
1. 更新 `p21/README.md`：
   - 顶部加 Phase 0 段（T-00）
   - P1a 状态 ⏳ → ✅
   - P1b 状态 🔲 → ✅（标 95%、剩 T-12/T-13）
   - P2 状态 🔲 → ⚠️（标 ~40%、剩 T-14~T-17）
   - P3 状态 🔲 → ⚠️（标 ~70%、剩 T-18）
   - P4 状态 ✅ → ⚠️（降级，剩 T-19）
2. 确认 docs 中 skill candidate migration 统一为 `0064_skill_candidates.sql`，列出新预占的 0065/0066/0067/0068。
3. README 末尾追加：`./fix01.md`、`./fix01.v2.md`。

**验证**
- grep 旧 skill candidate migration token 在 docs/ 下零命中。
- README 状态表与 fix01.v2.md §一一致。

---

## 四、依赖与并行（修订）

```
T-00 (Phase 0 契约锁定) ──→ 所有任务（参考资源，不阻塞代码改动）

T-06 (RepoFingerprint 128-bit) ─┬─→ T-01 (cwd 校验)
                                 ├─→ T-02 (List 过滤)
                                 └─→ T-09b (SkillsChanged 用 fp 替代 cwd)

T-04 (剔除 RedactedSample)        独立
T-03 (Reviewer authn)             独立
T-05 + T-05.1 (脱敏 + 日志)       独立
T-07 (Reader/Writer 拆分) ──→ T-07b (archtest)、T-10、T-11
T-08 (Store 错误细分) ──→ T-09 (superseded)、T-10 (resume)
T-13 (approval_policy schema) ──→ T-12 (TurnSubmitter 接线集成验证)

T-14 / T-15 / T-16 / T-17  并行
T-18 独立
T-19 独立
T-20 在以上任务合入后执行
```

**修订后的节奏**

**第 1 波（强并行，含 T-06 序列化锚点 + T-13 提前）**
T-00、T-04、T-05、T-05.1、T-06（**序列化锚点：合入后才解锁 T-01/T-02/T-09b**）、T-08、T-11、T-13、T-14、T-15、T-16、T-17、T-18、T-19。

**第 2 波（依赖第 1 波）**
T-01、T-02、T-03、T-07、T-07b、T-09、T-09b、T-10、T-12。

**第 3 波（收尾）**
T-20。

> **评审带宽限制**：第 1 波 14 项 PR 需 2 名 senior 评审约 3 天工作量；建议 T-06 提前 1 天单独评审合入再放出后续 PR，避免 merge conflict 雪球。

---

## 五、统一验收标准（修订）

### 安全红线（T-01 ~ T-06、T-05.1、T-17）

- 单测覆盖率：每个 task 新增/修改函数 ≥ 80% 行覆盖。
- **红队脚本** `docs/security/p21-redteam.sh`（CI 可选步骤），5 条断言全部 PASS：
  1. **跨仓枚举**：用仓 A fingerprint 调 list → 期望仅返回 A 的候选，结果落 `red_team_results.txt`。
  2. **伪造 reviewer**：caller 自报 `approved_by=admin`，无 ctx identity → 期望 `ErrReviewerAuthnRequired`（flag=1）或 `approved_source=DEGRADED_SELF_REPORT`（flag=0 且 audit 留痕）。
  3. **绕过脱敏**：构造含 17 类 secret 的 trajectory → 期望候选 SkillMD grep 全部 17 模式 0 命中；脱敏失败 → 候选状态 `dropped`。
  4. **redirect 到内网**：webhook server 返回 302 到 `127.0.0.1` / `10.0.0.0/8` → 期望 `ErrDisallowedAddress` 并日志记录。
  5. **env proxy bypass 尝试**：`HTTP_PROXY=http://attacker.example/` → 期望 `Transport.Proxy=nil` 生效，实际连接不走 proxy（用 mock 验证目标 IP）。
- `go vet` + 自定义 lint 0 报警。

### 功能闭合（T-07 ~ T-19）

- 现有 e2e suite 全绿。
- 新增 e2e：cron 触发 → turn 完成 → insight 落表 → webhook 三平台各收到一条；payload 走快照测试 + 手工抽样验证一次。

### 文档同步（T-20）

- `p21/README.md` 状态表与 fix01.v2.md §一一致。
- grep 旧 skill candidate migration token 在 docs/ 零命中。
- Phase 0 段落显式提及 `ResolveCodexIdentity` 已冻结。

---

## 六、回滚策略（修订）

**Schema 类**
- T-06 / T-09 / T-10 / T-13 全部走可加列 / 可空列 → 失败可单独 revert。
- T-06 在切换前同时计算两套指纹双写一段时间（≤7 天），确认一致后切读；如出问题 revert 切换分支。

**Feature flag 类（新增）**
- **T-03**：`SKILL_REVIEWER_AUTHN_REQUIRED=0` 临时降级到 self-report，audit 留痕 `DEGRADED_SELF_REPORT`。
- **T-12**：`ErrDuplicateDedupeKey` 必须直接转 `submitted` 不重试；若 cron 提交雪崩，临时设 `CRON_TURN_SUBMIT_DISABLED=1` 全局停止 driveJob 直至修复。
- **T-14/T-15/T-16**：`NOTIFY_PLATFORM_DISABLED_PLATFORMS=slack,dingtalk,feishu` 逐平台禁用；被禁平台的 NotifyRequest 返回成功（0 字节发送）+ WARN 日志。
- **T-19**：`PROMPT_CACHE_LEGACY=1` 一键回旧路径（多 `--system-prompt` 叠加，无 ephemeral cache_control）。

**编译期防护**
- **T-07 / T-07b**：自定义 vet 规则 `build/vet_observation_split.log` + `internal/archtest/trajectory_readonly_test.go` 在 CI 阻断回归。

**运维 kill-switch**
- **P2 总闸**：`NOTIFY_RETRY_MAX_ATTEMPTS=1` 快失败；`NOTIFY_QUEUE_DRAIN_TIMEOUT=5s` 防累积。
- **审计表**：`SKILL_AUDIT_LOG_RETENTION=90d` 自动清理，避免长期膨胀。

---

## 七、未纳入本期（follow-up，建议进 P22）

- 后台自动 retry promotion（T-10 仅暴露 RPC，自动化留下期）。
- 子 agent prompt cache 共享（P4 80% parity 上限，需独立 runtime）。
- PROACTIVE / coordinator / side_question 模式（需自写 Messages API provider）。
- 审计日志结构化查询 UI。
- webhook 失败死信队列。
