package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLSPIdleTimeoutWiringGuard keeps the resolved LSP idle timeout as a
// single producer-to-consumer contract across runtime, manager, adapter and lifecycle paths.
func TestMultiLSPIdleTimeoutWiringGuard(t *testing.T) {
	checks := map[string][]string{
		"internal/contract/config.go": {
			"IdleTimeout                      time.Duration",
		},
		"cmd/mcp-lsp/runtime.go": {
			"requireResolvedLSPConfig(cfg.LSP)",
			"resolvedLSPConfig.IdleTimeout",
			"createFallbackManager(adapters, root, log, idleTimeout)",
			"createGenericManagerWithBinary(adapter, adapters, root, log, idleTimeout",
			"multilsp.NewManagerWithError",
		},
		"cmd/mcp-lsp/multilsp/manager.go": {
			"IdleTimeout                      time.Duration",
			"idleTimeout                      time.Duration",
			"func NewManagerWithError(cfg Config) (Manager, error)",
			"clone.idleTimeout = m.idleTimeout",
		},
		"cmd/mcp-lsp/multilsp/language_service_config.go": {
			"idleTimeout:      cfg.IdleTimeout",
		},
		"cmd/mcp-lsp/multilsp/adapter.go": {
			"goplsServerArgs(a.idleTimeout, runtime.GOOS)",
		},
		"cmd/mcp-lsp/multilsp/recycler.go": {
			"timeout := mgr.idleTimeout",
			"r.pool.primary.idleTimeout",
		},
		"cmd/mcp-lsp/multilsp/recycler_lifecycle.go": {
			"idleEligible(current, now, mgr.idleTimeout)",
			"idleEligible(workspace, mgr.managerNow(), mgr.idleTimeout)",
		},
		"cmd/mcp-lsp/multilsp/release_scope.go": {
			"idleEligible(workspace, now, mgr.idleTimeout)",
		},
		"cmd/mcp-lsp/multilsp/pool.go": {
			"idleEligible(workspace, now, mgr.idleTimeout)",
			"return idleEligible(workspace, now, mgr.idleTimeout)",
		},
	}

	for relative, required := range checks {
		data, err := os.ReadFile(filepath.Join("..", "..", relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		text := string(data)
		for _, token := range required {
			if !strings.Contains(text, token) {
				t.Errorf("%s missing idle-timeout wiring token %q", relative, token)
			}
		}
	}

	forbidden := map[string][]string{
		"cmd/mcp-lsp/multilsp/recycler.go": {
			"func idleTimeoutForLanguage(",
			"idleTimeout                    =",
		},
		"cmd/mcp-lsp/multilsp/pool.go":               {"idleTimeoutForLanguage("},
		"cmd/mcp-lsp/multilsp/recycler_lifecycle.go": {"idleTimeoutForLanguage("},
		"cmd/mcp-lsp/multilsp/release_scope.go":      {"idleTimeoutForLanguage("},
	}
	for relative, tokens := range forbidden {
		data, err := os.ReadFile(filepath.Join("..", "..", relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		text := string(data)
		for _, token := range tokens {
			if strings.Contains(text, token) {
				t.Errorf("%s retains forbidden independent idle-timeout source %q", relative, token)
			}
		}
	}
}
