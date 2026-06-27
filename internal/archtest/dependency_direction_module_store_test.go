package archtest_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var moduleStoreImportAllowlist = map[string]map[string]string{
	"internal/module/dashboard/agent_status.go": {
		internalPrefix("internal/store/agentstatus"): "D01 legacy module-to-store import",
	},
	"internal/module/dashboard/ai_logs.go": {
		internalPrefix("internal/store/ailog"): "D01 legacy module-to-store import",
	},
	"internal/module/dashboard/contract.go": {
		internalPrefix("internal/store/agentstatus"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/ailog"):       "D01 legacy module-to-store import",
		internalPrefix("internal/store/auditlog"):    "D01 legacy module-to-store import",
		internalPrefix("internal/store/buslog"):      "D01 legacy module-to-store import",
		internalPrefix("internal/store/sharedfile"):  "D01 legacy module-to-store import",
	},
	"internal/module/dashboard/factory.go": {
		internalPrefix("internal/store/ailog"):     "D01 legacy module-to-store import",
		internalPrefix("internal/store/auditlog"):  "D01 legacy module-to-store import",
		internalPrefix("internal/store/buslog"):    "D01 legacy module-to-store import",
		internalPrefix("internal/store/systemlog"): "D01 legacy module-to-store import",
	},
	"internal/module/dashboard/logs.go": {
		internalPrefix("internal/store/auditlog"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/buslog"):   "D01 legacy module-to-store import",
	},
	"internal/module/dashboard/rpc.go": {
		internalPrefix("internal/store/agentstatus"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/ailog"):       "D01 legacy module-to-store import",
		internalPrefix("internal/store/auditlog"):    "D01 legacy module-to-store import",
		internalPrefix("internal/store/buslog"):      "D01 legacy module-to-store import",
		internalPrefix("internal/store/commandcard"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/prompt"):      "D01 legacy module-to-store import",
		internalPrefix("internal/store/sharedfile"):  "D01 legacy module-to-store import",
	},
	"internal/module/dashboard/service.go": {
		internalPrefix("internal/store/agentstatus"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/ailog"):       "D01 legacy module-to-store import",
		internalPrefix("internal/store/auditlog"):    "D01 legacy module-to-store import",
		internalPrefix("internal/store/buslog"):      "D01 legacy module-to-store import",
		internalPrefix("internal/store/commandcard"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/dbquery"):     "D01 legacy module-to-store import",
		internalPrefix("internal/store/prompt"):      "D01 legacy module-to-store import",
		internalPrefix("internal/store/sharedfile"):  "D01 legacy module-to-store import",
		internalPrefix("internal/store/systemlog"):   "D01 legacy module-to-store import",
	},
	"internal/module/dashboard/ui_page.go": {
		internalPrefix("internal/store/commandcard"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/prompt"):      "D01 legacy module-to-store import",
		internalPrefix("internal/store/sharedfile"):  "D01 legacy module-to-store import",
	},
	"internal/module/dashboard/workflow_material.go": {
		internalPrefix("internal/store/sharedfile"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/intent/authoring.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/intent/commit.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/intent/dedup.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/invalidation.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/match_when_support.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/section_write.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/service.go": {
		internalPrefix("internal/store/prompt"):       "D01 legacy module-to-store import",
		internalPrefix("internal/store/sharedfile"):   "D01 legacy module-to-store import",
		internalPrefix("internal/store/uipreference"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/service_surface.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/prompt/template_sections.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/skill/personal_audit.go": {
		internalPrefix("internal/store/auditlog"): "D01 legacy module-to-store import",
	},
	"internal/module/skill/service.go": {
		internalPrefix("internal/store/auditlog"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/binding_registration.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/command.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/contract_adapter.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/events.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/factory.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/thread"):  "D01 legacy module-to-store import",
	},
	"internal/module/thread/factory_config.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/thread"):  "D01 legacy module-to-store import",
	},
	"internal/module/thread/handoffrender/text.go": {
		internalPrefix("internal/store/thread"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/history.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/thread"):  "D01 legacy module-to-store import",
	},
	"internal/module/thread/lifecycle.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/lifecycle_helpers.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/thread"):  "D01 legacy module-to-store import",
	},
	"internal/module/thread/prompt_snapshot.go": {
		internalPrefix("internal/store/thread"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/router_resolve.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/scratchpad.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/service.go": {
		internalPrefix("internal/store/binding"):    "D01 legacy module-to-store import",
		internalPrefix("internal/store/prompt"):     "D01 legacy module-to-store import",
		internalPrefix("internal/store/sharedfile"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/thread"):     "D01 legacy module-to-store import",
	},
	"internal/module/thread/service_constructor.go": {
		internalPrefix("internal/store/binding"):    "D01 legacy module-to-store import",
		internalPrefix("internal/store/prompt"):     "D01 legacy module-to-store import",
		internalPrefix("internal/store/sharedfile"): "D01 legacy module-to-store import",
		internalPrefix("internal/store/thread"):     "D01 legacy module-to-store import",
	},
	"internal/module/thread/spawn.go": {
		internalPrefix("internal/store/thread"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/start_prompt_context.go": {
		internalPrefix("internal/store/thread"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/start_session_helpers.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
	},
	"internal/module/thread/stop.go": {
		internalPrefix("internal/store/binding"): "D01 legacy module-to-store import",
	},
	"internal/module/threadprompt/default_rules_provider.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/threadprompt/providers.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/threadprompt/runtime_catalog.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
	"internal/module/threadprompt/runtime_intent.go": {
		internalPrefix("internal/store/prompt"): "D01 legacy module-to-store import",
	},
}

type moduleStoreImportCollection struct {
	Legacy         []string
	Unknown        []string
	StaleAllowlist []string
}

func TestCollectModuleStoreImports(t *testing.T) {
	legacyFile := "internal/module/thread/service.go"
	legacyImport := internalPrefix("internal/store/thread")
	unknownImport := internalPrefix("internal/store/feedback")
	allowlist := map[string]map[string]string{
		legacyFile: {
			legacyImport: "legacy fixture",
		},
	}

	t.Run("stale_when_legacy_import_removed_but_allowlist_kept", func(t *testing.T) {
		got := collectModuleStoreImports([]parsedFile{
			{RelPath: legacyFile, Imports: nil},
		}, allowlist)

		if len(got.Legacy) != 0 || len(got.Unknown) != 0 {
			t.Fatalf("unexpected non-stale results: legacy=%v unknown=%v", got.Legacy, got.Unknown)
		}
		want := moduleStoreImportDescription(legacyFile, legacyImport)
		if !slices.Contains(got.StaleAllowlist, want) {
			t.Fatalf("stale allowlist missing %q in %v", want, got.StaleAllowlist)
		}
	})

	t.Run("unknown_and_stale_survive_net_count_replacement", func(t *testing.T) {
		got := collectModuleStoreImports([]parsedFile{
			{RelPath: legacyFile, Imports: []string{unknownImport}},
		}, allowlist)

		wantUnknown := moduleStoreImportDescription(legacyFile, unknownImport)
		if !slices.Contains(got.Unknown, wantUnknown) {
			t.Fatalf("unknown import missing %q in %v", wantUnknown, got.Unknown)
		}
		wantStale := moduleStoreImportDescription(legacyFile, legacyImport)
		if !slices.Contains(got.StaleAllowlist, wantStale) {
			t.Fatalf("stale allowlist missing %q in %v", wantStale, got.StaleAllowlist)
		}
		if len(got.Legacy) != 0 {
			t.Fatalf("replacement should not leave legacy imports: %v", got.Legacy)
		}
	})
}

// assertModuleNoStoreImports 冻结 module 层现有 direct store import 面。
// 新增未知 import、遗留 allowlist 漂移或超过预算都会失败；删除遗留 import 则允许继续推进解耦。
func assertModuleNoStoreImports(t *testing.T, files []parsedFile) {
	t.Helper()
	collected := collectModuleStoreImports(files, moduleStoreImportAllowlist)
	var violations []string
	if len(collected.Unknown) > 0 {
		violations = append(violations, fmt.Sprintf("unknown module store imports (%d):\n%s", len(collected.Unknown), strings.Join(collected.Unknown, "\n")))
	}
	if len(collected.StaleAllowlist) > 0 {
		violations = append(violations, fmt.Sprintf("stale moduleStoreImportAllowlist entries (%d):\n%s", len(collected.StaleAllowlist), strings.Join(collected.StaleAllowlist, "\n")))
	}
	if len(collected.Legacy) > moduleStoreLegacyImportBudget {
		violations = append(violations, fmt.Sprintf("legacy module store imports %d exceed budget %d", len(collected.Legacy), moduleStoreLegacyImportBudget))
	}
	failIfViolations(t, violations)
}

// collectModuleStoreImports 按 file + import path 精确区分 legacy、unknown 与 stale allowlist。
// 该 collector 显式跳过测试文件与 module.go，避免 assembly/test import 污染解耦预算。
func collectModuleStoreImports(files []parsedFile, allowlist map[string]map[string]string) moduleStoreImportCollection {
	seen := make(map[string]map[string]bool)
	var collected moduleStoreImportCollection
	for _, file := range files {
		collectModuleStoreImportsFromFile(file, allowlist, seen, &collected)
	}
	appendStaleModuleStoreAllowlist(allowlist, seen, &collected)
	slices.Sort(collected.Legacy)
	slices.Sort(collected.Unknown)
	slices.Sort(collected.StaleAllowlist)
	return collected
}

func collectModuleStoreImportsFromFile(file parsedFile, allowlist map[string]map[string]string, seen map[string]map[string]bool, collected *moduleStoreImportCollection) {
	if skipModuleStoreImportFile(file.RelPath) {
		return
	}
	for _, imp := range file.Imports {
		collectModuleStoreImport(file.RelPath, imp, allowlist, seen, collected)
	}
}

func collectModuleStoreImport(relPath, imp string, allowlist map[string]map[string]string, seen map[string]map[string]bool, collected *moduleStoreImportCollection) {
	if !strings.HasPrefix(imp, moduleStoreImportPrefix) {
		return
	}
	if seen[relPath] == nil {
		seen[relPath] = make(map[string]bool)
	}
	seen[relPath][imp] = true
	if moduleStoreImportAllowed(relPath, imp, allowlist) {
		collected.Legacy = append(collected.Legacy, moduleStoreImportDescription(relPath, imp))
		return
	}
	collected.Unknown = append(collected.Unknown, moduleStoreImportDescription(relPath, imp))
}

func skipModuleStoreImportFile(relPath string) bool {
	if strings.HasSuffix(relPath, "_test.go") || filepath.Base(relPath) == "module.go" {
		return true
	}
	return !strings.HasPrefix(filepath.ToSlash(relPath), "internal/module/")
}

func moduleStoreImportAllowed(relPath, imp string, allowlist map[string]map[string]string) bool {
	allowedImports, ok := allowlist[relPath]
	if !ok {
		return false
	}
	_, ok = allowedImports[imp]
	return ok
}

func appendStaleModuleStoreAllowlist(allowlist map[string]map[string]string, seen map[string]map[string]bool, collected *moduleStoreImportCollection) {
	for relPath, imports := range allowlist {
		for imp := range imports {
			if seen[relPath] != nil && seen[relPath][imp] {
				continue
			}
			collected.StaleAllowlist = append(collected.StaleAllowlist, moduleStoreImportDescription(relPath, imp))
		}
	}
}

func moduleStoreImportDescription(relPath, imp string) string {
	return fmt.Sprintf("%s imports %s", relPath, imp)
}
