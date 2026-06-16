# P21 Fix-01 可执行修复清单

> 基于 2026-04-27 代码审核结果生成。
> 目标：把 P21 文档与代码状态对齐，并按优先级闭合现存缺陷。
> 所有任务均可独立领取、独立验证。

---

## 一、文档与代码偏差总览

| 主线 | 文档标注 | 代码实际 | 偏差方向 | 处理 |
|---|---|---|---|---|
| P0a | ✅ 完成 | ✅ 完成 | 一致 | — |
| P0b | ⏳ 14 项缺陷 | 14 项基本一致（F8 已规避） | 一致 | 闭合 F1/F2/F3/F4/F6/F7/F9/F10/F11/F13/F14 |
| P1a | ⏳ 待启动 | ✅ 已实现 | **文档落后** | 文档升级为 ✅ |
| P1b | 🔲 未启动 | ✅ 95%（缺 turn 接线 + approval 白名单） | **文档严重落后** | 文档升级 + 收尾 2 项 |
| P2 | 🔲 未启动 | ⚠️ ~40%（配置 + SSRF 已落，平台适配器全缺） | **文档落后** | 文档升级 + 补 3 个适配器 |
| P3 | 🔲 阻塞 | ⚠️ ~70%（DDL/聚合/flusher 已落，缺 HTTP 路由） | **文档落后** | 文档升级 + 挂路由 |
| P4 | ✅ 80% parity | ⚠️ ~75%（缺 `--append-system-prompt` + ephemeral TTL） | **文档夸大** | 文档降级或补实现 |
| 迁移编号 | `0064_skill_candidates.sql` | 实际为 `0064_skill_candidates.sql` | 编号漂移 | 文档修正 |

---

## 二、任务总表（按优先级）

| 优先级 | 任务编号 | 主线 | 标题 | 工作量预估 |
|---|---|---|---|---|
| P0（数据安全红线） | T-01 | P0b/F2 | 审批校验 caller cwd 指纹 | S |
| P0 | T-02 | P0b/F3 | List/Lookup 增加 repo_fingerprint 过滤 | S |
| P0 | T-03 | P0b/F4 | Reviewer 身份认证落地 | M |
| P0 | T-04 | P0b/F14 | List 接口剔除 RedactedSample 字段 | XS |
| P0 | T-05 | P0b/F6 | 脱敏模式补齐至 ≥15 类 | M |
| P0 | T-06 | P0b/F1 | 统一 RepoFingerprint 算法 | S |
| P1（架构正确性） | T-07 | P0b/F7 | 拆分 observation Reader/Writer | M |
| P1 | T-08 | P0b/F10 | Store 区分 not-found vs 状态冲突 | S |
| P1 | T-09 | P0b/F11 | 实现 superseded 状态机 | M |
| P1 | T-10 | P0b/F9 | approved 失败后的 resume 路径 | M |
| P1 | T-11 | P0b/F13 | extractor / approval 字节限额 | S |
| P1 | T-12 | P1b | NoopTurnSubmitter 替换为 turn.Service | M |
| P1 | T-13 | P1b | approval_policy 白名单字段 + 校验 | M |
| P2（功能闭合） | T-14 | P2 | Slack webhook 适配器 | M |
| P2 | T-15 | P2 | 钉钉 webhook 适配器（含 HMAC 签名） | M |
| P2 | T-16 | P2 | 飞书 webhook 适配器（含签名） | M |
| P2 | T-17 | P2 | HTTP 重定向防 DNS rebinding 重校验 | S |
| P2 | T-18 | P3 | `dashboard/insights/list` HTTP 路由挂载 | S |
| P2 | T-19 | P4 | `--append-system-prompt` 与 ephemeral 5min TTL | M |
| P3（文档同步） | T-20 | 全局 | 更新 P21 README 状态表 + 修正迁移编号 | S |

工作量记号：XS<0.5d、S=0.5–1d、M=1–3d。

---

## 三、详细任务卡

### T-01 · P0b/F2 审批校验 caller cwd 指纹

**问题**
`internal/module/skill/candidate_review.go:117-142` 的 `validateCandidateApproval` 仅判断 `repo_fingerprint` 非空，未与 caller cwd 实际计算的指纹比对，导致跨仓审批可绕过。

**修改**
1. `candidate_review.go:117-142`：在 `validateCandidateApproval` 中接收 `callerCwd`，对其计算 `RepoFingerprint(callerCwd)`，与 candidate.repo_fingerprint 严格相等再放行；不一致返回 `ErrRepoFingerprintMismatch`（新增 sentinel）。
2. 上游 `ApproveCandidate(ctx, ...)` RPC 入参增加 `caller_cwd`（必填）；空值直接拒绝。
3. RPC schema 同步更新。

**验证**
- 单测：构造 candidate.repo_fingerprint=A，approve 时传 cwd 指纹=B → 期望 `ErrRepoFingerprintMismatch`。
- 单测：传相同指纹 → 通过。
- 集成：mock 两个不同 git 仓，互相调审批 → 全部拒绝。

---

### T-02 · P0b/F3 List/Lookup 增加 repo_fingerprint 过滤

**问题**
`sql/queries/skill_candidate.sql:16-20` 的 `ListPendingSkillCandidates` 没有 `repo_fingerprint` 过滤，任何调用方都能枚举其他仓的候选。

**修改**
1. SQL：`WHERE status = 'pending_review' AND repo_fingerprint = $1 ORDER BY created_at DESC LIMIT $2`。
2. 重新生成 sqlc，更新 store/服务层签名传入 caller cwd 指纹。
3. RPC handler 在请求上下文中解析 caller cwd → 计算指纹 → 透传。

**验证**
- 单测覆盖 SQL 过滤生效。
- 集成：仓 A 调用 list 仅看到 A 的候选；仓 B 调用看不到 A 的候选。

---

### T-03 · P0b/F4 Reviewer 身份认证

**问题**
当前 `ApprovedBy` 是 caller 自报字符串，无任何校验。

**修改**
1. 新增 `internal/module/skill/reviewer_authn.go`：定义 `ReviewerIdentity{Email, Source}` 与 `ReviewerAuthenticator` 接口。
2. 默认实现：从请求上下文（mcp 鉴权层 / RPC header）抽取经过验证的身份；无身份直接拒绝。
3. `ApproveCandidate` / `RejectCandidate` 调用前必须经过 authenticator；落库时使用解析后的 identity，覆盖 caller 自报值。
4. 审计字段：`approved_by`、`approved_source`、`approved_at` 同步写入。

**验证**
- 单测：未注入 identity 的 ctx → 拒绝。
- 单测：caller 自报 `approved_by=root` 与 ctx identity=alice 不一致 → 以 ctx 为准。

---

### T-04 · P0b/F14 List 接口剔除 RedactedSample

**问题**
`internal/module/skill/contract.go:90` 的 RPC `Candidate` 类型暴露了 `RedactedSample` 字段，违反 P21 设计（脱敏样本仅供内部审计）。

**修改**
1. 新增 `CandidateSummary`（不含 RedactedSample）作为 List/Lookup 响应类型。
2. `CandidateDetail`（含 RedactedSample）仅供 GetByID 单条接口使用，且响应前再做一次域脱敏。
3. List handler 切换为 Summary。

**验证**
- 单测：序列化 Summary JSON，断言无 `redacted_sample` 键。
- 黑盒：调用 list 接口，抓响应体确认。

---

### T-05 · P0b/F6 脱敏模式补齐至 ≥15 类

**问题**
`internal/module/turn/redaction.go:42-87` 仅 6 类（bearer / JWT / credential_env / http_cookie / long_hex / long_base64）。

**修改**
新增至少以下模式：
- `Authorization: Basic <b64>`
- `x-api-key: <token>`、`X-Auth-Token` <!-- guard:allow-secret placeholder examples, not real secrets. -->
- `password=` / `passwd=` / `pwd=` form & query
- `-----BEGIN (RSA|EC|OPENSSH|PGP) PRIVATE KEY-----` 多行块
- AWS Access Key ID `AKIA[0-9A-Z]{16}` & Secret 40 字符
- GitHub `ghp_` / `gho_` / `ghs_` / `ghu_` token
- Slack `xox[baprs]-` token
- Google API Key `AIza[0-9A-Za-z_-]{35}`
- 邮箱（可选，按 GDPR 策略开关）
- IPv4 / IPv6（可选）

每类一个表驱动测试用例，输入含 secret，输出必须为 `[REDACTED:<kind>]`。脱敏失败必须把 candidate 标记为 `dropped` 而非 `pending_review`（本任务也覆盖该 fail-closed 行为）。

**验证**
- 表驱动单测覆盖每类正反例。
- 集成：构造含多种 secret 的 trajectory → 抽取候选体内零命中（grep 全部模式）。

---

### T-06 · P0b/F1 统一 RepoFingerprint 算法

**问题**
turn 侧（`internal/module/turn/redaction.go:89-106`，48-bit）与 skill 侧实现不一致，跨模块比对失效。

**修改**
1. 新建 `internal/platform/repofingerprint/fingerprint.go`，单一实现：`func Compute(cwd string) (string, error)`，统一为 64-bit 十六进制。
2. turn / skill / cron / insight 全部改为调用该包。
3. 删除旧实现。
4. 数据迁移：现存 `skill_candidates.repo_fingerprint` 全量字段长度从 48-bit 升 64-bit 不冲突；存量数据如需重算，提供一次性脚本（可选，多数仓尚未上线）。

**验证**
- 单测覆盖：相同 cwd 在多模块下结果一致。
- 集成：T-01 的 cwd 比对在升级后仍通过。

---

### T-07 · P0b/F7 拆分 observation Reader/Writer

**问题**
`internal/module/turn/observation/contract.go:107-190` 单一 `Contract` 接口同时具备读写能力，trajectory_collector / insight flusher 等只读消费方有越权写风险。

**修改**
1. 拆出 `ObservationReader`（仅 GetByLocalTurnID / GetByCallID / Stream / Query 等只读方法）与 `ObservationWriter`（AttributeCall / Dedupe / WriteSnapshot 等）。
2. fx 装配：发布 Reader 给 trajectory_collector / insight.Flusher / extractor；Writer 仅注入 turn 内部聚合层。
3. 旧 `Contract` 标记 deprecated → 后续 PR 删除。

**验证**
- 编译期：trajectory_collector / insight 模块不再能 import Writer 方法。
- `go vet` + 自定义 lint：禁止跨模块 import Writer。

---

### T-08 · P0b/F10 Store 区分 not-found vs 状态冲突

**问题**
`sql/queries/skill_candidate.sql:22-42` 的状态守卫 UPDATE 在行不存在与状态不匹配两种情况都返回 `pgx.ErrNoRows`，调用方无法区分。

**修改**
SQL 改写为 CTE：先 SELECT WHERE id=$1 探测存在性，再带状态守卫 UPDATE，返回字段含 `existed bool, updated bool`。store 层根据组合返回 `ErrCandidateNotFound` 或 `ErrCandidateStateMismatch`。

**验证**
- 单测：删除候选后 update → ErrCandidateNotFound。
- 单测：状态不符 update → ErrCandidateStateMismatch。

---

### T-09 · P0b/F11 实现 superseded 状态机

**问题**
DDL 已声明 `superseded`，但无 `MarkSuperseded` 查询，无 dedup 触发逻辑。

**修改**
1. 新增 SQL `MarkCandidateSuperseded`：把同 `(scope, slug, repo_fingerprint)` 维度下旧 pending 候选改为 superseded。
2. extractor 写入新 candidate 时事务内调用，确保同维度只剩一条 pending。
3. List 默认排除 superseded（已隐含，确认 WHERE status='pending_review'）。

**验证**
- 单测：连续两次抽取 → 旧候选状态 superseded，新候选 pending。
- 单测：list 不返回 superseded。

---

### T-10 · P0b/F9 approved 失败后的 resume 路径

**问题**
`candidate_review.go` 的 approve 流程在 CreateSkill 失败后，candidate 已是 approved 但落盘失败，没有重试入口。

**修改**
1. 新增字段 `promotion_attempt_count INTEGER DEFAULT 0` 与 `last_promotion_error TEXT`（migration 增量）。
2. approve 流程改为：先标 approved → 调 CreateSkill；失败时记录 last_promotion_error 并增加 attempt count，但 candidate 维持 approved 状态。
3. 新增 RPC `RetryPromotion(candidate_id)`：仅 approved 状态可调，重新执行 CreateSkill；成功后转 `written` 终态。
4. 后台 leaseActor 选项（可后续）：定时扫描 approved 且 attempt < N 的候选自动 retry。本期先暴露 RPC，自动化留下一期。

**验证**
- 集成：mock CreateSkill 第一次失败 → candidate 保持 approved + last_promotion_error 写入；调用 RetryPromotion → 成功转 written。

---

### T-11 · P0b/F13 字节限额

**问题**
`skill_extractor.go` / approval 路径无 trajectory / skill_md 大小限制，存在内存膨胀与 DoS 风险。

**修改**
1. 新增配置常量：`MaxTrajectoryBytes = 256 * 1024`、`MaxSkillMDBytes = 64 * 1024`、`MaxCandidatesPerRepo = 200`（pending 数量软上限）。
2. extractor 入口校验 trajectory 总字节；超限直接丢弃，记录 metric。
3. SQL 写入前校验 skill_md 字节；超限丢弃。
4. CreateCandidate 前 SELECT count(*) WHERE status='pending_review' AND repo_fingerprint=$1，达到上限则丢最旧 superseded 或拒绝新写入（推荐拒绝并 metric）。

**验证**
- 单测：超过上限的 trajectory → 丢弃 + 计数器 +1。
- 单测：候选数达上限 → 写入返回 `ErrCandidateQuotaExceeded`。

---

### T-12 · P1b NoopTurnSubmitter 替换为真实 turn.Service

**问题**
`internal/module/cron/scheduler.go` 当前 `TurnSubmitter` 默认 noop，cron 触发后实际不提交 turn。

**修改**
1. 在 cron 模块的 fx wiring 中把 `turn.Service` 注入为 `TurnSubmitter` 的实现适配器（保留接口，不让 cron 直接依赖 turn 包）。
2. 适配器实现 `StartTurn(ctx, params)`：把 cron 的 dedupe_key、prompt、provider、skills 透传到 turn.Service.StartTurn。
3. 错误映射：turn 层的 idempotency 命中（同 dedupe_key 已存在）必须返回明确 sentinel，scheduler 据此跳过 markRunSubmitting → 直接进 observe 阶段。

**验证**
- 集成：手动触发 cron job → cron_job_runs 出现 running → finished；同时 turn 表出现对应 turn_id。
- 集成：crash 模拟（kill 在 submitting 阶段）→ 重启后 RecoverDanglingRuns 通过 dedupe lookup 找到已存在 turn → 不重复提交。

---

### T-13 · P1b approval_policy 白名单

**问题**
P21 spec 要求 cron 非交互场景 approval 不是 blanket auto-approve，而是白名单组合（provider + sandbox + tool）。当前 cron schema 无此字段。

**修改**
1. 增量 migration：`cron_jobs` 加 `approval_policy_json JSONB NOT NULL DEFAULT '{}'`。
2. 内容契约：`{"provider":"codex","sandbox":"strict","tools":["file.read","file.write"]}`，校验 provider/sandbox/tools 三者 must-match cron job 配置。
3. service 层：创建/更新 cron job 时校验 policy 与 provider/skills 兼容；执行 driveJob 时把 policy 透传到 turn 层（turn 层据此放行或拒绝具体工具调用）。
4. 默认值不放空 policy 直接执行：必填，否则拒绝创建。

**验证**
- 单测：缺 approval_policy_json → 创建拒绝。
- 集成：policy.tools=["file.read"] 但 turn 内尝试 file.write → 被拒并记录 audit。

---

### T-14 · P2 Slack webhook 适配器

**修改**
- 新建 `internal/module/notify/platform/slack/adapter.go`：实现 `Adapter` 接口，渲染 turn-completed / cron-finished / cron-failed 三种事件为 Slack `blocks` payload。
- 复用 `webhook.WebhookClient`（SSRF 防御已在）。
- 注册到 `platform.Registry`，alias 形如 `{"platform":"slack","url":"https://hooks.slack.com/..."}`。
- 不需要签名（Slack incoming webhook 用 URL 即凭据）。

**验证**
- 单测：渲染快照测试覆盖 3 种事件 payload。
- 本地：起一个 echo http server → 发一条 → 断言收到正确 JSON。

---

### T-15 · P2 钉钉 webhook 适配器

**修改**
- 新建 `internal/module/notify/platform/dingtalk/adapter.go`。
- payload 模板：`{"msgtype":"markdown","markdown":{"title":"...","text":"..."}}`。
- 签名：HMAC-SHA256(secret, "<timestamp>\n<secret>")，URL 追加 `&timestamp=&sign=`。
- 必须 HTTPS。

**验证**
- 单测：HMAC 计算用钉钉官方文档样例值校验。
- 集成：mock 钉钉 endpoint → 校验 query string 含 timestamp/sign。

---

### T-16 · P2 飞书 webhook 适配器

**修改**
- 新建 `internal/module/notify/platform/feishu/adapter.go`。
- payload：飞书 interactive card 或 text。
- 签名：HMAC-SHA256(secret, "<timestamp>\n<secret>")，body 追加 `timestamp` / `sign` 字段。
- 必须 HTTPS。

**验证**
- 单测覆盖 payload + 签名计算（飞书官方样例值）。

---

### T-17 · P2 HTTP 重定向 DNS rebinding 重校验

**问题**
`internal/module/notify/platform/webhook.go` 的 SSRF 防御只在初始 dial 时校验目标 IP，HTTP 3xx 跳转后未对新地址重校验。

**修改**
- WebhookClient 的 `http.Client.CheckRedirect` 注册回调：每次跳转把目标 URL 的 host 重新解析 → 对每个 IP 跑 `isBlockedIP` → 命中则返回 `http.ErrUseLastResponse` 并 abort。
- 同时禁用 `http.Client.Transport` 的 `Proxy`（显式 `Proxy: nil`），覆盖 env proxy。

**验证**
- 单测：mock server 跳转到 `127.0.0.1` → client 拒绝。
- 单测：env 设 `HTTP_PROXY=...` → 实际请求不走 proxy。

---

### T-18 · P3 dashboard/insights/list HTTP 路由

**问题**
`internal/module/insight` 的 service 层有 `ListRecent` / `ListByThread` 等方法，但未挂 HTTP/RPC 路由。

**修改**
- 新增 `internal/module/insight/rpc.go`：注册 `dashboard/insights/list` 与 `dashboard/insights/get`。
- 入参：`{thread_id?, limit, offset, before_created_at?}`；出参：分页 snapshot 列表。
- 挂载到现有 dashboard router；权限按现有 dashboard 鉴权策略。

**验证**
- 集成：跑一轮 turn → flusher 落表 → 调 `dashboard/insights/list` 拿到该 snapshot。

---

### T-19 · P4 `--append-system-prompt` + ephemeral 5min TTL

**问题**
- `internal/provider/claudecli/transport_config.go:236-241` 当前对 CachedPrefix + UncachedTail 都使用 `--system-prompt` 多次叠加；P21 设计要求 CachedPrefix 走 `--system-prompt`、UncachedTail 走 `--append-system-prompt`，以契合 Claude Code CLI 的 cache 边界。
- `cache_control` 未设置 `ttl=300s`、`type=ephemeral`、scope=org。

**修改**
1. transport_config 中区分两个 block 的 flag：CachedPrefix → `--system-prompt`；UncachedTail → `--append-system-prompt`。
2. 在 `composeLaunchSystemPromptBlocks` 输出时为 CachedPrefix 段落附 cache_control 元数据：`{type:"ephemeral", ttl_seconds:300}`，并经 CLI 透传到 Anthropic API。
3. 若 CLI 不支持透传：fallback 为 provider 自管 prompt cache（沿用现有 InputScoped 但 TTL=300s）；并在 P4 文档明确说明。

**验证**
- 抓 CLI 命令行：含且仅含一个 `--system-prompt` 与一个 `--append-system-prompt`。
- 抓 API request body：CachedPrefix 段落 cache_control.type=ephemeral、ttl=300；UncachedTail 无 cache_control。
- 性能：连续 2 轮请求 cache hit rate >= 90%（按现有 metric 看板）。

---

### T-20 · 文档同步

**修改**
1. 更新 `p21/README.md`（或对应总表）：
   - P1a 状态 ⏳ → ✅
   - P1b 状态 🔲 → ✅（标注 95%、剩余 T-12/T-13）
   - P2 状态 🔲 → ⚠️（标注 ~40%、剩余 T-14~T-17）
   - P3 状态 🔲 → ⚠️（标注 ~70%、剩余 T-18）
   - P4 状态 ✅ → ⚠️（降级，剩余 T-19）
2. 确认 docs 中 skill candidate migration 统一为 `0064_skill_candidates.sql`。
3. 在 P21 README 末尾追加链接：`./fix01.md`。

**验证**
- grep 旧 skill candidate migration token 在 docs/ 下零命中。
- README 状态表与 fix01.md §一致。

---

## 四、依赖与并行

```
T-06 (RepoFingerprint 统一) ─┬─→ T-01 (审批 cwd 校验)
                              └─→ T-02 (List 过滤)

T-04 (剔除 RedactedSample) 独立
T-03 (Reviewer authn)      独立
T-05 (脱敏模式)            独立
T-07 (Reader/Writer 拆分) ──→ T-10、T-11（写路径变化触发回归）
T-08 (Store 错误区分)     ──→ T-09 (superseded)、T-10 (resume)
T-11 (字节限额)            独立

T-12 (TurnSubmitter)      ──→ 集成验证依赖 T-13
T-13 (approval_policy)     独立 schema 改动

T-14 / T-15 / T-16 / T-17  完全并行（P2 域内）
T-18 独立
T-19 独立
T-20 在以上任务合入后执行
```

**建议节奏**
- 第 1 波（强并行）：T-04、T-05、T-06、T-08、T-11、T-13、T-14、T-15、T-16、T-17、T-18、T-19
- 第 2 波（依赖第 1 波）：T-01、T-02、T-03、T-07、T-09、T-10、T-12
- 第 3 波（收尾）：T-20

---

## 五、统一验收标准

**安全红线（T-01 ~ T-06、T-17）必须满足：**
- 单测覆盖率：每个 task 新增/修改函数 ≥ 80% 行覆盖。
- 一次跨仓审批攻防演练（red-team 脚本）全部失败：枚举其他仓候选、伪造 reviewer、绕过脱敏、redirect 到内网、env proxy 走代理 — 全部被拒。
- `go vet` / 自定义 lint 0 报警。

**功能闭合（T-07 ~ T-19）：**
- 现有 e2e suite 全绿。
- 新增 e2e 用例：cron 触发 → turn 完成 → insight 落表 → webhook 三平台各收到一条。

**文档同步（T-20）：**
- `p21/README.md` 状态表与 fix01.md §一一致。
- grep 旧 skill candidate migration token 在仓内零命中。

---

## 六、回滚策略

- 所有 schema 变更（T-09 superseded、T-10 promotion 字段、T-13 approval_policy）走可加列 / 可空列 → 失败可单独 revert。
- T-06 RepoFingerprint 统一在切换前同时计算两套指纹双写一段时间，确认一致后切读。
- T-19 cache 调整保留旧路径 feature flag `PROMPT_CACHE_LEGACY=1` 可一键回滚。
