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
//     (equal to DefinitionPath when co-located; kept as a
//     separate field so one column never carries two
//     semantics — see P0 §收口口径 clarification)
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
		problems = append(problems, validateRootBridgeEntry(entry, seen, repoRoot)...)
	}
	return problems
}

// validateRootBridgeEntry runs every per-entry integrity check and returns the
// list of problems. Split out from the outer loop so each check stays small
// enough for the CC guard (P0 baseline ≤ 10 per function).
// validateRootBridgeEntry 校验根目录桥接条目。
func validateRootBridgeEntry(entry rootBridgeException, seen map[string]struct{}, repoRoot string) []string {
	key := entry.DefinitionPath + "#" + entry.Symbol
	if _, dup := seen[key]; dup {
		return []string{"duplicate allowlist key: " + key}
	}
	seen[key] = struct{}{}
	var problems []string
	if !rootBridgeEntryHasMetadata(entry) {
		problems = append(problems, "incomplete metadata for "+key)
	}
	if !rootBridgeEntryHasKnownShape(entry) {
		problems = append(problems, "unknown bridge_shape for "+key+": "+string(entry.BridgeShape))
	}
	if !rootBridgeEntryHasKnownClass(entry) {
		problems = append(problems, "unknown exception_class for "+key+": "+string(entry.ExceptionClass))
	}
	if msg := rootBridgeEntryRemoveWhenConflict(entry); msg != "" {
		problems = append(problems, msg+": "+key)
	}
	problems = append(problems, rootBridgeEntryMissingFiles(entry, repoRoot, key)...)
	return problems
}

// rootBridgeEntryHasMetadata 处理根目录桥接条目has元数据。
func rootBridgeEntryHasMetadata(entry rootBridgeException) bool {
	return entry.DefinitionPath != "" && entry.CallSitePath != "" && entry.Symbol != "" &&
		entry.Reason != "" && entry.RemoveWhen != "" &&
		entry.RollbackWhen != "" && entry.RollbackAction != ""
}

func rootBridgeEntryHasKnownShape(entry rootBridgeException) bool {
	switch entry.BridgeShape {
	case rootBridgeShapeAppRoot, rootBridgeShapeOrchRoot, rootBridgeShapeSidecarRoot:
		return true
	default:
		return false
	}
}

func rootBridgeEntryHasKnownClass(entry rootBridgeException) bool {
	switch entry.ExceptionClass {
	case rootBridgeExceptionPermanent, rootBridgeExceptionTemporary:
		return true
	default:
		return false
	}
}

// rootBridgeEntryRemoveWhenConflict returns a non-empty message when the
// entry's RemoveWhen string does not match its ExceptionClass. Permanent
// entries must declare "n/a"; temporary entries must declare something
// concrete (not prefixed with "n/a").
func rootBridgeEntryRemoveWhenConflict(entry rootBridgeException) string {
	naPrefixed := strings.HasPrefix(entry.RemoveWhen, "n/a")
	if entry.ExceptionClass == rootBridgeExceptionTemporary && naPrefixed {
		return "temporary exception must declare a concrete remove_when"
	}
	if entry.ExceptionClass == rootBridgeExceptionPermanent && !naPrefixed {
		return "permanent exception must declare remove_when starting with \"n/a\""
	}
	return ""
}

func rootBridgeEntryMissingFiles(entry rootBridgeException, repoRoot, key string) []string {
	var out []string
	for _, path := range []string{entry.DefinitionPath, entry.CallSitePath} {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(path))); err != nil {
			out = append(out, "missing file referenced by allowlist: "+path+" ("+key+")")
		}
	}
	return out
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
