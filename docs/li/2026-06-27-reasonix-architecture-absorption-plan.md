# Reasonix-Inspired Architecture Absorption Implementation Plan v2

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Absorb the useful parts of Reasonix's architecture into super-agent-v3 without replacing the existing Fx module graph, owner-module boundaries, or the standalone `cmd/mcp-orch` runtime kernel.

**Architecture:** Keep super-agent-v3's current Fx module assembly. Absorb Reasonix's ideas as narrow owner-local ports, typed wire contracts, prompt prefix observability, desktop dependency guards, and explicit MCP namespace semantics. Do not introduce a global Reasonix-style `control.Controller`.

**Tech Stack:** Go 1.25.7, Uber Fx, jrpc2, Wails, React/Vite `frontend-app` JavaScript modules, SQLite/sqlc, existing `internal/archtest` guard framework.

**Verification Surface:** `internal/contract`, `internal/app`, `internal/module/thread`, `internal/module/prompt`, `internal/platform/eventsurface`, `internal/platform/rpc`, `internal/platform/toolbridge`, `internal/provider/codexapp`, `internal/provider/claudecli`, `internal/ui/wails`, `internal/archtest`, `frontend-app`.

**Execution Status 2026-06-28:** This file is the implementation plan, not the completion ledger. Current remaining-state tracking lives in `docs/li/reasonix-remaining-execution-closure-2026-06-28.md`. Phase 0/1 documentation preflight is closed; Phase 2 and later remain mixed until their lane-owned validation and controller F's final verification are recorded. ADR 0003 records MCP lifecycle ownership and gates only; it is not evidence that per-tool lifecycle filtering is active.

---

## 1. Cross-Agent Adjudication Result

Three read-only review agents checked the previous plan:

- Architecture review: direction is valid, but the implementation details need tightening.
- Interface/executability review: previous Task 2, Task 4, Task 5, Task 6, and Task 8 contained code or paths that do not match the current repository.
- Risk/verification review: the plan was too broad to execute in one wave and lacked clean-worktree, rollback, and final verification gates.

Decision:

- **Accept:** Reasonix's port decomposition, stable event wire, prefix-shape observability, desktop isolation, and MCP namespace ownership are useful design inputs.
- **Reject:** executing the previous document as written.
- **Required v2 posture:** split into phases, run read-only spikes before risky production changes, preserve existing compatibility layers, and require parity tests before routing production traffic through new ports.

## 2. Current Architecture Facts

- `internal/app/modules.go` is the root Fx composition point for the desktop and background app graph.
- `cmd/mcp-orch` owns DAG, orchestration, cron, and toolbridge runtime. The desktop app must not embed this runtime module.
- `internal/module/thread` owns thread lifecycle RPC such as `thread/start`, `thread/resume`, `thread/fork`, `thread/messages`, and thread config commands.
- `internal/module/turn` owns turn execution and approval callbacks. Its RPC helpers perform session resolution, pending-launch spawning, capability resolution, and runtime config hydration.
- `internal/platform/eventsurface` already has `Notification` and `ExpandNotifications`; RPC push and Wails bridge rely on compatibility expansion.
- `internal/module/prompt` already returns `StartAssembly` with `ResolvedSections`, `Boundary`, `Snapshot`, `SuppressedTools`, `UserContext`, and `SystemContext`.
- `internal/platform/toolbridge` already owns Codex dynamic tool surface construction and MCP tool aliasing.
- `frontend-app/src/shared/api/backendApi.js` already exposes guarded facades such as `startThread`, `startTurn`, `interruptTurn`, and `getThreadMessages`; UI code must not bypass those guards by calling the raw bridge unless there is a separate contract change.

## 3. Designs Worth Absorbing

1. **Session-facing port decomposition:** useful only if the new ports are typed and do not lose existing `thread/start` fields.
2. **Single event wire surface:** useful only if it evolves the existing `Notification` and `ExpandNotifications` compatibility layer.
3. **Prompt prefix stability:** useful only if the prefix shape is derived from the existing prompt assembly facts instead of inventing a parallel field that is not present in current code.
4. **Desktop dependency isolation:** useful only with the right guard scope; `internal/app` is allowed to assemble desktop/Wails dependencies.
5. **MCP namespace lifecycle:** useful only after the state owner and storage source are defined.

## 4. Designs Not To Copy

- Do not add a global `control.Controller`.
- Do not replace Fx modules with process-global registries.
- Do not fold `cmd/mcp-orch` into the desktop app.
- Do not move React UI work into `cmd/agent-terminal/frontend`.
- Do not add raw frontend API calls that bypass `backendApi.js` payload validation.

## 5. Phase Plan

| Phase | Purpose | Exit Gate |
| --- | --- | --- |
| Phase 0 | Create ADR and execution isolation gate. | ADR records acceptance gates, rollback, and clean-worktree requirement. |
| Phase 1 | Run read-only spikes for event methods, prompt prefix shape, and MCP lifecycle. | Spike docs identify exact source of truth and owner for each risky area. |
| Phase 2 | Add typed first-slice session lifecycle/read ports. | Start-request parity tests prove no current wire-critical fields are dropped. |
| Phase 3 | Stabilize event wire by evolving existing `Notification` and `ExpandNotifications`. | Backend and frontend method lists match the same golden method set. |
| Phase 4 | Add prompt prefix shape telemetry from existing prompt assembly facts. | Telemetry uses `ResolvedSections`, `Boundary`, `Snapshot`, and `SuppressedTools`; provider wire compatibility tests pass. |
| Phase 5 | Centralize MCP namespace helper; keep per-tool lifecycle filtering out of production code. | Namespace tests cover canonical/alias behavior; no lifecycle filtering is added before a separate state-owner decision. |
| Phase 6 | Add desktop dependency guard with the correct scope. | Guard excludes allowed desktop assembly packages and catches module/provider/platform leaks. |
| Phase 7 | Add frontend session API facade over existing guarded backend API. | Frontend tests prove the facade uses `backendApi.js` exports, not raw bridge calls. |

## 6. Planned File Structure

| File | Action | Responsibility |
| --- | --- | --- |
| `docs/adr/0002-session-ports-and-prefix-stability.md` | Create | Record architectural decision, acceptance gates, rollback, and phase order. |
| `docs/li/reasonix-absorption-spikes/event-wire-methods.md` | Create | Read-only event method inventory and parity decision. |
| `docs/li/reasonix-absorption-spikes/prompt-prefix-shape.md` | Create | Read-only prefix shape source decision. |
| `docs/li/reasonix-absorption-spikes/mcp-tool-lifecycle.md` | Create | Read-only MCP lifecycle state ownership decision. |
| `internal/contract/session_ports.go` | Create | Define typed first-slice session lifecycle/read ports. |
| `internal/contract/session_ports_test.go` | Create | Guard field parity for wire-critical start fields. |
| `internal/module/thread/session_lifecycle_port.go` | Create | Adapt thread lifecycle to the contract-facing lifecycle port. |
| `internal/module/thread/session_status_port.go` | Create | Adapt thread list/messages to typed read port DTOs. |
| `internal/module/thread/session_ports_test.go` | Create | Guard mapping parity between `thread.StartRequest` and `contract.SessionStartRequest`. |
| `internal/app/session_ports.go` | Create | Compose first-slice session ports from owner-local adapters. |
| `internal/app/modules.go` | Modify | Provide session port bundle from the Fx graph. |
| `internal/app/session_ports_test.go` | Create | Compile-check `sessionPorts` implements `contract.SessionPorts`. |
| `internal/platform/eventsurface/methods.go` | Create | Expose the complete backend wire method list. |
| `internal/platform/eventsurface/methods_test.go` | Create | Compare backend method list to frontend golden. |
| `frontend-app/src/shared/api/eventWireMethods.js` | Create | Frontend-owned golden list of event wire methods. |
| `frontend-app/src/shared/api/eventWire.js` | Create | Runtime parser for event wire notifications. |
| `frontend-app/src/shared/api/eventWire.test.js` | Create | Lock frontend parser and method-list behavior. |
| `internal/dto/provider/session.go` | Modify | Add `PrefixShape` to `StartAssembly`, not to `TurnRequest`. |
| `internal/contract/prompt.go` | Modify | Alias provider `PrefixShape` for cross-module usage. |
| `internal/module/prompt/prefix_shape.go` | Create | Build prefix shape from start assembly facts. |
| `internal/module/prompt/prefix_shape_test.go` | Create | Lock stable hash and churn reason behavior. |
| `internal/module/prompt/assembler.go` | Modify | Populate `StartAssembly.PrefixShape`. |
| `internal/provider/codexapp/driver.go` | Modify | Log prefix shape at provider session start, not at per-turn start. |
| `internal/provider/codexapp/driver_session_test.go` | Modify | Guard provider start compatibility with prefix shape present. |
| `internal/platform/toolbridge/mcp_namespace.go` | Create | Centralize MCP wrapped-name helpers. |
| `internal/platform/toolbridge/mcp_namespace_test.go` | Create | Lock namespace parsing, alias, and collision behavior. |
| `internal/platform/toolbridge/handler_peer_decode_helpers.go` | Modify | Replace local wrapper helpers with namespace API. |
| `internal/platform/toolbridge/handler_host_tools.go` | Modify | Replace host-tool wrapped-name parsing with namespace API. |
| `internal/archtest/desktop_dependency_test.go` | Create | Prevent Wails imports from leaking into `internal/module`, `internal/provider`, and non-UI `internal/platform`. |
| `frontend-app/src/shared/api/sessionApi.js` | Create | Small facade over existing guarded `backendApi.js` functions. |
| `frontend-app/src/shared/api/sessionApi.test.js` | Create | Prove the facade does not import or call raw bridge API. |

## 7. Implementation Tasks

### Task 0: Create Execution Gate ADR

**Files:**
- Create: `docs/adr/0002-session-ports-and-prefix-stability.md`

- [ ] **Step 1: Add ADR**

```markdown
# ADR 0002: Session Ports And Prefix Stability

日期：2026-06-27

状态：Proposed

## 背景

Reasonix 的架构里有值得吸收的边界设计：session-facing ports、稳定 event wire、prompt prefix 观测、desktop dependency 隔离、MCP tool namespace 生命周期。super-agent-v3 不能照搬 Reasonix 的全局 Controller，因为当前系统以 Fx modules、owner modules、独立 mcp-orch runtime 为边界。

## 决策

1. 只吸收边界模式，不引入全局 Controller。
2. session ports 第一阶段只覆盖 lifecycle/read surface，并要求 `thread/start` 字段无损映射。
3. event wire 必须演进现有 `eventsurface.Notification` 和 `ExpandNotifications`。
4. prefix shape 必须来自当前 prompt assembly 事实源：`Boundary`、`ResolvedSections`、`Snapshot`、`SuppressedTools`。
5. MCP lifecycle 只有在状态 owner 和来源明确后才能进入生产 filtering。
6. desktop dependency guard 限定 `internal/module`、`internal/provider`、`internal/platform` 非 UI 子包；`internal/app` 允许装配 Wails。

## 执行门槛

- 在干净隔离 worktree 上执行，或明确列出当前 dirty 文件并只 stage 本计划文件。
- Phase 1 三份 spike 文档完成前，不允许修改 prompt/provider/toolbridge/event production code。
- 每个 phase 结束必须运行本计划列出的验证命令。
- 如果 `internal/archtest/baseline.json` 变化，必须在报告中解释 diff。

## 回滚

- 每个 phase 独立提交。
- 若 session port 接入后 parity test 失败，回滚 Phase 2，不继续 Phase 3。
- 若 event method golden 与 frontend 消费不一致，保留现有 `ExpandNotifications` 行为并停止迁移。
```

- [ ] **Step 2: Verify ADR diff**

Run:

```bash
git diff -- docs/adr/0002-session-ports-and-prefix-stability.md
```

Expected: diff contains only the ADR.

### Task 1: Add Read-Only Spike Documents

**Files:**
- Create: `docs/li/reasonix-absorption-spikes/event-wire-methods.md`
- Create: `docs/li/reasonix-absorption-spikes/prompt-prefix-shape.md`
- Create: `docs/li/reasonix-absorption-spikes/mcp-tool-lifecycle.md`

- [ ] **Step 1: Inventory event wire methods**

Run:

```bash
rg -n 'Method[A-Za-z0-9_]+\\s*=\\s*"' internal/platform/eventsurface -g '*.go'
rg -n 'item/agentMessage/delta|item/completed|turn/stalled|approval/resolved|cron/job/runStateChanged|thread/messages/page' frontend-app/src -g '*.js'
```

Write `docs/li/reasonix-absorption-spikes/event-wire-methods.md`:

```markdown
# Event Wire Methods Spike

## Source Of Truth

- Backend typed constants live in `internal/platform/eventsurface/bind.go`; legacy compatibility methods such as `ui/thread/changed` and `ui/sidebar/changed` live in `internal/platform/eventsurface/legacy.go`.
- Compatibility expansion lives in `internal/platform/eventsurface/legacy.go`, including `workspace/run/` source-event handling.
- RPC push uses `eventsurface.ExpandNotifications`.
- Wails bridge uses `eventsurface.ExpandNotifications`.

## Required Method Set

The implementation must inventory every `Method*` constant from the whole `internal/platform/eventsurface` package, including bind constants and legacy compatibility methods. The raw provider allowlist is separate from compatibility/source-event prefixes; do not silently expand raw provider visibility while stabilizing frontend parsing.

## Decision

Create `internal/platform/eventsurface/methods.go` and `frontend-app/src/shared/api/eventWireMethods.js` from the same explicit list. Backend tests read the frontend list and fail if the two sets diverge.
```

- [ ] **Step 2: Inventory prompt prefix facts**

Run:

```bash
rg -n 'type StartAssembly|type PromptAssemblySnapshot|ResolvedSections|SuppressedTools|Boundary' internal/dto/provider/session.go internal/module/prompt internal/contract -g '*.go'
```

Write `docs/li/reasonix-absorption-spikes/prompt-prefix-shape.md`:

```markdown
# Prompt Prefix Shape Spike

## Existing Facts

- `StartAssembly` already contains `Boundary`, `ResolvedSections`, `Snapshot`, `SuppressedTools`, `UserContext`, and `SystemContext`.
- `PromptAssemblySnapshot` already contains `Hash` and `SectionSnapshot`.
- No prefix-shape field exists on the current start assembly before this plan.

## Decision

Add `PrefixShape` to `internal/dto/provider.StartAssembly`. Build it in `internal/module/prompt` from base instructions, developer instructions, boundary, resolved sections, and suppressed tool names. Provider logs must include only shape metadata and hash, not prompt contents.
```

- [ ] **Step 3: Inventory MCP lifecycle facts**

Run:

```bash
rg -n 'enabled|Enabled|MCPTool|ListTools|addMCPToolsToSurface|wrappedMCPToolName|mcpWrappedToolName' internal/contract internal/dto/mcp internal/module/mcp_server internal/platform/toolbridge -g '*.go'
```

Write `docs/li/reasonix-absorption-spikes/mcp-tool-lifecycle.md`:

```markdown
# MCP Tool Lifecycle Spike

## Existing Facts

- MCP server config has `enabled`.
- MCP tool DTO has name, description, input schema, and output schema.
- Toolbridge currently handles canonical names and aliases, not per-tool suspend/remove state.

## Decision

Phase 5 may centralize namespace helpers immediately. Per-tool lifecycle filtering must not be added until a later schema decision defines state owner, storage, and migration. For this plan, lifecycle absorption is limited to naming ownership and compatibility tests.
```

- [ ] **Step 4: Verify spike docs**

Run:

```bash
git diff -- docs/li/reasonix-absorption-spikes
```

Expected: three docs exist and contain no production code changes.

### Task 2: Define Typed Session Ports

**Files:**
- Create: `internal/contract/session_ports.go`
- Create: `internal/contract/session_ports_test.go`

- [ ] **Step 1: Add typed first-slice port definitions**

```go
package contract

import (
	"context"
	"encoding/json"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// SessionStartRequest 是内部 session port 的稳定启动 DTO。
// 它覆盖 thread.StartRequest 中 adapter 必须透传的启动字段；PromptAssemblyRef、PromptVersionID、
// AgentTitle 和 PromptKeyStale 仍由 thread service 内部填充，不从该 port 入口传入。
type SessionStartRequest struct {
	Provider, AgentID, ParentAgentID, AgentType, AgentMemoryScope string
	CWD, Model, ModelProvider, Name                               string
	Prompt, BaseInstructions                                      string
	BaseInstructionBlocks                                         []BaseInstructionBlock
	DeveloperInstructions                                         string
	ApprovalPolicy                                                string
	Sandbox                                                       json.RawMessage
	Summary                                                       string
	Effort                                                        string
	Personality                                                   string
	Language                                                      string
	GitRoot                                                       string
	IsWorktree                                                    bool
	ToolSurfaceMode                                               string
	EnabledTools                                                  []string
	AdditionalWorkingDirectories                                  []string
	MCPSnapshot                                                   MCPSnapshot
	SessionFlags                                                  map[string]bool
	Config                                                        map[string]any
	LaunchSkillNames                                              []string
	LaunchSkillRefs                                               []dto.SkillRef
	ForceLaunchSkills                                             bool
	AgentKey                                                      string
	PromptKey                                                     string
	OwnerThreadID, LaunchIntentID                                 string
	DeferSpawn                                                    bool
}

// SessionStartResult 是内部 session port 的稳定启动结果。
type SessionStartResult struct {
	ThreadID        string `json:"thread_id"`
	AgentID         string `json:"agent_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Status          string `json:"status,omitempty"`
	Model           string `json:"model,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ModelProvider   string `json:"modelProvider,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	ApprovalPolicy  string `json:"approvalPolicy,omitempty"`
	AgentKey        string `json:"agent_key,omitempty"`
	AgentTitle      string `json:"agent_title,omitempty"`
	PromptKey       string `json:"prompt_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
	PromptKeyStale  bool   `json:"prompt_key_stale,omitempty"`
	PendingLaunch   bool   `json:"pending_launch,omitempty"`
}

// SessionThreadRef 是 session status 端口暴露给 UI/RPC 的线程摘要。
type SessionThreadRef struct {
	ID               string `json:"id"`
	Name             string `json:"name,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	Status           string `json:"status,omitempty"`
	CreatedAt        int64  `json:"created_at,omitempty"`
	UpdatedAt        int64  `json:"updated_at,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ProviderThreadID string `json:"providerThreadId,omitempty"`
	SessionID        string `json:"sessionId,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	Model            string `json:"model,omitempty"`
	Port             int    `json:"port,omitempty"`
}

type SessionLifecyclePort interface {
	StartSession(ctx context.Context, req SessionStartRequest) (SessionStartResult, error)
	ResumeSession(ctx context.Context, threadID string) (SessionStartResult, error)
	ForkSession(ctx context.Context, threadID string) (SessionStartResult, error)
	ArchiveSession(ctx context.Context, threadID string) error
}

type SessionStatusPort interface {
	ListSessions(ctx context.Context) ([]SessionThreadRef, error)
	ReadMessages(ctx context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error)
}

type SessionPorts interface {
	SessionLifecyclePort
	SessionStatusPort
}
```

**Wire boundary note:** `SessionStartRequest` is an internal adapter DTO only. It does not replace JSON-RPC `thread/start` wire decoding in `internal/module/thread/rpc_types.go`. The existing `startParams` owns JSON tags, snake/camel compatibility aliases, custom `UnmarshalJSON`, and unknown-field rejection. If a later phase migrates the RPC wire type, that change must first port the alias map, rejection behavior, and compatibility tests before switching handlers.

- [ ] **Step 2: Add contract DTO guard for intentionally internal fields**

```go
package contract

import (
	"reflect"
	"testing"
)

func TestSessionStartRequestDoesNotExposeThreadInternalFields(t *testing.T) {
	typ := reflect.TypeOf(SessionStartRequest{})
	for _, name := range []string{
		"PromptAssemblyRef",
		"PromptVersionID",
		"AgentTitle",
		"PromptKeyStale",
	} {
		if _, ok := typ.FieldByName(name); ok {
			t.Fatalf("SessionStartRequest must not expose thread-internal field %s", name)
		}
	}
}
```

The cross-type field drift guard belongs in `internal/module/thread/session_ports_test.go`, where the test can import `internal/contract` and reflect over the real `thread.StartRequest` without reversing the `contract -> module` dependency direction.

- [ ] **Step 3: Verify contract package**

Run:

```bash
./scripts/test_with_guard.sh internal/contract/session_ports.go
./scripts/test_with_guard.sh internal/contract/session_ports_test.go
./scripts/test_with_guard.sh ./internal/contract -count=1
```

Expected: package passes.

### Task 3: Adapt Thread Service To Session Ports

**Files:**
- Create: `internal/module/thread/session_lifecycle_port.go`
- Create: `internal/module/thread/session_status_port.go`
- Create: `internal/module/thread/session_ports_test.go`
- Create: `internal/app/session_ports.go`
- Modify: `internal/app/modules.go`
- Create: `internal/app/session_ports_test.go`

- [ ] **Step 1: Add thread lifecycle adapter**

```go
package thread

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

type sessionLifecyclePort struct {
	service Service
}

func NewSessionLifecyclePort(service Service) contract.SessionLifecyclePort {
	return sessionLifecyclePort{service: service}
}

func (p sessionLifecyclePort) StartSession(ctx context.Context, req contract.SessionStartRequest) (contract.SessionStartResult, error) {
	got, err := p.service.Start(ctx, startRequestFromSession(req))
	if err != nil {
		return contract.SessionStartResult{}, err
	}
	return sessionStartResultFromStart(got), nil
}

func (p sessionLifecyclePort) ResumeSession(ctx context.Context, threadID string) (contract.SessionStartResult, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return contract.SessionStartResult{}, fmt.Errorf("session lifecycle: thread id is required")
	}
	got, err := p.service.Resume(ctx, ResumeRequest{ThreadID: threadID})
	if err != nil {
		return contract.SessionStartResult{}, err
	}
	return sessionStartResultFromResume(got), nil
}

func (p sessionLifecyclePort) ForkSession(ctx context.Context, threadID string) (contract.SessionStartResult, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return contract.SessionStartResult{}, fmt.Errorf("session lifecycle: fork source thread id is required")
	}
	got, err := p.service.Fork(ctx, threadID)
	if err != nil {
		return contract.SessionStartResult{}, err
	}
	return contract.SessionStartResult{ThreadID: got.NewThreadID}, nil
}

func (p sessionLifecyclePort) ArchiveSession(ctx context.Context, threadID string) error {
	return p.service.Archive(ctx, strings.TrimSpace(threadID))
}
```

- [ ] **Step 2: Add mapping helpers with explicit fields**

```go
func startRequestFromSession(req contract.SessionStartRequest) StartRequest {
	return StartRequest{
		Provider:                     req.Provider,
		AgentID:                      req.AgentID,
		ParentAgentID:                req.ParentAgentID,
		AgentType:                    req.AgentType,
		AgentMemoryScope:             req.AgentMemoryScope,
		CWD:                          req.CWD,
		Model:                        req.Model,
		ModelProvider:                req.ModelProvider,
		Name:                         req.Name,
		Prompt:                       req.Prompt,
		BaseInstructions:             req.BaseInstructions,
		BaseInstructionBlocks:        cloneSessionBaseInstructionBlocks(req.BaseInstructionBlocks),
		DeveloperInstructions:        req.DeveloperInstructions,
		ApprovalPolicy:               req.ApprovalPolicy,
		Sandbox:                      clone.RawMessage(req.Sandbox),
		Summary:                      req.Summary,
		Effort:                       req.Effort,
		Personality:                  req.Personality,
		Language:                     req.Language,
		GitRoot:                      req.GitRoot,
		IsWorktree:                   req.IsWorktree,
		ToolSurfaceMode:              req.ToolSurfaceMode,
		EnabledTools:                 clone.Strings(req.EnabledTools),
		AdditionalWorkingDirectories: clone.Strings(req.AdditionalWorkingDirectories),
		MCPSnapshot:                  cloneSessionMCPSnapshot(req.MCPSnapshot),
		SessionFlags:                 cloneSessionBoolMap(req.SessionFlags),
		Config:                       clone.RuntimeConfigMap(req.Config),
		LaunchSkillNames:             clone.Strings(req.LaunchSkillNames),
		LaunchSkillRefs:              append([]dto.SkillRef(nil), req.LaunchSkillRefs...),
		ForceLaunchSkills:            req.ForceLaunchSkills,
		AgentKey:                     req.AgentKey,
		PromptKey:                    req.PromptKey,
		OwnerThreadID:                req.OwnerThreadID,
		LaunchIntentID:               req.LaunchIntentID,
		DeferSpawn:                   req.DeferSpawn,
	}
}

func sessionStartResultFromStart(got StartResult) contract.SessionStartResult {
	return contract.SessionStartResult{
		ThreadID:        got.ThreadID,
		AgentID:         got.AgentID,
		SessionID:       got.SessionID,
		Status:          got.Status,
		Model:           got.Model,
		Provider:        got.Provider,
		ModelProvider:   got.ModelProvider,
		CWD:             got.CWD,
		ApprovalPolicy:  got.ApprovalPolicy,
		AgentKey:        got.AgentKey,
		AgentTitle:      got.AgentTitle,
		PromptKey:       got.PromptKey,
		PromptVersionID: got.PromptVersionID,
		PromptKeyStale:  got.PromptKeyStale,
		PendingLaunch:   got.PendingLaunch,
	}
}

func sessionStartResultFromResume(got ResumeResult) contract.SessionStartResult {
	return contract.SessionStartResult{
		ThreadID:  got.ThreadID,
		SessionID: got.SessionID,
		Status:    got.Status,
		Model:     got.Model,
		CWD:       got.CWD,
	}
}

func cloneSessionBaseInstructionBlocks(in []contract.BaseInstructionBlock) []contract.BaseInstructionBlock {
	if len(in) == 0 {
		return nil
	}
	out := append([]contract.BaseInstructionBlock(nil), in...)
	for index := range out {
		out[index].EnableWhen = append([]byte(nil), out[index].EnableWhen...)
	}
	return out
}

func cloneSessionMCPSnapshot(in contract.MCPSnapshot) contract.MCPSnapshot {
	return contract.MCPSnapshot{
		Servers:                  clone.Strings(in.Servers),
		Tools:                    clone.Strings(in.Tools),
		Instructions:             clone.StringMap(in.Instructions),
		ServerConfigs:            cloneSessionMCPServerConfigs(in.ServerConfigs),
		InstructionsDeltaEnabled: in.InstructionsDeltaEnabled,
		InstructionAttachments:   append([]contract.MCPAttachmentRef(nil), in.InstructionAttachments...),
	}
}

func cloneSessionMCPServerConfigs(in map[string]contract.MCPServerConfig) map[string]contract.MCPServerConfig {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]contract.MCPServerConfig, len(in))
	for name, cfg := range in {
		out[name] = contract.MCPServerConfig{
			Transport: cfg.Transport,
			URL:       cfg.URL,
			Headers:   clone.StringMap(cfg.Headers),
			Command:   cfg.Command,
			Args:      clone.Strings(cfg.Args),
			Env:       clone.StringMap(cfg.Env),
			Enabled:   cloneSessionBoolPtr(cfg.Enabled),
		}
	}
	return out
}

func cloneSessionBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneSessionBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

```

Add imports for `dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"` and `github.com/anthropic-ai/super-agent-v3/internal/util/clone` in this file. `Config` must use `clone.RuntimeConfigMap` so nested runtime maps/slices do not alias the caller request.

- [ ] **Step 3: Add StartRequest field drift guard**

```go
func TestSessionStartRequestCoversThreadStartRequestFields(t *testing.T) {
	startType := reflect.TypeOf(StartRequest{})
	sessionType := reflect.TypeOf(contract.SessionStartRequest{})
	exemptions := map[string]string{
		"PromptAssemblyRef": "injected by thread service before prompt assembly",
		"PromptVersionID":  "materialized by thread service after prompt routing",
		"AgentTitle":       "derived from prompt routing for UI metadata",
		"PromptKeyStale":   "derived by thread service from prompt routing result",
	}
	for index := 0; index < startType.NumField(); index++ {
		field := startType.Field(index)
		if _, ok := sessionType.FieldByName(field.Name); ok {
			continue
		}
		reason := strings.TrimSpace(exemptions[field.Name])
		if reason == "" {
			t.Fatalf("contract.SessionStartRequest missing StartRequest field %s; add a mapping or a documented exemption", field.Name)
		}
	}
	for field, reason := range exemptions {
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("empty SessionStartRequest field exemption for %s", field)
		}
		if _, ok := startType.FieldByName(field); !ok {
			t.Fatalf("SessionStartRequest exemption %s no longer exists on StartRequest", field)
		}
	}
}
```

This test must live in package `thread` and import `reflect`, `strings`, and `internal/contract`. It is the authoritative field guard for `StartRequest -> SessionStartRequest`; the contract package guard above only locks the intentional non-wire fields.

- [ ] **Step 4: Add mapping parity test**

```go
func TestStartRequestFromSessionPreservesAllWireCriticalFields(t *testing.T) {
	sandbox := json.RawMessage(`{"type":"workspace-write"}`)
	mcpEnabled := true
	req := contract.SessionStartRequest{
		Provider:             "codex",
		AgentID:              "agent-1",
		ParentAgentID:        "parent-1",
		AgentType:            "worker",
		AgentMemoryScope:     "project",
		CWD:                  "/repo",
		Model:                "gpt-5",
		ModelProvider:        "openai",
		Name:                 "Plan",
		Prompt:               "Prompt",
		BaseInstructions:     "base",
		BaseInstructionBlocks: []contract.BaseInstructionBlock{{Key: "base", Region: contract.PromptRegionStatic, Ordinal: 1, Body: "body", EnableWhen: []byte(`{"provider":"codex"}`)}},
		DeveloperInstructions: "dev",
		ApprovalPolicy:        "on-request",
		Sandbox:               sandbox,
		Summary:               "summary",
		Effort:                "high",
		Personality:           "steady",
		Language:              "zh",
		GitRoot:               "/repo",
		IsWorktree:            true,
		ToolSurfaceMode:       "chat",
		EnabledTools:          []string{"grep", "edit"},
		AdditionalWorkingDirectories: []string{
			"/repo/extra",
		},
		MCPSnapshot: contract.MCPSnapshot{
			Servers:                  []string{"server-a"},
			Tools:                    []string{"tool-a"},
			Instructions:             map[string]string{"server-a": "use server-a"},
			ServerConfigs: map[string]contract.MCPServerConfig{
				"server-a": {
					Transport: "stdio",
					Command:   "node",
					Args:      []string{"server.js"},
					Headers:   map[string]string{"Authorization": "x"},
					Env:       map[string]string{"TOKEN": "x"},
					Enabled:   &mcpEnabled,
				},
			},
			InstructionsDeltaEnabled: true,
			InstructionAttachments:   []contract.MCPAttachmentRef{{Name: "docs", URI: "file:///docs"}},
		},
		SessionFlags:      map[string]bool{"simple": true},
		Config:            map[string]any{"sandbox": map[string]any{"mode": "workspace-write"}, "features": []any{"mcp"}},
		LaunchSkillNames:  []string{"review"},
		LaunchSkillRefs:   []dto.SkillRef{{Name: "review", Key: "skill-review", Scope: "project", Source: dto.SkillSourceManual}},
		ForceLaunchSkills: true,
		AgentKey:          "agent-key",
		PromptKey:         "prompt-key",
		OwnerThreadID:     "owner-thread",
		LaunchIntentID:    "intent-1",
		DeferSpawn:        true,
	}
	got := startRequestFromSession(req)

	if got.Provider != req.Provider || got.AgentID != req.AgentID || got.ParentAgentID != req.ParentAgentID || got.AgentType != req.AgentType || got.AgentMemoryScope != req.AgentMemoryScope {
		t.Fatalf("identity fields not preserved: %#v", got)
	}
	if got.CWD != req.CWD || got.Model != req.Model || got.ModelProvider != req.ModelProvider || got.Name != req.Name || got.Prompt != req.Prompt {
		t.Fatalf("core fields not preserved: %#v", got)
	}
	if got.BaseInstructions != req.BaseInstructions || got.DeveloperInstructions != req.DeveloperInstructions || got.ApprovalPolicy != req.ApprovalPolicy {
		t.Fatalf("instruction fields not preserved: %#v", got)
	}
	if !reflect.DeepEqual(got.BaseInstructionBlocks, req.BaseInstructionBlocks) || string(got.Sandbox) != string(sandbox) {
		t.Fatalf("base blocks or sandbox not preserved: %#v", got)
	}
	if got.Summary != req.Summary || got.Effort != req.Effort || got.Personality != req.Personality || got.Language != req.Language {
		t.Fatalf("profile fields not preserved: %#v", got)
	}
	if got.GitRoot != req.GitRoot || !got.IsWorktree || got.ToolSurfaceMode != req.ToolSurfaceMode {
		t.Fatalf("workspace/tool fields not preserved: %#v", got)
	}
	if !reflect.DeepEqual(got.EnabledTools, req.EnabledTools) || !reflect.DeepEqual(got.AdditionalWorkingDirectories, req.AdditionalWorkingDirectories) {
		t.Fatalf("tool directories not preserved: %#v", got)
	}
	if !reflect.DeepEqual(got.MCPSnapshot, req.MCPSnapshot) || !reflect.DeepEqual(got.SessionFlags, req.SessionFlags) || !reflect.DeepEqual(got.Config, req.Config) {
		t.Fatalf("runtime context not preserved: %#v", got)
	}
	if !reflect.DeepEqual(got.LaunchSkillNames, req.LaunchSkillNames) || !reflect.DeepEqual(got.LaunchSkillRefs, req.LaunchSkillRefs) || !got.ForceLaunchSkills {
		t.Fatalf("launch skills not preserved: %#v", got)
	}
	if got.AgentKey != req.AgentKey || got.PromptKey != req.PromptKey || got.OwnerThreadID != req.OwnerThreadID || got.LaunchIntentID != req.LaunchIntentID || !got.DeferSpawn {
		t.Fatalf("routing fields not preserved: %#v", got)
	}
	assertStartRequestMappingDoesNotAlias(t, req, got)
}

func assertStartRequestMappingDoesNotAlias(t *testing.T, req contract.SessionStartRequest, got StartRequest) {
	t.Helper()
	req.BaseInstructionBlocks[0].EnableWhen[0] = 'X'
	req.Sandbox[0] = '['
	req.EnabledTools[0] = "mutated"
	req.AdditionalWorkingDirectories[0] = "/mutated"
	req.MCPSnapshot.Servers[0] = "mutated"
	req.MCPSnapshot.Instructions["server-a"] = "mutated"
	mcpConfig := req.MCPSnapshot.ServerConfigs["server-a"]
	mcpConfig.Headers["Authorization"] = "mutated"
	mcpConfig.Args[0] = "mutated"
	*mcpConfig.Enabled = false
	req.SessionFlags["simple"] = false
	req.Config["sandbox"].(map[string]any)["mode"] = "mutated"
	req.LaunchSkillNames[0] = "mutated"
	req.LaunchSkillRefs[0].Name = "mutated"

	if got.BaseInstructionBlocks[0].EnableWhen[0] == 'X' || got.Sandbox[0] == '[' {
		t.Fatalf("base blocks or sandbox alias caller buffers: %#v", got)
	}
	if got.EnabledTools[0] == "mutated" || got.AdditionalWorkingDirectories[0] == "/mutated" || !got.SessionFlags["simple"] {
		t.Fatalf("mapped request aliases caller slices or maps: %#v", got)
	}
	if got.MCPSnapshot.Servers[0] == "mutated" || got.MCPSnapshot.Instructions["server-a"] == "mutated" {
		t.Fatalf("MCP snapshot aliases caller fields: %#v", got.MCPSnapshot)
	}
	gotMCPConfig := got.MCPSnapshot.ServerConfigs["server-a"]
	if gotMCPConfig.Headers["Authorization"] == "mutated" || gotMCPConfig.Args[0] == "mutated" || gotMCPConfig.Enabled == nil || !*gotMCPConfig.Enabled {
		t.Fatalf("MCP server config aliases caller fields: %#v", gotMCPConfig)
	}
	if got.Config["sandbox"].(map[string]any)["mode"] == "mutated" {
		t.Fatalf("Config was not deep-cloned: %#v", got.Config)
	}
	if got.LaunchSkillNames[0] == "mutated" || got.LaunchSkillRefs[0].Name == "mutated" {
		t.Fatalf("launch skill fields alias caller slices: %#v %#v", got.LaunchSkillNames, got.LaunchSkillRefs)
	}
}
```

Test imports must include `encoding/json`, `reflect`, `strings`, `testing`, `internal/contract`, and provider `dto`.

- [ ] **Step 5: Add typed status adapter**

```go
package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type sessionStatusPort struct {
	service Service
}

func NewSessionStatusPort(service Service) contract.SessionStatusPort {
	return sessionStatusPort{service: service}
}

func (p sessionStatusPort) ListSessions(ctx context.Context) ([]contract.SessionThreadRef, error) {
	refs, err := p.service.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contract.SessionThreadRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, sessionThreadRefFromThread(ref))
	}
	return out, nil
}

func (p sessionStatusPort) ReadMessages(ctx context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	return p.service.ReadMessages(ctx, threadID, limit, before)
}

func sessionThreadRefFromThread(ref Ref) contract.SessionThreadRef {
	return contract.SessionThreadRef{
		ID:               ref.ID,
		Name:             ref.Name,
		AgentID:          ref.AgentID,
		Status:           ref.Status,
		CreatedAt:        ref.CreatedAt,
		UpdatedAt:        ref.UpdatedAt,
		Provider:         ref.Provider,
		ProviderThreadID: ref.ProviderThreadID,
		SessionID:        ref.SessionID,
		CWD:              ref.CWD,
		Model:            ref.Model,
		Port:             ref.Port,
	}
}
```

- [ ] **Step 6: Compose ports in app**

```go
type sessionPorts struct {
	contract.SessionLifecyclePort
	contract.SessionStatusPort
}

func newSessionPorts(lifecycle contract.SessionLifecyclePort, status contract.SessionStatusPort) contract.SessionPorts {
	return sessionPorts{SessionLifecyclePort: lifecycle, SessionStatusPort: status}
}

var _ contract.SessionPorts = sessionPorts{}
```

Wire these providers in `internal/app/modules.go` near existing thread facade providers:

```go
thread.NewSessionLifecyclePort,
thread.NewSessionStatusPort,
newSessionPorts,
```

- [ ] **Step 7: Verify**

Run:

```bash
./scripts/test_with_guard.sh internal/module/thread/session_lifecycle_port.go
./scripts/test_with_guard.sh internal/module/thread/session_status_port.go
./scripts/test_with_guard.sh internal/module/thread/session_ports_test.go
./scripts/test_with_guard.sh internal/app/session_ports.go
./scripts/test_with_guard.sh internal/app/session_ports_test.go
./scripts/test_with_guard.sh internal/app/modules.go
./scripts/test_with_guard.sh ./internal/app ./internal/module/thread -count=1
```

Expected: all commands exit `0`.

### Task 4: Evolve Existing Event Wire Contract

**Files:**
- Create: `internal/platform/eventsurface/methods.go`
- Create: `internal/platform/eventsurface/methods_test.go`
- Modify: `internal/platform/rpc/push.go`
- Modify: `internal/platform/rpc/push_test.go`
- Create: `frontend-app/src/shared/api/eventWireMethods.js`
- Create: `frontend-app/src/shared/api/eventWire.js`
- Create: `frontend-app/src/shared/api/eventWire.test.js`

- [ ] **Step 1: Add typed frontend method list and raw/open allowlist**

```js
export const EVENT_TYPED_WIRE_METHODS = Object.freeze([
  'ui/state/changed',
  'ui/thread/changed',
  'ui/sidebar/changed',
  'turn/started',
  'turn/completed',
  'turn/interrupted',
  'turn/stalled',
  'turn/resumed',
  'turn/output/delta',
  'item/agentMessage/delta',
  'item/reasoning/textDelta',
  'item/commandExecution/outputDelta',
  'item/tool/call',
  'item/completed',
  'item/commandExecution/requestApproval',
  'item/fileChange/requestApproval',
  'skill/requestApproval',
  'approval/resolved',
  'thread/started',
  'thread/stopped',
  'thread/messages/page',
  'thread/compacted',
  'thread/tokenusage/updated',
  'skills/changed',
  'ui/preferences/changed',
  'ui/thread/patch',
  'ui/shared-files/changed',
  'ui/memory/changed',
  'ui/prompts/changed',
  'agent/launched',
  'agent/stopped',
  'agent/recovering',
  'agent/failed',
  'agent/runtime/reported',
  'task/node/statusChanged',
  'cron/job/runStateChanged',
]);

export const EVENT_RAW_WIRE_METHODS = Object.freeze([
  'error',
  'configWarning',
  'deprecationNotice',
  'approval/request',
  'thread/name/updated',
  'thread/tokenUsage/updated',
  'thread/tokenusage/updated',
]);

export const EVENT_RAW_WIRE_PREFIXES = Object.freeze([
  'item/',
  'turn/plan/',
  'turn/diff/',
  'agent/event/',
  'account/',
  'app/list/',
  'fuzzyFileSearch/',
]);

export const EVENT_RAW_WIRE_SUFFIXES = Object.freeze([
  '/requestApproval',
]);

export const EVENT_COMPAT_WIRE_PREFIXES = Object.freeze([
  'workspace/run/',
]);

// Compatibility alias for any existing caller that imports the old name.
export const EVENT_WIRE_METHODS = EVENT_TYPED_WIRE_METHODS;
```

- [ ] **Step 2: Add backend typed method list and raw/open allowlist using existing constants**

```go
type RawWireAllowlist struct {
	Methods  []string
	Prefixes []string
	Suffixes []string
}

func AllTypedWireMethods() []string {
	return []string{
		MethodUIStateChanged,
		MethodUIThreadChanged,
		MethodUISidebarChanged,
		MethodTurnStarted,
		MethodTurnCompleted,
		MethodTurnInterrupted,
		MethodTurnStalled,
		MethodTurnResumed,
		MethodTurnOutputDelta,
		MethodAgentMessageDelta,
		MethodReasoningTextDelta,
		MethodCommandOutputDelta,
		MethodToolCall,
		MethodItemCompleted,
		MethodCommandApprovalRequested,
		MethodFileApprovalRequested,
		MethodSkillApprovalRequested,
		MethodApprovalResolved,
		MethodThreadStarted,
		MethodThreadStopped,
		MethodThreadMessages,
		MethodThreadCompacted,
		MethodThreadTokenUsage,
		MethodSkillsChanged,
		MethodUIPreferencesChanged,
		MethodUIThreadPatch,
		MethodUISharedFilesChanged,
		MethodUIMemoryChanged,
		MethodUIPromptsChanged,
		MethodAgentLaunched,
		MethodAgentStopped,
		MethodAgentRecovering,
		MethodAgentFailed,
		MethodAgentRuntimeReported,
		MethodTaskNodeStatusChanged,
		MethodCronJobRunStateChanged,
	}
}

func CompatWirePrefixes() []string {
	return []string{
		"workspace/run/",
	}
}

func RawWireAllowlistSpec() RawWireAllowlist {
	return RawWireAllowlist{
		Methods: []string{
			"error",
			"configWarning",
			"deprecationNotice",
			"approval/request",
			"thread/name/updated",
			"thread/tokenUsage/updated",
			"thread/tokenusage/updated",
		},
		Prefixes: []string{
			"item/",
			"turn/plan/",
			"turn/diff/",
			"agent/event/",
			"account/",
			"app/list/",
			"fuzzyFileSearch/",
		},
		Suffixes: []string{
			"/requestApproval",
		},
	}
}

func RawWireAllowed(spec RawWireAllowlist, method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return false
	}
	for _, exact := range spec.Methods {
		if method == exact {
			return true
		}
	}
	for _, prefix := range spec.Prefixes {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	for _, suffix := range spec.Suffixes {
		if strings.HasSuffix(method, suffix) {
			return true
		}
	}
	return false
}
```

Add `strings` to `internal/platform/eventsurface/methods.go`. `CompatWirePrefixes` exists for frontend/source-event parsing only; it must not be folded into `RawWireAllowlistSpec` without an explicit behavior-change test.

- [ ] **Step 3: Make RPC raw push use the shared allowlist**

In `internal/platform/rpc/push.go`, derive typed suppression from the shared typed method list, then delegate open raw method decisions to `eventsurface`:

```go
var typedPushMethods = newTypedPushMethods()

func newTypedPushMethods() map[string]struct{} {
	out := make(map[string]struct{}, len(eventsurface.AllTypedWireMethods()))
	for _, method := range eventsurface.AllTypedWireMethods() {
		out[strings.ToLower(method)] = struct{}{}
	}
	return out
}

func shouldPushRawProviderMethod(method string) bool {
	method = strings.TrimSpace(approvalMethodCatalog.normalize(method))
	if method == "" {
		return false
	}
	if _, ok := typedPushMethods[strings.ToLower(method)]; ok {
		return false
	}
	return eventsurface.RawWireAllowed(eventsurface.RawWireAllowlistSpec(), method)
}
```

Update `internal/platform/rpc/push_test.go` so the existing raw push cases still pass through the shared allowlist instead of a private copy in `rpc`. Add a regression assertion that `workspace/run/created` is not accepted by `shouldPushRawProviderMethod`; `workspace/run/` remains an eventsurface compatibility/source-event prefix, not a raw provider prefix.

- [ ] **Step 4: Add bidirectional parity tests against frontend lists**

```go
package eventsurface

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var frontendStringRE = regexp.MustCompile(`'([^']+)'`)

func TestTypedWireMethodsMatchFrontendList(t *testing.T) {
	root := repoRootForEventSurfaceTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "frontend-app/src/shared/api/eventWireMethods.js"))
	if err != nil {
		t.Fatalf("read frontend event methods: %v", err)
	}
	assertStringSetEqual(t, "typed wire methods", AllTypedWireMethods(), frontendFrozenStringArray(t, raw, "EVENT_TYPED_WIRE_METHODS"))
}

func TestRawWireAllowlistMatchesFrontendList(t *testing.T) {
	root := repoRootForEventSurfaceTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "frontend-app/src/shared/api/eventWireMethods.js"))
	if err != nil {
		t.Fatalf("read frontend event methods: %v", err)
	}
	spec := RawWireAllowlistSpec()
	assertStringSetEqual(t, "raw wire methods", spec.Methods, frontendFrozenStringArray(t, raw, "EVENT_RAW_WIRE_METHODS"))
	assertStringSetEqual(t, "raw wire prefixes", spec.Prefixes, frontendFrozenStringArray(t, raw, "EVENT_RAW_WIRE_PREFIXES"))
	assertStringSetEqual(t, "raw wire suffixes", spec.Suffixes, frontendFrozenStringArray(t, raw, "EVENT_RAW_WIRE_SUFFIXES"))
	assertStringSetEqual(t, "compat wire prefixes", CompatWirePrefixes(), frontendFrozenStringArray(t, raw, "EVENT_COMPAT_WIRE_PREFIXES"))
}

func TestWireAllowlistCoversLegacyAndRawOpenMethods(t *testing.T) {
	expanded := map[string]bool{}
	for _, notification := range ExpandNotifications(MethodThreadStarted, map[string]any{"threadId": "thread-1"}) {
		expanded[notification.Method] = true
	}
	for _, method := range []string{MethodUIThreadChanged, MethodUISidebarChanged} {
		if !expanded[method] {
			t.Fatalf("ExpandNotifications missing legacy expansion method %q", method)
		}
	}

	spec := RawWireAllowlistSpec()
	for _, method := range []string{
		"item/plan/delta",
		"turn/plan/delta",
		"account/rateLimits/updated",
		"approval/request",
		"thread/name/updated",
		"item/custom/requestApproval",
	} {
		if !RawWireAllowed(spec, method) {
			t.Fatalf("raw wire allowlist rejects %q", method)
		}
	}
	if RawWireAllowed(spec, "unknown/domain/event") {
		t.Fatalf("raw wire allowlist accepted unknown method")
	}
	if RawWireAllowed(spec, "workspace/run/created") {
		t.Fatalf("raw wire allowlist must not accept workspace run source events")
	}
	workspaceExpanded := map[string]bool{}
	for _, notification := range ExpandNotifications("workspace/run/created", map[string]any{"threadId": "thread-1"}) {
		workspaceExpanded[notification.Method] = true
	}
	if !workspaceExpanded[MethodUISidebarChanged] {
		t.Fatalf("workspace run source events must still trigger sidebar refresh")
	}
}

func frontendFrozenStringArray(t *testing.T, raw []byte, name string) []string {
	t.Helper()
	prefix := []byte("export const " + name + " = Object.freeze([")
	start := bytes.Index(raw, prefix)
	if start < 0 {
		t.Fatalf("frontend list %s not found", name)
	}
	rest := raw[start+len(prefix):]
	end := bytes.Index(rest, []byte("]);"))
	if end < 0 {
		t.Fatalf("frontend list %s missing closing ]);", name)
	}
	matches := frontendStringRE.FindAllSubmatch(rest[:end], -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, string(match[1]))
	}
	return out
}

func assertStringSetEqual(t *testing.T, label string, want, got []string) {
	t.Helper()
	want = sortedUniqueStrings(want)
	got = sortedUniqueStrings(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Fatalf("%s mismatch\nwant:\n%s\n\ngot:\n%s", label, strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func sortedUniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func repoRootForEventSurfaceTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found from %s: %v", root, err)
	}
	return root
}
```

- [ ] **Step 5: Add frontend parser without rejecting raw/open compatibility methods**

```js
import {
  EVENT_COMPAT_WIRE_PREFIXES,
  EVENT_RAW_WIRE_METHODS,
  EVENT_RAW_WIRE_PREFIXES,
  EVENT_RAW_WIRE_SUFFIXES,
  EVENT_TYPED_WIRE_METHODS,
} from './eventWireMethods.js';

const typedWireMethodSet = new Set(EVENT_TYPED_WIRE_METHODS);
const rawWireMethodSet = new Set(EVENT_RAW_WIRE_METHODS);

export function isKnownEventWireMethod(method) {
  if (!method || typeof method !== 'string') return false;
  if (typedWireMethodSet.has(method) || rawWireMethodSet.has(method)) return true;
  return EVENT_RAW_WIRE_PREFIXES.some((prefix) => method.startsWith(prefix)) ||
    EVENT_COMPAT_WIRE_PREFIXES.some((prefix) => method.startsWith(prefix)) ||
    EVENT_RAW_WIRE_SUFFIXES.some((suffix) => method.endsWith(suffix));
}

export function asEventWireNotification(method, payload) {
  if (!method || typeof method !== 'string') {
    throw new Error('event wire method is required');
  }
  if (!isKnownEventWireMethod(method)) {
    throw new Error(`unknown event wire method: ${method}`);
  }
  return { method, payload };
}
```

- [ ] **Step 6: Verify event surface**

Run:

```bash
./scripts/test_with_guard.sh internal/platform/eventsurface/methods.go
./scripts/test_with_guard.sh internal/platform/eventsurface/methods_test.go
./scripts/test_with_guard.sh internal/platform/rpc/push.go
./scripts/test_with_guard.sh internal/platform/rpc/push_test.go
./scripts/test_with_guard.sh ./internal/platform/eventsurface ./internal/platform/rpc ./internal/ui/wails -count=1
cd frontend-app && npm test -- eventWire.test.js
```

Expected: backend and frontend tests pass. Existing `ExpandNotifications` behavior remains intact unless a test explicitly changes it.

### Task 5: Add Prompt Prefix Shape From Existing Assembly Facts

**Files:**
- Modify: `internal/dto/provider/session.go`
- Modify: `internal/contract/prompt.go`
- Create: `internal/module/prompt/prefix_shape.go`
- Create: `internal/module/prompt/prefix_shape_test.go`
- Modify: `internal/module/prompt/assembler.go`
- Modify: `internal/provider/codexapp/driver.go`
- Modify: `internal/provider/codexapp/driver_session_test.go`

- [ ] **Step 1: Add provider DTO shape**

```go
type PrefixShape struct {
	Hash                  string   `json:"hash,omitempty"`
	StaticSectionNames    []string `json:"staticSectionNames,omitempty"`
	DynamicSectionNames   []string `json:"dynamicSectionNames,omitempty"`
	SuppressedToolNames   []string `json:"suppressedToolNames,omitempty"`
	CachedPrefixBytes     int      `json:"cachedPrefixBytes,omitempty"`
	UncachedTailBytes     int      `json:"uncachedTailBytes,omitempty"`
	DeveloperBytes        int      `json:"developerBytes,omitempty"`
	ChurnReason           string   `json:"churnReason,omitempty"`
}
```

Add to `StartAssembly`:

```go
PrefixShape PrefixShape `json:"prefixShape,omitempty"`
```

- [ ] **Step 2: Alias shape in contract**

```go
type PrefixShape = dto.PrefixShape
```

- [ ] **Step 3: Build shape in prompt package**

```go
package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func BuildPrefixShape(base, developer string, boundary *contract.PromptAssemblyBoundary, sections []ResolvedPromptSection, suppressedTools []string, reason string) contract.PrefixShape {
	staticNames := make([]string, 0, len(sections))
	dynamicNames := make([]string, 0, len(sections))
	h := sha256.New()
	writeShapePart(h, "base", base)
	writeShapePart(h, "developer", developer)
	if boundary != nil {
		writeShapePart(h, "cached", boundary.CachedPrefix)
		writeShapePart(h, "uncached", boundary.UncachedTail)
	}
	for _, section := range sections {
		name := strings.TrimSpace(section.Name)
		if name == "" {
			continue
		}
		writeShapePart(h, name, section.Content)
		if section.Region == PromptRegionStatic && !section.Volatile {
			staticNames = append(staticNames, name)
		} else {
			dynamicNames = append(dynamicNames, name)
		}
	}
	sort.Strings(staticNames)
	sort.Strings(dynamicNames)
	tools := append([]string(nil), suppressedTools...)
	sort.Strings(tools)
	for _, tool := range tools {
		writeShapePart(h, "suppressed_tool", tool)
	}
	return contract.PrefixShape{
		Hash:                hex.EncodeToString(h.Sum(nil)),
		StaticSectionNames:  staticNames,
		DynamicSectionNames: dynamicNames,
		SuppressedToolNames: tools,
		CachedPrefixBytes:   len(promptBoundaryCachedPrefix(boundary)),
		UncachedTailBytes:   len(promptBoundaryUncachedTail(boundary)),
		DeveloperBytes:      len(developer),
		ChurnReason:         strings.TrimSpace(reason),
	}
}

func writeShapePart(h hash.Hash, name, content string) {
	h.Write([]byte(strings.TrimSpace(name)))
	h.Write([]byte{0})
	h.Write([]byte(content))
	h.Write([]byte{0})
}

func promptBoundaryCachedPrefix(boundary *contract.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return boundary.CachedPrefix
}

func promptBoundaryUncachedTail(boundary *contract.PromptAssemblyBoundary) string {
	if boundary == nil {
		return ""
	}
	return boundary.UncachedTail
}
```

- [ ] **Step 4: Populate shape during `AssembleStart`**

In `internal/module/prompt/assembler.go`, set:

```go
PrefixShape: BuildPrefixShape(base, dev, boundary, resolved, suppressedTools, ""),
```

For `simpleStartAssembly`, pass `nil` sections and the computed `suppressedTools`.

- [ ] **Step 5: Log provider start shape without logging prompt contents**

At Codex provider session start, log:

```go
pkglogger.Debug("codexapp: start prompt prefix shape",
	"agent_id", req.AgentID,
	"prefix_hash", req.StartAssembly.PrefixShape.Hash,
	"static_sections", req.StartAssembly.PrefixShape.StaticSectionNames,
	"dynamic_sections", req.StartAssembly.PrefixShape.DynamicSectionNames,
	"cached_prefix_bytes", req.StartAssembly.PrefixShape.CachedPrefixBytes,
	"uncached_tail_bytes", req.StartAssembly.PrefixShape.UncachedTailBytes,
)
```

Do not add `PrefixShape` to `dto.TurnRequest`.

- [ ] **Step 6: Verify prompt/provider packages**

Run:

```bash
./scripts/test_with_guard.sh internal/dto/provider/session.go
./scripts/test_with_guard.sh internal/contract/prompt.go
./scripts/test_with_guard.sh internal/module/prompt/prefix_shape.go
./scripts/test_with_guard.sh internal/module/prompt/prefix_shape_test.go
./scripts/test_with_guard.sh internal/module/prompt/assembler.go
./scripts/test_with_guard.sh internal/provider/codexapp/driver.go
./scripts/test_with_guard.sh internal/provider/codexapp/driver_session_test.go
./scripts/test_with_guard.sh ./internal/module/prompt ./internal/module/thread ./internal/provider/codexapp ./internal/provider/claudecli -count=1
```

Expected: all commands exit `0`.

### Task 6: Centralize MCP Namespace Helpers

**Files:**
- Create: `internal/platform/toolbridge/mcp_namespace.go`
- Create: `internal/platform/toolbridge/mcp_namespace_test.go`
- Modify: `internal/platform/toolbridge/handler_peer_decode_helpers.go`
- Modify: `internal/platform/toolbridge/handler_host_tools.go`

- [ ] **Step 1: Add namespace helper only**

```go
package toolbridge

import "strings"

type MCPToolNamespace struct {
	Server string
	Tool   string
}

func WrapMCPToolName(server, tool string) string {
	server = strings.TrimSpace(server)
	tool = strings.TrimSpace(tool)
	if server == "" || tool == "" {
		return tool
	}
	return "mcp__" + server + "__" + tool
}

func SplitMCPToolName(name string) (MCPToolNamespace, bool) {
	trimmed := strings.TrimSpace(name)
	if !strings.HasPrefix(trimmed, "mcp__") {
		return MCPToolNamespace{}, false
	}
	rest := strings.TrimPrefix(trimmed, "mcp__")
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return MCPToolNamespace{}, false
	}
	return MCPToolNamespace{Server: strings.TrimSpace(parts[0]), Tool: strings.TrimSpace(parts[1])}, true
}
```

- [ ] **Step 2: Replace current local helpers**

Replace `wrappedMCPToolName` and `mcpWrappedToolName` with `WrapMCPToolName` and `SplitMCPToolName`.

Update both `internal/platform/toolbridge/handler_peer_decode_helpers.go` and `internal/platform/toolbridge/handler_host_tools.go`; both currently participate in wrapped-name construction or parsing.

Do not add `active`, `suspended`, or `removed` filtering in this phase. The spike decision says lifecycle filtering needs a separate state-owner change.

- [ ] **Step 3: Verify toolbridge**

Run:

```bash
./scripts/test_with_guard.sh internal/platform/toolbridge/mcp_namespace.go
./scripts/test_with_guard.sh internal/platform/toolbridge/mcp_namespace_test.go
./scripts/test_with_guard.sh internal/platform/toolbridge/handler_peer_decode_helpers.go
./scripts/test_with_guard.sh internal/platform/toolbridge/handler_host_tools.go
./scripts/test_with_guard.sh ./internal/platform/toolbridge -count=1
```

Expected: existing Codex surface alias tests still pass.

### Task 7: Add Desktop Dependency Guard With Correct Scope

**Files:**
- Create: `internal/archtest/desktop_dependency_test.go`

- [ ] **Step 1: Add scoped guard**

```go
package archtest_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestDesktopDependenciesStayOutOfCoreRuntime(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	for _, file := range parseImportFiles(t, root, "internal/module", "internal/provider", "internal/platform") {
		if strings.HasPrefix(file.RelPath, "internal/platform/ui/") {
			continue
		}
		for _, imp := range file.Imports {
			if strings.HasPrefix(imp, "github.com/wailsapp/wails") {
				violations = append(violations, fmt.Sprintf("%s imports Wails dependency %s", file.RelPath, imp))
			}
		}
	}
	failIfViolations(t, violations)
}
```

This intentionally does not scan `internal/app`, because `internal/app/app.go` is the desktop assembly boundary and currently imports Wails.

- [ ] **Step 2: Verify archtest**

Run:

```bash
./scripts/test_with_guard.sh internal/archtest/desktop_dependency_test.go
./scripts/test_with_guard.sh ./internal/archtest -count=1
make guard
```

Expected: both commands exit `0`; inspect and report any `internal/archtest/baseline.json` diff.

### Task 8: Add Frontend Session Facade Over Existing Guards

**Files:**
- Create: `frontend-app/src/shared/api/sessionApi.js`
- Create: `frontend-app/src/shared/api/sessionApi.test.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.js`

- [ ] **Step 1: Add facade over guarded exports**

```js
import {
  getThreadMessages,
  interruptTurn,
  startThread,
  startTurn,
} from './backendApi.js';

export const sessionApi = Object.freeze({
  start(params) {
    return startThread(params);
  },
  startTurn(params) {
    return startTurn(params);
  },
  interrupt(threadId, cwd, source = 'frontend') {
    return interruptTurn({ threadId, cwd, source });
  },
  messages(threadId, limit = 100, before = '') {
    return getThreadMessages({ threadId, limit, before });
  },
});
```

- [ ] **Step 2: Add facade test**

```js
import fs from 'node:fs';
import { beforeEach, expect, test, vi } from 'vitest';
import { sessionApi } from './sessionApi.js';
import {
  callBackend,
  getThreadMessages,
  interruptTurn,
  startThread,
  startTurn,
} from './backendApi.js';

vi.mock('./backendApi.js', () => ({
  callBackend: vi.fn(() => {
    throw new Error('sessionApi must not call raw callBackend');
  }),
  getThreadMessages: vi.fn(),
  interruptTurn: vi.fn(),
  startThread: vi.fn(),
  startTurn: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

test('sessionApi exposes stable session method names', () => {
  expect(Object.keys(sessionApi).sort()).toEqual(['interrupt', 'messages', 'start', 'startTurn']);
});

test('sessionApi does not import raw bridge API', () => {
  const source = fs.readFileSync(new URL('./sessionApi.js', import.meta.url), 'utf8');
  expect(source).not.toContain('wailsBridge');
  expect(source).not.toContain('callAPI');
  expect(source).not.toContain('callBackend');
});

test('sessionApi delegates only to guarded backendApi exports', async () => {
  startThread.mockResolvedValue({ id: 'thread-1' });
  startTurn.mockResolvedValue({ ok: true });
  interruptTurn.mockResolvedValue({ interrupted: true });
  getThreadMessages.mockResolvedValue({ messages: [] });

  await expect(sessionApi.start({ cwd: '/repo', name: 'draft' })).resolves.toEqual({ id: 'thread-1' });
  await expect(sessionApi.startTurn({ threadId: 'thread-1', text: 'hello' })).resolves.toEqual({ ok: true });
  await expect(sessionApi.interrupt('thread-1', '/repo', 'ui_stop')).resolves.toEqual({ interrupted: true });
  await expect(sessionApi.messages('thread-1', 25, 'cursor-1')).resolves.toEqual({ messages: [] });

  expect(startThread).toHaveBeenCalledWith({ cwd: '/repo', name: 'draft' });
  expect(startTurn).toHaveBeenCalledWith({ threadId: 'thread-1', text: 'hello' });
  expect(interruptTurn).toHaveBeenCalledWith({ threadId: 'thread-1', cwd: '/repo', source: 'ui_stop' });
  expect(getThreadMessages).toHaveBeenCalledWith({ threadId: 'thread-1', limit: 25, before: 'cursor-1' });
  expect(callBackend).not.toHaveBeenCalled();
});
```

- [ ] **Step 3: Move one call site**

In `frontend-app/src/entities/client/model/useClientStore.js`, import `sessionApi` and replace one direct `startThread(...)` call in `startNewDraftThread` with:

```js
const thread = await sessionApi.start({
  cwd: request.cwd,
  name: sendDraftThreadName(request.text),
  ...launchPreferences,
  deferSpawn: true,
  launchIntentId: request.launchIntentId,
});
```

- [ ] **Step 4: Verify frontend**

Run:

```bash
cd frontend-app
npm test -- sessionApi.test.js eventWire.test.js
npm run lint
```

Expected: tests and lint pass.

## 8. Final Verification

After all phases, run:

Before this final package-level pass, every phase-owned Go file listed in the task sections above must already have passed its single-file guard. The package-level commands below are not a substitute for the per-file guard rule.

```bash
./scripts/test_with_guard.sh ./internal/contract ./internal/app ./internal/module/thread ./internal/module/prompt ./internal/platform/eventsurface ./internal/platform/rpc ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli ./internal/ui/wails ./internal/archtest -count=1
make guard
make build-plain
cd frontend-app && npm run lint && npm test && npm run build
git status --short
```

Expected:

- All Go package tests pass.
- `make guard` exits `0`.
- `make build-plain` exits `0`.
- If `make build-plain` updates ignored embedded frontend output, report it as generated output and do not stage legacy Vue source or ignored `cmd/agent-terminal/frontend/dist`.
- Frontend lint, tests, and build pass.
- `git status --short` shows only phase-owned files plus any explicitly approved pre-existing dirty files.

## 9. Scope Control

- Keep this as architecture absorption, not a UI redesign.
- Keep `cmd/mcp-orch` independent.
- Keep provider mirror directories generated-only.
- Keep `cmd/agent-terminal/frontend` out of scope.
- Do not stage unrelated current worktree changes.
- Do not introduce MCP per-tool lifecycle filtering until a separate owner/storage decision exists.
