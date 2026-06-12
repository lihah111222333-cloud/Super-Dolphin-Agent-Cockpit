# Task 05: Prompt/Command/Shared/Feedback/Insight Store

## Agent Prompt

你负责主应用资产类 store 的 SQLite 等价迁移：prompt template 基础 CRUD、command card、shared file DB 索引、feedback、session insight。不要处理 prompt recall topic 并发锁，那个由 Task 09 单独负责。

## Scope

依赖：Task 03。

可并行：可与 Task 04、06、07、08、09 并行。

## 修改点

- Modify SQL:
  - `sql/queries/prompt_template.sql`
  - `sql/queries/prompt_intent_drafts.sql`
  - `sql/queries/command_card.sql`
  - `sql/queries/shared_file.sql`
  - `sql/queries/agent_feedback.sql`
  - `sql/queries/session_insight.sql`
- Modify stores:
  - `internal/store/prompt/store.go`
  - `internal/store/prompt/intent_drafts.go`
  - `internal/store/commandcard/store.go`
  - `internal/store/sharedfile/store.go`
  - `internal/store/feedback/store.go`
  - `internal/store/insight/store.go`
- Modify tests:
  - `internal/store/prompt/*_test.go`
  - `internal/store/commandcard/*_test.go`
  - `internal/store/sharedfile/*_test.go`
  - `internal/store/feedback/*_test.go`
  - `internal/store/insight/*_test.go`

## 语义要求

- JSON arrays such as `tags`, `variables`, `skills_selected`, `payload`, `generated_card`, `issues` must stay valid JSON text.
- `jsonb_array_elements_text(tags)` must be replaced by Go-side decoding or SQLite `json_each`; choose one pattern and add tests.
- CWD scope filtering must keep current behavior used by `internal/module/prompt` and dashboard.
- Shared file disk source behavior must not change:
  - DB list does not scan disk.
  - DB content can be empty for large files.
  - path validation remains in `internal/platform/sharedfilepath`.
- `session_insights` large-list queries must retain limit and not read all payloads when only counts/list metadata are needed.

## 不允许改

- 不要 implement `LockRecallTopicInCWD` or recall duplicate serialization here.
- 不要 change shared file sandbox paths or gitignore policy.

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/store/prompt ./internal/store/commandcard ./internal/store/sharedfile ./internal/store/feedback ./internal/store/insight -count=1
./scripts/test_with_guard.sh ./internal/module/prompt -count=1
make sqlc-verify
```

静态扫描：

```bash
rg -n "jsonb|::jsonb|jsonb_array|ILIKE|NOW\\(|pgtype|pgx" sql/queries/prompt_template.sql sql/queries/prompt_intent_drafts.sql sql/queries/command_card.sql sql/queries/shared_file.sql sql/queries/agent_feedback.sql sql/queries/session_insight.sql internal/store/prompt internal/store/commandcard internal/store/sharedfile internal/store/feedback internal/store/insight
```

预期：无 PG-only 语法；prompt、shared file disk integration、feedback/insight tests 通过。

