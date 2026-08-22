package archtest

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

// TestPortableRepositoryWorkloadManifest binds a strict portable manifest to
// the retained provider-neutral contract rather than the retired remoteci tree.
func TestPortableRepositoryWorkloadManifest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest := remoteportable.PortableManifest{
		SchemaVersion:            remoteportable.PortableManifestSchemaVersion,
		ContractID:               remoteportable.PortableManifestContractID,
		Engine:                   remoteportable.PortableManifestEngine{RequiredProtocolABIDigest: digest, RequiredToolchainDigest: digest, RequiredCapabilities: []string{"go"}},
		Target:                   remoteportable.PortableManifestTarget{RepositoryID: "super-agent-v3", ObjectFormat: "sha1", WorktreeMount: "worktree"},
		Workloads:                []remoteportable.PortableManifestWorkload{{UnitID: "go-test/example", CommandDigest: digest, TargetPlatform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionDigest: digest, Kind: string(remoteportable.RepositoryWorkloadKindGoTest), ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}},
		CatalogPolicies:          []remoteportable.PortableManifestCatalogPolicy{{Platform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), Runtime: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}, {Platform: string(remoteportable.RepositoryWorkloadPlatformWindowsAMD64), Runtime: string(remoteportable.RepositoryWorkloadRuntimeWindowsNative), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}},
		Profile:                  remoteportable.ProfileLocalFast,
		CapacityPolicyDigest:     digest,
		RequiredNodeCapabilities: []string{"go"},
		LogPolicy:                remoteportable.PortableManifestLogPolicy{ResultSchema: "remote-ci-result/v1", EventSchema: "remote-ci-event/v1", MaxResultBytes: 1 << 20, MaxEventBytes: 64 << 10},
		Authority:                remoteportable.PortableManifestAuthority{ContractID: remoteportable.PortableManifestContractID, PostgreSQLIdentity: "remote-ci-postgresql-primary/v1"},
		Modules:                  []remoteportable.RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}},
		ExcludedNestedModules:    []string{},
	}
	raw, err := remoteportable.PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatalf("encode portable manifest: %v", err)
	}
	decoded, err := remoteportable.DecodePortableManifest(raw)
	if err != nil {
		t.Fatalf("decode portable manifest: %v", err)
	}
	if decoded.Target.RepositoryID != manifest.Target.RepositoryID || len(decoded.Workloads) != 1 {
		t.Fatalf("portable manifest identity drifted: %#v", decoded)
	}
}
