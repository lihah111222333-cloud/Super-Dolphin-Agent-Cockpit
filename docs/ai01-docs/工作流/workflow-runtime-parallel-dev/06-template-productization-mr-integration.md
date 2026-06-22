# Template Productization And MR Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将模板从“可渲染草稿”推进到可保存、可版本化、可信任、可复用，并为后续 MR/PR 生命周期绑定打基础。

**Architecture:** `workflowtemplate` 继续只负责模板目录和草稿渲染；模板产品化增加独立的保存、版本、信任和兼容性层。MR/PR 集成通过 `ChangeRequest` 对象关联 workflow run、artifact 和外部代码平台状态。

**Tech Stack:** Go workflow template module, shared workflow templates, React UI, Git/GitLab/GitHub integration adapters later.

---

## Ownership

**Primary owner:** Template and change request agent.

**Write scope:**
- `internal/module/workflowtemplate/`
- `internal/platform/shared/workflowtemplates/`
- `internal/platform/toolbridge/host_tools.go`
- `frontend-app/src/pages/workflows/`
- new `ChangeRequest` contract/store files if Agent Workflow Layer contract is available

**Do not modify:**
- runtime state machine
- wakeup lease/final output store
- command runner security
- workbench recovery actions

## Functional Requirements

- Template render output remains a draft and does not start a run.
- Template schema validates node type, exec config, output mapping and final output.
- Unsupported runtime capabilities are rejected before save/publish.
- Users can save a runnable DAG as a template.
- Templates have version, category, trust metadata and compatibility metadata.
- Templates can be searched and rolled back.
- ChangeRequest links workflow run, branch, commits, checks, review gate and MR/PR URL.

## Tasks

### Task 1: Strengthen Template Schema Validation

**Files:**
- Modify: `internal/platform/shared/workflowtemplates/types.go`
- Modify: `internal/platform/shared/workflowtemplates/render.go`
- Test: `internal/platform/shared/workflowtemplates/render_validation_test.go`

- [ ] Validate node type against supported runtime capabilities.
- [ ] Validate `config.exec.cwd` for Agent nodes.
- [ ] Validate output mapping and final node uniqueness.
- [ ] Reject Hybrid/HITL templates until runtime support lands.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/platform/shared/workflowtemplates -count=1
```

Expected: exit 0.

### Task 2: Add Save-As-Template Contract

**Files:**
- Modify: `internal/module/workflowtemplate/`
- Modify: `internal/platform/toolbridge/host_tools.go`
- Test: focused workflowtemplate tests

- [ ] Add a host tool or RPC for saving a validated DAG draft as a template.
- [ ] Persist template metadata without mixing it with runtime run/node state.
- [ ] Require version, category and compatibility fields.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/workflowtemplate -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\internal\platform\toolbridge\host_tools.go
```

Expected: exit 0.

### Task 3: Add Template Browser UI

**Files:**
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- Create focused template browser components under `frontend-app/src/pages/workflows/components/`

- [ ] Add search and category filters.
- [ ] Show version, trust and compatibility metadata.
- [ ] Add save-as-template action for validated DAGs.
- [ ] Add rollback action for previous template versions.

Run:

```powershell
cd frontend-app
npm run lint
npm test
```

Expected: lint exit 0 and tests exit 0.

### Task 4: Define ChangeRequest Contract

**Files:**
- Modify or create: `internal/contract/change_request.go`
- Create store and tests if Agent Workflow Layer has landed its artifact model.

- [ ] Define fields for workflow run id, branch, commits, checks, review gate, MR/PR URL and status.
- [ ] Keep provider-specific fields behind a generic external reference object.
- [ ] Add JSON round-trip tests.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 .\internal\contract\change_request.go
```

Expected: exit 0 if the file exists in this task. If ChangeRequest is implemented in Agent Workflow Layer first, run that package's targeted guard.

### Task 5: Add Minimal MR/PR Status Link

**Files:**
- Modify focused files in the ChangeRequest store/service from Task 4.
- Modify frontend workflow detail components.

- [ ] Allow a workflow run to link to an MR/PR URL.
- [ ] Show branch, latest check status and review gate status in UI.
- [ ] Avoid provider-specific write operations in the first increment.

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/contract -count=1
cd frontend-app
npm run lint
```

Expected: Go guard exit 0 and frontend lint exit 0.

## Acceptance

- Template rendering remains read-only.
- A valid DAG can be saved as a versioned template.
- Unsupported Hybrid/HITL capability cannot enter a published executable template.
- Template browser exposes search, version and trust metadata.
- A workflow run can link to an MR/PR without coupling runtime to a specific Git provider.
