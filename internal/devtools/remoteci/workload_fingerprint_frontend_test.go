package remoteci

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
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
	criticalGuards := frontendPreflightTargetDigest(t, base, gate.FrontendPreflightTargetCriticalGuards)
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
	if got := frontendCanonicalDigest(t, changed, gate.GateIDFrontendBuild); got != build {
		t.Fatal("frontend build digest changed after an excluded E2E spec edit")
	}
	if got := frontendCanonicalDigest(t, changed, gate.GateIDFrontendPreflight); got == preflight {
		t.Fatal("frontend preflight parent digest ignored critical-guard E2E input")
	}
	if got := frontendPreflightTargetDigest(t, changed, gate.FrontendPreflightTargetCriticalGuards); got == criticalGuards {
		t.Fatal("critical guards digest ignored critical-guard E2E input")
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
	baselineDynamic := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	replaceFrontendFingerprintFile(dynamic, "docs/doc/codemap/project-map/index/app-ui.tsv", []byte("changed map size\n"))
	if got := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == baselineDynamic {
		t.Fatal("dynamic Playwright observation did not fail closed to the complete tree")
	}
}

func TestFrontendBuildFingerprintIncludesRecoveryAndRequiredDistManifest(t *testing.T) {
	baseline := frontendCanonicalDigest(t, frontendFingerprintTestSnapshot(), gate.GateIDFrontendBuild)
	for _, filePath := range []string{"frontend-app/recovery.html", "frontend-app/src/recovery-main.jsx", "frontend-app/src/recovery-production.js", "frontend-app/required-dist-entries.txt"} {
		changed := frontendFingerprintTestSnapshot()
		replaceFrontendFingerprintFile(changed, filePath, []byte("changed build input\n"))
		if got := frontendCanonicalDigest(t, changed, gate.GateIDFrontendBuild); got == baseline {
			t.Fatalf("frontend build digest ignored %s", filePath)
		}
	}
}

func TestFrontendBuildFingerprintIncludesViteImportedSpecSegmentHelper(t *testing.T) {
	snapshot := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(snapshot, "frontend-app/src/main.jsx", []byte("import './test/production-helper.js';"))
	replaceFrontendFingerprintFile(snapshot, "frontend-app/src/test/production-helper.js", []byte("export const productionHelper = true;\n"))
	baseline := frontendCanonicalDigest(t, snapshot, gate.GateIDFrontendBuild)
	replaceFrontendFingerprintFile(snapshot, "frontend-app/src/test/production-helper.js", []byte("export const productionHelper = false;\n"))
	if got := frontendCanonicalDigest(t, snapshot, gate.GateIDFrontendBuild); got == baseline {
		t.Fatal("frontend build digest ignored a Vite-imported production helper below a test-named directory")
	}
	if !frontendTestSourcePath("src/App.test.jsx") || !frontendTestSourcePath("src/__tests__/render.jsx") {
		t.Fatal("real test source naming/path was not classified as a test source")
	}
}

func TestFrontendBuildFingerprintTracksViteConfigSiblingHelper(t *testing.T) {
	snapshot := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(snapshot, "frontend-app/vite.config.js", []byte("import helper from '../scripts/build-helper.mjs'; export default { helper };"))
	replaceFrontendFingerprintFile(snapshot, "scripts/build-helper.mjs", []byte("export default true;\n"))
	baseline := frontendCanonicalDigest(t, snapshot, gate.GateIDFrontendBuild)
	replaceFrontendFingerprintFile(snapshot, "scripts/build-helper.mjs", []byte("export default false;\n"))
	if got := frontendCanonicalDigest(t, snapshot, gate.GateIDFrontendBuild); got == baseline {
		t.Fatal("frontend build digest ignored a Vite config sibling helper")
	}
}

func TestFrontendPlaywrightFingerprintHandlesAddInitScriptPath(t *testing.T) {
	static := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(static, "frontend-app/tests/e2e/business-flows.spec.js", []byte("await page.addInitScript({ path: '../../scripts/init-helper.mjs' });"))
	baseline := frontendPlaywrightDigest(t, static, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	replaceFrontendFingerprintFile(static, "frontend-app/scripts/init-helper.mjs", []byte("export const initHelper = false;"))
	if got := frontendPlaywrightDigest(t, static, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == baseline {
		t.Fatal("Playwright digest ignored a statically named addInitScript helper")
	}
	dynamic := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(dynamic, "frontend-app/tests/e2e/business-flows.spec.js", []byte("const initPath = '../../scripts/init-helper.mjs'; await page.addInitScript({ path: initPath });"))
	dynamicBaseline := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	replaceFrontendFingerprintFile(dynamic, "internal/fixture/unrelated.go", []byte("package fixture\nconst changed = true\n"))
	if got := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == dynamicBaseline {
		t.Fatal("computed addInitScript path did not fail closed to the whole tree")
	}
}

func TestFrontendPlaywrightFingerprintHandlesAddInitScriptContentRead(t *testing.T) {
	snapshot := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(snapshot, "frontend-app/tests/e2e/business-flows.spec.js", []byte("await page.addInitScript({ content: fs.readFileSync('./init-content.js', 'utf8') });"))
	replaceFrontendFingerprintFile(snapshot, "frontend-app/tests/e2e/init-content.js", []byte("export const initContent = true;\n"))
	baseline := frontendPlaywrightDigest(t, snapshot, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	replaceFrontendFingerprintFile(snapshot, "frontend-app/tests/e2e/init-content.js", []byte("export const initContent = false;\n"))
	if got := frontendPlaywrightDigest(t, snapshot, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == baseline {
		t.Fatal("Playwright digest ignored a content read by addInitScript")
	}
}

func TestFrontendPlaywrightFingerprintTracksRequireResolveDependencies(t *testing.T) {
	static := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(static, "frontend-app/tests/e2e/business-flows.spec.js", []byte("const content = fs.readFileSync(require.resolve('../../scripts/init-helper.mjs'), 'utf8');\n"))
	baseline := frontendPlaywrightDigest(t, static, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	replaceFrontendFingerprintFile(static, "frontend-app/scripts/init-helper.mjs", []byte("export const initHelper = false;\n"))
	if got := frontendPlaywrightDigest(t, static, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == baseline {
		t.Fatal("Playwright digest ignored a require.resolve helper dependency")
	}
	dynamic := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(dynamic, "frontend-app/tests/e2e/business-flows.spec.js", []byte("const helperPath = process.env.HELPER_PATH;\nfs.readFileSync(require.resolve(helperPath), 'utf8');\n"))
	dynamicBaseline := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	replaceFrontendFingerprintFile(dynamic, "internal/fixture/unrelated.go", []byte("package fixture\nconst dynamic = true\n"))
	if got := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == dynamicBaseline {
		t.Fatal("dynamic require.resolve path did not fail closed to the complete tree")
	}
}

func TestFrontendPlaywrightFingerprintInitScriptCallbackReadPolicy(t *testing.T) {
	static := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(static, "frontend-app/tests/e2e/business-flows.spec.js", []byte("await page.addInitScript(() => { window.__CAPTURE__ = { ready: true }; });\n"))
	baseline := frontendPlaywrightDigest(t, static, "tests/e2e/business-flows.spec.js#business-read-surfaces")
	replaceFrontendFingerprintFile(static, "internal/fixture/unrelated.go", []byte("package fixture\nconst staticCallback = true\n"))
	if got := frontendPlaywrightDigest(t, static, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got != baseline {
		t.Fatal("pure set-global addInitScript callback expanded to the complete tree")
	}
	for _, source := range []string{
		"await page.addInitScript(() => { await fetch('/runtime.json'); });\n",
		"await page.addInitScript(() => { const config = window.__RUNTIME_CONFIG__; window.__CAPTURE__ = config; });\n",
	} {
		dynamic := frontendFingerprintTestSnapshot()
		replaceFrontendFingerprintFile(dynamic, "frontend-app/tests/e2e/business-flows.spec.js", []byte(source))
		dynamicBaseline := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces")
		replaceFrontendFingerprintFile(dynamic, "internal/fixture/unrelated.go", []byte("package fixture\nconst dynamicCallback = true\n"))
		if got := frontendPlaywrightDigest(t, dynamic, "tests/e2e/business-flows.spec.js#business-read-surfaces"); got == dynamicBaseline {
			t.Fatalf("external-read addInitScript callback did not fail closed: %q", source)
		}
	}
}

func TestFrontendPreflightFingerprintIncludesWorkspaceAndGeneratedContractInputs(t *testing.T) {
	base := frontendFingerprintTestSnapshot()
	for _, target := range []string{gate.FrontendPreflightTargetTurnContractVerify, gate.FrontendPreflightTargetTurnContractFieldGuard} {
		baseline := frontendPreflightTargetDigest(t, base, target)
		for _, filePath := range []string{"go.work", "go.work.sum", "frontend-app/src/shared/contracts/turnContracts.generated.js"} {
			changed := frontendFingerprintTestSnapshot()
			replaceFrontendFingerprintFile(changed, filePath, []byte("changed preflight input\n"))
			if got := frontendPreflightTargetDigest(t, changed, target); got == baseline {
				t.Fatalf("%s digest ignored %s", target, filePath)
			}
		}
	}
}

func TestFrontendEmbedFingerprintIncludesGitMetadataAndEmbedOwner(t *testing.T) {
	base := frontendCanonicalDigest(t, frontendFingerprintTestSnapshot(), gate.GateIDFrontendEmbedVerify)
	for _, filePath := range []string{".gitignore", ".gitattributes", "cmd/agent-terminal/main.go"} {
		changed := frontendFingerprintTestSnapshot()
		replaceFrontendFingerprintFile(changed, filePath, []byte("changed embed input\n"))
		if got := frontendCanonicalDigest(t, changed, gate.GateIDFrontendEmbedVerify); got == base {
			t.Fatalf("frontend embed digest ignored %s", filePath)
		}
	}
}

func TestFrontendPreflightAtomicFingerprintsKeepUnrelatedTargetsReusable(t *testing.T) {
	base := frontendFingerprintTestSnapshot()
	targets := gate.FrontendPreflightTargets()
	baseline := make(map[string]string, len(targets))
	for _, target := range targets {
		baseline[target] = frontendPreflightTargetDigest(t, base, target)
	}
	assertTurnContractMutation(t, baseline)
	assertCriticalTypecheckMutation(t, baseline)
	assertRPCMutation(t, baseline)
	assertCriticalGuardE2EMutation(t, baseline)
	assertCriticalGuardActionProducerMutation(t, baseline)
	assertTurnFieldSourceMutation(t, baseline)
	assertNewFrontendSourceMutation(t, baseline)
	assertRPCExcludesUnrelatedMutation(t, baseline)
	assertDependencyMutation(t, baseline)
}

func TestFrontendSuiteFallbackFingerprintsBindRealFrontendClosure(t *testing.T) {
	changedID := frontendVitestWorkloadID(t, gate.GateIDFrontendTest, gate.FrontendChangedSuiteCarrierTarget)
	fullID := frontendVitestWorkloadID(t, gate.GateIDFrontendFullTest, gate.FrontendFullSuiteCarrierTarget)
	if changedID == fullID {
		t.Fatal("changed and full suite workload identities unexpectedly match")
	}
	base := frontendFingerprintTestSnapshot()
	changedDigest := frontendWorkloadInputDigest(t, base, changedID)
	fullDigest := frontendWorkloadInputDigest(t, base, fullID)
	if changedDigest != fullDigest {
		t.Fatal("changed and full suite closures unexpectedly differ for the same frontend non-E2E source")
	}
	mutatedSource := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(mutatedSource, "frontend-app/src/production.js", []byte("export const production = false;\n"))
	if frontendWorkloadInputDigest(t, mutatedSource, changedID) == changedDigest || frontendWorkloadInputDigest(t, mutatedSource, fullID) == fullDigest {
		t.Fatal("frontend suite closure ignored a non-E2E source mutation")
	}
	mutatedE2E := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(mutatedE2E, "frontend-app/tests/e2e/business-flows.spec.js", []byte("test('unrelated e2e', async () => {});\n"))
	if got := frontendWorkloadInputDigest(t, mutatedE2E, changedID); got != changedDigest {
		t.Fatalf("changed suite closure changed after unrelated E2E mutation: %q != %q", got, changedDigest)
	}
	if got := frontendWorkloadInputDigest(t, mutatedE2E, fullID); got != fullDigest {
		t.Fatalf("full suite closure changed after unrelated E2E mutation: %q != %q", got, fullDigest)
	}
}

func frontendVitestWorkloadID(t *testing.T, parent gate.GateID, target string) string {
	t.Helper()
	return string(parent) + "::vitest-file::" + base64.RawURLEncoding.EncodeToString([]byte(target))
}

func TestFrontendPreflightCarrierFingerprintUsesGuardClosure(t *testing.T) {
	base := frontendFingerprintTestSnapshot()
	carrier, err := gate.FrontendPreflightCarrierTarget(gate.FrontendPreflightTargetTurnContractVerify)
	if err != nil {
		t.Fatal(err)
	}
	id := frontendVitestWorkloadID(t, gate.GateIDFrontendTest, carrier)
	baseline := frontendWorkloadInputDigest(t, base, id)
	changed := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(changed, "scripts/turncontract/verify.go", []byte("package main\nconst verify = false\n"))
	if got := frontendWorkloadInputDigest(t, changed, id); got == baseline {
		t.Fatal("frontend preflight carrier fingerprint ignored its real guard input")
	}
}

func frontendWorkloadInputDigest(t *testing.T, snapshot *remoteGitTreeSnapshot, id string) string {
	t.Helper()
	digest, err := snapshot.workloadInputDigest(context.Background(), gate.Workload{ID: id})
	if err != nil {
		t.Fatalf("workloadInputDigest(%s): %v", id, err)
	}
	return digest
}

func assertTurnContractMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	turnChanged := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(turnChanged, "scripts/turncontract/verify.go", []byte("package main\nconst changed = true\n"))
	for _, target := range []string{gate.FrontendPreflightTargetTurnContractVerify, gate.FrontendPreflightTargetTurnContractFieldGuard} {
		if got := frontendPreflightTargetDigest(t, turnChanged, target); got == baseline[target] {
			t.Fatalf("%s digest ignored its turn-contract input change", target)
		}
	}
	assertUnchangedTargets(t, baseline, turnChanged, []string{gate.FrontendPreflightTargetCriticalGuards, gate.FrontendPreflightTargetCriticalTypecheck,
		gate.FrontendPreflightTargetContractsVitest, gate.FrontendPreflightTargetRPCAudit, gate.FrontendPreflightTargetDependencyContract}, "turn-contract")
}

func assertCriticalTypecheckMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	typecheckChanged := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(typecheckChanged, "frontend-app/scripts/critical-typecheck-guard.mjs", []byte("export const changed = true;\n"))
	for _, target := range []string{gate.FrontendPreflightTargetCriticalGuards, gate.FrontendPreflightTargetCriticalTypecheck,
		gate.FrontendPreflightTargetContractsVitest} {
		if got := frontendPreflightTargetDigest(t, typecheckChanged, target); got == baseline[target] {
			t.Fatalf("%s digest ignored its critical typecheck input change", target)
		}
	}
	assertUnchangedTargets(t, baseline, typecheckChanged, []string{gate.FrontendPreflightTargetTurnContractVerify, gate.FrontendPreflightTargetTurnContractFieldGuard,
		gate.FrontendPreflightTargetRPCAudit, gate.FrontendPreflightTargetDependencyContract}, "critical typecheck")
}

func assertRPCMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	rpcChanged := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(rpcChanged, "frontend-app/src/shared/api/backend/backendApi.js", []byte("export const rpcChanged = true;\n"))
	for _, target := range []string{gate.FrontendPreflightTargetCriticalGuards, gate.FrontendPreflightTargetCriticalTypecheck,
		gate.FrontendPreflightTargetContractsVitest, gate.FrontendPreflightTargetRPCAudit,
		gate.FrontendPreflightTargetTurnContractFieldGuard} {
		if got := frontendPreflightTargetDigest(t, rpcChanged, target); got == baseline[target] {
			t.Fatalf("%s digest ignored its RPC input change", target)
		}
	}
	assertUnchangedTargets(t, baseline, rpcChanged, []string{gate.FrontendPreflightTargetTurnContractVerify,
		gate.FrontendPreflightTargetDependencyContract}, "RPC")
}

func assertCriticalGuardE2EMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	changed := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(changed, "frontend-app/tests/e2e/business-flows.spec.js", []byte("test('changed critical guard input', async () => {});\n"))
	if got := frontendPreflightTargetDigest(t, changed, gate.FrontendPreflightTargetCriticalGuards); got == baseline[gate.FrontendPreflightTargetCriticalGuards] {
		t.Fatal("critical guards digest ignored a tests/e2e input change")
	}
	assertUnchangedTargets(t, baseline, changed, []string{gate.FrontendPreflightTargetTurnContractVerify,
		gate.FrontendPreflightTargetTurnContractFieldGuard, gate.FrontendPreflightTargetCriticalTypecheck,
		gate.FrontendPreflightTargetContractsVitest, gate.FrontendPreflightTargetRPCAudit,
		gate.FrontendPreflightTargetDependencyContract}, "critical guard E2E")
}

func assertCriticalGuardActionProducerMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	for _, filePath := range []string{"frontend-app/config/action-producer-registry.json", "frontend-app/config/action-producer-test-matrix.json"} {
		changed := frontendFingerprintTestSnapshot()
		replaceFrontendFingerprintFile(changed, filePath, []byte(`{"changed":true}`))
		if got := frontendPreflightTargetDigest(t, changed, gate.FrontendPreflightTargetCriticalGuards); got == baseline[gate.FrontendPreflightTargetCriticalGuards] {
			t.Fatalf("critical guards digest ignored its action-producer config input %s", filePath)
		}
		assertUnchangedTargets(t, baseline, changed, []string{gate.FrontendPreflightTargetTurnContractVerify,
			gate.FrontendPreflightTargetTurnContractFieldGuard, gate.FrontendPreflightTargetRPCAudit,
			gate.FrontendPreflightTargetDependencyContract}, "critical action-producer config")
	}
}

func assertTurnFieldSourceMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	changed := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(changed, "frontend-app/src/production.js", []byte("export const production = false;\n"))
	for _, target := range []string{gate.FrontendPreflightTargetCriticalGuards,
		gate.FrontendPreflightTargetTurnContractFieldGuard, gate.FrontendPreflightTargetCriticalTypecheck,
		gate.FrontendPreflightTargetContractsVitest, gate.FrontendPreflightTargetRPCAudit} {
		if got := frontendPreflightTargetDigest(t, changed, target); got == baseline[target] {
			t.Fatalf("%s digest ignored a frontend-app/src production consumer change", target)
		}
	}
	assertUnchangedTargets(t, baseline, changed, []string{gate.FrontendPreflightTargetTurnContractVerify,
		gate.FrontendPreflightTargetDependencyContract}, "turn field source")
}

func assertNewFrontendSourceMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	changed := frontendFingerprintTestSnapshot()
	// A future registry/list addition must be observed even when its path was
	// absent from the original fixture; the closure is derived from the real
	// observer roots rather than a frozen file-name prefix list.
	replaceFrontendFingerprintFile(changed, "frontend-app/src/new-contract-consumer.js", []byte("export const newConsumer = true;\n"))
	for _, target := range []string{gate.FrontendPreflightTargetCriticalGuards,
		gate.FrontendPreflightTargetTurnContractFieldGuard, gate.FrontendPreflightTargetCriticalTypecheck,
		gate.FrontendPreflightTargetContractsVitest, gate.FrontendPreflightTargetRPCAudit} {
		if got := frontendPreflightTargetDigest(t, changed, target); got == baseline[target] {
			t.Fatalf("%s digest ignored a newly added frontend-app/src consumer", target)
		}
	}
	assertUnchangedTargets(t, baseline, changed, []string{gate.FrontendPreflightTargetTurnContractVerify,
		gate.FrontendPreflightTargetDependencyContract}, "new frontend source")
}

func assertRPCExcludesUnrelatedMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	for _, filePath := range []string{"frontend-app/public/wails/runtime.js", "frontend-app/config/vitest-suite-policy.json", "frontend-app/index.html"} {
		changed := frontendFingerprintTestSnapshot()
		replaceFrontendFingerprintFile(changed, filePath, []byte("rpc-unrelated-change\n"))
		if got := frontendPreflightTargetDigest(t, changed, gate.FrontendPreflightTargetRPCAudit); got != baseline[gate.FrontendPreflightTargetRPCAudit] {
			t.Fatalf("RPC audit digest changed after unrelated %s edit", filePath)
		}
	}
}

func assertDependencyMutation(t *testing.T, baseline map[string]string) {
	t.Helper()
	dependencyChanged := frontendFingerprintTestSnapshot()
	replaceFrontendFingerprintFile(dependencyChanged, "frontend-app/package-lock.json", []byte(`{"lockfileVersion":3,"changed":true}`))
	if got := frontendPreflightTargetDigest(t, dependencyChanged, gate.FrontendPreflightTargetDependencyContract); got == baseline[gate.FrontendPreflightTargetDependencyContract] {
		t.Fatal("dependency contract digest ignored package-lock input change")
	}
	for _, target := range gate.FrontendPreflightTargets() {
		if got := frontendPreflightTargetDigest(t, dependencyChanged, target); got == baseline[target] {
			t.Fatalf("%s digest ignored shared package-lock input change", target)
		}
	}
}

func assertUnchangedTargets(t *testing.T, baseline map[string]string, snapshot *remoteGitTreeSnapshot, targets []string, mutation string) {
	t.Helper()
	for _, target := range targets {
		if got := frontendPreflightTargetDigest(t, snapshot, target); got != baseline[target] {
			t.Fatalf("%s digest changed after unrelated %s input edit", target, mutation)
		}
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

func frontendPreflightTargetDigest(t *testing.T, snapshot *remoteGitTreeSnapshot, target string) string {
	t.Helper()
	digest, err := snapshot.frontendPreflightInputDigest(target)
	if err != nil {
		t.Fatalf("frontendPreflightInputDigest(%s): %v", target, err)
	}
	return digest
}

func frontendFingerprintTestSnapshot() *remoteGitTreeSnapshot {
	sources := map[string][]byte{
		"frontend-app/package.json":                                    []byte(`{"scripts":{"dev":"vite"}}`),
		"frontend-app/package-lock.json":                               []byte(`{"lockfileVersion":3}`),
		"frontend-app/recovery.html":                                   []byte(`<script type="module" src="/src/recovery-main.jsx"></script>`),
		"frontend-app/required-dist-entries.txt":                       []byte("index.html\nrecovery.html\n"),
		"frontend-app/src/shared/contracts/turnContracts.generated.js": []byte("export const generatedSchemas = {};"),
		"frontend-app/scripts/init-helper.mjs":                         []byte("export const initHelper = true;"),
		"go.work":                                                      []byte("go 1.26\n"),
		"go.work.sum":                                                  []byte("workspace\n"),
		".gitignore":                                                   []byte("cmd/agent-terminal/web-dist/\n"),
		".gitattributes":                                               []byte("* text=auto\n"),
		"cmd/agent-terminal/main.go":                                   []byte("package main\n"),
		"frontend-app/scripts/critical-typecheck-guard.mjs":            []byte(`export const criticalTypecheck = true;`),
		"frontend-app/scripts/remote-suite-carriers/changed.test.mjs":  []byte(`// protocol carrier`),
		"frontend-app/scripts/remote-suite-carriers/full.test.mjs":     []byte(`// protocol carrier`),
		"frontend-app/scripts/remote-preflight-carriers/turncontract-verify.test.mjs": []byte(`// protocol carrier`),
		"frontend-app/src/shared/api/backend/backendApi.js":                           []byte(`export const backendApi = true;`),
		"scripts/turncontract/verify.go":                                              []byte("package main\nconst verify = true\n"),
		"frontend-app/index.html":                                                     []byte(`<script type="module" src="/src/main.jsx"></script>`),
		"frontend-app/vite.config.js":                                                 []byte(`import policy from './config/vitest-suite-policy.json'; export default { test: { policy } };`),
		"frontend-app/config/vitest-suite-policy.json":                                []byte(`{"schemaVersion":1}`),
		"frontend-app/public/wails/runtime.js":                                        []byte(`export const runtime = true;`),
		"frontend-app/src/main.jsx":                                                   []byte(`import './production.js';`),
		"frontend-app/src/production.js":                                              []byte(`export const production = true;`),
		"frontend-app/src/recovery-main.jsx":                                          []byte(`import './recovery-production.js';`),
		"frontend-app/src/recovery-production.js":                                     []byte(`export const recoveryProduction = true;`),
		"frontend-app/src/unit.test.js":                                               []byte(`test('unit', () => {});`),
		"frontend-app/tests/e2e/business-flows.spec.js":                               []byte(`import { expect, test } from '@playwright/test'; test('business', async () => {});`),
		"frontend-app/tests/e2e/desktop-wide.spec.js":                                 []byte(`import { helper } from '../../scripts/agentic-e2e-helper.mjs'; test('desktop', async () => helper);`),
		"frontend-app/playwright.business-flows.config.js":                            []byte(`import { defineConfig } from '@playwright/test'; export default defineConfig({});`),
		"frontend-app/playwright.desktop-wide.config.js":                              []byte(`import { defineConfig } from '@playwright/test'; export default defineConfig({});`),
		"frontend-app/scripts/agentic-e2e-helper.mjs":                                 []byte(`export const helper = 1;`),
		"frontend-app/tests/e2e/other.spec.js":                                        []byte(`test('other', async () => {});`),
		"docs/doc/codemap/project-map/index/app-ui.tsv":                               []byte("size_bytes\t123\n"),
		"docs/doc/codemap/project-map/AI_PROJECT_MAP.md":                              []byte("map\n"),
		"scripts/codemap_policy.txt":                                                  []byte("schema\t1\n"),
		"scripts/generate_ai_project_map.mjs":                                         []byte("generator\n"),
		"internal/devtools/projectmaptrusted/project_map.go":                          []byte("package projectmaptrusted\n"),
		"cmd/super-dolphin-gate/project_map_cli.go":                                   []byte("package main\n"),
		"internal/fixture/unrelated.go":                                               []byte("package fixture\n"),
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
