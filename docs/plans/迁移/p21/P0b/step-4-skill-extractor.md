# P0b Step 4：skill_extractor + 二次 redaction

## 目标

把 evaluator 判定 eligible 的 `Trajectory` 在 `runner.actors` worker 内交给 LLM 提炼成 SKILL.md，对输出做强制二次脱敏，再写入 `skill_candidates(status='pending_review')`。失败只记日志 / metric，不影响主 turn。

## 前置依赖

- Step 1：`internal/store/skillcandidate/Store`（含 `skill_md` 字段）。
- Step 2：`Trajectory` 类型。
- Step 3：`Evaluator.Evaluate` 已判定 eligible。
- `internal/contract/dream.go:1` 提供 `DreamExecutor.ExecuteDream(ctx, prompt) (string, error)` 与 `ErrDreamExecutorNotConfigured` 哨兵。
- `runner.actors`（active Fx tag: `group:"runners"`）：本步在 worker 中跑，不在 bus callback 内。

## 文件清单

### 新建

| 路径 | 说明 |
|---|---|
| `internal/module/turn/skill_extractor.go` | `Extractor` 接口 + 默认实现 + worker loop。 |
| `internal/module/turn/skill_extractor_test.go` | golden 脱敏测试 + 失败路径测试。 |
| `internal/module/turn/redaction.go` | `Redactor` 接口 + 默认 regex-based 实现。 |
| `internal/module/turn/redaction_test.go` | pattern 覆盖率测试。 |

### 修改

| 路径 | 说明 |
|---|---|
| `internal/module/turn/module.go` | `fx.Provide(NewExtractor, NewRedactor)`；worker 加入 `group:"runners"`。 |

## 契约

```go
package turn

import "context"

type ExtractedSkill struct {
    Slug            string
    SKILLMd         string // 已脱敏
    ContentHash     string // sha256 of SKILLMd
    Sample          string // redacted_sample（前 N 字节，N 建议 1024）
    Scope           string // 默认 "project"
    RepoFingerprint string // 由 Trajectory.Cwd 派生（建议 sha256(abs(cwd))[:12]）
}

type Extractor interface {
    Extract(ctx context.Context, t Trajectory) (*ExtractedSkill, error)
}

// Redactor 是二次脱敏器，必须在 LLM 提炼输出后跑一次。
// hits 列出命中的 pattern 名，便于 metric 上报；err 表示 redactor 自身故障（非命中失败）。
type Redactor interface {
    Redact(input string) (output string, hits []string, err error)
}
```

## 实施流程（按序）

1. evaluator 判定 eligible（Step 3 已完成）。
2. 由 runner worker（**不是** bus callback）从 collector `Drain()` 拉 `Trajectory`。引用 P0 §"关键实现约束"：长跑 goroutine、批量 flush、重试策略都交给 `Runner.Run(ctx)`。
3. 调 `DreamExecutor.ExecuteDream(ctx, prompt)` 跑 LLM 提炼。
    - 命中 `errors.Is(err, contract.ErrDreamExecutorNotConfigured)` → **skip + log + metric**，不重试，不影响主 turn，不写 candidate。
4. 对 LLM 输出跑 `Redactor.Redact(...)`：
    - `err != nil` → metric 自增，丢弃 candidate。
    - `len(hits) > 0` 且 `output` 仍可能含残留 secret pattern（再 scan 一次） → 丢弃 candidate，**不允许** fallback 到未脱敏入库。
5. 计算 `content_hash = sha256(redactedSKILLMd)`；截 `redactedSample = redactedSKILLMd[:min(len, 1024)]`。
6. 派生 `repo_fingerprint = derive(t.Cwd)`（abs path 后取 sha256 前 12 hex；空 cwd → 空 fingerprint，由 Step 5 review gate 拒绝 project scope 写入）。
7. 调 `Store.Insert(InsertParams{ Scope: "project", Slug, ContentHash, RepoFingerprint, SkillMD: redactedSKILLMd, RedactedSample, CreatedAt: now })`。
    - 命中唯一约束（已存在相同四元组的 candidate）→ 不当 error 处理，metric `skill_candidate_dedup_hit` 自增。

## Redactor 必须覆盖的 pattern

| 名称 | 正则 |
|---|---|
| Bearer token | `(?i)Bearer\s+[A-Za-z0-9-._~+/]+=*` |
| JWT | `eyJ[A-Za-z0-9_=-]+\.[A-Za-z0-9_=-]+\.?[A-Za-z0-9_.+/=-]*` |
| 凭据 env 后跟值 | `(?i)(OPENAI_API_KEY\|ANTHROPIC_API_KEY\|AWS_(SECRET_)?ACCESS_KEY(_ID)?\|GITHUB_TOKEN\|HF_TOKEN\|HUGGINGFACE_TOKEN)\s*[=:]\s*\S+` |
| HTTP cookie 头部 | `(?i)\b(Cookie\|Set-Cookie)\s*:\s*[^\r\n]+` |
| generic base64 / hex | 连续 32+ 位 `[A-Za-z0-9+/=]` 或 `[0-9a-fA-F]` |

每个命中 → 替换为 `[REDACTED:<pattern_name>]`，并把 `<pattern_name>` 写入 `hits`。

## 实施约束

- 在 `runner.actors`（active Fx tag: `group:"runners"`）worker 中跑，bus callback 内**不做**提炼（P0 §"关键实现约束"）。
- 不允许 fallback 到"未脱敏入库"（P0 §"关键实现约束"：redaction 失败时直接丢弃 candidate）。
- 失败只记日志 / metric，**不影响主 turn**：worker 内的任何 panic / error 不能传播回 turn lifecycle 路径。
- 不直写技能目录（P0 §"自动提炼默认不直写技能目录"）：extractor 只产 `skill_candidates` 行，落盘必须由 Step 5 review gate 显式 approve 后调 `CreateSkill`。
- DreamExecutor `Execute` 调用必须带超时 ctx（建议 90s），超时不重试。
- `repo_fingerprint` 派生函数与 Step 5 review gate 共享同一实现（建议放到 `internal/module/turn/redaction.go` 同包内的 `RepoFingerprint(cwd) string`，或 `internal/module/skill/` 公开函数；Step 5 引用同一函数）。
- prompt 模板里**禁止**直接拼接原始 tool args / results；先 `Redactor.Redact` 一次再喂给 LLM（防止 prompt 内带 secret 被 LLM 反吐）。

## 验收标准

### 必测项（建议测试名）

- `TestExtractor_GoldenRedactsSecrets`：构造 `Trajectory.ToolCalls` 含 `Bearer abc...`、JWT `eyJ...`、`OPENAI_API_KEY=sk-xxx`；fake DreamExecutor 把这些字面回写到输出；断言 `candidate.SkillMD` 与 `candidate.RedactedSample` 不含原始密钥任何子串，`hits` 至少包含 3 类 pattern。
- `TestExtractor_DreamExecutorMissingSkips`：`DreamExecutor` 返回 `ErrDreamExecutorNotConfigured` → 不 panic、不写 candidate、log 与 metric 各产生一次。
- `TestExtractor_RedactionFailureDropsCandidate`：Redactor 模拟 `err != nil` → candidate 未写入、metric `skill_candidate_redaction_failure` 自增。
- `TestExtractor_HappyPathInsertsCandidate`：完整成功路径 → `Store.Insert` 被调一次、入参 `Scope="project"` / `Status` 由 store 默认 `pending_review`。
- `TestExtractor_DedupHitDoesNotPropagateError`：第二次提炼相同 `(scope, slug, content_hash, repo_fingerprint)` → 不返回 error 给 worker、metric `skill_candidate_dedup_hit` 自增。
- `TestExtractor_PromptDoesNotLeakRawSecrets`：断言喂给 fake DreamExecutor 的 prompt 已被 Redactor 处理（即 prompt 内不含原始 bearer / JWT）。
- `TestRedactor_AllPatterns`：每个 pattern 一条 case，验证替换文本与 `hits` 命名。

### 命令

```bash
go test ./internal/module/turn/ -run "Extractor|Redactor"
```

### 集成验证

- 启动整套 fx app；用 fake DreamExecutor 注入到 `group:"runners"` worker；触发一次 eligible turn；DB 中能看到 `skill_candidates` 行 status=`pending_review` 且 `skill_md` 已脱敏。

## 已知风险 / 反模式

- **在 bus callback 内跑 LLM**：违反 P0 §"关键实现约束"，会拖慢 turn。
- **redaction 失败时回退到原文落库**：违反 P0 §"关键实现约束"，必须丢弃。
- **prompt 内拼接未脱敏 tool result**：LLM 可能把 secret 复述到 SKILL.md，必须先脱敏再喂。
- **DreamExecutor 缺失时阻塞重试**：`ErrDreamExecutorNotConfigured` 必须 fast skip。
- **repo_fingerprint 用相对路径**：相对路径在不同 cwd 下解析不同，必须先 `filepath.Abs` 再 hash。
- **把脱敏当 best-effort**：必须 fail-closed（命中无法消除则丢弃）。