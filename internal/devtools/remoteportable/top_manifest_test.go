package remoteportable

import (
	"bytes"
	"strings"
	"testing"
)

func TestPortableManifestTopLevelStrictRoundTrip(t *testing.T) {
	manifest := topManifestFixture()
	encoded, err := PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePortableManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := PortableManifestCanonicalEncode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("canonical top-level round-trip changed bytes")
	}
}

func TestPortableManifestTopLevelRejectsMalformedJSON(t *testing.T) {
	valid, err := PortableManifestCanonicalEncode(topManifestFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{
		[]byte(`null`), append(append([]byte(nil), valid...), []byte(` {}`)...),
		[]byte(`{"schema_version":"remote-ci-portable-manifest/v1","schema_version":"remote-ci-portable-manifest/v1"}`),
	} {
		if _, err := DecodePortableManifest(input); err == nil {
			t.Fatalf("DecodePortableManifest accepted malformed JSON %s", input)
		}
	}
}

func TestPortableManifestFieldAndABIGuards(t *testing.T) {
	if err := ValidatePortableManifestFieldRegistry(); err != nil {
		t.Fatal(err)
	}
	if _, err := PortableManifestABIDigest(); err != nil {
		t.Fatal(err)
	}
	if digest, err := PortableManifestContentDigest(topManifestFixture()); err != nil || !validPortableDigest(digest) {
		t.Fatalf("portable content digest = %q, err = %v", digest, err)
	}
	if digest, err := PortableBindingProtocolABIDigest(); err != nil || !validPortableDigest(digest) {
		t.Fatalf("portable binding ABI digest = %q, err = %v", digest, err)
	}
}

func TestPortableManifestRejectsUnsafeWorkloadTargetsAndUnboundedLogs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PortableManifest)
	}{
		{name: "module escape", mutate: func(manifest *PortableManifest) { manifest.Workloads[0].ModuleRoot = "../escape" }},
		{name: "absolute package", mutate: func(manifest *PortableManifest) { manifest.Workloads[0].Package = "/tmp" }},
		{name: "flag package", mutate: func(manifest *PortableManifest) { manifest.Workloads[0].Package = "./-flag" }},
		{name: "invalid test name", mutate: func(manifest *PortableManifest) { manifest.Workloads[0].Name = "not_a_test" }},
		{name: "package name", mutate: func(manifest *PortableManifest) { manifest.Workloads[0].Kind = RepositoryWorkloadKindGoPackage }},
		{name: "execution policy", mutate: func(manifest *PortableManifest) { manifest.Workloads[0].ExecutionPolicy = "unsafe" }},
		{name: "platform runtime", mutate: func(manifest *PortableManifest) {
			manifest.Workloads[0].RuntimeCapability = string(RepositoryWorkloadRuntimeWindowsNative)
		}},
		{name: "event limit", mutate: func(manifest *PortableManifest) { manifest.LogPolicy.MaxEventBytes = int64(^uint64(0) >> 1) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := topManifestFixture()
			testCase.mutate(&manifest)
			if _, err := PortableManifestCanonicalEncode(manifest); err == nil {
				t.Fatal("unsafe portable manifest was accepted")
			}
		})
	}
}

func topManifestFixture() PortableManifest {
	digest := "sha256:" + strings.Repeat("a", 64)
	return PortableManifest{
		SchemaVersion: PortableManifestSchemaVersion, ContractID: PortableManifestContractID,
		Engine:    PortableManifestEngine{RequiredProtocolABIDigest: digest, RequiredToolchainDigest: digest, RequiredCapabilities: []string{"go"}},
		Target:    PortableManifestTarget{RepositoryID: "fixture", ObjectFormat: "sha1", WorktreeMount: "worktree"},
		Workloads: []PortableManifestWorkload{{UnitID: "go-test/example", CommandDigest: digest, TargetPlatform: string(RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionDigest: digest, Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}},
		Profile:   ProfileLocalFast, CapacityPolicyDigest: digest, RequiredNodeCapabilities: []string{"go1.26.5"},
		LogPolicy: PortableManifestLogPolicy{ResultSchema: "remote-ci-result/v1", EventSchema: "remote-ci-event/v1", MaxResultBytes: 1 << 20, MaxEventBytes: 64 << 10},
		Authority: PortableManifestAuthority{ContractID: PortableManifestContractID, PostgreSQLIdentity: "remote-ci-postgresql-primary/v1"},
		Modules:   []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/fixture"}}, ExcludedNestedModules: []string{},
	}
}
