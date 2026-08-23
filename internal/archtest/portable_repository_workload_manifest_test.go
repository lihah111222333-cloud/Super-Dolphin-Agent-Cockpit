package archtest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remotecicli"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

// TestPortableRepositoryWorkloadManifest binds a strict portable manifest to
// the retained provider-neutral contract rather than the retired remoteci tree.
func TestPortableRepositoryWorkloadManifest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	workload := remoteportable.PortableManifestWorkload{UnitID: "go-test/example", CommandDigest: digest, TargetPlatform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, Kind: string(remoteportable.RepositoryWorkloadKindGoTest), ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}
	executionDigest, err := remoteportable.DerivePortableWorkloadExecutionDigest(workload)
	if err != nil {
		t.Fatalf("derive fixture execution digest: %v", err)
	}
	workload.ExecutionDigest = executionDigest
	manifest := remoteportable.PortableManifest{
		SchemaVersion:            remoteportable.PortableManifestSchemaVersion,
		ContractID:               remoteportable.PortableManifestContractID,
		Engine:                   remoteportable.PortableManifestEngine{RequiredProtocolABIDigest: digest, RequiredToolchainDigest: digest, RequiredCapabilities: []string{"go"}},
		Target:                   remoteportable.PortableManifestTarget{RepositoryID: "super-agent-v3", ObjectFormat: "sha1", WorktreeMount: "worktree"},
		Workloads:                []remoteportable.PortableManifestWorkload{workload},
		CatalogPolicies:          []remoteportable.PortableManifestCatalogPolicy{{Platform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), Runtime: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}, {Platform: string(remoteportable.RepositoryWorkloadPlatformWindowsAMD64), Runtime: string(remoteportable.RepositoryWorkloadRuntimeUbuntuWineContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}},
		Profile:                  remoteportable.ProfileLocalFast,
		RequiredNodeCapabilities: []string{"go"},
		LogPolicy:                remoteportable.PortableManifestLogPolicy{ResultSchema: "remote-ci-result/v1", EventSchema: "remote-ci-event/v1", MaxResultBytes: 1 << 20, MaxEventBytes: 64 << 10},
		Authority:                remoteportable.PortableManifestAuthority{ContractID: remoteportable.PortableManifestContractID, PostgreSQLIdentity: "remote-ci-postgresql-primary/v1"},
		CatalogLimits:            remoteportable.PortableManifestCatalogLimits{MaxTreeOutputBytes: 16 << 20, MaxTreeEntries: 100_000},
		Modules:                  []remoteportable.RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}},
		ExcludedNestedModules:    []string{},
	}
	manifest.CapacityPolicyDigest, err = remoteportable.DerivePortableCapacityPolicyDigest(manifest)
	if err != nil {
		t.Fatalf("derive fixture capacity policy digest: %v", err)
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

	repositoryManifestPath := filepath.Join(findRepoRoot(t), ".super-dolphin", "remote-ci-workloads.json")
	repositoryRaw, err := os.ReadFile(repositoryManifestPath)
	if err != nil {
		t.Fatalf("read repository portable manifest: %v", err)
	}
	var repositoryMaterial remoteportable.PortableManifest
	if err := json.Unmarshal(repositoryRaw, &repositoryMaterial); err != nil {
		t.Fatalf("unmarshal repository portable manifest material: %v", err)
	}
	repositoryCapacityDigest, err := remoteportable.DerivePortableCapacityPolicyDigest(repositoryMaterial)
	if err != nil {
		t.Fatalf("derive repository capacity policy digest: %v", err)
	}
	if repositoryMaterial.CapacityPolicyDigest != repositoryCapacityDigest {
		t.Fatalf("repository capacity policy digest = %q, want %q", repositoryMaterial.CapacityPolicyDigest, repositoryCapacityDigest)
	}
	repositoryCanonical, err := remoteportable.PortableManifestCanonicalEncode(repositoryMaterial)
	if err != nil {
		t.Fatalf("canonicalize repository portable manifest: %v", err)
	}
	if !bytes.Equal(repositoryRaw, repositoryCanonical) {
		t.Fatalf("repository portable manifest bytes are not canonical: raw_bytes=%d canonical_bytes=%d", len(repositoryRaw), len(repositoryCanonical))
	}
	repositoryManifest, err := remoteportable.DecodePortableManifest(repositoryRaw)
	if err != nil {
		t.Fatalf("decode repository portable manifest: %v", err)
	}
	protocolDigest, err := remotecicli.RemoteCIEngineProtocolABIDigest()
	if err != nil {
		t.Fatalf("derive current remote-CI protocol ABI: %v", err)
	}
	if repositoryManifest.ContractID != remoteportable.PortableManifestContractID || repositoryManifest.Engine.RequiredProtocolABIDigest != protocolDigest {
		t.Fatalf("repository portable manifest engine binding drifted: contract=%q protocol=%q", repositoryManifest.ContractID, repositoryManifest.Engine.RequiredProtocolABIDigest)
	}
	if len(repositoryManifest.CatalogPolicies) != 2 || repositoryManifest.CatalogPolicies[0].Runtime != string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer) || repositoryManifest.CatalogPolicies[1].Runtime != string(remoteportable.RepositoryWorkloadRuntimeUbuntuWineContainer) {
		t.Fatalf("repository portable manifest runtime policies drifted: %#v", repositoryManifest.CatalogPolicies)
	}
}
