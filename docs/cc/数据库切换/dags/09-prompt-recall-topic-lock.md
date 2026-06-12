# Task 09: Prompt Recall Topic Lock

## Agent Prompt

你负责替换 `pg_advisory_xact_lock` 支撑的 prompt recall topic 并发控制。此路径业务可达，不能删除锁。目标是同一 cwd/topic 的 recall section 并发写入只成功一次，不同 cwd 同 topic 可并行，global/project scope 覆盖规则与当前重复检测一致。

## Scope

依赖：Task 03。

可并行：可与 Task 04、05、06、07、08 并行，但会触碰 `prompt_template_sections`，合并时需与 Task 05 协调。

## 修改点

- Modify SQL:
  - `sql/queries/prompt_template_sections.sql`
- Modify store:
  - `internal/store/prompt/store.go`
  - `internal/store/prompt/contract.go`
- Modify service:
  - `internal/module/prompt/template_sections.go`
  - `internal/module/prompt/intent/commit.go`
- Modify tests:
  - `internal/store/prompt/store_recall_test.go`
  - `internal/module/prompt/service_provider_intent_test.go`
  - `internal/module/prompt/intent_commit_test.go`
- Schema dependency:
  - Use `prompt_recall_topics(cwd TEXT NOT NULL, topic TEXT NOT NULL, template_id INTEGER NOT NULL, section_key TEXT NOT NULL, PRIMARY KEY(cwd, topic))` or an equivalent SQLite-enforced uniqueness strategy.

## 语义要求

- `LockRecallTopicInCWD(ctx, cwd, topic)` must still validate non-empty trimmed values.
- Lock + duplicate scan + section upsert must be in the same transaction.
- If using `prompt_recall_topics`, maintain it in the same transaction as `UpsertSection`.
- All `BEGIN IMMEDIATE`, CAS writes, and cross-process topic lock contention must use the shared bounded retry helper from Task 03, or have a test proving retry is unnecessary.
- Same cwd/topic concurrent write:
  - exactly one succeeds, or one updates the same target section and the other receives duplicate error matching current business behavior.
  - duplicate visible recall topic count = 0.
- Different cwd same topic can both succeed.
- Global recall duplicate behavior remains consistent with `promptRecallDuplicateExists` and `promptIntentRecallDuplicateExists`.

## 不允许改

- 不要 replace with process-local mutex.
- 不要 remove duplicate scan from `template_sections.go`.
- 不要 allow invalid recall topic names to reach SQL.

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/store/prompt ./internal/module/prompt -count=1
make sqlc-verify
```

新增并发测试：

- two goroutines / two DB handles write same cwd/topic into different sections -> one business duplicate error, final visible count = 1.
- two different cwd values same topic -> both succeed.
- global existing recall blocks project duplicate only where current code blocks it; allowed override cases remain allowed.
