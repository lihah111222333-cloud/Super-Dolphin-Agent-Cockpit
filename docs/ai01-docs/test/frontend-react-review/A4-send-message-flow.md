# A4 Send Message Flow

Date: 2026-05-29
Branch: `agent/a4-send-message-flow-20260529`
Worktree: `/home/ai01@f666.com/.config/superpowers/worktrees/Super-Dolphin/a4-send-message-flow-20260529`

## Scope

Files reviewed and implemented:

- `cmd/agent-terminal/frontend/src/features/send-message/**`
- `cmd/agent-terminal/frontend/src/entities/thread/api/**`
- `cmd/agent-terminal/frontend/src/entities/turn/api/**`

The `mcp-go-agent-orchestration` DAG tools were not available in this Codex tool context. Native subagent dispatch is allowed, so task lifecycle was tracked in this report without persistent mcp-orch DAG observability.

## TDD Evidence

Initial red:

```text
npx vitest run src/features/send-message/model/sendMessageController.test.js
FAIL src/features/send-message/model/sendMessageController.test.js
Error: Cannot find module './sendMessageController.js'
```

Expanded red:

```text
7 tests, 4 failed
- attachment-only send returned without RPC
- missing cwd did not reject
- turn/start failure did not log thread.send.failed
- stale selected thread did not reject
```

Green:

```text
npx vitest run src/features/send-message/model/sendMessageController.test.js
Test Files  1 passed (1)
Tests  7 passed (7)
```

## Behavior Matrix

| Scenario | Expected RPC order | Actual RPC order | Result |
| --- | --- | --- | --- |
| No active thread + cwd + text | `thread/start -> turn/start` | `thread/start -> turn/start` | pass |
| No active thread + empty text + attachments | `thread/start -> turn/start` | `thread/start -> turn/start` | pass |
| Existing active thread | `turn/start` | `turn/start` | pass |
| Missing cwd | no RPC | no RPC | pass |
| `thread/start` failed | `thread/start` only | `thread/start` only | pass |
| `turn/start` failed | `turn/start`, then preserve draft + log | `turn/start`, then preserve draft + log | pass |
| Stale selected thread | no RPC | no RPC | pass |

## Contract Checks

- `thread/start` payload includes `cwd` and `deferSpawn: true`.
- `turn/start` payload includes `cwd`, `threadId`, `input`, and `manualSkillSelection: false`.
- Attachment-only drafts produce explicit input items instead of being treated as empty sends.
- Missing `cwd` fails before any RPC and records `missing_cwd` when a logger is supplied.
- Stale selected-thread state fails before any RPC and records `thread.send.stale_selected_thread`.
- `turn/start` failures are rethrown after recording `thread.send.failed`, allowing composer-level draft restoration to preserve user input.

## Concerns

- The runtime orchestration DAG could not be updated because no `mcp-go-agent-orchestration` tools are exposed in this session.
- This slice adds the controller surface and focused tests only; UI wiring remains outside A4 ownership.
