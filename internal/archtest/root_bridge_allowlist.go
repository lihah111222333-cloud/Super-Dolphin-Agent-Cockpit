package archtest

import (
	"os"
	"path/filepath"
	"strings"
)

// rootBridgeException 记录 root runtime bridge guard 的单个例外。
// DefinitionPath 定义桥接函数，CallSitePath 标出 fx.Invoke/OnStart 的引用点；
// 两者分开存储，避免一个字段同时承载定义和调用语义。Reason、RemoveWhen、
// RollbackWhen、RollbackAction 必须非空，让永久和临时例外都有可审计的退出条件。
// 该 allowlist 只表达 runtime ownership 语义，不与 freeze_registry 的数值冻结共用存储。
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

// rootBridgeAllowlist 枚举允许聚合 group:"runners" 并调用 platformrunner.RunGroup 的进程入口桥。
// guard 只按 path+symbol 精确放行，拒绝“整个文件都豁免”的粗粒度例外。
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

// rootBridgeAllowlistIntegrityViolations 校验 allowlist 自身的语义完整性。
// 它只检查例外记录的字段、枚举和文件存在性，不替代调用点 matcher 的行为检查。
func rootBridgeAllowlistIntegrityViolations(repoRoot string) []string {
	var problems []string
	seen := make(map[string]struct{}, len(rootBridgeAllowlist))
	for _, entry := range rootBridgeAllowlist {
		problems = append(problems, validateRootBridgeEntry(entry, seen, repoRoot)...)
	}
	return problems
}

// validateRootBridgeEntry 汇总单条 root bridge 例外的完整性检查。
// 返回所有问题而不是首个问题，方便 guard 一次报告字段、枚举和文件引用错误。
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

// rootBridgeEntryHasMetadata 确认例外条目包含审计和回滚所需的全部字段。
func rootBridgeEntryHasMetadata(entry rootBridgeException) bool {
	return entry.DefinitionPath != "" && entry.CallSitePath != "" && entry.Symbol != "" &&
		entry.Reason != "" && entry.RemoveWhen != "" &&
		entry.RollbackWhen != "" && entry.RollbackAction != ""
}

// rootBridgeEntryHasKnownShape 校验 bridge shape 是否为 guard 支持的枚举值。
func rootBridgeEntryHasKnownShape(entry rootBridgeException) bool {
	switch entry.BridgeShape {
	case rootBridgeShapeAppRoot, rootBridgeShapeOrchRoot, rootBridgeShapeSidecarRoot:
		return true
	default:
		return false
	}
}

// rootBridgeEntryHasKnownClass 校验例外类别是否为永久或临时。
func rootBridgeEntryHasKnownClass(entry rootBridgeException) bool {
	switch entry.ExceptionClass {
	case rootBridgeExceptionPermanent, rootBridgeExceptionTemporary:
		return true
	default:
		return false
	}
}

// rootBridgeEntryRemoveWhenConflict 检查 RemoveWhen 是否与例外类别一致。
// 永久例外必须显式声明 n/a；临时例外必须给出可执行的移除条件。
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

// rootBridgeEntryMissingFiles 校验 DefinitionPath 和 CallSitePath 都仍存在于仓库中。
func rootBridgeEntryMissingFiles(entry rootBridgeException, repoRoot, key string) []string {
	var out []string
	for _, path := range []string{entry.DefinitionPath, entry.CallSitePath} {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(path))); err != nil {
			out = append(out, "missing file referenced by allowlist: "+path+" ("+key+")")
		}
	}
	return out
}

// isRootBridgeException 判断指定 path+symbol 是否命中 root bridge 精确例外。
// matcher 必须先把 OnStart hook 解析到所属函数；仅按文件名豁免会扩大放行面，因此不支持。
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
