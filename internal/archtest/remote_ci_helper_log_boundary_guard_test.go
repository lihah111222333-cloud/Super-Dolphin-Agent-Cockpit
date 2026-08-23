package archtest

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteruntime"
)

// TestRemoteCIHelperLogBoundaryGuard keeps helper stderr local and bounded;
// only executeLifecycleCoordinator may publish remote-ci-event/v1.
func TestRemoteCIHelperLogBoundaryGuard(t *testing.T) {
	root := findRepoRoot(t)
	helper := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteruntime/helper_log.go"))
	transport := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteruntime/transport.go"))
	authority := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remotecicli/postgresql_authority.go"))
	engine := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remotecicli/execute.go"))
	runner := readRemoteCIContractGuardFile(t, filepath.Join(root, "scripts/deploy/linux/linux_runner_daemon.sh"))
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs/契约/remote-ci-eci-imagecache-contract.md"))
	codemap := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs/doc/codemap/14-remote-ci.md"))
	for _, marker := range []string{"remote-ci-helper-log/v1", "HelperLogEvidence", "MaxNodeStderrBytes", "helperLogEvidence", "wrapHelperLogError"} {
		if !strings.Contains(helper+transport, marker) {
			t.Errorf("helper log boundary is missing marker %q", marker)
		}
	}
	for _, marker := range []string{"executeLifecycleCoordinator", "remote-ci-event/v1", "appendAndWriteEvents", "writeExecuteEvent"} {
		if !strings.Contains(authority, marker) {
			t.Errorf("outer event owner is missing marker %q", marker)
		}
	}
	if !strings.Contains(engine, "remoteruntime.HelperLogSchemaVersion") {
		t.Fatal("helper-local schema is not bound into the engine ABI digest")
	}
	if !strings.Contains(runner, "remote-ci-helper-log/v1") || strings.Contains(runner, "remote-ci-event/v1") {
		t.Fatal("Linux runner helper stderr must use helper-local schema and never outer remote-ci-event/v1")
	}
	for _, marker := range []string{"remote-ci-helper-log/v1", "executeLifecycleCoordinator", "bounded", "helper stderr"} {
		if !strings.Contains(contract+codemap, marker) {
			t.Errorf("contract/codemap is missing helper boundary marker %q", marker)
		}
	}
	if strings.Contains(helper, "remote-ci-event/v1") || strings.Contains(transport, "remote-ci-event/v1") {
		t.Fatal("helper/transport production source must not claim remote-ci-event/v1 ownership")
	}
	if fields := remoteCIHelperLogFieldNames(reflect.TypeFor[remoteruntime.HelperLogEvidence]()); !reflect.DeepEqual(fields, []string{"Bytes", "Digest", "SchemaVersion"}) {
		t.Fatalf("HelperLogEvidence fields = %v, want bounded summary fields", fields)
	}
}

func remoteCIHelperLogFieldNames(typ reflect.Type) []string {
	fields := make([]string, 0, typ.NumField())
	for field := range typ.Fields() {
		fields = append(fields, field.Name)
	}
	slices.Sort(fields)
	return fields
}
