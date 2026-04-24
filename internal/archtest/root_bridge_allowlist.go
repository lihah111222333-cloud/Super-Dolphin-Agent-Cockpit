package archtest

import (
	"os"
	"path/filepath"
	"strings"
)

// rootBridgeException declares a permanent or temporary exception for the P22
// runtime-ownership guards. Per docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md
// (§收口口径), the P22 semantic allowlist must record — per entry — at minimum:
//
//   - DefinitionPath: file that defines the bridge function
//   - CallSitePath:   file that references it via fx.Invoke(...) / OnStart(...)
//                     (equal to DefinitionPath when co-located; kept as a
//                     separate field so one column never carries two
//                     semantics — see P0 §收口口径 clarification)
//   - Symbol:         bridge function name
//   - BridgeShape:    one of rootBridgeShape{App,Orch,Sidecar}Root
//   - ExceptionClass: rootBridgeExceptionPermanent|Temporary
//   - Reason / RemoveWhen / RollbackWhen / RollbackAction — all non-empty
//
// This allowlist is deliberately kept **separate** from freeze_registry.go:
// per P0 §TDD 与清理要求, semantic runtime-ownership exceptions must not
// share storage with numeric file/package freeze accounting.
type rootBridgeException struct {
	DefinitionPath string
	CallSitePath   string
	Symbol         string
	BridgeShape    rootBridgeShape
	ExceptionClass rootBridgeExceptionClass
	Reason         string
	RemoveWhen     string
	RollbackWhen   string
	RollbackAction string
}

type rootBridgeShape string

const (
	rootBridgeShapeAppRoot     rootBridgeShape = "app_root_bridge"
	rootBridgeShapeOrchRoot    rootBridgeShape = "orch_root_bridge"
	rootBridgeShapeSidecarRoot rootBridgeShape = "runner_only_sidecar_bridge"
)

type rootBridgeExceptionClass string

const (
	rootBridgeExceptionPermanent rootBridgeExceptionClass = "permanent"
	rootBridgeExceptionTemporary rootBridgeExceptionClass = "temporary"
)

// rootBridgeAllowlist enumerates the per-process-entry bridges that aggregate
// `group:"runners"` and call platformrunner.RunGroup(...). Every entry here
// is the *only* shape permitted under the P0 runtime-ownership guards; file-
// level "any OnStart in this file is fine" exemption is explicitly rejected.
//
// Consumed by:
//   - TestFXInvokeGuard          (fx_invoke_guard_test.go)
//   - TestLifecycleOnStartGuard  (lifecycle_onstart_guard_test.go)
//   - TestRootBridgeAllowlistIntegrity (root_bridge_allowlist_test.go)
var rootBridgeAllowlist = []rootBridgeException{
	{
		DefinitionPath: "internal/app/runner.go",
		CallSitePath:   "internal/app/app.go",
		Symbol:         "BindRuntime",
		BridgeShape:    rootBridgeShapeAppRoot,
		ExceptionClass: rootBridgeExceptionPermanent,
		Reason:         "desktop/headless app root: aggregates group:\"runners\" and calls platformrunner.RunGroup inside OnStart",
		RemoveWhen:     "n/a — permanent architectural exception",
		RollbackWhen:   "the desktop/headless app entry is redesigned to drop the singleton root runtime bridge",
		RollbackAction: "remove this entry together with internal/app/runner.go BindRuntime and its fx.Invoke call site in internal/app/app.go",
	},
	{
		DefinitionPath: "cmd/mcp-orch/runtime.go",
		CallSitePath:   "cmd/mcp-orch/fx.go",
		Symbol:         "bindRuntime",
		BridgeShape:    rootBridgeShapeOrchRoot,
		ExceptionClass: rootBridgeExceptionPermanent,
		Reason:         "mcp-orch sidecar root: aggregates group:\"runners\" and calls platformrunner.RunGroup inside OnStart",
		RemoveWhen:     "n/a — permanent architectural exception",
		RollbackWhen:   "the mcp-orch entry is redesigned to drop the singleton root runtime bridge",
		RollbackAction: "remove this entry together with cmd/mcp-orch/runtime.go bindRuntime and its fx.Invoke call site in cmd/mcp-orch/fx.go",
	},
	{
		DefinitionPath: "cmd/mcp-lsp/fx.go",
		CallSitePath:   "cmd/mcp-lsp/fx.go",
		Symbol:         "bindRuntime",
		BridgeShape:    rootBridgeShapeSidecarRoot,
		ExceptionClass: rootBridgeExceptionPermanent,
		Reason:         "mcp-lsp runner-only sidecar root: aggregates group:\"runners\" and calls platformrunner.RunGroup inside OnStart",
		RemoveWhen:     "n/a — permanent architectural exception",
		RollbackWhen:   "mcp-lsp sidecar is redesigned to drop the singleton root runtime bridge",
		RollbackAction: "remove this entry together with cmd/mcp-lsp/fx.go bindRuntime",
	},
	{
		DefinitionPath: "cmd/mcp-ida/fx.go",
		CallSitePath:   "cmd/mcp-ida/fx.go",
		Symbol:         "bindRuntime",
		BridgeShape:    rootBridgeShapeSidecarRoot,
		ExceptionClass: rootBridgeExceptionPermanent,
		Reason:         "mcp-ida runner-only sidecar root: aggregates group:\"runners\" and calls platformrunner.RunGroup inside OnStart",
		RemoveWhen:     "n/a — permanent architectural exception",
		RollbackWhen:   "mcp-ida sidecar is redesigned to drop the singleton root runtime bridge",
		RollbackAction: "remove this entry together with cmd/mcp-ida/fx.go bindRuntime",
	},
}

// rootBridgeAllowlistIntegrityViolations validates the semantic allowlist
// schema itself. It is deliberately the only "guaranteed-running" runtime-
// ownership check in P0 — matcher-level subtests are parked as t.Skip cases
// until their owning slice's PR lands the red→green flip.
func rootBridgeAllowlistIntegrityViolations(repoRoot string) []string {
	var problems []string
	seen := make(map[string]struct{}, len(rootBridgeAllowlist))
	for _, entry := range rootBridgeAllowlist {
		key := entry.DefinitionPath + "#" + entry.Symbol
		if _, dup := seen[key]; dup {
			problems = append(problems, "duplicate allowlist key: "+key)
			continue
		}
		seen[key] = struct{}{}

		if entry.DefinitionPath == "" || entry.CallSitePath == "" || entry.Symbol == "" ||
			entry.Reason == "" || entry.RemoveWhen == "" ||
			entry.RollbackWhen == "" || entry.RollbackAction == "" {
			problems = append(problems, "incomplete metadata for "+key)
		}

		switch entry.BridgeShape {
		case rootBridgeShapeAppRoot, rootBridgeShapeOrchRoot, rootBridgeShapeSidecarRoot:
		default:
			problems = append(problems, "unknown bridge_shape for "+key+": "+string(entry.BridgeShape))
		}

		switch entry.ExceptionClass {
		case rootBridgeExceptionPermanent, rootBridgeExceptionTemporary:
		default:
			problems = append(problems, "unknown exception_class for "+key+": "+string(entry.ExceptionClass))
		}

		if entry.ExceptionClass == rootBridgeExceptionTemporary && strings.HasPrefix(entry.RemoveWhen, "n/a") {
			problems = append(problems, "temporary exception must declare a concrete remove_when: "+key)
		}
		if entry.ExceptionClass == rootBridgeExceptionPermanent && !strings.HasPrefix(entry.RemoveWhen, "n/a") {
			problems = append(problems, "permanent exception must declare remove_when starting with \"n/a\": "+key)
		}

		for _, path := range []string{entry.DefinitionPath, entry.CallSitePath} {
			if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(path))); err != nil {
				problems = append(problems, "missing file referenced by allowlist: "+path+" ("+key+")")
			}
		}
	}
	return problems
}

// isRootBridgeException reports whether an fx.Invoke / OnStart call sits
// inside a known root-bridge entry. The lookup is by (path, symbol); matchers
// must resolve OnStart hooks to their enclosing function before asking.
//
// Exempting by filename alone is intentionally NOT supported — P0 §守卫改动建议
// explicitly rejects "whole-file" exemption for the root bridge.
func isRootBridgeException(path, symbol string) bool {
	for _, entry := range rootBridgeAllowlist {
		if entry.Symbol != symbol {
			continue
		}
		if entry.CallSitePath == path || entry.DefinitionPath == path {
			return true
		}
	}
	return false
}
