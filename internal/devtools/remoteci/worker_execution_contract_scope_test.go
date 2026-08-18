package remoteci

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestWorkerExecutionDigestScopesSQLCImplementationToSQLCInput(t *testing.T) {
	sources := func(body string) map[string][]byte {
		return map[string][]byte{
			"internal/devtools/gate/executor.go":      []byte("package gate\nfunc executeProgram() { runSQLCVerify() }\n"),
			"internal/devtools/gate/executor_sqlc.go": fmt.Appendf(nil, "package gate\nfunc runSQLCVerify() { validateTrustedGitAlternatesFile() }\nfunc validateTrustedGitAlternatesFile() { %s }\n", body),
		}
	}
	digest := func(body string) string {
		t.Helper()
		return workerExecutionSyntheticDigest(t, sources(body), []workerExecutionRoot{{directory: "internal/devtools/gate", symbol: "executeProgram"}}, false, false)
	}
	if before, after := digest("_ = 0o600"), digest("_ = 0o644"); before != after {
		t.Fatalf("SQLC-only implementation invalidated the shared worker digest: %q != %q", before, after)
	}
	beforeV4 := workerExecutionSyntheticDigestPreviousV4(t, sources("_ = 0o600"))
	afterV4 := workerExecutionSyntheticDigestPreviousV4(t, sources("_ = 0o644"))
	if beforeV4 == afterV4 {
		t.Fatal("previous unscoped v4 digest omitted the SQLC implementation change")
	}
	signatureDigest := func(parameter string) string {
		t.Helper()
		return workerExecutionSyntheticDigest(t, map[string][]byte{
			"internal/devtools/gate/executor.go":      []byte("package gate\nfunc executeProgram() { runSQLCVerify() }\n"),
			"internal/devtools/gate/executor_sqlc.go": fmt.Appendf(nil, "package gate\nfunc runSQLCVerify() { validateTrustedGitAlternatesFile(1) }\nfunc validateTrustedGitAlternatesFile(value %s) {}\n", parameter),
		}, []workerExecutionRoot{{directory: "internal/devtools/gate", symbol: "executeProgram"}}, false, false)
	}
	if before, after := signatureDigest("int"), signatureDigest("int64"); before == after {
		t.Fatal("SQLC function signature change was omitted from the shared worker digest")
	}
}

func TestWorkerExecutionDigestIgnoresDocumentationOnlyChange(t *testing.T) {
	digest := func(comment string) string {
		t.Helper()
		return workerExecutionSyntheticDigest(t, map[string][]byte{
			"fixture.go": fmt.Appendf(nil, "package fixture\n// %s\nfunc root() {}\n", comment),
		}, []workerExecutionRoot{{directory: ".", symbol: "root"}}, false, false)
	}
	if before, after := digest("old documentation"), digest("new documentation"); before != after {
		t.Fatalf("documentation-only edit invalidated worker digest: %q != %q", before, after)
	}
}

func TestSQLCWorkloadInputOwnsScopedExecutorImplementation(t *testing.T) {
	digest := func(objectID string) string {
		t.Helper()
		snapshot := &remoteGitTreeSnapshot{entries: []remoteGitTreeEntry{
			{mode: "100644", kind: "blob", objectID: objectID, path: "internal/devtools/gate/executor_sqlc_manifest.go"},
			{mode: "100644", kind: "blob", objectID: strings.Repeat("b", 40), path: "sqlc.yaml"},
		}}
		value, err := snapshot.canonicalGateInputDigest(gate.GateIDSQLCVerify)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if before, after := digest(strings.Repeat("a", 40)), digest(strings.Repeat("c", 40)); before == after {
		t.Fatal("SQLC executor implementation change was omitted from sqlc:verify input digest")
	}
}

func workerExecutionSyntheticDigestPreviousV4(t *testing.T, sources map[string][]byte) string {
	t.Helper()
	snapshot := &remoteGitTreeSnapshot{goSources: sources}
	closure, err := snapshot.resolveWorkerExecutionClosureWithRoots(
		[]workerExecutionRoot{{directory: "internal/devtools/gate", symbol: "executeProgram"}}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	assets := &workerExecutionAssets{entries: map[string]remoteGitTreeEntry{}, fragments: map[string]workerExecutionFragment{}}
	digest, err := digestWorkerExecutionClosurePreviousV4(closure, assets)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
