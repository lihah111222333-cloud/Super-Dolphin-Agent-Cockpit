package archtest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCIIdentityAndFingerprintDomainsHaveSingleOwners guards protocol-domain
// ownership and the field/material links consumed by gate and remoteci.
func TestRemoteCIIdentityAndFingerprintDomainsHaveSingleOwners(t *testing.T) {
	root := findRepoRoot(t)
	assertRemoteCIIdentityDomainConstants(t)
	sources := assertRemoteCIIdentityDomainBindings(t, root)
	assertRemoteCIIdentityDomainLiterals(t, sources)
	assertRemoteCIEnvironmentReplayDomainOwner(t, root)
}

// assertRemoteCIIdentityDomainConstants 锁定身份、输入指纹与来源重放域的唯一版本。
func assertRemoteCIIdentityDomainConstants(t *testing.T) {
	t.Helper()
	if got, want := cicontract.WorkloadPassIdentityDomain, "pass-identity/v2"; got != want {
		t.Fatalf("PASS identity domain = %q, want %q", got, want)
	}
	if got, want := cicontract.WorkloadInputFingerprintDomain, "input-fingerprint/v2"; got != want {
		t.Fatalf("input fingerprint domain = %q, want %q", got, want)
	}
	if got, want := cicontract.WorkloadInputFingerprintSchemaVersion, uint32(4); got != want {
		t.Fatalf("input fingerprint schema = %d, want %d", got, want)
	}
	if got, want := cicontract.WorkloadPassSourceReplayDomain, "workload-pass-source-replay/v1"; got != want {
		t.Fatalf("PASS source replay domain = %q, want %q", got, want)
	}
	if got, want := cicontract.WorkloadPassEnvironmentReplayDomain, "workload-pass-environment-replay/v1"; got != want {
		t.Fatalf("PASS environment replay domain = %q, want %q", got, want)
	}
}

// assertRemoteCIEnvironmentReplayDomainOwner 拒绝在 cicontract 之外复制 origin-tree proof 域字面量。
func assertRemoteCIEnvironmentReplayDomainOwner(t *testing.T, root string) {
	t.Helper()
	const literal = `"workload-pass-environment-replay/v1"`
	scanGoFiles(t, root, func(relative, path string) {
		if strings.HasPrefix(relative, "internal/devtools/cicontract/") || strings.HasSuffix(relative, "_test.go") {
			return
		}
		source := readRemoteCIContractGuardFile(t, path)
		if strings.Contains(source, literal) {
			t.Errorf("%s copies the environment replay domain outside cicontract", relative)
		}
	})
}

// assertRemoteCIIdentityDomainBindings 证明生产材料仅引用 cicontract 的域所有者。
func assertRemoteCIIdentityDomainBindings(t *testing.T, root string) map[string]string {
	t.Helper()
	identity := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_pass_evidence.go"))
	if !strings.Contains(identity, "Domain:            cicontract.WorkloadPassIdentityDomain") {
		t.Fatal("PASS identity material is not bound to cicontract.WorkloadPassIdentityDomain")
	}
	fingerprint := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/workload_fingerprint_git.go"))
	if !strings.Contains(fingerprint, "cicontract.WorkloadInputFingerprintDomain") {
		t.Fatal("input fingerprint material is not bound to cicontract.WorkloadInputFingerprintDomain")
	}
	owner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/workload_fingerprint.go"))
	if !strings.Contains(owner, "remoteWorkloadInputSchemaVersion = cicontract.WorkloadInputFingerprintSchemaVersion") {
		t.Fatal("internal fingerprint schema 4 is not sourced from cicontract")
	}
	replay := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_pass_source_replay.go"))
	if !strings.Contains(replay, "Domain:                   cicontract.WorkloadPassSourceReplayDomain") {
		t.Fatal("PASS source replay material is not bound to cicontract.WorkloadPassSourceReplayDomain")
	}
	return map[string]string{
		"internal/devtools/gate/workload_pass_evidence.go":       identity,
		"internal/devtools/gate/workload_pass_source_replay.go":  replay,
		"internal/devtools/remoteci/workload_fingerprint.go":     owner,
		"internal/devtools/remoteci/workload_fingerprint_git.go": fingerprint,
	}
}

// assertRemoteCIIdentityDomainLiterals 拒绝 cicontract 之外复制协议域字面量。
func assertRemoteCIIdentityDomainLiterals(t *testing.T, sources map[string]string) {
	t.Helper()
	for relative, source := range sources {
		if strings.Contains(source, `"pass-identity/v2"`) || strings.Contains(source, `"input-fingerprint/v2"`) || strings.Contains(source, `"workload-pass-source-replay/v1"`) {
			t.Errorf("%s copies a domain literal outside cicontract", relative)
		}
	}
}
