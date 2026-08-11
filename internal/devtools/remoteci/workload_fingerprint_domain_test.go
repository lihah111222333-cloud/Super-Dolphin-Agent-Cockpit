package remoteci

import "testing"

// TestRemoteWorkloadInputFingerprintDomainVector 固定 input-fingerprint/v2 与内部 schema 4 的规范向量。
func TestRemoteWorkloadInputFingerprintDomainVector(t *testing.T) {
	entries := []remoteGitTreeEntry{{mode: "100644", kind: "blob", objectID: "111111111111111111111111111111111111", path: "internal/example.go"}}
	got, err := (&remoteGitTreeSnapshot{}).digestEntries(entries)
	if err != nil {
		t.Fatalf("digestEntries() error = %v", err)
	}
	const want = "sha256:da0ad0a0c26ed068029e8fbb1dd53188f8c254bffef6a5c3fbd716a314528a7a"
	if got != want {
		t.Fatalf("digestEntries() = %q, want fixed vector %q", got, want)
	}
}

// TestRemoteWorkloadInputFingerprintLegacyNoDomainDoesNotMatch 锁定旧 schema 4 无域材料与 v2 digest 不相等。
func TestRemoteWorkloadInputFingerprintLegacyNoDomainDoesNotMatch(t *testing.T) {
	entries := []remoteGitTreeEntry{{mode: "100644", kind: "blob", objectID: "111111111111111111111111111111111111", path: "internal/example.go"}}
	got, err := (&remoteGitTreeSnapshot{}).digestEntries(entries)
	if err != nil {
		t.Fatalf("digestEntries() error = %v", err)
	}
	const legacyNoDomain = "sha256:e2749090562eaf56739e96f469a6582d61d1ceb79f643524d4da450784bc0857"
	if got == legacyNoDomain {
		t.Fatalf("input-fingerprint/v2 digest reused legacy no-domain digest %q", got)
	}
}

// TestRemoteWorkloadGoTestInputFingerprintDomainVector 固定 Go test 输入材料也使用同一域。
func TestRemoteWorkloadGoTestInputFingerprintDomainVector(t *testing.T) {
	entries := []remoteGitTreeEntry{{mode: "100644", kind: "blob", objectID: "111111111111111111111111111111111111", path: "internal/example.go"}}
	sources := []remoteGoTestSource{{path: "internal/example_test.go", text: []byte("\x22\x22\x22\x22")}}
	got, err := digestGoTestEntries(entries, sources)
	if err != nil {
		t.Fatalf("digestGoTestEntries() error = %v", err)
	}
	const want = "sha256:3090b34f9d759b3e9f6667ea5dd3d35068b47871025f3adfd953075a2c9a9447"
	if got != want {
		t.Fatalf("digestGoTestEntries() = %q, want fixed vector %q", got, want)
	}
}
