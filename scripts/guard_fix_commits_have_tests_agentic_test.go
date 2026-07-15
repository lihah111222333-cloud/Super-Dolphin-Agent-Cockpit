package main

import (
	"strings"
	"testing"
)

func TestFixCommitAllowsMJSRegressionTest(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	writeFixTestGuardFile(t, root, "frontend-app/scripts/agentic-e2e.mjs", "export function probe() { return 'ok'; }\n")
	writeFixTestGuardFile(t, root, "frontend-app/scripts/agentic-e2e.test.mjs", "import { probe } from './agentic-e2e.mjs';\nif (probe() !== 'ok') throw new Error('probe failed');\n")
	runFixTestGuardGit(t, root, "add", ".")
	runFixTestGuardGit(t, root, "commit", "-m", "fix: 修复 agentic e2e 证据")

	out, err := runFixTestGuard(t, root, "--range", "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("guard rejected mjs regression test: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fix-test guard OK") {
		t.Fatalf("output missing success marker\n%s", out)
	}
}

func TestFixCommitAllowsFrontendScriptIntegrationTest(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	writeFixTestGuardFile(t, root, "frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx", "export function ProviderSettingsPanel() { return null; }\n")
	writeFixTestGuardFile(t, root, "frontend-app/scripts/agentic-e2e.test.mjs", "const source = 'settings-provider-writable-roots';\nif (!source.includes('settings-provider')) throw new Error('missing provider anchor');\n")
	runFixTestGuardGit(t, root, "add", ".")
	runFixTestGuardGit(t, root, "commit", "-m", "fix: 恢复设置根目录占位提示")

	out, err := runFixTestGuard(t, root, "--range", "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("guard rejected frontend script integration test: %v\n%s", err, out)
	}
	if !strings.Contains(out, "fix-test guard OK") {
		t.Fatalf("output missing success marker\n%s", out)
	}
}
