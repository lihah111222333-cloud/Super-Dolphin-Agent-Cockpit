package remoteci

import (
	"context"
	"crypto/sha1"
	"fmt"
	"sort"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestFrontendRetryFingerprintsSeparateE2ESpecsFromVitestAndBuild(t *testing.T) {
	base := frontendFingerprintTestSnapshot()
	business := frontendPlaywrightDigest(t, base, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	desktop := frontendPlaywrightDigest(t, base, "tests/e2e/desktop-wide.spec.js#desktop-shell")
	vitest := frontendVitestDigest(t, base)
	build := frontendCanonicalDigest(t, base, gate.GateIDFrontendBuild)
	preflight := frontendCanonicalDigest(t, base, gate.GateIDFrontendPreflight)
	lint := frontendCanonicalDigest(t, base, gate.GateIDFrontendLint)
	parent := frontendCanonicalDigest(t, base, gate.GateIDFrontendE2E)

	changed := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(changed, "frontend-app/tests/e2e/business-flows.spec.js", []byte("import { expect, test } from '@playwright/test';\ntest('business changed', async () => {});\n"))
	if got := frontendPlaywrightDigest(t, changed, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == business {
		t.Fatal("business Playwright target reused digest after its spec changed")
	}
	if got := frontendPlaywrightDigest(t, changed, "tests/e2e/desktop-wide.spec.js#desktop-shell"); got != desktop {
		t.Fatal("desktop Playwright target changed after an unrelated business spec edit")
	}
	if got := frontendVitestDigest(t, changed); got != vitest {
		t.Fatal("Vitest digest changed after an excluded E2E spec edit")
	}
	configChanged := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(configChanged, "frontend-app/playwright.business-flows.config.js", []byte("export default {};\n"))
	if got := frontendVitestDigest(t, configChanged); got != vitest {
		t.Fatal("Vitest digest changed after an excluded Playwright config edit")
	}
	for _, gateID := range []gate.GateID{gate.GateIDFrontendBuild, gate.GateIDFrontendPreflight} {
		if got := frontendCanonicalDigest(t, changed, gateID); got != map[gate.GateID]string{gate.GateIDFrontendBuild: build, gate.GateIDFrontendPreflight: preflight}[gateID] {
			t.Fatalf("%s digest changed after an excluded E2E spec edit", gateID)
		}
	}
	if got := frontendCanonicalDigest(t, changed, gate.GateIDFrontendLint); got == lint {
		t.Fatal("frontend lint digest ignored its eslint-visible E2E spec edit")
	}
	if got := frontendCanonicalDigest(t, changed, gate.GateIDFrontendE2E); got == parent {
		t.Fatal("FrontendE2E parent digest ignored its changed target")
	}
}

func TestFrontendRetryFingerprintsStaticImportsAndDynamicObservers(t *testing.T) {
	base := frontendFingerprintTestSnapshot()
	desktop := frontendPlaywrightDigest(t, base, "tests/e2e/desktop-wide.spec.js#desktop-shell")
	business := frontendPlaywrightDigest(t, base, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	changedHelper := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(changedHelper, "frontend-app/scripts/agentic-e2e-helper.mjs", []byte("export const helper = 2;\n"))
	if got := frontendPlaywrightDigest(t, changedHelper, "tests/e2e/desktop-wide.spec.js#desktop-shell"); got == desktop {
		t.Fatal("desktop target omitted a changed statically imported helper")
	}
	if got := frontendPlaywrightDigest(t, changedHelper, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got != business {
		t.Fatal("business target changed after an unrelated desktop helper edit")
	}
	dynamic := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(dynamic, "frontend-app/tests/e2e/business-flows.spec.js", []byte("const moduleName = './unknown.mjs';\nawait import(moduleName);\n"))
	baselineDynamic := frontendPlaywrightDigest(t, frontendFingerprintTestSnapshot(), "tests/e2e/business-flows.spec.js#business-read-surfaces")
	replaceFrontendFingerprintFile(dynamic, "docs/doc/codemap/project-map/index/app-ui.tsv", []byte("changed map size\n"))
	if got := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == baselineDynamic {
		t.Fatal("dynamic Playwright observation did not fail closed to the complete tree")
	}
}

func TestFrontendRetryFingerprintsProjectMapOnlyForProjectMapGate(t *testing.T) {
	base := frontendFingerprintTestSnapshot()
	projectMap := frontendCanonicalDigest(t, base, gate.GateIDProjectMapCheck)
	vitest := frontendVitestDigest(t, base)
	playwright := frontendPlaywrightDigest(t, base, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	desktop := frontendPlaywrightDigest(t, base, "tests/e2e/desktop-wide.spec.js#desktop-shell")
	changed := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(changed, "docs/doc/codemap/project-map/index/app-ui.tsv", []byte("changed size\n"))
	if got := frontendCanonicalDigest(t, changed, gate.GateIDProjectMapCheck); got == projectMap {
		t.Fatal("project-map digest ignored a generated app-ui index change")
	}
	if got := frontendVitestDigest(t, changed); got != vitest {
		t.Fatal("Vitest digest changed after a project-map-only edit")
	}
	if got := frontendPlaywrightDigest(t, changed, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got != playwright {
		t.Fatal("Playwright digest changed after a project-map-only edit")
	}
	if got := frontendPlaywrightDigest(t, changed, "tests/e2e/desktop-wide.spec.js#desktop-shell"); got != desktop {
		t.Fatal("desktop Playwright digest changed after a project-map-only edit")
	}
	sourceChanged := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(sourceChanged, "internal/fixture/unrelated.go", []byte("package fixture\n\nconst changed = true\n"))
	if got := frontendCanonicalDigest(t, sourceChanged, gate.GateIDProjectMapCheck); got == projectMap {
		t.Fatal("project-map digest reused after the filesystem-scanned source tree changed")
	}
}

func frontendPlaywrightDigest(t *testing.T, snapshot *remoteGitTreeSnapshot, target string) string {
	t.Helper()
	digest, err := snapshot.frontendPlaywrightInputDigest(context.Background(), target)
	if err != nil {
		t.Fatalf("frontendPlaywrightInputDigest(%s): %v", target, err)
	}
	return digest
}

func frontendVitestDigest(t *testing.T, snapshot *remoteGitTreeSnapshot) string {
	t.Helper()
	digest, err := snapshot.vitestInputDigest("src/unit.test.js")
	if err != nil {
		t.Fatalf("vitestInputDigest(): %v", err)
	}
	return digest
}

func frontendCanonicalDigest(t *testing.T, snapshot *remoteGitTreeSnapshot, gateID gate.GateID) string {
	t.Helper()
	digest, err := snapshot.canonicalGateInputDigest(gateID)
	if err != nil {
		t.Fatalf("canonicalGateInputDigest(%s): %v", gateID, err)
	}
	return digest
}

func frontendFingerprintTestSnapshot() *remoteGitTreeSnapshot {
	sources := map[string][]byte{
		"frontend-app/package.json":                          []byte(`{"scripts":{"dev":"vite"}}`),
		"frontend-app/package-lock.json":                     []byte(`{"lockfileVersion":3}`),
		"frontend-app/index.html":                            []byte(`<script type="module" src="/src/main.jsx"></script>`),
		"frontend-app/vite.config.js":                        []byte(`import policy from './config/vitest-suite-policy.json'; export default { test: { policy } };`),
		"frontend-app/config/vitest-suite-policy.json":       []byte(`{"schemaVersion":1}`),
		"frontend-app/public/wails/runtime.js":               []byte(`export const runtime = true;`),
		"frontend-app/src/main.jsx":                          []byte(`import './production.js';`),
		"frontend-app/src/production.js":                     []byte(`export const production = true;`),
		"frontend-app/src/unit.test.js":                      []byte(`test('unit', () => {});`),
		"frontend-app/tests/e2e/business-flows.spec.js":      []byte(`import { expect, test } from '@playwright/test'; test('business', async () => {});`),
		"frontend-app/tests/e2e/desktop-wide.spec.js":        []byte(`import { helper } from '../../scripts/agentic-e2e-helper.mjs'; test('desktop', async () => helper);`),
		"frontend-app/playwright.business-flows.config.js":   []byte(`import { defineConfig } from '@playwright/test'; export default defineConfig({});`),
		"frontend-app/playwright.desktop-wide.config.js":     []byte(`import { defineConfig } from '@playwright/test'; export default defineConfig({});`),
		"frontend-app/scripts/agentic-e2e-helper.mjs":        []byte(`export const helper = 1;`),
		"frontend-app/tests/e2e/other.spec.js":               []byte(`test('other', async () => {});`),
		"docs/doc/codemap/project-map/index/app-ui.tsv":      []byte("size_bytes\t123\n"),
		"docs/doc/codemap/project-map/AI_PROJECT_MAP.md":     []byte("map\n"),
		"scripts/codemap_policy.txt":                         []byte("schema\t1\n"),
		"scripts/generate_ai_project_map.mjs":                []byte("generator\n"),
		"internal/devtools/projectmaptrusted/project_map.go": []byte("package projectmaptrusted\n"),
		"cmd/super-dolphin-gate/project_map_cli.go":          []byte("package main\n"),
		"internal/fixture/unrelated.go":                      []byte("package fixture\n"),
	}
	entries := make([]remoteGitTreeEntry, 0, len(sources))
	byPath := make(map[string]remoteGitTreeEntry, len(sources))
	paths := make([]string, 0, len(sources))
	for filePath := range sources {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		source := sources[filePath]
		sum := sha1.Sum(source)
		entry := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: fmt.Sprintf("%x", sum), path: filePath}
		entries = append(entries, entry)
		byPath[filePath] = entry
	}
	return &remoteGitTreeSnapshot{entries: entries, byPath: byPath, frontendSources: sources}
}

func replaceFrontendFingerprintFile(snapshot *remoteGitTreeSnapshot, filePath string, source []byte) {
	sum := sha1.Sum(source)
	entry := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: fmt.Sprintf("%x", sum), path: filePath}
	snapshot.byPath[filePath] = entry
	snapshot.frontendSources[filePath] = source
	for index, existing := range snapshot.entries {
		if existing.path == filePath {
			snapshot.entries[index] = entry
			return
		}
	}
	snapshot.entries = append(snapshot.entries, entry)
}
