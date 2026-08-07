# Chat Slash Command Palette Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Codex-like slash command palette to the Chat composer that searches built-ins, Skills, prompts, automations, and canonical MCP tools while preserving structured Skill/tool bindings across draft, send, success, and rollback flows.

**Architecture:** Add a read-only `toolbridge/tools/list` RPC for the active canonical tool surface, then normalize all five command sources behind focused frontend adapters and a cached catalog service. Keep the palette interaction local to the composer, while the client store owns scoped capability identities and serializes them into the existing `selectedSkills`, `selectedSkillRefs`, `manualSkillSelection`, and `enabledTools` turn fields.

**Tech Stack:** Go, Fx, jrpc2 strict handlers, React 19, TanStack Query 5, Zustand 5, Vitest, Testing Library, Lucide React, existing Super Dolphin Agent CSS tokens and Wails RPC bridge.

**Verification Surface:** `internal/platform/toolbridge`, `internal/app`, frontend RPC contract matrix and response guards, slash-command model/adapters/hooks/components, composer draft/send store, `frontend-app` lint/test/build, React Doctor, required LSP navigation and diagnostics, desktop/mobile light/dark browser checks.

**Design Spec:** `docs/superpowers/specs/2026-07-11-chat-slash-command-palette-design.md`

---

## Execution Constraints

- Continue on the current dedicated feature branch/worktree; do not merge or rewrite the user's unrelated work while executing tasks.
- Never stage or modify these pre-existing user changes:
  - `internal/archtest/orchestration_service_type_loader_test.go`
  - `internal/platform/toolbridge/codex_surface_helpers_test.go`
  - `internal/platform/toolbridge/codex_surface_test.go`
  - `internal/platform/toolbridge/handler_peer_decode.go`
  - `internal/platform/toolbridge/handler_peer_decode_helpers.go`
- Do not commit `.superpowers/` or `frontend-app/pnpm-lock.yaml`.
- Do not use `git add -A` or `git add .`; every commit below stages only its listed paths.
- Keep the running UI available at `http://127.0.0.1:5175/` unless a restart is required for the project MCP configuration.
- Treat malformed catalog data, unavailable tool surfaces, and stale selected capabilities as explicit errors. Do not add static command data as a fallback for runtime Skills, prompts, automations, or tools.

## Current LSP Evidence And Blocker

- Frontend LSP is available. `grep` and `structure` located `ComposerDock` at `frontend-app/src/pages/chat/composer/ComposerDock.jsx:51`; `file(read_file)` read the exact function; `xref` found the production use at `frontend-app/src/pages/chat/thread/Conversation.jsx:360`; frontend diagnostics returned `No diagnostics found.`
- Go `structure`, `inspect`, `xref`, and `file(diagnostics)` were retried against `internal/platform/toolbridge/handler_host_tools.go` with `language_id="go"` and the exact workspace root. They fail with:

```text
failed to auto-install gopls (go [install golang.org/x/tools/gopls@latest]):
exec: "go": executable file not found in $PATH
```

- Shell discovery confirms `/usr/local/bin/go` and `/usr/local/bin/gopls` exist. The project MCP `PATH` in `.codex/config.toml` omits `/usr/local/bin`. Task 1 repairs that launch environment before any Go source edit.

## File Structure

### LSP preflight

- Modify: `.codex/config.toml`
  - Adds the installed Go/gopls directory to the project MCP environment.

### Read-only canonical tool catalog

- Create: `internal/platform/toolbridge/tool_catalog.go`
  - Lists host, orchestration, and LSP tools for an explicit workspace; applies lifecycle policy and emits canonical names with source identity.
- Create: `internal/platform/toolbridge/tool_catalog_test.go`
  - Locks canonical naming, source metadata, lifecycle filtering, deduplication, and fail-closed discovery.
- Create: `internal/app/tool_catalog_rpc.go`
  - Exposes the strict `toolbridge/tools/list` read-only RPC.
- Create: `internal/app/tool_catalog_rpc_test.go`
  - Locks payload validation, response shape, errors, and handler registration.
- Modify: `internal/app/modules.go`
  - Provides the new grouped RPC handler map.

### Frontend RPC contract

- Modify: `frontend-app/src/shared/api/backend/backendRpcMethods.js`
- Modify: `frontend-app/src/shared/api/backendApi.js`
  - Adds `TOOLBRIDGE_TOOLS_LIST` and exports `listToolbridgeTools`.
- Modify: `frontend-app/src/shared/api/backend/backendApiFactoryOps.js`
  - Adds strict `{ cwd }` payload construction and API factory method.
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.test.js`
  - Validates the complete tool catalog response before consumers see it.
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`
  - Covers facade dispatch and the P1 read contract.

### Slash command domain

- Create: `frontend-app/src/features/slash-commands/model/slashCommandModel.js`
- Create: `frontend-app/src/features/slash-commands/model/slashCommandModel.test.js`
  - Owns trigger parsing, replacement, item validation, search ranking, category order, and stable DOM IDs.
- Create: `frontend-app/src/features/slash-commands/adapters/builtinSlashCommandAdapter.js`
- Create: `frontend-app/src/features/slash-commands/adapters/skillSlashCommandAdapter.js`
- Create: `frontend-app/src/features/slash-commands/adapters/promptSlashCommandAdapter.js`
- Create: `frontend-app/src/features/slash-commands/adapters/automationSlashCommandAdapter.js`
- Create: `frontend-app/src/features/slash-commands/adapters/mcpToolSlashCommandAdapter.js`
  - Each adapter owns one runtime schema and produces the common command item type.
- Create: `frontend-app/src/features/slash-commands/services/slashCommandCatalogService.js`
- Create: `frontend-app/src/features/slash-commands/services/slashCommandCatalogService.test.js`
  - Owns source calls and lazy prompt body lookup; never substitutes static runtime data.
- Create: `frontend-app/src/features/slash-commands/hooks/useSlashCommandCatalog.js`
- Create: `frontend-app/src/features/slash-commands/hooks/useSlashCommandPalette.js`
- Create: `frontend-app/src/features/slash-commands/hooks/useSlashCommandPalette.test.jsx`
  - Owns per-category TanStack Query state, dismissal, highlight, keyboard handling, selection semantics, and focus restoration.
- Create: `frontend-app/src/features/slash-commands/components/SlashCommandPalette.jsx`
- Create: `frontend-app/src/features/slash-commands/components/SlashCommandPalette.test.jsx`
- Create: `frontend-app/src/features/slash-commands/components/SlashCommandPalette.css`
  - Renders the grouped listbox, partial errors, disabled reasons, empty state, and bounded scrolling.
- Create: `frontend-app/src/features/slash-commands/components/ComposerCapabilityChips.jsx`
- Create: `frontend-app/src/features/slash-commands/components/ComposerCapabilityChips.test.jsx`
  - Renders removable selected Skill/tool chips and unavailable state.

### Composer capability state and integration

- Create: `frontend-app/src/entities/client/model/composerCapabilities.js`
- Create: `frontend-app/src/entities/client/model/composerCapabilities.test.js`
  - Normalizes, deduplicates, snapshots, restores, reconciles, validates, and serializes selected capabilities.
- Modify: `frontend-app/src/entities/client/model/composerAttachments.js`
- Modify: `frontend-app/src/entities/client/model/composerAttachments.test.js`
  - Extends the existing scoped composer snapshot with capability identities.
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreRuntimeCore.js`
  - Saves/restores capability snapshots and restores the target project's new-chat snapshot during project switches.
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreUtils.js`
  - Adds initial `composerCapabilities` state.
- Modify: `frontend-app/src/entities/client/model/helpers/threadSelectionActions.js`
  - Restores capabilities whenever a thread/new-chat composer scope changes.
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreSendModel.js`
  - Captures capability payloads and rollback state; clears optimistically and restores on failure.
- Modify: `frontend-app/src/entities/client/model/helpers/a2Slice/composerSliceActions.js`
  - Adds capability actions, passes structured fields to `turn/start`, and implements `/clear`'s atomic composer reset.
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`
  - Covers scoped restore, exact turn payload, success clear, failure rollback, retry, and stale blocking.
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`
  - Composes the palette and chips, lets palette keys pre-empt send keys, and keeps capability-only send disabled.
- Modify: `frontend-app/src/pages/chat/composer/ComposerTextarea.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerTextarea.test.jsx`
  - Exposes listbox ARIA relationships without moving textarea focus.
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.css`
- Modify: `frontend-app/src/styles.test.js`
  - Anchors the opaque bounded palette above the composer in desktop/mobile and light/dark modes.
- Modify: `frontend-app/src/shared/i18n/appI18n.zh.json`
- Modify: `frontend-app/src/shared/i18n/appI18n.en.json`
  - Adds all palette, category, error, disabled, builtin, and chip-removal copy.

## Task 1: Repair And Verify The Go LSP Gate

**Files:**
- Modify: `.codex/config.toml`
- Read: `docs/internal-notes/LSP系统提示词.md`
- Inspect: `internal/platform/toolbridge/handler_host_tools.go`
- Inspect: `frontend-app/src/pages/chat/composer/ComposerDock.jsx`

- [ ] **Step 1: Reproduce the narrowed Go LSP failure before editing**

Run the repository LSP tools:

```text
structure(document_symbol,
  file_path="internal/platform/toolbridge/handler_host_tools.go",
  work_dir="/Users/l4place/Documents/Super-Dolphin")

file(diagnostics,
  file_path="internal/platform/toolbridge/handler_host_tools.go",
  language_id="go",
  work_dir="/Users/l4place/Documents/Super-Dolphin")
```

Expected: FAIL with `exec: "go": executable file not found in $PATH`. Record this exact baseline in the implementation notes; do not call it a diagnostic pass.

- [ ] **Step 2: Add the installed Go toolchain directory to the MCP environment**

Change only the `PATH` line in `.codex/config.toml` to:

```toml
PATH = "/Users/l4place/Documents/Super-Dolphin/.build-cache/pnpm-home/bin:/Users/l4place/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin:/Users/l4place/Documents/Super-Dolphin/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
```

Run:

```bash
/usr/local/bin/go version
/usr/local/bin/gopls version
```

Expected: both commands exit `0` and print a version.

- [ ] **Step 3: Restart the Codex app task so the MCP process receives the new environment**

Close and reopen the project task, then confirm the tool surface exposes:

```text
mcp__lsp__grep
mcp__lsp__file
mcp__lsp__structure
mcp__lsp__inspect
mcp__lsp__xref
mcp__lsp__lsp_edit
mcp__lsp__lsp_completion
```

Expected: all seven tools are callable.

- [ ] **Step 4: Collect the complete LSP evidence chain**

Run:

```text
grep(text_search, query="ListToolsForCodex",
  path="internal/platform/toolbridge",
  glob="*.go",
  work_dir="/Users/l4place/Documents/Super-Dolphin")

structure(document_symbol,
  file_path="internal/platform/toolbridge/handler_host_tools.go",
  work_dir="/Users/l4place/Documents/Super-Dolphin")

inspect(hover,
  pos="internal/platform/toolbridge/handler_host_tools.go:89:19",
  work_dir="/Users/l4place/Documents/Super-Dolphin")

xref(references,
  pos="internal/platform/toolbridge/handler_host_tools.go:89:19",
  work_dir="/Users/l4place/Documents/Super-Dolphin")

file(read_file,
  pos="internal/platform/toolbridge/handler_host_tools.go:89",
  limit=50,
  work_dir="/Users/l4place/Documents/Super-Dolphin")

file(diagnostics,
  file_path="internal/platform/toolbridge/handler_host_tools.go",
  work_dir="/Users/l4place/Documents/Super-Dolphin")
```

Expected: symbol, hover, references, and source are returned; diagnostics contains no Error, Warning, Information, or Hint. If diagnostics still fails, stop before Task 3 and report the tool/action, workspace, target file, error, and narrowed retries.

- [ ] **Step 5: Commit the project MCP environment repair**

```bash
git add .codex/config.toml
git commit -m "chore(lsp): expose Go toolchain to project MCP"
```

Expected: one commit containing only `.codex/config.toml`.

## Task 2: Slash Trigger, Ranking, Built-ins, And Copy

**Files:**
- Create: `frontend-app/src/features/slash-commands/model/slashCommandModel.js`
- Create: `frontend-app/src/features/slash-commands/model/slashCommandModel.test.js`
- Create: `frontend-app/src/features/slash-commands/adapters/builtinSlashCommandAdapter.js`
- Modify: `frontend-app/src/shared/i18n/appI18n.zh.json`
- Modify: `frontend-app/src/shared/i18n/appI18n.en.json`

- [ ] **Step 1: Write failing trigger and ranking tests**

Create `slashCommandModel.test.js` with these cases:

```js
import { describe, expect, it } from 'vitest';
import {
  parseSlashCommandTrigger,
  rankSlashCommandItems,
  replaceSlashCommandTrigger,
} from './slashCommandModel.js';

describe('parseSlashCommandTrigger', () => {
  it.each([
    ['/', { leading: '', query: '', raw: '/' }],
    ['  /review', { leading: '  ', query: 'review', raw: '/review' }],
  ])('opens only for the first non-whitespace slash: %s', (draft, expected) => {
    expect(parseSlashCommandTrigger(draft)).toEqual(expected);
  });

  it.each(['hello /review', 'https://example.com/a', '/review now', '/review\nnext', 'C:/repo'])
    ('does not open for %s', (draft) => {
      expect(parseSlashCommandTrigger(draft)).toBeNull();
    });

  it('preserves leading whitespace while replacing the trigger', () => {
    expect(replaceSlashCommandTrigger('  /review', 'Review this change')).toBe('  Review this change');
  });
});

describe('rankSlashCommandItems', () => {
  it('orders by match quality and then builtin, skill, prompt, automation, tool', () => {
    const items = [
      { id: 'mcp_tool:lsp:review', kind: 'mcp_tool', name: 'review', label: 'Review tool', description: '', keywords: [] },
      { id: 'prompt:review', kind: 'prompt', name: 'review-notes', label: 'Review notes', description: '', keywords: [] },
      { id: 'skill:review', kind: 'skill', name: 'review', label: 'Review', description: '', keywords: [] },
      { id: 'builtin:new', kind: 'builtin', name: 'new', label: 'New chat', description: '', keywords: ['review'] },
    ];
    expect(rankSlashCommandItems(items, 'review').map((item) => item.id)).toEqual([
      'skill:review',
      'prompt:review',
      'mcp_tool:lsp:review',
      'builtin:new',
    ]);
  });
});
```

- [ ] **Step 2: Run the focused model test and confirm RED**

```bash
cd frontend-app
npx vitest run src/features/slash-commands/model/slashCommandModel.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because `slashCommandModel.js` does not exist.

- [ ] **Step 3: Implement the pure model**

Create `slashCommandModel.js` with this public surface:

```js
const SLASH_TRIGGER_RE = /^(\s*)\/([^\s]*)$/u;

export const SLASH_COMMAND_KIND_ORDER = Object.freeze([
  'builtin',
  'skill',
  'prompt',
  'automation',
  'mcp_tool',
]);

export function parseSlashCommandTrigger(value) {
  const draft = typeof value === 'string' ? value : '';
  const match = SLASH_TRIGGER_RE.exec(draft);
  if (!match) return null;
  return { leading: match[1], query: match[2], raw: `/${match[2]}` };
}

export function replaceSlashCommandTrigger(draft, replacement) {
  const trigger = parseSlashCommandTrigger(draft);
  if (!trigger) throw new Error('slash command trigger is not active');
  return `${trigger.leading}${String(replacement ?? '')}`;
}

export function slashCommandOptionId(item) {
  return `slash-command-${item.id.replace(/[^a-zA-Z0-9_-]/g, '-')}`;
}
```

Implement `normalizeSlashCommandItem(raw)` so all nine required fields (`id`, `kind`, `name`, `label`, `description`, `keywords`, `payload`, `disabled`, `disabledReason`) are validated, `kind` is one of `SLASH_COMMAND_KIND_ORDER`, `disabled` is boolean, `keywords` is a deduplicated string array, and malformed items throw. Implement `rankSlashCommandItems(items, query)` with match ranks `name/label prefix -> name/label contains -> keyword/description contains`, then `SLASH_COMMAND_KIND_ORDER`, then original input index.

- [ ] **Step 4: Add bilingual command copy and built-ins**

Add this nested object under `chat` in both locale JSON files, translated in the English file:

```json
"slashCommands": {
  "label": "命令与能力",
  "loading": "正在加载",
  "noResults": "没有匹配的命令",
  "projectRequired": "请先选择项目",
  "unavailable": "当前不可用",
  "categories": {
    "builtin": "聊天命令",
    "skill": "技能",
    "prompt": "提示词",
    "automation": "自动化",
    "mcp_tool": "MCP 工具"
  },
  "builtins": {
    "newLabel": "新对话",
    "newDescription": "保留当前项目并创建新对话草稿",
    "clearLabel": "清空输入",
    "clearDescription": "清空文本、附件和已选能力"
  },
  "removeCapability": "移除能力",
  "staleCapability": "能力已失效，请移除后重新选择",
  "unverifiedCapability": "能力目录尚未验证，请等待同步",
  "promptLoadFailed": "读取提示词失败",
  "catalogLoadFailed": "目录加载失败"
}
```

Create `builtinSlashCommandAdapter.js`:

```js
export function builtinSlashCommandItems(copy) {
  return [
    {
      id: 'builtin:new', kind: 'builtin', name: 'new',
      label: copy.builtins.newLabel, description: copy.builtins.newDescription,
      keywords: ['new', 'chat'], payload: { action: 'new' }, disabled: false, disabledReason: '',
    },
    {
      id: 'builtin:clear', kind: 'builtin', name: 'clear',
      label: copy.builtins.clearLabel, description: copy.builtins.clearDescription,
      keywords: ['clear', 'reset'], payload: { action: 'clear' }, disabled: false, disabledReason: '',
    },
  ];
}
```

- [ ] **Step 5: Run tests and commit**

```bash
cd frontend-app
npx vitest run src/features/slash-commands/model/slashCommandModel.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

```bash
git add frontend-app/src/features/slash-commands/model/slashCommandModel.js frontend-app/src/features/slash-commands/model/slashCommandModel.test.js frontend-app/src/features/slash-commands/adapters/builtinSlashCommandAdapter.js frontend-app/src/shared/i18n/appI18n.zh.json frontend-app/src/shared/i18n/appI18n.en.json
git commit -m "feat(chat): add slash command domain model"
```

Expected: one frontend-only commit.

## Task 3: Canonical Toolbridge Catalog RPC

**Files:**
- Create: `internal/platform/toolbridge/tool_catalog.go`
- Create: `internal/platform/toolbridge/tool_catalog_test.go`
- Create: `internal/app/tool_catalog_rpc.go`
- Create: `internal/app/tool_catalog_rpc_test.go`
- Modify: `internal/app/modules.go`

- [ ] **Step 1: Write failing tool catalog tests**

Create `tool_catalog_test.go` in package `toolbridge` and reuse the existing package test peers and lifecycle owner:

```go
func TestListToolCatalogReturnsCanonicalWorkspaceTools(t *testing.T) {
	root := t.TempDir()
	owner := newFakeMCPToolLifecycleOwner()
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent", Description: "Launch"}}, nil)},
			mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{
				{Name: "edit", Description: "Edit source"},
				{Name: "completion", Description: "Complete source"},
				{Name: "grep", Description: "Search source"},
			}, nil)},
		}},
		lifecycle: owner,
		lifecyclePolicy: owner,
	}

	got, err := h.ListToolCatalog(context.Background(), root)
	if err != nil {
		t.Fatalf("ListToolCatalog() error = %v", err)
	}
	assertToolCatalogEntry(t, got, mcpdto.ClientKindLSP, "lsp_edit", "Edit source")
	assertToolCatalogEntry(t, got, mcpdto.ClientKindLSP, "lsp_completion", "Complete source")
	assertToolCatalogEntry(t, got, mcpdto.ClientKindLSP, "grep", "Search source")
	assertToolCatalogEntry(t, got, mcpdto.ClientKindOrch, "launch_agent", "Launch")
}

func TestListToolCatalogFailsClosedWhenPeerDiscoveryFails(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcpdto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
	}}}
	got, err := h.ListToolCatalog(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "orch down") || len(got) != 0 {
		t.Fatalf("tools=%+v error=%v, want fail-closed orch error", got, err)
	}
}

func TestListToolCatalogFiltersDisabledWorkspaceTool(t *testing.T) {
	root := t.TempDir()
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleDisabled, "disabled by user")
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent"}}, nil)},
			mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		lifecycle: owner,
		lifecyclePolicy: owner,
	}
	got, err := h.ListToolCatalog(context.Background(), root)
	if err != nil {
		t.Fatalf("ListToolCatalog() error = %v", err)
	}
	for _, item := range got {
		if item.ToolName == "grep" {
			t.Fatalf("disabled grep leaked into catalog: %+v", got)
		}
	}
	assertToolCatalogEntry(t, got, mcpdto.ClientKindOrch, "launch_agent", "")
}

func assertToolCatalogEntry(t *testing.T, items []ToolCatalogEntry, serverName, toolName, description string) {
	t.Helper()
	for _, item := range items {
		if item.ServerName == serverName && item.ToolName == toolName {
			if item.DisplayName != toolName || item.Description != description || !item.Enabled || item.DisabledReason != "" {
				t.Fatalf("catalog entry = %+v", item)
			}
			return
		}
	}
	t.Fatalf("missing %s/%s in %+v", serverName, toolName, items)
}
```

- [ ] **Step 2: Write failing RPC dispatch tests**

Create `internal/app/tool_catalog_rpc_test.go` with a stub that implements `ListToolCatalog` and register the map on `platformrpc.Server`:

```go
type stubToolCatalogLister struct {
	tools []toolbridge.ToolCatalogEntry
	cwd   string
	calls int
	err   error
}

func (s *stubToolCatalogLister) ListToolCatalog(_ context.Context, cwd string) ([]toolbridge.ToolCatalogEntry, error) {
	s.cwd = cwd
	s.calls++
	return append([]toolbridge.ToolCatalogEntry(nil), s.tools...), s.err
}

func TestToolCatalogRPCDispatchesStrictWorkspaceRequest(t *testing.T) {
	lister := &stubToolCatalogLister{tools: []toolbridge.ToolCatalogEntry{{
		ServerName: "lsp", ToolName: "grep", DisplayName: "grep",
		Description: "Search source", Enabled: true,
	}}}
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(toolCatalogHandlers(lister).Handlers)

	raw, err := server.Dispatch(t.Context(), "toolbridge/tools/list", json.RawMessage(`{"cwd":"/repo/app"}`))
	if err != nil {
		t.Fatalf("Dispatch toolbridge/tools/list: %v", err)
	}
	if lister.cwd != "/repo/app" || !bytes.Contains(raw, []byte(`"toolName":"grep"`)) {
		t.Fatalf("cwd=%q raw=%s", lister.cwd, raw)
	}
}

func TestToolCatalogRPCRejectsInvalidPayloadBeforeLister(t *testing.T) {
	for _, raw := range []string{`{}`, `{"cwd":" "}`, `{"cwd":"/repo","extra":true}`} {
		t.Run(raw, func(t *testing.T) {
			lister := &stubToolCatalogLister{}
			server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
			server.Register(toolCatalogHandlers(lister).Handlers)
			if _, err := server.Dispatch(t.Context(), "toolbridge/tools/list", json.RawMessage(raw)); err == nil {
				t.Fatalf("Dispatch(%s) error = nil", raw)
			}
			if lister.calls != 0 {
				t.Fatalf("lister calls = %d, want 0", lister.calls)
			}
		})
	}
}
```

- [ ] **Step 3: Run focused Go tests and confirm RED**

```bash
go test ./internal/platform/toolbridge ./internal/app -run 'TestListToolCatalog|TestToolCatalogRPC' -count=1
```

Expected: FAIL because `ToolCatalogEntry`, `ListToolCatalog`, and the RPC provider do not exist.

- [ ] **Step 4: Implement the workspace-aware catalog**

Create `internal/platform/toolbridge/tool_catalog.go` with this wire type and method boundary:

```go
type ToolCatalogEntry struct {
	ServerName    string `json:"serverName"`
	ToolName      string `json:"toolName"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	Enabled       bool   `json:"enabled"`
	DisabledReason string `json:"disabledReason"`
}

func (h *Handler) ListToolCatalog(ctx context.Context, cwd string) ([]ToolCatalogEntry, error) {
	workspaceRoot, err := normalizeMCPToolLifecycleWorkspaceRoot(cwd)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var hostTools []mcpdto.MCPTool
	if h != nil && h.hostTools != nil {
		hostTools = h.hostTools.ListHostTools()
	}
	outcomes := h.listPeerToolsForCodex(ctx, mcpdto.ClientKindOrch, mcpdto.ClientKindLSP)
	if err := joinPeerToolErrors(outcomes); err != nil {
		return nil, fmt.Errorf("toolbridge tool catalog peer discovery failed: %w", err)
	}
	for i := range outcomes {
		if err := h.backfillMCPToolLifecycle(ctx, workspaceRoot, outcomes[i].clientKind, outcomes[i].clientKind, outcomes[i].tools); err != nil {
			return nil, err
		}
		outcomes[i].tools, err = h.filterMCPToolLifecycleTools(ctx, workspaceRoot, outcomes[i].clientKind, outcomes[i].clientKind, outcomes[i].tools)
		if err != nil {
			return nil, err
		}
	}
	return h.buildToolCatalog(hostTools, outcomes)
}

func (h *Handler) buildToolCatalog(hostTools []mcpdto.MCPTool, outcomes []peerToolsListOutcome) ([]ToolCatalogEntry, error) {
	seenSources := make(map[string]string)
	host, err := canonicalCatalogTools("host", hostTools)
	if err != nil {
		return nil, err
	}
	merged := h.appendDynamicToolsWithShadowWarning(nil, seenSources, "host", host)
	for _, outcome := range outcomes {
		canonical, err := canonicalCatalogTools(outcome.clientKind, outcome.tools)
		if err != nil {
			return nil, err
		}
		merged = h.appendDynamicToolsWithShadowWarning(merged, seenSources, outcome.clientKind, canonical)
	}
	if len(merged) == 0 {
		return nil, ErrNoPeerAvailable
	}
	out := make([]ToolCatalogEntry, 0, len(merged))
	for _, tool := range merged {
		out = append(out, ToolCatalogEntry{
			ServerName: seenSources[tool.Name], ToolName: tool.Name, DisplayName: tool.Name,
			Description: strings.TrimSpace(tool.Description), Enabled: true, DisabledReason: "",
		})
	}
	return out, nil
}

func canonicalCatalogTools(serverName string, tools []mcpdto.MCPTool) ([]mcpdto.MCPTool, error) {
	seen := make(map[string]struct{}, len(tools))
	out := make([]mcpdto.MCPTool, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return nil, fmt.Errorf("toolbridge tool catalog %s tool name is required", serverName)
		}
		if serverName != "host" {
			name = canonicalCodexToolName(serverName, name)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("toolbridge tool catalog %s canonical name %q conflicts", serverName, name)
		}
		seen[name] = struct{}{}
		tool.Name = name
		out = append(out, tool)
	}
	return out, nil
}
```

This preserves stable host, orchestration, then LSP order. Cross-source shadows follow the existing host-first toolbridge rule; same-source canonical collisions fail instead of publishing an ambiguous entry.

- [ ] **Step 5: Implement and register the strict RPC**

Create `internal/app/tool_catalog_rpc.go`:

```go
const toolCatalogListMethod = "toolbridge/tools/list"

type toolCatalogLister interface {
	ListToolCatalog(context.Context, string) ([]toolbridge.ToolCatalogEntry, error)
}

type toolCatalogListRequest struct {
	CWD string `json:"cwd"`
}

type toolCatalogListResponse struct {
	Tools []toolbridge.ToolCatalogEntry `json:"tools"`
}

func newToolCatalogHandlers(h *toolbridge.Handler) platformrpc.HandlerMapResult {
	return toolCatalogHandlers(h)
}

func toolCatalogHandlers(lister toolCatalogLister) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		toolCatalogListMethod: platformrpc.StrictHandler(func(ctx context.Context, req toolCatalogListRequest) (toolCatalogListResponse, error) {
			cwd := strings.TrimSpace(req.CWD)
			if cwd == "" {
				return toolCatalogListResponse{}, platformrpc.ErrInvalidParams("toolbridge/tools/list: cwd is required")
			}
			if lister == nil {
				return toolCatalogListResponse{}, platformrpc.ErrInvalidState("toolbridge tool catalog is not configured")
			}
			tools, err := lister.ListToolCatalog(ctx, cwd)
			if err != nil {
				return toolCatalogListResponse{}, err
			}
			if tools == nil {
				tools = []toolbridge.ToolCatalogEntry{}
			}
			return toolCatalogListResponse{Tools: tools}, nil
		}),
	}}
}
```

Add `fx.Provide(newToolCatalogHandlers)` in `internal/app/modules.go` beside the toolbridge app adapters.

- [ ] **Step 6: Run tests, LSP diagnostics, and commit**

```bash
go test ./internal/platform/toolbridge ./internal/app -run 'TestListToolCatalog|TestToolCatalogRPC' -count=1
go test ./internal/platform/toolbridge ./internal/app -count=1
```

Expected: PASS.

Run LSP diagnostics on both new Go files and `internal/app/modules.go`. Expected: no diagnostics at any severity.

```bash
git add internal/platform/toolbridge/tool_catalog.go internal/platform/toolbridge/tool_catalog_test.go internal/app/tool_catalog_rpc.go internal/app/tool_catalog_rpc_test.go internal/app/modules.go
git commit -m "feat(toolbridge): expose bindable tool catalog"
```

Expected: the commit does not include any of the five pre-existing user-modified Go files.

## Task 4: Frontend Tool Catalog API Contract

**Files:**
- Modify: `frontend-app/src/shared/api/backend/backendRpcMethods.js`
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/shared/api/backend/backendApiFactoryOps.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.test.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`

- [ ] **Step 1: Write failing facade and response guard tests**

Add to `backendApi.test.js`:

```js
it('lists the canonical toolbridge catalog for one cwd', async () => {
  const callAPI = vi.fn().mockResolvedValue({ tools: [] });
  const api = createBackendApi({ callAPI });

  await api.listToolbridgeTools({ cwd: '/repo/app' });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TOOLBRIDGE_TOOLS_LIST, { cwd: '/repo/app' });
  expect(() => api.listToolbridgeTools({ cwd: '/repo/app', serverName: 'lsp' }))
    .toThrow('toolbridge/tools/list: unsupported payload field serverName');
});
```

Add response validator cases:

```js
expect(() => validate(RPC_METHODS.TOOLBRIDGE_TOOLS_LIST, {
  tools: [{
    serverName: 'lsp', toolName: 'lsp_edit', displayName: 'lsp_edit',
    description: 'Edit source', enabled: true, disabledReason: '',
  }],
})).not.toThrow();

expect(() => validate(RPC_METHODS.TOOLBRIDGE_TOOLS_LIST, {
  tools: [{ serverName: 'lsp', displayName: 'grep', description: '', enabled: true, disabledReason: '' }],
})).toThrow('toolbridge/tools/list response tools[0].toolName must be a non-empty string');
```

- [ ] **Step 2: Run focused tests and confirm RED**

```bash
cd frontend-app
npx vitest run src/shared/api/backendApi.test.js src/shared/api/backendResponseValidators.test.js src/shared/api/backendApi.contractMatrix.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the method and validator are missing.

- [ ] **Step 3: Implement the strict facade and guard**

Add `TOOLBRIDGE_TOOLS_LIST: 'toolbridge/tools/list'` to both RPC constant objects. In `backendApiFactoryOps.js`, add:

```js
function toolbridgeToolsListPayload(params) {
  const method = RPC_METHODS.TOOLBRIDGE_TOOLS_LIST;
  const payload = { ...assertStrictPlainObject(method, params) };
  const cwd = normalizeString(takePayloadField(payload, 'cwd'));
  assertNoExtraPayloadFields(method, payload);
  if (!cwd) throw new Error(`${method}: cwd is required`);
  return { cwd };
}
```

Add this method to `createMCPServerApi`:

```js
listToolbridgeTools: (params) => callBackend(
  RPC_METHODS.TOOLBRIDGE_TOOLS_LIST,
  toolbridgeToolsListPayload(params),
),
```

Export it from `backendApi.js`. Add this strict response validator and register it under `methods.TOOLBRIDGE_TOOLS_LIST`:

```js
const TOOLBRIDGE_TOOLS_RESPONSE_KEYS = new Set(['tools']);
const TOOLBRIDGE_TOOL_RESPONSE_KEYS = new Set([
  'serverName', 'toolName', 'displayName', 'description', 'enabled', 'disabledReason',
]);

function validateToolbridgeToolsListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, TOOLBRIDGE_TOOLS_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.tools)) {
    throw new TypeError(`${method} response tools must be an array`);
  }
  value.tools.forEach((tool, index) => {
    const label = `tools[${index}]`;
    if (!tool || typeof tool !== 'object' || Array.isArray(tool)) {
      throw new TypeError(`${method} response ${label} must be an object`);
    }
    assertOnlyResponseKeys(method, tool, TOOLBRIDGE_TOOL_RESPONSE_KEYS, label);
    for (const key of ['serverName', 'toolName', 'displayName']) {
      if (!normalizeString(tool[key])) {
        throw new TypeError(`${method} response ${label}.${key} must be a non-empty string`);
      }
    }
    for (const key of ['description', 'disabledReason']) {
      if (typeof tool[key] !== 'string') {
        throw new TypeError(`${method} response ${label}.${key} must be a string`);
      }
    }
    if (typeof tool.enabled !== 'boolean') {
      throw new TypeError(`${method} response ${label}.enabled must be a boolean`);
    }
  });
  return value;
}

// Inside createBackendResponseValidators:
[methods.TOOLBRIDGE_TOOLS_LIST]: validateToolbridgeToolsListResponse,
```

- [ ] **Step 4: Add the contract matrix record**

Add:

```js
TOOLBRIDGE_TOOLS_LIST: contract(
  'TOOLBRIDGE_TOOLS_LIST',
  'listToolbridgeTools',
  'P1',
  'mcpServer',
  [TESTS.API, TESTS.SURFACE],
  ['canonical bindable tool read', 'strict cwd payload'],
  false,
  { responseValidator: 'toolbridgeToolsListResponse' },
),
```

Assert the validator name in `backendApi.contractMatrix.test.js`.

- [ ] **Step 5: Run tests and commit**

```bash
cd frontend-app
npx vitest run src/shared/api/backendApi.test.js src/shared/api/backendResponseValidators.test.js src/shared/api/backendApi.contractMatrix.test.js --no-file-parallelism --maxWorkers=1
npm run typecheck:contracts
npm run audit:rpc-contracts
```

Expected: PASS.

```bash
git add frontend-app/src/shared/api/backend/backendRpcMethods.js frontend-app/src/shared/api/backendApi.js frontend-app/src/shared/api/backend/backendApiFactoryOps.js frontend-app/src/shared/api/backendResponseValidators.js frontend-app/src/shared/api/backendResponseValidators.test.js frontend-app/src/shared/api/backendApi.test.js frontend-app/src/shared/api/backendApi.contractMatrix.js frontend-app/src/shared/api/backendApi.contractMatrix.test.js
git commit -m "feat(frontend): add tool catalog API contract"
```

## Task 5: Runtime Catalog Adapters And Service

**Files:**
- Create: `frontend-app/src/features/slash-commands/adapters/skillSlashCommandAdapter.js`
- Create: `frontend-app/src/features/slash-commands/adapters/promptSlashCommandAdapter.js`
- Create: `frontend-app/src/features/slash-commands/adapters/automationSlashCommandAdapter.js`
- Create: `frontend-app/src/features/slash-commands/adapters/mcpToolSlashCommandAdapter.js`
- Create: `frontend-app/src/features/slash-commands/services/slashCommandCatalogService.js`
- Create: `frontend-app/src/features/slash-commands/services/slashCommandCatalogService.test.js`

- [ ] **Step 1: Write failing adapter and service tests**

Use injected API spies and real response shapes:

```js
it('normalizes all project catalogs and keeps prompt content lazy', async () => {
  const api = {
    getDashboardPage: vi.fn()
      .mockResolvedValueOnce({ skills: [{
        name: 'review', display_name: 'Code Review', dir: '/repo/.agents/skills/review',
        skill_file: '/repo/.agents/skills/review/SKILL.md', scope: 'project',
        description: 'Review code', summary: '', trigger_words: ['audit'], force_words: [],
      }] })
      .mockResolvedValueOnce({ dags: [{
        id: 'dag-1', title: 'Release check', description: 'Check release',
        config: { prompt: 'Check the release candidate' },
      }] }),
    getDashboardPrompts: vi.fn().mockResolvedValue({ prompts: [{
      id: 'prompt-1', name: 'Review prompt', description: 'Review carefully', enabled: true,
    }] }),
    getPrompt: vi.fn().mockResolvedValue({ prompt: { content: 'Review this change carefully.' } }),
    listToolbridgeTools: vi.fn().mockResolvedValue({ tools: [{
      serverName: 'lsp', toolName: 'lsp_edit', displayName: 'LSP Edit',
      description: 'Edit source', enabled: true, disabledReason: '',
    }] }),
  };
  const service = createSlashCommandCatalogService(api);

  await expect(service.loadSkills('/repo')).resolves.toEqual([
    expect.objectContaining({ id: 'skill:project::review:/repo/.agents/skills/review', kind: 'skill', name: 'review' }),
  ]);
  await expect(service.loadPrompts('/repo')).resolves.toEqual([
    expect.objectContaining({ id: 'prompt:prompt-1', kind: 'prompt' }),
  ]);
  expect(api.getPrompt).not.toHaveBeenCalled();
  await expect(service.loadPromptContent('/repo', 'prompt-1')).resolves.toBe('Review this change carefully.');
  await expect(service.loadAutomations('/repo')).resolves.toEqual([
    expect.objectContaining({ kind: 'automation', disabled: false }),
  ]);
  await expect(service.loadMCPTools('/repo')).resolves.toEqual([
    expect.objectContaining({ id: 'mcp_tool:lsp:lsp_edit', name: 'lsp_edit' }),
  ]);
});
```

Add this table after the happy-path test so every category proves that one malformed item rejects the whole category instead of being skipped:

```js
const validSkill = {
  name: 'review', display_name: 'Code Review', dir: '/repo/.agents/skills/review',
  skill_file: '/repo/.agents/skills/review/SKILL.md', scope: 'project',
  description: 'Review code', summary: '', trigger_words: [], force_words: [],
};
const validPrompt = { id: 'prompt-1', name: 'Review prompt', description: '', enabled: true };
const validAutomation = {
  id: 'dag-1', title: 'Release check', description: '', prompt: 'Check release',
};
const validTool = {
  serverName: 'lsp', toolName: 'grep', displayName: 'grep', description: '',
  enabled: true, disabledReason: '',
};

it.each([
  {
    label: 'skill', method: 'loadSkills',
    api: { getDashboardPage: vi.fn().mockResolvedValue({ skills: [validSkill, { ...validSkill, name: '' }] }) },
  },
  {
    label: 'prompt', method: 'loadPrompts',
    api: { getDashboardPrompts: vi.fn().mockResolvedValue({ prompts: [validPrompt, { ...validPrompt, id: '' }] }) },
  },
  {
    label: 'automation', method: 'loadAutomations',
    api: { getDashboardPage: vi.fn().mockResolvedValue({ dags: [validAutomation, { ...validAutomation, id: '' }] }) },
  },
  {
    label: 'MCP tool', method: 'loadMCPTools',
    api: { listToolbridgeTools: vi.fn().mockResolvedValue({ tools: [validTool, { ...validTool, toolName: '' }] }) },
  },
])('rejects the whole $label catalog when one item is malformed', async ({ api, method }) => {
  const service = createSlashCommandCatalogService(api);
  await expect(service[method]('/repo')).rejects.toThrow();
});

it('keeps an automation without executable content visible but disabled', async () => {
  const api = { getDashboardPage: vi.fn().mockResolvedValue({
    dags: [{ id: 'dag-empty', title: 'Metadata only', description: 'No task body', config: {} }],
  }) };
  const service = createSlashCommandCatalogService(api);
  await expect(service.loadAutomations('/repo')).resolves.toEqual([
    expect.objectContaining({
      id: 'automation:dag-empty',
      kind: 'automation',
      disabled: true,
      disabledReason: expect.stringMatching(/\S/u),
    }),
  ]);
});
```

- [ ] **Step 2: Run the focused service test and confirm RED**

```bash
cd frontend-app
npx vitest run src/features/slash-commands/services/slashCommandCatalogService.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the adapters and service do not exist.

- [ ] **Step 3: Implement the four runtime schemas**

Each adapter must call `normalizeSlashCommandItem` from Task 2. Use these exact payload identities:

```js
// Skill payload
{
  capability: {
    kind: 'skill',
    key: `skill:${scope}:${personalType}:${name}:${dir}`,
    name,
    label,
    ref: { name, scope, personalType, path: dir },
  },
}

// Prompt payload
{ promptId: id }

// Automation payload
{ title, content }

// MCP payload
{
  capability: {
    kind: 'mcp_tool',
    key: `mcp_tool:${serverName}:${toolName}`,
    name: toolName,
    label: displayName,
    serverName,
  },
}
```

For automation content, use the first non-empty string in this fixed order:

```js
raw.prompt
raw.command_template
raw.commandTemplate
raw.config?.prompt
raw.config?.command
```

Do not derive task text from descriptions. Missing content produces a disabled item with `disabledReason`.

- [ ] **Step 4: Implement the injected catalog service**

Create `slashCommandCatalogService.js`:

```js
import { adaptAutomationCommands } from '../adapters/automationSlashCommandAdapter.js';
import { adaptMCPToolCommands } from '../adapters/mcpToolSlashCommandAdapter.js';
import { adaptPromptCommands, promptContentFromResponse } from '../adapters/promptSlashCommandAdapter.js';
import { adaptSkillCommands } from '../adapters/skillSlashCommandAdapter.js';
import {
  getDashboardPage,
  getDashboardPrompts,
  getPrompt,
  listToolbridgeTools,
} from '../../../shared/api/backendApi.js';

const defaultApi = Object.freeze({
  getDashboardPage,
  getDashboardPrompts,
  getPrompt,
  listToolbridgeTools,
});

export function createSlashCommandCatalogService(api = defaultApi) {
  return Object.freeze({
    async loadSkills(cwd) {
      return adaptSkillCommands(await api.getDashboardPage({ cwd, page: 'skills' }));
    },
    async loadPrompts(cwd) {
      return adaptPromptCommands(await api.getDashboardPrompts({ cwd }));
    },
    async loadAutomations(cwd) {
      return adaptAutomationCommands(await api.getDashboardPage({ cwd, page: 'dags' }));
    },
    async loadMCPTools(cwd) {
      return adaptMCPToolCommands(await api.listToolbridgeTools({ cwd }));
    },
    async loadPromptContent(cwd, promptId) {
      return promptContentFromResponse(await api.getPrompt({ cwd, id: promptId }));
    },
  });
}

export const slashCommandCatalogService = createSlashCommandCatalogService();
```

`promptContentFromResponse` accepts only the existing prompt body fields (`prompt.content`, `prompt.prompt_text`, `prompt.promptText`) and throws when none is a non-empty string.

- [ ] **Step 5: Run tests and commit**

```bash
cd frontend-app
npx vitest run src/features/slash-commands/model/slashCommandModel.test.js src/features/slash-commands/services/slashCommandCatalogService.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

```bash
git add frontend-app/src/features/slash-commands/adapters/skillSlashCommandAdapter.js frontend-app/src/features/slash-commands/adapters/promptSlashCommandAdapter.js frontend-app/src/features/slash-commands/adapters/automationSlashCommandAdapter.js frontend-app/src/features/slash-commands/adapters/mcpToolSlashCommandAdapter.js frontend-app/src/features/slash-commands/services/slashCommandCatalogService.js frontend-app/src/features/slash-commands/services/slashCommandCatalogService.test.js
git commit -m "feat(chat): adapt slash command data sources"
```

## Task 6: Scoped Composer Capability Snapshots

**Files:**
- Create: `frontend-app/src/entities/client/model/composerCapabilities.js`
- Create: `frontend-app/src/entities/client/model/composerCapabilities.test.js`
- Modify: `frontend-app/src/entities/client/model/composerAttachments.js`
- Modify: `frontend-app/src/entities/client/model/composerAttachments.test.js`
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreRuntimeCore.js`
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreUtils.js`
- Modify: `frontend-app/src/entities/client/model/helpers/threadSelectionActions.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`

- [ ] **Step 1: Write failing capability identity tests**

Create `composerCapabilities.test.js`:

```js
it('deduplicates exact capabilities and restores snapshots as unverified', () => {
  const selected = addComposerCapability([], {
    kind: 'skill', key: 'skill:project::review:/repo/.agents/skills/review',
    name: 'review', label: 'Code Review', availability: 'ready',
    ref: { name: 'review', scope: 'project', personalType: '', path: '/repo/.agents/skills/review' },
  });
  const duplicate = addComposerCapability(selected, selected[0]);
  expect(duplicate).toHaveLength(1);

  const snapshot = snapshotComposerCapabilities(duplicate);
  expect(snapshot[0]).not.toHaveProperty('availability');
  expect(restoreComposerCapabilities(snapshot)).toEqual([
    expect.objectContaining({ key: selected[0].key, availability: 'unverified' }),
  ]);
});
```

Extend `composerAttachments.test.js` to assert a snapshot containing only one capability is not empty and is deeply cloned.

- [ ] **Step 2: Write failing scoped store restore tests**

Extend the existing `keeps composer drafts isolated by selected thread and project cwd` test with this fixture in `resetClientStoreForTests`:

```js
const reviewCapability = {
  kind: 'skill',
  key: 'skill:project::review:/repo/app/.agents/skills/review',
  name: 'review',
  label: 'Code Review',
  availability: 'ready',
  ref: {
    name: 'review', scope: 'project', personalType: '',
    path: '/repo/app/.agents/skills/review',
  },
};

resetClientStoreForTests({
  cwd: '/repo/app',
  projectScopeCwd: '/repo/app',
  activeProject: '/repo/app',
  projects: ['/repo/app', '/repo/other'],
  activeThreadId: 'thread-a',
  threads: [
    { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
    { id: 'thread-b', cwd: '/repo/app', name: 'Thread B', provider: 'codex', status: 'idle' },
  ],
  draft: 'draft for A',
  attachments: [{ path: '/tmp/a.txt', name: 'a.txt' }],
  composerCapabilities: [reviewCapability],
});
```

Add these assertions at the existing switches:

```js
await useClientStore.getState().setActiveThread('thread-b');
expect(useClientStore.getState().composerCapabilities).toEqual([]);

await useClientStore.getState().setActiveThread('thread-a');
expect(useClientStore.getState().composerCapabilities).toEqual([
  expect.objectContaining({ key: reviewCapability.key, availability: 'unverified' }),
]);

await useClientStore.getState().setActiveProjectPath('/repo/other');
expect(useClientStore.getState().composerCapabilities).toEqual([]);

backend.setActiveProject.mockResolvedValueOnce({
  projects: ['/repo/app', '/repo/other'], active: '/repo/app',
});
backend.getSidebarState.mockResolvedValueOnce({
  activeThreadId: '',
  threads: [
    { id: 'thread-a', cwd: '/repo/app', name: 'Thread A', provider: 'codex', status: 'idle' },
    { id: 'thread-b', cwd: '/repo/app', name: 'Thread B', provider: 'codex', status: 'idle' },
  ],
});
await useClientStore.getState().setActiveProjectPath('/repo/app');
await useClientStore.getState().setActiveThread('thread-a');
expect(useClientStore.getState().composerCapabilities).toEqual([
  expect.objectContaining({ key: reviewCapability.key, availability: 'unverified' }),
]);
```

- [ ] **Step 3: Run focused tests and confirm RED**

```bash
cd frontend-app
npx vitest run src/entities/client/model/composerCapabilities.test.js src/entities/client/model/composerAttachments.test.js src/entities/client/model/useClientStore.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because capability state and snapshot support are absent.

- [ ] **Step 4: Implement the capability model**

Use one ordered flat list in store state:

```js
export const CAPABILITY_READY = 'ready';
export const CAPABILITY_UNVERIFIED = 'unverified';
export const CAPABILITY_STALE = 'stale';

export function addComposerCapability(current, raw) {
  const capability = normalizeComposerCapability(raw, CAPABILITY_READY);
  const items = normalizeComposerCapabilities(current, CAPABILITY_READY);
  return items.some((item) => item.key === capability.key) ? items : [...items, capability];
}

export function removeComposerCapability(current, key) {
  return normalizeComposerCapabilities(current).filter((item) => item.key !== String(key).trim());
}

export function snapshotComposerCapabilities(current) {
  return normalizeComposerCapabilities(current).map(({ availability: _, ...identity }) => structuredClone(identity));
}

export function restoreComposerCapabilities(snapshot) {
  return normalizeComposerCapabilities(snapshot, CAPABILITY_UNVERIFIED);
}

export function cloneComposerCapabilities(current) {
  return normalizeComposerCapabilities(current).map((item) => structuredClone(item));
}

export function reconcileComposerCapabilities(current, { kind, status, items = [] }) {
  if (kind !== 'skill' && kind !== 'mcp_tool') {
    throw new Error(`unsupported composer capability kind ${kind}`);
  }
  const availableKeys = new Set(items.map((item) => item?.payload?.capability?.key).filter(Boolean));
  return normalizeComposerCapabilities(current).map((capability) => {
    if (capability.kind !== kind) return capability;
    if (status !== 'success') return { ...capability, availability: CAPABILITY_UNVERIFIED };
    return {
      ...capability,
      availability: availableKeys.has(capability.key) ? CAPABILITY_READY : CAPABILITY_STALE,
    };
  });
}

export function composerCapabilitiesReady(current) {
  return normalizeComposerCapabilities(current).every((item) => item.availability === CAPABILITY_READY);
}
```

`normalizeComposerCapability` must reject unknown `kind`, empty `key/name/label`, incomplete Skill refs, or an MCP tool without `serverName`. It must accept only `ready`, `unverified`, and `stale` availability values.

- [ ] **Step 5: Extend the existing composer snapshot**

Update the JSDoc input and normalized output in `composerAttachments.js`:

```js
/** @typedef {{ draft?: unknown, attachments?: AttachmentObjectInput[], composerCapabilities?: unknown[] }} ComposerDraftSnapshotInput */

export function normalizeComposerDraftSnapshot(value = {}) {
  return {
    draft: optionalTextField(value.draft),
    attachments: cloneComposerAttachments(value.attachments),
    composerCapabilities: snapshotComposerCapabilities(value.composerCapabilities),
  };
}
```

Update `isEmptyComposerDraftSnapshot` to require all three fields to be empty.

- [ ] **Step 6: Save and restore capabilities for every composer scope**

In `clientStoreUtils.js`, initialize:

```js
composerCapabilities: [],
```

In `attachComposerDraftRuntime`, save normalized identity snapshots and restore them through `restoreComposerCapabilities`. In every `threadSelectionActions.js` patch that currently restores `draft` and `attachments`, add:

```js
composerCapabilities: restored.composerCapabilities,
```

In `clearChatSurfaceForCwdSwitch`, restore the target scope using the requested `cwd`:

```js
set((state) => {
  const activeThreadId = preserveActiveThreadId ? normalizeBackendThreadId(state.activeThreadId) : '';
  const targetScope = { ...state, activeProject: cwd, cwd };
  const restored = runtime.restoreComposerDraft(targetScope, activeThreadId);
  return {
    ...clearedChatSurfaceState(state, activeThreadId, cwd),
    draft: restored.draft,
    attachments: restored.attachments,
    composerCapabilities: restored.composerCapabilities,
  };
});
```

- [ ] **Step 7: Run tests and commit**

```bash
cd frontend-app
npx vitest run src/entities/client/model/composerCapabilities.test.js src/entities/client/model/composerAttachments.test.js src/entities/client/model/useClientStore.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

```bash
git add frontend-app/src/entities/client/model/composerCapabilities.js frontend-app/src/entities/client/model/composerCapabilities.test.js frontend-app/src/entities/client/model/composerAttachments.js frontend-app/src/entities/client/model/composerAttachments.test.js frontend-app/src/entities/client/model/helpers/a1/clientStoreRuntimeCore.js frontend-app/src/entities/client/model/helpers/a1/clientStoreUtils.js frontend-app/src/entities/client/model/helpers/threadSelectionActions.js frontend-app/src/entities/client/model/useClientStore.test.js
git commit -m "feat(chat): persist composer capabilities"
```

## Task 7: Structured Send, Success Clear, And Failure Rollback

**Files:**
- Modify: `frontend-app/src/entities/client/model/composerCapabilities.js`
- Modify: `frontend-app/src/entities/client/model/composerCapabilities.test.js`
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreSendModel.js`
- Modify: `frontend-app/src/entities/client/model/helpers/a2Slice/composerSliceActions.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`

- [ ] **Step 1: Write failing serialization and stale tests**

Add to `composerCapabilities.test.js`:

```js
it('serializes ready skills and tools into the turn facade', () => {
  expect(composerCapabilityRequestFields([
    {
      kind: 'skill', key: 'skill:project::review:/repo/skills/review',
      name: 'review', label: 'Code Review', availability: 'ready',
      ref: { name: 'review', scope: 'project', personalType: '', path: '/repo/skills/review' },
    },
    {
      kind: 'mcp_tool', key: 'mcp_tool:lsp:lsp_edit', name: 'lsp_edit',
      label: 'LSP Edit', serverName: 'lsp', availability: 'ready',
    },
  ])).toEqual({
    selectedSkills: ['review'],
    selectedSkillRefs: [{ name: 'review', scope: 'project', path: '/repo/skills/review' }],
    manualSkillSelection: true,
    enabledTools: ['lsp_edit'],
  });
});

it.each(['stale', 'unverified'])('rejects a %s capability before RPC', (availability) => {
  expect(() => composerCapabilityRequestFields([{
    kind: 'mcp_tool', key: 'mcp_tool:lsp:grep', name: 'grep', label: 'grep', serverName: 'lsp', availability,
  }])).toThrow(`composer capability mcp_tool:lsp:grep is ${availability}`);
});
```

- [ ] **Step 2: Write failing store send tests**

Add tests to `useClientStore.test.js` that assert:

```js
expect(backend.startTurn).toHaveBeenCalledWith({
  cwd: '/repo/app',
  threadId: 'thread-1',
  input: [{ type: 'text', text: 'Review this change' }],
  selectedSkills: ['review'],
  selectedSkillRefs: [{ name: 'review', scope: 'project', path: '/repo/app/.agents/skills/review' }],
  manualSkillSelection: true,
  enabledTools: ['lsp_edit'],
});
```

Define this shared fixture and use it in two separate tests:

```js
const boundCapabilities = [
  {
    kind: 'skill',
    key: 'skill:project::review:/repo/app/.agents/skills/review',
    name: 'review',
    label: 'Code Review',
    availability: 'ready',
    ref: {
      name: 'review', scope: 'project', personalType: '',
      path: '/repo/app/.agents/skills/review',
    },
  },
  {
    kind: 'mcp_tool',
    key: 'mcp_tool:lsp:lsp_edit',
    name: 'lsp_edit',
    label: 'LSP Edit',
    serverName: 'lsp',
    availability: 'ready',
  },
];

it('clears text, attachments, and capabilities after a successful send', async () => {
  resetClientStoreForTests({
    cwd: '/repo/app', activeProject: '/repo/app', activeThreadId: 'thread-1',
    draft: 'Review this change',
    attachments: [{ path: '/tmp/change.patch', name: 'change.patch' }],
    composerCapabilities: boundCapabilities,
  });
  backend.startTurn.mockResolvedValueOnce({ ok: true });

  await expect(useClientStore.getState().sendDraft()).resolves.toBe(true);
  expect(useClientStore.getState()).toEqual(expect.objectContaining({
    draft: '', attachments: [], composerCapabilities: [],
  }));
});

it('restores text, attachments, and capabilities after a failed send', async () => {
  resetClientStoreForTests({
    cwd: '/repo/app', activeProject: '/repo/app', activeThreadId: 'thread-1',
    draft: 'Review this change',
    attachments: [{ path: '/tmp/change.patch', name: 'change.patch' }],
    composerCapabilities: boundCapabilities,
  });
  backend.startTurn.mockRejectedValueOnce(new Error('turn/start failed'));

  await expect(useClientStore.getState().sendDraft()).rejects.toThrow('turn/start failed');
  expect(useClientStore.getState()).toEqual(expect.objectContaining({
    draft: 'Review this change',
    attachments: [expect.objectContaining({ path: '/tmp/change.patch' })],
    composerCapabilities: [
      expect.objectContaining({ key: 'skill:project::review:/repo/app/.agents/skills/review' }),
      expect.objectContaining({ key: 'mcp_tool:lsp:lsp_edit' }),
    ],
  }));
});
```

Augment the existing fresh-thread recovery test so both the failed original `startTurn` and successful retry use the same `selectedSkills`, `selectedSkillRefs`, `manualSkillSelection`, and `enabledTools` fields. Add these fail-fast tests:

```js
it.each(['unverified', 'stale'])('blocks %s capabilities before turn/start', async (availability) => {
  resetClientStoreForTests({
    cwd: '/repo/app', activeProject: '/repo/app', activeThreadId: 'thread-1',
    draft: 'Review this change', attachments: [],
    composerCapabilities: [{
      kind: 'mcp_tool', key: 'mcp_tool:lsp:grep', name: 'grep',
      label: 'grep', serverName: 'lsp', availability,
    }],
  });
  await expect(useClientStore.getState().sendDraft()).rejects.toThrow(
    `composer capability mcp_tool:lsp:grep is ${availability}`,
  );
  expect(backend.startTurn).not.toHaveBeenCalled();
});

it('does not send capability-only composer state', async () => {
  resetClientStoreForTests({
    cwd: '/repo/app', activeProject: '/repo/app', activeThreadId: 'thread-1',
    draft: '', attachments: [],
    composerCapabilities: [{
      kind: 'mcp_tool', key: 'mcp_tool:lsp:grep', name: 'grep',
      label: 'grep', serverName: 'lsp', availability: 'ready',
    }],
  });
  await expect(useClientStore.getState().sendDraft()).resolves.toBe(false);
  expect(backend.startTurn).not.toHaveBeenCalled();
});
```

- [ ] **Step 3: Run focused tests and confirm RED**

```bash
cd frontend-app
npx vitest run src/entities/client/model/composerCapabilities.test.js src/entities/client/model/useClientStore.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because send requests still hardcode `manualSkillSelection: false` and do not snapshot capabilities.

- [ ] **Step 4: Serialize only ready capabilities**

Implement `composerCapabilityRequestFields`:

```js
export function composerCapabilityRequestFields(current) {
  const capabilities = normalizeComposerCapabilities(current);
  const unavailable = capabilities.find((item) => item.availability !== CAPABILITY_READY);
  if (unavailable) {
    throw new Error(`composer capability ${unavailable.key} is ${unavailable.availability}`);
  }
  const skills = capabilities.filter((item) => item.kind === 'skill');
  const tools = capabilities.filter((item) => item.kind === 'mcp_tool');
  const payload = { manualSkillSelection: skills.length > 0 };
  if (skills.length > 0) {
    payload.selectedSkills = skills.map((item) => item.name);
    payload.selectedSkillRefs = skills.map((item) => Object.fromEntries(
      Object.entries(item.ref).filter(([, value]) => String(value ?? '').trim() !== ''),
    ));
  }
  if (tools.length > 0) payload.enabledTools = tools.map((item) => item.name);
  return payload;
}
```

- [ ] **Step 5: Extend request, optimistic, and rollback state**

In `createSendDraftRequest`, after confirming `input.length > 0`, add:

```js
const capabilityPayload = composerCapabilityRequestFields(state.composerCapabilities);

return {
  cwd: requestCwd,
  text,
  attachments,
  input,
  capabilityPayload,
  previousDraft: state.draft,
  previousAttachments: state.attachments,
  previousComposerCapabilities: cloneComposerCapabilities(state.composerCapabilities),
  previousActiveThreadId,
  previousThreadId,
  launchIntentId,
  provisionalThreadId,
  optimisticItem: {
    id: `user-${launchIntentId}`,
    role: 'user',
    text,
    attachments,
    time: clockNowISO(),
    done: true,
    optimistic: true,
  },
};
```

Add `composerCapabilities: []` to `optimisticSendDraftState`. Add `composerCapabilities: request.previousComposerCapabilities` to the visible rollback patch and include the capability identities in `saveFailedSendDraftSnapshot`.

- [ ] **Step 6: Pass the request fields and add store actions**

Change `startDraftTurn` to:

```js
return deps.startTurnWithStoppedThreadRecovery({
  cwd: request.cwd,
  threadId,
  input: request.input,
  ...request.capabilityPayload,
});
```

Add these actions to `createComposerActionSet`:

```js
addComposerCapability: (capability) => runtime.set((state) => ({
  composerCapabilities: deps.capability.addComposerCapability(state.composerCapabilities, capability),
})),
removeComposerCapability: (key) => runtime.set((state) => ({
  composerCapabilities: deps.capability.removeComposerCapability(state.composerCapabilities, key),
})),
reconcileComposerCapabilities: (catalog) => runtime.set((state) => ({
  composerCapabilities: deps.capability.reconcileComposerCapabilities(state.composerCapabilities, catalog),
})),
clearComposer: () => runtime.set({ draft: '', attachments: [], composerCapabilities: [] }),
```

Add a `capability` dependency group to `composerActionDeps` using the pure model functions.

- [ ] **Step 7: Run tests and commit**

```bash
cd frontend-app
npx vitest run src/entities/client/model/composerCapabilities.test.js src/entities/client/model/useClientStore.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

```bash
git add frontend-app/src/entities/client/model/composerCapabilities.js frontend-app/src/entities/client/model/composerCapabilities.test.js frontend-app/src/entities/client/model/helpers/a1/clientStoreSendModel.js frontend-app/src/entities/client/model/helpers/a2Slice/composerSliceActions.js frontend-app/src/entities/client/model/useClientStore.test.js
git commit -m "feat(chat): send composer capability bindings"
```

## Task 8: Accessible Catalog Queries And Palette Interaction

**Files:**
- Create: `frontend-app/src/features/slash-commands/hooks/useSlashCommandCatalog.js`
- Create: `frontend-app/src/features/slash-commands/hooks/useSlashCommandPalette.js`
- Create: `frontend-app/src/features/slash-commands/hooks/useSlashCommandPalette.test.jsx`
- Create: `frontend-app/src/features/slash-commands/components/SlashCommandPalette.jsx`
- Create: `frontend-app/src/features/slash-commands/components/SlashCommandPalette.test.jsx`
- Create: `frontend-app/src/features/slash-commands/components/SlashCommandPalette.css`

- [ ] **Step 1: Write failing query and interaction tests**

Render the hook under a test `QueryClientProvider` with retries disabled. Cover:

```js
fireEvent.change(textarea, { target: { value: '/' } });
expect(await screen.findByRole('listbox', { name: '命令与能力' })).toBeVisible();

fireEvent.keyDown(textarea, { key: 'ArrowDown' });
fireEvent.keyDown(textarea, { key: 'Enter' });
expect(store.addComposerCapability).toHaveBeenCalledWith(expect.objectContaining({ kind: 'skill', name: 'review' }));
expect(setDraft).toHaveBeenCalledWith('');

fireEvent.change(textarea, { target: { value: '/prompt' } });
fireEvent.keyDown(textarea, { key: 'Enter' });
await waitFor(() => expect(setDraft).toHaveBeenCalledWith('Loaded prompt body'));

service.loadPromptContent.mockRejectedValueOnce(new Error('prompt unavailable'));
fireEvent.change(textarea, { target: { value: '/prompt' } });
fireEvent.keyDown(textarea, { key: 'Enter' });
await waitFor(() => expect(store.notifyAction).toHaveBeenCalledWith(expect.stringContaining('prompt unavailable'), 'error'));
expect(setDraft).not.toHaveBeenCalledWith('');
```

Add this interaction matrix; each row gets its own test so a failure identifies the exact input contract:

```js
it.each(['Enter', 'Tab'])('selects the active option with %s', async (key) => {
  fireEvent.change(textarea, { target: { value: '/rev' } });
  fireEvent.keyDown(textarea, { key });
  await waitFor(() => expect(store.addComposerCapability).toHaveBeenCalledTimes(1));
});

it('dismisses with Escape without mutating the trigger', async () => {
  fireEvent.change(textarea, { target: { value: '/rev' } });
  expect(await screen.findByRole('listbox')).toBeVisible();
  fireEvent.keyDown(textarea, { key: 'Escape' });
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  expect(setDraft).not.toHaveBeenCalledWith('');
});

it.each([
  { isComposing: true, keyCode: 13 },
  { isComposing: false, keyCode: 229 },
])('does not consume IME key events: %o', async ({ isComposing, keyCode }) => {
  fireEvent.change(textarea, { target: { value: '/rev' } });
  fireEvent.keyDown(textarea, { key: 'Enter', keyCode, isComposing });
  expect(store.addComposerCapability).not.toHaveBeenCalled();
});
```

In `SlashCommandPalette.test.jsx`, hover the last enabled option and assert `setActiveId(last.id)`, click it and assert `selectItem(last)`, then verify ArrowDown from the last enabled option wraps to the first and ArrowUp wraps back. Click a disabled option and assert `selectItem` is not called. Render `categoryStates.skill.status = 'error'` alongside successful built-ins and assert both the Skill error row and builtin option remain visible. Render `cwd = ''` and assert `projectRequired`; render a successful empty catalog and assert `noResults`.

- [ ] **Step 2: Run focused tests and confirm RED**

```bash
cd frontend-app
npx vitest run src/features/slash-commands/hooks/useSlashCommandPalette.test.jsx src/features/slash-commands/components/SlashCommandPalette.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the hooks and component do not exist.

- [ ] **Step 3: Implement independent cached category queries**

In `useSlashCommandCatalog`, create four project queries with these keys:

```js
['slash-command-catalog', 'skill', cwd]
['slash-command-catalog', 'prompt', cwd]
['slash-command-catalog', 'automation', cwd]
['slash-command-catalog', 'mcp_tool', cwd]
```

Each query is enabled only when `Boolean(cwd) && (paletteOpen || hasSelectedCapabilities)`. Built-ins are synchronous. Return:

```js
{
  items,
  categoryStates: {
    builtin: { status: 'success', error: '' },
    skill: { status: 'idle|loading|success|error|disabled', error: '' },
    prompt: { status: 'idle|loading|success|error|disabled', error: '' },
    automation: { status: 'idle|loading|success|error|disabled', error: '' },
    mcp_tool: { status: 'idle|loading|success|error|disabled', error: '' },
  },
}
```

Never combine errors into a single all-or-nothing promise. On each successful Skill/tool query, call `store.reconcileComposerCapabilities` with the authoritative item identities. On loading/error/disabled, reconcile that category to `unverified`; when a previously selected identity is absent from a successful response, mark it `stale`.

- [ ] **Step 4: Implement palette state and selection semantics**

Use `dismissedDraft` so `Escape` closes the current unchanged trigger without immediately reopening it. The hook public API must be:

```js
{
  open,
  listboxId,
  activeId,
  activeOptionId,
  items,
  categoryStates,
  selecting,
  handleKeyDown,
  selectItem,
  setActiveId,
}
```

Selection behavior is fixed:

- `builtin/new`: call `store.newThread()` and keep the current project.
- `builtin/clear`: call `store.clearComposer()`.
- `skill`: call `store.addComposerCapability(payload.capability)`, remove only the slash trigger, and focus textarea.
- `mcp_tool`: same as Skill, using canonical `toolName`.
- `prompt`: await `service.loadPromptContent(cwd, promptId)` before changing the draft; preserve the trigger and notify error on failure.
- `automation`: replace the trigger with `${title}\n\n${content}`; do not execute the DAG.

`handleKeyDown` returns `true` only when it consumed ArrowUp, ArrowDown, Enter, Tab, or Escape. It must return `false` during IME composition.

- [ ] **Step 5: Implement the listbox component**

Render one `role="listbox"` with `role="option"` rows. Category headers use `role="presentation"`; disabled rows set `aria-disabled="true"` and remain navigable. Use Lucide icons for the five categories, and render a restrained `aria-live="polite"` status outside the options.

The component must show each category's loading/error/disabled state even when another category has results.

- [ ] **Step 6: Run tests and commit**

```bash
cd frontend-app
npx vitest run src/features/slash-commands/hooks/useSlashCommandPalette.test.jsx src/features/slash-commands/components/SlashCommandPalette.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

```bash
git add frontend-app/src/features/slash-commands/hooks/useSlashCommandCatalog.js frontend-app/src/features/slash-commands/hooks/useSlashCommandPalette.js frontend-app/src/features/slash-commands/hooks/useSlashCommandPalette.test.jsx frontend-app/src/features/slash-commands/components/SlashCommandPalette.jsx frontend-app/src/features/slash-commands/components/SlashCommandPalette.test.jsx frontend-app/src/features/slash-commands/components/SlashCommandPalette.css
git commit -m "feat(chat): add accessible slash command palette"
```

## Task 9: Composer Integration, Capability Chips, And Responsive Styling

**Files:**
- Create: `frontend-app/src/features/slash-commands/components/ComposerCapabilityChips.jsx`
- Create: `frontend-app/src/features/slash-commands/components/ComposerCapabilityChips.test.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerTextarea.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerTextarea.test.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.css`
- Modify: `frontend-app/src/styles.test.js`

- [ ] **Step 1: Write failing chip and composer integration tests**

Add this chip test (the component must render `role="list"` and `role="listitem"` so the order is observable without CSS selectors):

```jsx
it('renders deduplicated capabilities in insertion order and removes by identity', () => {
  const onRemove = vi.fn();
  const skill = {
    kind: 'skill', key: 'skill:review', name: 'review', label: 'Code Review',
    availability: 'ready', ref: { name: 'review', scope: 'project', path: '/repo/review' },
  };
  const tool = {
    kind: 'mcp_tool', key: 'mcp_tool:lsp:grep', name: 'grep', label: 'LSP grep',
    serverName: 'lsp', availability: 'stale',
  };
  render(<ComposerCapabilityChips
    items={[skill, tool, { ...skill }]}
    copy={{
      removeCapability: '移除能力',
      staleCapability: '能力已失效，请移除后重新选择',
      unverifiedCapability: '能力目录尚未验证，请等待同步',
    }}
    onRemove={onRemove}
  />);

  const chips = screen.getAllByRole('listitem');
  expect(chips).toHaveLength(2);
  expect(chips[0]).toHaveTextContent('Code Review');
  expect(chips[1]).toHaveTextContent('LSP grep');
  expect(chips[1]).toHaveAttribute('title', '能力已失效，请移除后重新选择');
  fireEvent.click(screen.getByRole('button', { name: '移除能力: Code Review' }));
  expect(onRemove).toHaveBeenCalledWith(skill.key);
});
```

Add a second case with `availability: 'unverified'` and assert the unverified reason is exposed through the same visible/tooltip contract.

Extend `ComposerDock.test.jsx`:

```js
fireEvent.change(screen.getByRole('textbox', { name: '输入给 Agent 的内容' }), {
  target: { value: '/' },
});
expect(await screen.findByRole('listbox', { name: '命令与能力' })).toBeVisible();

fireEvent.keyDown(screen.getByRole('textbox'), { key: 'ArrowDown' });
expect(screen.getByRole('textbox')).toHaveAttribute('aria-expanded', 'true');
expect(screen.getByRole('textbox')).toHaveAttribute('aria-activedescendant');

fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
expect(sendMessage).not.toHaveBeenCalled();
expect(store.addComposerCapability).toHaveBeenCalled();
```

Add tests proving normal Enter still sends when the palette is closed, Shift+Enter still inserts a newline, IME Enter is ignored, `/clear` clears text/attachments/capabilities, and capability-only input leaves the send button disabled.

- [ ] **Step 2: Run focused tests and confirm RED**

```bash
cd frontend-app
npx vitest run src/features/slash-commands/components/ComposerCapabilityChips.test.jsx src/pages/chat/composer/ComposerDock.test.jsx src/pages/chat/composer/ComposerTextarea.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because chips and ARIA integration are absent.

- [ ] **Step 3: Add chip rendering and textarea ARIA props**

`ComposerCapabilityChips` receives `items`, `copy`, and `onRemove`. Render a compact list and an icon-only `X` button with:

```jsx
aria-label={`${copy.removeCapability}: ${item.label}`}
title={`${copy.removeCapability}: ${item.label}`}
```

Extend `ComposerTextarea` with `ariaControls`, `ariaExpanded`, and `ariaActiveDescendant`, mapping them to `aria-controls`, `aria-expanded`, and `aria-activedescendant`. Do not change focus ownership.

- [ ] **Step 4: Compose the palette before the existing send handler**

In `ComposerDock`, initialize `useSlashCommandPalette` with `draft`, `setDraft`, `projectPath`, `store`, `textareaRef`, current locale copy, and an optional injected service for tests. Handle keys in this order:

```js
const handleKeyDown = (event) => {
  if (palette.handleKeyDown(event, { isComposing: composer.isComposing() })) return;
  handleSendKeyDown(event);
};
```

Render `SlashCommandPalette` inside `.composer-card` and above the text flow, then `ComposerCapabilityChips` before the textarea. Keep:

```js
const hasComposerInput = Boolean(textValue(draft) || attachments.length > 0);
```

Do not count capability chips as sendable user input. Add `capabilitiesReady` to `canSend` so stale/unverified chips block the button before RPC.

- [ ] **Step 5: Add bounded opaque styling and CSS guards**

Use existing tokens only:

```css
.composer-card {
  position: relative;
}

.slash-command-palette {
  position: absolute;
  inset-inline-start: 16px;
  bottom: calc(100% + 8px);
  width: min(520px, calc(100% - 32px));
  max-height: 360px;
  overflow: hidden;
  z-index: 30;
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: 8px;
  box-shadow: var(--shadow);
}

.slash-command-palette__results {
  max-height: 360px;
  overflow-y: auto;
  overscroll-behavior: contain;
}

@media (max-width: 720px) {
  .slash-command-palette {
    inset-inline: 8px;
    width: auto;
    max-height: min(360px, 52vh);
  }
}
```

Add `styles.test.js` assertions for opaque `background`, `max-height`, internal `overflow-y: auto`, the mobile inset, and absence of a gradient.

- [ ] **Step 6: Run focused tests and commit**

```bash
cd frontend-app
npx vitest run src/features/slash-commands/components/ComposerCapabilityChips.test.jsx src/pages/chat/composer/ComposerDock.test.jsx src/pages/chat/composer/ComposerTextarea.test.jsx src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

```bash
git add frontend-app/src/features/slash-commands/components/ComposerCapabilityChips.jsx frontend-app/src/features/slash-commands/components/ComposerCapabilityChips.test.jsx frontend-app/src/pages/chat/composer/ComposerDock.jsx frontend-app/src/pages/chat/composer/ComposerDock.test.jsx frontend-app/src/pages/chat/composer/ComposerTextarea.jsx frontend-app/src/pages/chat/composer/ComposerTextarea.test.jsx frontend-app/src/pages/chat/composer/ComposerDock.css frontend-app/src/styles.test.js
git commit -m "feat(chat): integrate slash palette with composer"
```

## Task 10: Full Diagnostics, Regression, And Browser Verification

**Files:**
- Verify every file changed in Tasks 1-9.
- Modify only the exact failing implementation/test file if verification exposes a regression.

- [ ] **Step 1: Run required LSP diagnostics**

Use `file(diagnostics)` on:

```text
internal/platform/toolbridge/tool_catalog.go
internal/app/tool_catalog_rpc.go
internal/app/modules.go
frontend-app/src/features/slash-commands/model/slashCommandModel.js
frontend-app/src/features/slash-commands/services/slashCommandCatalogService.js
frontend-app/src/features/slash-commands/hooks/useSlashCommandCatalog.js
frontend-app/src/features/slash-commands/hooks/useSlashCommandPalette.js
frontend-app/src/features/slash-commands/components/SlashCommandPalette.jsx
frontend-app/src/features/slash-commands/components/ComposerCapabilityChips.jsx
frontend-app/src/entities/client/model/composerCapabilities.js
frontend-app/src/entities/client/model/helpers/a1/clientStoreSendModel.js
frontend-app/src/entities/client/model/helpers/a2Slice/composerSliceActions.js
frontend-app/src/pages/chat/composer/ComposerDock.jsx
frontend-app/src/pages/chat/composer/ComposerTextarea.jsx
```

Expected: no Error, Warning, Information, or Hint. Fix every reported item or record a blocker with file, line, rule, and reason.

- [ ] **Step 2: Run Go package regression tests**

```bash
go test ./internal/platform/toolbridge ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Run React Doctor before the final frontend gate**

Invoke the repository `react-doctor` skill, then run its changed-file regression scan:

```bash
cd frontend-app
npx react-doctor@latest --verbose --diff
```

Expected: the health score does not regress. Resolve findings caused by this feature; document unrelated pre-existing findings without modifying unrelated modules.

- [ ] **Step 4: Run the full required frontend gate**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all three commands exit `0`. `npm test` must include guard, contract typecheck, RPC audit, and the complete single-worker Vitest suite.

- [ ] **Step 5: Verify the running UI in the in-app browser**

At `http://127.0.0.1:5175/`, verify all of these at desktop `1440x900` and mobile `390x844`, in both themes and both locales:

1. `/` opens above the composer without moving the timeline.
2. `/rev` filters and Arrow keys cycle; Enter and Tab select; Escape closes without changing text.
3. IME composition does not select or send.
4. Skill selection creates a removable chip and still requires task text.
5. MCP selection displays the canonical tool name and sends the same name.
6. Prompt selection inserts the full body only after successful detail load.
7. Automation inserts editable title/content and does not launch a run.
8. `/new` keeps the active project; `/clear` clears text, attachments, and chips.
9. One failed category remains visibly failed while other categories remain usable.
10. The list scrolls internally at 360px and has an opaque surface in light/dark mode.
11. Project selector, model selector, attachment button, send, interrupt, sidebar, and process timeline still work.

Capture one desktop and one mobile screenshot for the implementation handoff; do not commit screenshots unless the repository already has a designated acceptance-artifact path.

- [ ] **Step 6: Confirm atomic boundaries and untouched user changes**

```bash
git status --short
git diff --check
git log --oneline -10
```

Expected:

- no whitespace errors;
- no `.superpowers/` or `frontend-app/pnpm-lock.yaml` staged;
- the five pre-existing user-modified Go files remain uncommitted and unchanged by this feature;
- feature work is split into the commit subjects listed in Tasks 1-9.

- [ ] **Step 7: Commit only verification-driven fixes, when present**

If verification required a code fix, stage only those exact files and use:

```bash
git commit -m "fix(chat): close slash palette regressions"
```

If no file changed, create no empty verification commit.

## Specification Coverage Map

| Approved requirement | Implemented by |
| --- | --- |
| First non-whitespace slash trigger and dismissal | Tasks 2, 8, 9 |
| Built-in, Skill, prompt, automation, MCP categories | Tasks 2, 3, 4, 5, 8 |
| Canonical MCP names from actual toolbridge surface | Tasks 3, 4, 5 |
| Skill and MCP removable structured chips | Tasks 6, 7, 9 |
| Lazy prompt content and editable automation insertion | Tasks 5, 8 |
| `/new` and `/clear` immediate semantics | Tasks 2, 8, 9 |
| Project/thread-scoped draft persistence | Task 6 |
| Success clear and complete failure rollback | Task 7 |
| Stale/unverified capability blocking | Tasks 6, 7, 8, 9 |
| Independent loading and explicit partial errors | Tasks 5, 8 |
| ARIA listbox, keyboard, mouse, and IME behavior | Tasks 8, 9 |
| Opaque bounded responsive light/dark UI | Task 9 |
| LSP, Go, frontend, contract, browser verification | Tasks 1, 10 |
