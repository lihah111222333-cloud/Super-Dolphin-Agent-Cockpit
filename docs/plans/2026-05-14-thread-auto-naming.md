# Thread Auto Naming Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide a seamless chat experience by defaulting new threads to "新对话", hiding the "pending launch" UI badges, and automatically extracting a conversational title upon the first message.

**Architecture:** Pure frontend fallback modification to avoid any backend AgentID routing bugs, coupled with a simple string-extraction hook during the lazy-spawn process.

**Tech Stack:** Vue3, Go

---

### Task 1: Update Frontend Display Name Fallback

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/stores/thread-store-view.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/utils/thread-page-utils.js`

- [ ] **Step 1: Modify thread store view fallback**
In `cmd/agent-terminal/frontend/vue-app/stores/thread-store-view.js`, inside the `displayName(thread)` function:
Change the final return statement to fallback to `'新对话'` instead of `thread.id`.
```javascript
  function displayName(thread) {
    if (!thread?.id) return '';
    const threadName = (thread.name || '').toString().trim();
    if (threadName && threadName !== thread.id) return threadName;
    const alias = (state.agentMetaById[thread.id]?.alias || '').toString().trim();
    return alias || threadName || '新对话';
  }
```

- [ ] **Step 2: Modify thread page utils fallback**
In `cmd/agent-terminal/frontend/vue-app/utils/thread-page-utils.js`, around line 362:
Change `displayName = displayName || threadId;` to fallback to `'新对话'`.
```javascript
    displayName = displayName || '新对话';
```

### Task 2: Remove Pending Launch Badges

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/components/unified-chat/CmdCardGrid.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/components/unified-chat/ThreadRailSidePanel.js`

- [ ] **Step 1: Remove badge from CmdCardGrid**
In `cmd/agent-terminal/frontend/vue-app/components/unified-chat/CmdCardGrid.js`, find and remove the `<span v-if="card.pendingLaunch" class="thread-pending-badge"...>待启动</span>` element completely.

- [ ] **Step 2: Remove badge from ThreadRailSidePanel**
In `cmd/agent-terminal/frontend/vue-app/components/unified-chat/ThreadRailSidePanel.js`, find and remove the `<span v-if="editingThreadId !== thread.id && thread.pendingLaunch" class="thread-pending-badge"...>待启动</span>` element completely.

### Task 3: Hook Up Backend Title Extraction

**Files:**
- Modify: `internal/module/thread/spawn.go`

- [ ] **Step 1: Add extraction logic to runPendingSpawn**
In `internal/module/thread/spawn.go`, locate `runPendingSpawn` (around line 343) where `displayName` is resolved:
```go
	displayName := resolveDisplayName(ctx, s.threadStore, agentID, req.Prompt, assembly.DisplayName)
```
Add the `ExtractTitle` logic immediately after that line, and before `prependAgentBadge`:
```go
	displayName := resolveDisplayName(ctx, s.threadStore, agentID, req.Prompt, assembly.DisplayName)
	if displayName == "" || displayName == "新对话" {
		if ext := ExtractTitle(req.Prompt); ext != "" {
			displayName = ext
		}
	}
	displayName = prependAgentBadge(displayName, req.AgentTitle, req.AgentKey)
```

- [ ] **Step 2: Commit the changes**
```bash
git add cmd/agent-terminal/frontend/vue-app/stores/thread-store-view.js cmd/agent-terminal/frontend/vue-app/utils/thread-page-utils.js cmd/agent-terminal/frontend/vue-app/components/unified-chat/CmdCardGrid.js cmd/agent-terminal/frontend/vue-app/components/unified-chat/ThreadRailSidePanel.js internal/module/thread/spawn.go
git commit -m "feat: thread auto naming and ui tag removal"
```
