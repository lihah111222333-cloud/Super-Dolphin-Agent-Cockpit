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
	fields, err := PortableManifestConsumerFields()
	if err != nil {
		t.Fatal(err)
	}
	fieldMaterial := strings.Join(fields, "\x00")
	for _, field := range []string{"PortableManifest.catalog_policies", "PortableManifestCatalogPolicy.platform", "PortableManifestCatalogPolicy.runtime", "PortableManifestCatalogPolicy.cpu_milli", "PortableManifestCatalogPolicy.memory_bytes", "PortableManifestCatalogPolicy.historical_estimate_ms", "PortableManifestCatalogPolicy.execution_policy"} {
		if !strings.Contains(fieldMaterial, field) {
			t.Fatalf("catalog policy field %q is absent from dynamic ABI field registry", field)
		}
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

func TestPortableManifestCatalogPoliciesAreSortedDualPlatformAndExplicit(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PortableManifest)
	}{
		{name: "empty both", mutate: func(manifest *PortableManifest) { manifest.CatalogPolicies = nil; manifest.Workloads = nil }},
		{name: "unsorted policy", mutate: func(manifest *PortableManifest) {
			manifest.CatalogPolicies[0], manifest.CatalogPolicies[1] = manifest.CatalogPolicies[1], manifest.CatalogPolicies[0]
		}},
		{name: "zero cpu", mutate: func(manifest *PortableManifest) { manifest.CatalogPolicies[0].CPUMilli = 0 }},
		{name: "runtime drift", mutate: func(manifest *PortableManifest) {
			manifest.CatalogPolicies[1].Runtime = string(RepositoryWorkloadRuntimeUbuntuContainer)
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := topManifestFixture()
			testCase.mutate(&manifest)
			if _, err := PortableManifestCanonicalEncode(manifest); err == nil {
				t.Fatal("invalid catalog policy was accepted")
			}
		})
	}
}

func TestPortableManifestAllowsManualOrAutoOnlyInputs(t *testing.T) {
	manualOnly := topManifestFixture()
	manualOnly.CatalogPolicies = nil
	encoded, err := PortableManifestCanonicalEncode(manualOnly)
	if err != nil {
		t.Fatalf("manual-only manifest rejected: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"catalog_policies":[]`)) {
		t.Fatalf("manual-only manifest did not canonicalize empty catalog policies: %s", encoded)
	}
	autoOnly := topManifestFixture()
	autoOnly.Workloads = nil
	autoEncoded, err := PortableManifestCanonicalEncode(autoOnly)
	if err != nil {
		t.Fatalf("auto-only manifest rejected: %v", err)
	}
	if !bytes.Contains(autoEncoded, []byte(`"workloads":[]`)) {
		t.Fatalf("auto-only manifest did not canonicalize empty workloads: %s", autoEncoded)
	}
	if _, err := DecodePortableManifest(autoEncoded); err != nil {
		t.Fatalf("auto-only manifest failed strict round-trip: %v", err)
	}
}

func TestPortableGoFileMatchesPlatformUsesExactGoFilenameSuffix(t *testing.T) {
	cases := []struct {
		name  string
		linux bool
		win   bool
	}{
		{name: "pkg.go", linux: true, win: true},
		{name: "pkg_linux.go", linux: true, win: false},
		{name: "pkg_windows_test.go", linux: false, win: true},
		{name: "pkg_linuxish.go", linux: true, win: true},
		{name: "pkg_plan9.go", linux: false, win: false},
		{name: "pkg_windows_amd64.go", linux: false, win: true},
		{name: "pkg_windows_arm64.go", linux: false, win: false},
		{name: "pkg_unix.go", linux: true, win: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := portableGoFileMatchesPlatform(testCase.name, string(RepositoryWorkloadPlatformLinuxAMD64)); got != testCase.linux {
				t.Fatalf("Linux match = %v, want %v", got, testCase.linux)
			}
			if got := portableGoFileMatchesPlatform(testCase.name, string(RepositoryWorkloadPlatformWindowsAMD64)); got != testCase.win {
				t.Fatalf("Windows match = %v, want %v", got, testCase.win)
			}
		})
	}
}

func topManifestFixture() PortableManifest {
	digest := "sha256:" + strings.Repeat("a", 64)
	return PortableManifest{
		SchemaVersion: PortableManifestSchemaVersion, ContractID: PortableManifestContractID,
		Engine:          PortableManifestEngine{RequiredProtocolABIDigest: digest, RequiredToolchainDigest: digest, RequiredCapabilities: []string{"go"}},
		Target:          PortableManifestTarget{RepositoryID: "fixture", ObjectFormat: "sha1", WorktreeMount: "worktree"},
		Workloads:       []PortableManifestWorkload{{UnitID: "go-test/example", CommandDigest: digest, TargetPlatform: string(RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionDigest: digest, Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}, {UnitID: "go-test/windows/example", CommandDigest: digest, TargetPlatform: string(RepositoryWorkloadPlatformWindowsAMD64), RuntimeCapability: string(RepositoryWorkloadRuntimeWindowsNative), CPUMilli: 2_000, MemoryBytes: 2 << 30, HistoricalEstimateMS: 2_000, ExecutionDigest: digest, Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}},
		CatalogPolicies: []PortableManifestCatalogPolicy{{Platform: string(RepositoryWorkloadPlatformLinuxAMD64), Runtime: string(RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}, {Platform: string(RepositoryWorkloadPlatformWindowsAMD64), Runtime: string(RepositoryWorkloadRuntimeWindowsNative), CPUMilli: 2_000, MemoryBytes: 2 << 30, HistoricalEstimateMS: 2_000, ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}},
		Profile:         ProfileLocalFast, CapacityPolicyDigest: digest, RequiredNodeCapabilities: []string{"go1.26.5"},
		LogPolicy: PortableManifestLogPolicy{ResultSchema: "remote-ci-result/v1", EventSchema: "remote-ci-event/v1", MaxResultBytes: 1 << 20, MaxEventBytes: 64 << 10},
		Authority: PortableManifestAuthority{ContractID: PortableManifestContractID, PostgreSQLIdentity: "remote-ci-postgresql-primary/v1"},
		Modules:   []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/fixture"}}, ExcludedNestedModules: []string{},
	}
}
