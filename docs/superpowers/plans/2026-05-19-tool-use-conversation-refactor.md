# Tool Use Conversation Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render tool usage inline in the conversation transcript, in a Codex-style lifecycle block, while preserving the existing live ticker and current provider event contracts.

**Architecture:** Use the existing backend `ToolCallBegin` / `ToolCallEnd` lifecycle as the source of truth for live tool items. Ship the refactor in two phases: first render existing `timeline.Item{kind:"tool"}` inline on the frontend, then add a normalized backend conversation event read model so history replay and live streaming share the same display shape.

**Tech Stack:** Go event DTOs and uistate projection, Vue 3 browser ESM components, Vite/Vitest frontend tests, existing thread/messages RPC and event-surface bridge.

---

## Research Summary

The application already has most backend lifecycle data needed for Codex-style tool display:

- Codex inbound tool calls are detected in `internal/provider/codexapp/session.go:213` and executed through `handleInboundToolCall` at `internal/provider/codexapp/session.go:233`.
- Codex session enrichment publishes synthetic `ToolCallBegin` and `ToolCallEnd` in `internal/provider/codexapp/session_enrich.go:173` and `internal/provider/codexapp/session_enrich.go:183`.
- Provider raw events map to typed tool DTOs in `internal/provider/codexapp/event_map.go:285` and `internal/provider/claudecli/event_map.go:140`.
- The shared lifecycle contract is `internal/dto/tool/event.go:9`, keyed by `internal/dto/shared/event.go:97`.
- `uistate` projects tool begin/end into `timeline.Item{Kind:"tool"}` in `internal/module/uistate/timeline/projector.go:205` and `internal/module/uistate/timeline/projector.go:229`.
- Frontend `ChatTimeline` already has a minimal `kind === 'tool'` rendering branch at `cmd/agent-terminal/frontend/vue-app/components/ChatTimeline.js:467`.
- The blocker is `cmd/agent-terminal/frontend/vue-app/components/timeline/useTimelineItems.js:11`: `thinking`, `command`, and `tool` are classified as bottom-only status items, so tools are filtered out of the main transcript and only summarized by the presence ticker.
- Historical Codex rollout parsing currently accepts only message items in `internal/provider/codexapp/history_rollout.go:57`; tool calls/results in rollout JSONL are ignored.
- Offline persisted history parsing has the same message-only limitation in `internal/util/historyjsonl/history.go:173`.

`mcp-go-agent-orchestration` lifecycle tools were not exposed in this Codex session. The research used Codex subagents plus local LSP tools, which is an allowed native subagent path; the run simply lacked persistent mcp-orch DAG observability.

## Scope Decision

Do not widen `contract.Session` or make provider packages emit a new conversation DTO directly in the first implementation pass. The lowest-risk path is:

1. Frontend phase: render existing live `tool` timeline items inline with a dedicated component.
2. Backend read-model phase: add normalized conversation events for history replay and future cleanup.
3. Migration phase: move `thread/messages` or a sibling RPC to return merged dialog and tool-use events without breaking existing live event-surface consumers.

Keep `thinking` and `command` bottom-only for now. This request is about tool usage, and changing all process item categories at once would invalidate unrelated streaming and command tests.

## Target UX

Inline tool-use blocks should appear between assistant messages in transcript order:

- Running: compact row with spinner, tool name, and arguments preview when available.
- Completed: success state, elapsed time, and result preview.
- Failed: error state, compact error summary, expandable details.
- Approval: approval rows remain separate in phase 1 but should visually link by `callId` in phase 2.

The live presence ticker remains as an active-turn summary. It should continue reading raw `props.items`, not the inline-filtered transcript list.

## File Structure

Create:

- `cmd/agent-terminal/frontend/vue-app/components/timeline/ToolUseBlock.ts`
  - Focused renderer for one normalized frontend tool item.

Modify:

- `cmd/agent-terminal/frontend/vue-app/components/timeline/useTimelineItems.js`
  - Stop filtering `tool` out of the main transcript.
  - Keep `thinking` and `command` bottom-only.
- `cmd/agent-terminal/frontend/vue-app/components/timeline/useTimelineHelpers.js`
  - Add tool display helpers used by `ToolUseBlock`.
- `cmd/agent-terminal/frontend/vue-app/components/ChatTimeline.js`
  - Register `ToolUseBlock`.
  - Replace the old inline `kind === 'tool'` process-row branch with the new component.
- `cmd/agent-terminal/frontend/vue-app/chat-timeline-split-guard.test.js`
  - Update expectations that currently assert tool items are filtered out.
- `cmd/agent-terminal/frontend/vue-app/chat-timeline-presence-popover.test.js`
  - Guard that the presence ticker still summarizes raw tool items.
- `cmd/agent-terminal/frontend/vue-app/streaming-sync-fix.test.js`
  - Guard that tool blocks do not break streaming assistant finalization.
- `cmd/agent-terminal/frontend/vue-app/thread-history-ui.test.js`
  - Add phase-2 history hydration tests for tool messages/events.
- `internal/dto/provider/message.go`
  - Phase 2 only: document and test supported metadata/eventType fields for tool-use history rows.
- `internal/provider/codexapp/history_rollout.go`
  - Phase 2 only: parse Codex rollout tool call/result shapes.
- `internal/util/historyjsonl/history.go`
  - Phase 2 only: mirror persisted history parsing behavior.
- `internal/module/thread/history.go`
  - Phase 2 only: preserve tool event metadata through `decorateThreadMessages`.

Do not edit generated frontend `dist` assets unless a release/build task explicitly requires it.

## Data Contract

Phase 1 uses existing frontend timeline item fields:

```ts
type ToolTimelineItem = {
  id: string;
  kind: 'tool';
  status?: 'running' | 'completed' | 'failed' | string;
  callId?: string;
  requestId?: number;
  tool?: string;
  toolName?: string;
  preview?: string;
  output?: string;
  error?: string;
  file?: string;
  elapsedMs?: number;
  success?: boolean;
  done?: boolean;
  ts?: string;
};
```

Phase 2 represents tool-use history rows as `dto.Message` with existing fields:

```go
dto.Message{
    Role:      "tool",
    EventType: "tool_call",
    Method:    "item/tool/call",
    Content:   "<compact fallback text>",
    Metadata: map[string]any{
        "kind": "tool",
        "phase": "begin|end",
        "callId": "...",
        "toolName": "...",
        "status": "running|completed|failed",
        "argumentsPreview": "...",
        "result": "...",
        "error": "...",
        "elapsedMs": 123,
        "success": true,
    },
}
```

Frontend history hydration should prefer `metadata.kind === "tool"` or `eventType === "tool_call"` over `role`. This avoids forcing a new public provider role into the display layer.

## Task 1: Render Existing Tool Items Inline

**Files:**

- Modify: `cmd/agent-terminal/frontend/vue-app/components/timeline/useTimelineItems.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/chat-timeline-split-guard.test.js`

- [ ] **Step 1: Update the failing test first**

In `chat-timeline-split-guard.test.js`, change the test that expects `tool` to be absent from `timelineItems`. The new assertion should keep `thinking` and `command` filtered while allowing `tool`.

Expected shape:

```js
expect(vm.timelineItems.map((item) => item.kind)).toContain('tool');
expect(vm.timelineItems.map((item) => item.kind)).not.toContain('thinking');
expect(vm.timelineItems.map((item) => item.kind)).not.toContain('command');
```

- [ ] **Step 2: Run the focused frontend test and confirm failure**

Run:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/chat-timeline-split-guard.test.js
```

Expected before implementation: the updated inline-tool expectation fails because `useTimelineItems` still filters `tool`.

- [ ] **Step 3: Change the filter**

In `useTimelineItems.js`, replace:

```js
return kind === 'thinking' || kind === 'command' || kind === 'tool';
```

with:

```js
return kind === 'thinking' || kind === 'command';
```

- [ ] **Step 4: Re-run the focused test**

Run:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/chat-timeline-split-guard.test.js
```

Expected: updated inline-tool tests pass; unrelated assertions may expose required rendering updates in later tasks.

## Task 2: Extract a ToolUseBlock Component

**Files:**

- Create: `cmd/agent-terminal/frontend/vue-app/components/timeline/ToolUseBlock.ts`
- Modify: `cmd/agent-terminal/frontend/vue-app/components/ChatTimeline.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/components/timeline/useTimelineHelpers.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/chat-timeline-split-guard.test.js`

- [ ] **Step 1: Add rendering tests for lifecycle states**

Add tests covering:

- running tool item renders tool name and running status
- completed tool item renders elapsed time and preview
- failed tool item renders error text
- long preview is present in an expandable/details region or a stable text container

Use existing `ChatTimeline` mount helpers in `chat-timeline-split-guard.test.js`; do not create a second test harness unless the existing helper cannot mount TypeScript component imports.

- [ ] **Step 2: Create the component**

`ToolUseBlock.ts` should expose a Vue component with props:

```ts
type ToolUseBlockProps = {
  item: Record<string, unknown>;
  displayFilePath?: (path: string) => string;
  toolSummaryText?: (item: Record<string, unknown>) => string;
};
```

Behavior:

- `status` is `failed` when `item.status === 'failed'`, `item.success === false`, or `item.error` is non-empty.
- `status` is `running` when `item.status === 'running'` or `item.done !== true`.
- `status` is `completed` otherwise.
- Display name uses `item.tool || item.toolName || 'unknown tool'`.
- Detail uses `item.preview || item.output || item.error || item.file`.
- Elapsed time shows only when `elapsedMs` is finite and greater than zero.

Use ordinary Vue template markup. Do not add a dependency on a new icon library in this task.

- [ ] **Step 3: Wire it into ChatTimeline**

In `ChatTimeline.js`:

- import `ToolUseBlock`
- add it to `components`
- replace the existing `kind === 'tool'` branch at `ChatTimeline.js:467` with:

```html
<ToolUseBlock
  :item="item"
  :display-file-path="displayFilePath"
  :tool-summary-text="toolSummaryText"
/>
```

- [ ] **Step 4: Add CSS using existing class conventions**

Use existing chat/process class family names. Add only scoped class names used by the component, for example:

- `tool-use-block`
- `tool-use-block--running`
- `tool-use-block--completed`
- `tool-use-block--failed`
- `tool-use-block__summary`
- `tool-use-block__details`

Keep dimensions stable. Long tool names and previews must wrap without resizing adjacent fixed controls.

- [ ] **Step 5: Verify focused rendering tests**

Run:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/chat-timeline-split-guard.test.js
```

Expected: lifecycle rendering tests pass.

## Task 3: Preserve Presence Ticker Behavior

**Files:**

- Modify: `cmd/agent-terminal/frontend/vue-app/chat-timeline-presence-popover.test.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/components/timeline/usePresencePopover.js`
- Modify only if needed: `cmd/agent-terminal/frontend/vue-app/components/timeline/useTimelineItems.js`

- [ ] **Step 1: Add a regression test**

Add a test where `props.items` includes:

```js
[
  { id: 'assistant-1', kind: 'assistant', text: 'Working', ts: '2026-05-19T10:00:00Z' },
  { id: 'tool-1', kind: 'tool', tool: 'lsp_grep', preview: 'query: ToolCallBegin', status: 'completed', elapsedMs: 18, ts: '2026-05-19T10:00:01Z' },
]
```

Assert:

- `timelineItems` includes `tool`
- presence/ticker summary still includes `lsp_grep` or the normalized tool label

- [ ] **Step 2: Keep the raw-source ticker**

If the regression test fails, ensure `usePresencePopover` reads from `trailingProcessItems` / `latestPresenceItems` derived from raw `props.items`, not from `timelineItems`.

- [ ] **Step 3: Run focused tests**

Run:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/chat-timeline-presence-popover.test.js vue-app/chat-timeline-split-guard.test.js
```

Expected: inline tool display and ticker summary both pass.

## Task 4: Guard Streaming and History Merge Ordering

**Files:**

- Modify: `cmd/agent-terminal/frontend/vue-app/streaming-sync-fix.test.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/stores/thread-history-ui.js` only if merge behavior breaks

- [ ] **Step 1: Add streaming-order test**

Create a case with:

```js
[
  { id: 'u1', kind: 'user', text: 'Search the code', ts: '2026-05-19T10:00:00Z' },
  { id: 'a1', kind: 'assistant', text: 'I will inspect it.', done: true, ts: '2026-05-19T10:00:01Z' },
  { id: 'tool-1', kind: 'tool', tool: 'lsp_grep', status: 'completed', preview: '2 files', ts: '2026-05-19T10:00:02Z' },
  { id: 'a2', kind: 'assistant', text: 'Found the path', done: false, ts: '2026-05-19T10:00:03Z' },
]
```

Assert the final rendered/derived order remains user, assistant, tool, streaming assistant.

- [ ] **Step 2: Run streaming tests**

Run:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/streaming-sync-fix.test.js
```

Expected: no duplicate assistant bubble and no dropped inline tool item.

## Task 5: Backend History Parsing for Codex Rollout Tool Items

**Files:**

- Modify: `internal/provider/codexapp/history_rollout.go`
- Modify: `internal/provider/codexapp/history_rollout_test.go`
- Modify: `internal/dto/provider/message.go` only for comments/tests if needed

- [ ] **Step 1: Add rollout tests**

Add a test with Codex JSONL order:

```json
{"timestamp":"2026-05-19T10:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I will inspect files."}]}}
{"timestamp":"2026-05-19T10:00:01Z","type":"response_item","payload":{"type":"tool_call","call_id":"call-1","name":"lsp_grep","arguments":{"query":"ToolCallBegin"}}}
{"timestamp":"2026-05-19T10:00:02Z","type":"response_item","payload":{"type":"tool_result","call_id":"call-1","name":"lsp_grep","result":"2 files","success":true,"elapsed_ms":18}}
{"timestamp":"2026-05-19T10:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Found it."}]}}
```

Assert four provider messages are returned in order, with the two tool rows carrying:

```go
Role: "tool"
EventType: "tool_call"
Method: "item/tool/call" // begin
Method: "item/completed" // end
Metadata["kind"] == "tool"
Metadata["callId"] == "call-1"
Metadata["toolName"] == "lsp_grep"
```

- [ ] **Step 2: Extend rollout payload parsing**

In `history_rollout.go`, split parsing into:

- `parseRolloutMessagePayload`
- `parseRolloutToolCallPayload`
- `parseRolloutToolResultPayload`

Keep fail-fast behavior for malformed payloads that claim to be tool events but miss `call_id` or tool name: return `(Message{}, false)` from the line parser and log only at caller level if needed. Do not synthesize IDs from system time.

- [ ] **Step 3: Reuse existing names**

Use metadata field names already used by event surface:

- `callId`
- `toolName`
- `argumentsPreview`
- `result`
- `error`
- `success`
- `elapsedMs`

- [ ] **Step 4: Run Codex provider tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
```

Expected: Codex provider tests pass.

## Task 6: Mirror Persisted History JSONL Parsing

**Files:**

- Modify: `internal/util/historyjsonl/history.go`
- Add or modify tests in the same package as existing history JSONL tests
- Modify: `internal/module/thread/read_view_test.go`

- [ ] **Step 1: Add persisted history tests**

Add the same Codex JSONL shape from Task 5 to the lower-level history JSONL tests. Assert tool rows are preserved as `dto.Message` with metadata.

- [ ] **Step 2: Share parsing logic where practical**

If direct sharing would create an import cycle, duplicate only the minimal parser in `internal/util/historyjsonl` and keep tests identical. Do not import `provider/codexapp` into `internal/util`.

- [ ] **Step 3: Update thread read-view fallback test**

In `internal/module/thread/read_view_test.go`, extend `TestReadMessagesFallsBackToPersistedRolloutWithoutSession` so fallback history contains dialog plus a tool begin/end pair. Assert `ReadMessages` returns the tool rows and keeps chronological order.

- [ ] **Step 4: Run affected Go tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/util/historyjsonl ./internal/module/thread -count=1
```

Expected: persisted history and thread read-view tests pass.

## Task 7: Hydrate Tool History Rows Into Timeline Items

**Files:**

- Modify: `cmd/agent-terminal/frontend/vue-app/stores/thread-history-ui.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/thread-history-ui.test.js`

- [ ] **Step 1: Add frontend hydration test**

Add a test where `thread/messages` returns:

```js
[
  { id: 1, role: 'assistant', content: 'I will inspect files.', createdAt: '2026-05-19T10:00:00Z' },
  {
    id: 2,
    role: 'tool',
    eventType: 'tool_call',
    method: 'item/tool/call',
    content: 'lsp_grep',
    createdAt: '2026-05-19T10:00:01Z',
    metadata: { kind: 'tool', phase: 'begin', callId: 'call-1', toolName: 'lsp_grep', argumentsPreview: '{"query":"ToolCallBegin"}', status: 'running' },
  },
  {
    id: 3,
    role: 'tool',
    eventType: 'tool_call',
    method: 'item/completed',
    content: '2 files',
    createdAt: '2026-05-19T10:00:02Z',
    metadata: { kind: 'tool', phase: 'end', callId: 'call-1', toolName: 'lsp_grep', result: '2 files', success: true, elapsedMs: 18, status: 'completed' },
  },
  { id: 4, role: 'assistant', content: 'Found it.', createdAt: '2026-05-19T10:00:03Z' },
]
```

Assert hydrated timeline kinds are:

```js
['assistant', 'tool', 'assistant']
```

The begin and end rows for the same `callId` should coalesce into one `tool` timeline item with `status: 'completed'`, `preview: '2 files'`, and `elapsedMs: 18`.

- [ ] **Step 2: Implement `historyMessageToTimelineItem` tool branch**

Before role-based dialog handling, detect:

```js
const metadata = parseHistoryMetadata(message?.metadata);
const isToolHistory = metadata?.kind === 'tool' || message?.eventType === 'tool_call';
```

Map to:

```js
{
  id: `${threadId}-history-tool-${metadata.callId || message.id}`,
  kind: 'tool',
  status,
  callId,
  tool: toolName,
  toolName,
  preview,
  error,
  elapsedMs,
  success,
  done: status !== 'running',
  ts,
}
```

- [ ] **Step 3: Coalesce begin/end history rows**

After `orderedMessages.map(...)`, merge adjacent or same-call tool history rows by `callId + toolName`. The end row should update the begin row rather than rendering a duplicate. Preserve original order by keeping the first row's position.

- [ ] **Step 4: Run hydration tests**

Run:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/thread-history-ui.test.js
```

Expected: history hydration includes inline tool rows and preserves existing image/internal metadata tests.

## Task 8: Optional Normalized Conversation Events RPC

**Files:**

- Create: `internal/dto/conversation/event.go`
- Create: `internal/module/thread/conversation_events.go`
- Modify: `internal/module/thread/rpc.go`
- Add tests: `internal/module/thread/conversation_events_test.go`

This task should be implemented only after Tasks 1-7 pass. It is the cleanup path that unifies live and history surfaces.

- [ ] **Step 1: Define DTOs**

Create:

```go
package conversation

import "time"

type Event struct {
    ID        string         `json:"id"`
    ThreadID  string         `json:"threadId,omitempty"`
    AgentID   string         `json:"agentId,omitempty"`
    TurnID    string         `json:"turnId,omitempty"`
    Kind      string         `json:"kind"`
    Role      string         `json:"role,omitempty"`
    Text      string         `json:"text,omitempty"`
    Tool      *ToolUse       `json:"tool,omitempty"`
    Timestamp time.Time      `json:"createdAt,omitempty"`
    Metadata  map[string]any `json:"metadata,omitempty"`
}

type ToolUse struct {
    CallID           string `json:"callId,omitempty"`
    ToolName         string `json:"toolName,omitempty"`
    Status           string `json:"status,omitempty"`
    ArgumentsPreview string `json:"argumentsPreview,omitempty"`
    Preview          string `json:"preview,omitempty"`
    Error            string `json:"error,omitempty"`
    ElapsedMS        int64  `json:"elapsedMs,omitempty"`
    Success          *bool  `json:"success,omitempty"`
}
```

- [ ] **Step 2: Add a read model**

`internal/module/thread/conversation_events.go` should merge:

- provider dialog messages from `ReadMessages`
- tool rows from `dto.Message.Metadata`
- live uistate timeline items when available for active threads

Dedupe key:

```text
threadId + agentId + turnId + callId + toolName + kind
```

Fail fast on malformed tool metadata that has `kind: tool` but no `callId` and no stable message ID. Do not generate IDs from system time.

- [ ] **Step 3: Register additive RPC**

Add:

```go
"thread/conversation/events": platformrpc.ThreadHandler(...)
```

Keep `thread/messages` unchanged until frontend migration is complete.

- [ ] **Step 4: Run thread tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/thread -count=1
```

Expected: new RPC tests pass; existing `thread/messages` tests still pass.

## Task 9: Verification

Run checks matching the changed surface:

Frontend:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

Go, if Tasks 5-8 are implemented:

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/util/historyjsonl ./internal/module/thread -count=1
make guard
```

If Task 8 changes shared DTOs or RPC registration, also run:

```bash
make build-plain
```

Before final reporting:

```bash
git status --short
```

Report unrelated dirty files separately and do not stage them.

## Rollout Notes

- Phase 1 is user-visible but frontend-only: inline tool blocks appear for live timeline items while history replay remains best-effort.
- Phase 2 makes Codex rollout/offline history replay show the same inserted tool blocks after reload.
- Phase 3 adds the optional normalized conversation RPC; keep it additive until the old `thread/messages` hydration path has equivalent test coverage.
- Do not remove `item/tool/call`, `item/completed`, or existing `ui/thread/patch` event-surface methods during this migration.
- Do not weaken guard thresholds or update `internal/archtest/baseline.json` unless a specific implementation task actually changes guard-owned files and the diff is inspected.
