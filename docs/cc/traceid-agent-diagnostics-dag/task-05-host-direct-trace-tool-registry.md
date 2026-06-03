# T05 - Host-Direct Trace Tool Registry

Depends on: T03, T04, T06

## Objective

Expose trace diagnosis as a host-direct tool named `observability_trace_get`.

## Source Anchors

- `internal/platform/toolbridge/host_tools.go:10-34`
- `internal/platform/toolbridge/module.go:71-85`
- `internal/platform/observability/module.go:28-46`
- `internal/platform/observability/service.go:93-107`

## Scope

Add `internal/platform/toolbridge/observability_trace_tool.go` with a registry that implements:

- `ListHostTools`
- `HasTool`
- `CallHostTool`

Input schema:

- `trace_id` string, required
- `limit` integer, optional
- `force_refresh` boolean, optional
- `include_stack` boolean, optional

## Requirements

- `ListHostTools` must return the tool only when `svc != nil && svc.Enabled()`.
- `HasTool` must still recognize `observability_trace_get` when tracing is disabled so stale direct calls are handled by the host registry instead of falling through to peer routing.
- Stale direct calls when tracing is disabled must return an explicit disabled/degraded result, not a clean diagnosis.
- Validate input through the existing structured decode helper pattern.
- Add server-side validation for non-empty `trace_id`, maximum `limit`, boolean fields, and unknown-field policy; do not rely only on JSON schema or `DecodeInput`.
- Call `DiagnoseTrace`, not raw `Query`.

## Acceptance Criteria

- The registry can be composed with existing host tools.
- `observability_trace_get` is discoverable only when tracing is enabled.
- Tool output uses the redacted `TraceDiagnosis` projection.
- Disabled stale calls are covered by tests that prove host routing returns the explicit disabled/degraded result.
