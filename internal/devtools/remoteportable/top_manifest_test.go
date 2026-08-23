package remoteportable

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPortableManifestTopLevelStrictRoundTrip(t *testing.T) {
	manifest := topManifestFixture(t)
	encoded, err := PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePortableManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Workloads[1].TargetPlatform != string(RepositoryWorkloadPlatformWindowsAMD64) || decoded.Workloads[1].RuntimeCapability != string(RepositoryWorkloadRuntimeUbuntuWineContainer) {
		t.Fatalf("Windows workload round-trip target drifted: %#v", decoded.Workloads[1])
	}
	if decoded.CatalogPolicies[1].Platform != string(RepositoryWorkloadPlatformWindowsAMD64) || decoded.CatalogPolicies[1].Runtime != string(RepositoryWorkloadRuntimeUbuntuWineContainer) {
		t.Fatalf("Windows catalog policy round-trip target drifted: %#v", decoded.CatalogPolicies[1])
	}
	reencoded, err := PortableManifestCanonicalEncode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("canonical top-level round-trip changed bytes")
	}
}

func TestPortableManifestRejectsRetiredWindowsNativeRuntime(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*PortableManifest)
	}{
		{name: "workload", mutate: func(manifest *PortableManifest) {
			manifest.Workloads = slices.Clone(manifest.Workloads)
			manifest.Workloads[1].RuntimeCapability = "windows_native"
		}},
		{name: "catalog policy", mutate: func(manifest *PortableManifest) {
			manifest.CatalogPolicies = slices.Clone(manifest.CatalogPolicies)
			manifest.CatalogPolicies[1].Runtime = "windows_native"
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := topManifestFixture(t)
			testCase.mutate(&manifest)
			if _, err := PortableManifestCanonicalEncode(manifest); err == nil {
				t.Fatal("retired windows_native runtime was accepted")
			}
		})
	}
}

func TestPortableManifestCatalogLimitsRejectMissingAndUnsafeBudgets(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PortableManifest)
	}{
		{name: "missing", mutate: func(manifest *PortableManifest) { manifest.CatalogLimits = PortableManifestCatalogLimits{} }},
		{name: "zero bytes", mutate: func(manifest *PortableManifest) { manifest.CatalogLimits.MaxTreeOutputBytes = 0 }},
		{name: "bytes over ceiling", mutate: func(manifest *PortableManifest) {
			manifest.CatalogLimits.MaxTreeOutputBytes = PortableManifestCatalogMaxTreeOutputBytes + 1
		}},
		{name: "entries over ceiling", mutate: func(manifest *PortableManifest) {
			manifest.CatalogLimits.MaxTreeEntries = PortableManifestCatalogMaxTreeEntries + 1
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := topManifestFixture(t)
			testCase.mutate(&manifest)
			if _, err := PortableManifestCanonicalEncode(manifest); err == nil {
				t.Fatal("unsafe catalog limits were accepted")
			}
		})
	}
}

func TestPortableManifestWorkloadExecutionDigestRoundTripAndTamperMatrix(t *testing.T) {
	manifest := topManifestFixture(t)
	workload := manifest.Workloads[0]
	expected, err := DerivePortableWorkloadExecutionDigest(workload)
	if err != nil {
		t.Fatal(err)
	}
	if workload.ExecutionDigest != expected {
		t.Fatalf("fixture execution digest = %q, want %q", workload.ExecutionDigest, expected)
	}
	encoded, err := PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePortableManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DerivePortableWorkloadExecutionDigest(decoded.Workloads[0]); err != nil || got != decoded.Workloads[0].ExecutionDigest {
		t.Fatalf("decoded execution digest = %q, err = %v", got, err)
	}
	cases := []struct {
		name   string
		mutate func(*PortableManifestWorkload)
	}{
		{name: "unit_id", mutate: func(value *PortableManifestWorkload) { value.UnitID = "go-test/changed" }},
		{name: "command_digest", mutate: func(value *PortableManifestWorkload) { value.CommandDigest = "sha256:" + strings.Repeat("b", 64) }},
		{name: "target_platform", mutate: func(value *PortableManifestWorkload) {
			value.TargetPlatform = string(RepositoryWorkloadPlatformWindowsAMD64)
		}},
		{name: "runtime_capability", mutate: func(value *PortableManifestWorkload) {
			value.RuntimeCapability = string(RepositoryWorkloadRuntimeUbuntuWineContainer)
		}},
		{name: "cpu_milli", mutate: func(value *PortableManifestWorkload) { value.CPUMilli++ }},
		{name: "memory_bytes", mutate: func(value *PortableManifestWorkload) { value.MemoryBytes++ }},
		{name: "historical_estimate_ms", mutate: func(value *PortableManifestWorkload) { value.HistoricalEstimateMS++ }},
		{name: "execution_digest", mutate: func(value *PortableManifestWorkload) { value.ExecutionDigest = "sha256:" + strings.Repeat("c", 64) }},
		{name: "kind", mutate: func(value *PortableManifestWorkload) { value.Kind = RepositoryWorkloadKindGoPackage }},
		{name: "module_root", mutate: func(value *PortableManifestWorkload) { value.ModuleRoot = "nested" }},
		{name: "package", mutate: func(value *PortableManifestWorkload) { value.Package = "./nested" }},
		{name: "name", mutate: func(value *PortableManifestWorkload) { value.Name = "TestChanged" }},
		{name: "execution_policy", mutate: func(value *PortableManifestWorkload) {
			value.ExecutionPolicy = string(RepositoryWorkloadExecutionPolicyRace)
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := workload
			testCase.mutate(&candidate)
			if testCase.name != "execution_digest" {
				candidate.ExecutionDigest = workload.ExecutionDigest
			}
			candidateManifest := manifest
			candidateManifest.Workloads = []PortableManifestWorkload{candidate}
			if _, err := PortableManifestCanonicalEncode(candidateManifest); err == nil {
				t.Fatalf("single-field tamper %q was accepted", testCase.name)
			}
		})
	}
}

func TestPortableManifestCapacityPolicyDigestRoundTripAndTamperMatrix(t *testing.T) {
	manifest := topManifestFixture(t)
	expected, err := DerivePortableCapacityPolicyDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CapacityPolicyDigest != expected {
		t.Fatalf("fixture capacity policy digest = %q, want %q", manifest.CapacityPolicyDigest, expected)
	}
	encoded, err := PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePortableManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DerivePortableCapacityPolicyDigest(decoded); err != nil || got != decoded.CapacityPolicyDigest {
		t.Fatalf("decoded capacity policy digest = %q, err = %v", got, err)
	}
	cases := []struct {
		name   string
		mutate func(*PortableManifest)
	}{
		{name: "profile", mutate: func(value *PortableManifest) { value.Profile = ProfilePush }},
		{name: "catalog policy cpu", mutate: func(value *PortableManifest) { value.CatalogPolicies[0].CPUMilli++ }},
		{name: "required capability", mutate: func(value *PortableManifest) { value.RequiredNodeCapabilities[0] = "go1.26.6" }},
		{name: "workload unit", mutate: func(value *PortableManifest) { value.Workloads[0].UnitID = "go-test/changed" }},
		{name: "workload platform", mutate: func(value *PortableManifest) {
			value.Workloads[0].TargetPlatform = string(RepositoryWorkloadPlatformWindowsAMD64)
		}},
		{name: "workload runtime", mutate: func(value *PortableManifest) {
			value.Workloads[0].RuntimeCapability = string(RepositoryWorkloadRuntimeUbuntuWineContainer)
		}},
		{name: "workload cpu", mutate: func(value *PortableManifest) { value.Workloads[0].CPUMilli++ }},
		{name: "workload memory", mutate: func(value *PortableManifest) { value.Workloads[0].MemoryBytes++ }},
		{name: "workload estimate", mutate: func(value *PortableManifest) { value.Workloads[0].HistoricalEstimateMS++ }},
		{name: "capacity policy digest", mutate: func(value *PortableManifest) { value.CapacityPolicyDigest = "sha256:" + strings.Repeat("b", 64) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := manifest
			candidate.CatalogPolicies = slices.Clone(manifest.CatalogPolicies)
			candidate.RequiredNodeCapabilities = slices.Clone(manifest.RequiredNodeCapabilities)
			candidate.Workloads = slices.Clone(manifest.Workloads)
			testCase.mutate(&candidate)
			if _, err := PortableManifestCanonicalEncode(candidate); err == nil {
				t.Fatalf("single-field tamper %q was accepted", testCase.name)
			}
		})
	}
}

func TestPortableManifestTopLevelRejectsMalformedJSON(t *testing.T) {
	valid, err := PortableManifestCanonicalEncode(topManifestFixture(t))
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
	for _, field := range []string{"PortableManifest.modules", "PortableManifest.excluded_nested_modules", "PortableManifest.catalog_limits", "PortableManifestWorkload.execution_digest", "RepositoryWorkloadModule.root", "RepositoryWorkloadModule.module_path", "PortableManifest.catalog_policies", "PortableManifestCatalogPolicy.platform", "PortableManifestCatalogPolicy.runtime", "PortableManifestCatalogPolicy.cpu_milli", "PortableManifestCatalogPolicy.memory_bytes", "PortableManifestCatalogPolicy.historical_estimate_ms", "PortableManifestCatalogPolicy.execution_policy", "PortableManifestCatalogLimits.max_tree_output_bytes", "PortableManifestCatalogLimits.max_tree_entries"} {
		if !strings.Contains(fieldMaterial, field) {
			t.Fatalf("manifest field %q is absent from dynamic ABI field registry", field)
		}
	}
	if _, err := PortableManifestABIDigest(); err != nil {
		t.Fatal(err)
	}
	if digest, err := PortableManifestContentDigest(topManifestFixture(t)); err != nil || !validPortableDigest(digest) {
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
			manifest.Workloads[0].RuntimeCapability = string(RepositoryWorkloadRuntimeUbuntuWineContainer)
		}},
		{name: "event limit", mutate: func(manifest *PortableManifest) { manifest.LogPolicy.MaxEventBytes = int64(^uint64(0) >> 1) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := topManifestFixture(t)
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
			manifest := topManifestFixture(t)
			testCase.mutate(&manifest)
			if _, err := PortableManifestCanonicalEncode(manifest); err == nil {
				t.Fatal("invalid catalog policy was accepted")
			}
		})
	}
}

func TestPortableManifestAllowsManualOrAutoOnlyInputs(t *testing.T) {
	manualOnly := topManifestFixture(t)
	manualOnly.CatalogPolicies = nil
	manualOnly.CapacityPolicyDigest = ""
	capacityDigest, err := DerivePortableCapacityPolicyDigest(manualOnly)
	if err != nil {
		t.Fatal(err)
	}
	manualOnly.CapacityPolicyDigest = capacityDigest
	encoded, err := PortableManifestCanonicalEncode(manualOnly)
	if err != nil {
		t.Fatalf("manual-only manifest rejected: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"catalog_policies":[]`)) {
		t.Fatalf("manual-only manifest did not canonicalize empty catalog policies: %s", encoded)
	}
	autoOnly := topManifestFixture(t)
	autoOnly.Workloads = nil
	autoOnly.CapacityPolicyDigest = ""
	autoOnly.CapacityPolicyDigest, err = DerivePortableCapacityPolicyDigest(autoOnly)
	if err != nil {
		t.Fatal(err)
	}
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

func topManifestFixture(t testing.TB) PortableManifest {
	t.Helper()
	digest := "sha256:" + strings.Repeat("a", 64)
	workloads := []PortableManifestWorkload{{UnitID: "go-test/example", CommandDigest: digest, TargetPlatform: string(RepositoryWorkloadPlatformLinuxAMD64), RuntimeCapability: string(RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}, {UnitID: "go-test/windows/example", CommandDigest: digest, TargetPlatform: string(RepositoryWorkloadPlatformWindowsAMD64), RuntimeCapability: string(RepositoryWorkloadRuntimeUbuntuWineContainer), CPUMilli: 2_000, MemoryBytes: 2 << 30, HistoricalEstimateMS: 2_000, Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}}
	for index := range workloads {
		executionDigest, err := DerivePortableWorkloadExecutionDigest(workloads[index])
		if err != nil {
			t.Fatal(err)
		}
		workloads[index].ExecutionDigest = executionDigest
	}
	manifest := PortableManifest{
		SchemaVersion: PortableManifestSchemaVersion, ContractID: PortableManifestContractID,
		Engine:          PortableManifestEngine{RequiredProtocolABIDigest: digest, RequiredToolchainDigest: digest, RequiredCapabilities: []string{"go"}},
		Target:          PortableManifestTarget{RepositoryID: "fixture", ObjectFormat: "sha1", WorktreeMount: "worktree"},
		Workloads:       workloads,
		CatalogPolicies: []PortableManifestCatalogPolicy{{Platform: string(RepositoryWorkloadPlatformLinuxAMD64), Runtime: string(RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}, {Platform: string(RepositoryWorkloadPlatformWindowsAMD64), Runtime: string(RepositoryWorkloadRuntimeUbuntuWineContainer), CPUMilli: 2_000, MemoryBytes: 2 << 30, HistoricalEstimateMS: 2_000, ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal)}},
		Profile:         ProfileLocalFast, CapacityPolicyDigest: "", RequiredNodeCapabilities: []string{"go1.26.5"},
		LogPolicy:     PortableManifestLogPolicy{ResultSchema: "remote-ci-result/v1", EventSchema: "remote-ci-event/v1", MaxResultBytes: 1 << 20, MaxEventBytes: 64 << 10},
		Authority:     PortableManifestAuthority{ContractID: PortableManifestContractID, PostgreSQLIdentity: "remote-ci-postgresql-primary/v1"},
		CatalogLimits: PortableManifestCatalogLimits{MaxTreeOutputBytes: 16 << 20, MaxTreeEntries: 100_000},
		Modules:       []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/fixture"}}, ExcludedNestedModules: []string{},
	}
	capacityDigest, err := DerivePortableCapacityPolicyDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.CapacityPolicyDigest = capacityDigest
	return manifest
}

type inventoryTreeReader struct {
	entries []GitTreeEntry
	blobs   map[string][]byte
}

func (reader inventoryTreeReader) ListTree(context.Context, string) ([]GitTreeEntry, error) {
	return reader.entries, nil
}

func (reader inventoryTreeReader) ReadBlob(_ context.Context, _, filePath string) ([]byte, error) {
	data, ok := reader.blobs[filePath]
	if !ok {
		return nil, errors.New("blob is missing")
	}
	return data, nil
}

func TestBuildPortableGoWorkloadInventoryHonorsExcludedNestedModules(t *testing.T) {
	reader := inventoryFixtureReader()
	inventory, err := BuildPortableGoWorkloadInventory(context.Background(), reader, "tree", string(RepositoryWorkloadPlatformLinuxAMD64), []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}, {Root: "declared", ModulePath: "example.invalid/declared"}}, []string{"excluded", "unbound"})
	if err != nil {
		t.Fatal(err)
	}
	wantPackages := []string{".\x00./rootpkg", "declared\x00.", "declared\x00./sub"}
	if !reflect.DeepEqual(inventory.GoPackages, wantPackages) {
		t.Fatalf("GoPackages = %#v, want %#v", inventory.GoPackages, wantPackages)
	}
	if !reflect.DeepEqual(inventory.NestedGoModules, []string{".", "declared"}) {
		t.Fatalf("NestedGoModules = %#v", inventory.NestedGoModules)
	}
}

func TestBuildPortableGoWorkloadInventoryRejectsModuleTopologyDrift(t *testing.T) {
	cases := []struct {
		name     string
		excluded []string
		modules  []RepositoryWorkloadModule
		want     string
	}{
		{name: "undeclared", want: "undeclared and not excluded"},
		{name: "stale exclusion", excluded: []string{"missing"}, want: "is stale"},
		{name: "declared exclusion", excluded: []string{"declared"}, modules: []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}, {Root: "declared", ModulePath: "example.invalid/declared"}}, want: "contains declared module"},
		{name: "outside exclusion", excluded: []string{"unbound"}, modules: []RepositoryWorkloadModule{{Root: "declared", ModulePath: "example.invalid/declared"}}, want: "outside declared modules"},
		{name: "root module undeclared", modules: []RepositoryWorkloadModule{{Root: "declared", ModulePath: "example.invalid/declared"}}, want: "root Go module is undeclared"},
		{name: "module path drift", modules: []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/wrong"}}, want: "path drifted"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			modules := testCase.modules
			if modules == nil {
				modules = []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}}
			}
			excluded := testCase.excluded
			if excluded == nil {
				excluded = []string{}
			}
			_, err := BuildPortableGoWorkloadInventory(context.Background(), inventoryFixtureReader(), "tree", string(RepositoryWorkloadPlatformLinuxAMD64), modules, excluded)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want substring %q", err, testCase.want)
			}
		})
	}
}

func TestBuildPortableGoWorkloadInventoryRejectsInvalidExcludedDeclarations(t *testing.T) {
	for _, excluded := range [][]string{{"excluded", "excluded"}, {"excluded", "declared"}} {
		_, err := BuildPortableGoWorkloadInventory(context.Background(), inventoryFixtureReader(), "tree", string(RepositoryWorkloadPlatformLinuxAMD64), []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}, {Root: "declared", ModulePath: "example.invalid/declared"}}, excluded)
		if err == nil {
			t.Fatalf("excluded %v was accepted", excluded)
		}
	}
}

func TestBuildPortableGoWorkloadInventoryRootNestedDeclarationExclusionMatrix(t *testing.T) {
	cases := []struct {
		name     string
		modules  []RepositoryWorkloadModule
		excluded []string
		want     []string
		wantErr  string
	}{
		{name: "root and nested declared", modules: []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}, {Root: "declared", ModulePath: "example.invalid/declared"}}, excluded: []string{"excluded", "unbound"}, want: []string{".\x00./rootpkg", "declared\x00.", "declared\x00./sub"}},
		{name: "root declared nested excluded", modules: []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}}, excluded: []string{"declared", "excluded", "unbound"}, want: []string{".\x00./rootpkg"}},
		{name: "nested omitted from exclusions", modules: []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}}, excluded: []string{"excluded", "unbound"}, wantErr: "undeclared and not excluded"},
		{name: "declared nested cannot be excluded", modules: []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}, {Root: "declared", ModulePath: "example.invalid/declared"}}, excluded: []string{"declared", "excluded", "unbound"}, wantErr: "contains declared module"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			inventory, err := BuildPortableGoWorkloadInventory(context.Background(), inventoryFixtureReader(), "tree", string(RepositoryWorkloadPlatformLinuxAMD64), testCase.modules, testCase.excluded)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inventory.GoPackages, testCase.want) {
				t.Fatalf("GoPackages = %#v, want %#v", inventory.GoPackages, testCase.want)
			}
		})
	}
}

func TestBuildPortableGoWorkloadInventorySkipsKnownV3WJExcludedSubtrees(t *testing.T) {
	excluded := []string{"backend/.tmp-cgobench", "build/gate/runtime-proxy", "build/gate/runtime-tools", "third_party/kelindar-event"}
	entries := make([]GitTreeEntry, 0, len(excluded)*2+2)
	blobs := map[string][]byte{"go.mod": []byte("module example.invalid/root\n\ngo 1.24.0\n"), "root.go": []byte("package root\n")}
	for _, module := range excluded {
		goMod := module + "/go.mod"
		entries = append(entries, GitTreeEntry{Mode: "100644", Kind: "blob", Path: goMod}, GitTreeEntry{Mode: "100644", Kind: "blob", Path: module + "/main.go"})
		blobs[goMod] = []byte("module example.invalid/" + strings.ReplaceAll(module, "/", "-") + "\n\ngo 1.24.0\n")
	}
	entries = append(entries, GitTreeEntry{Mode: "100644", Kind: "blob", Path: "go.mod"}, GitTreeEntry{Mode: "100644", Kind: "blob", Path: "root.go"})
	inventory, err := BuildPortableGoWorkloadInventory(context.Background(), inventoryTreeReader{entries: entries, blobs: blobs}, "tree", string(RepositoryWorkloadPlatformLinuxAMD64), []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/root"}}, excluded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inventory.GoPackages, []string{".\x00."}) {
		t.Fatalf("GoPackages = %#v, excluded subtrees leaked", inventory.GoPackages)
	}
}

func inventoryFixtureReader() inventoryTreeReader {
	paths := []string{"go.mod", "rootpkg/root.go", "declared/go.mod", "declared/declared.go", "declared/sub/sub.go", "excluded/go.mod", "excluded/hidden.go", "unbound/go.mod", "unbound/hidden.go"}
	entries := make([]GitTreeEntry, 0, len(paths))
	blobs := map[string][]byte{
		"go.mod":               []byte("module example.invalid/root\n\ngo 1.24.0\n"),
		"declared/go.mod":      []byte("module example.invalid/declared\n\ngo 1.24.0\n"),
		"excluded/go.mod":      []byte("module example.invalid/excluded\n\ngo 1.24.0\n"),
		"unbound/go.mod":       []byte("module example.invalid/unbound\n\ngo 1.24.0\n"),
		"rootpkg/root.go":      []byte("package rootpkg\n"),
		"declared/declared.go": []byte("package declared\n"),
		"declared/sub/sub.go":  []byte("package sub\n"),
		"excluded/hidden.go":   []byte("package excluded\n"),
		"unbound/hidden.go":    []byte("package unbound\n"),
	}
	for _, filePath := range paths {
		entries = append(entries, GitTreeEntry{Mode: "100644", Kind: "blob", Path: filePath})
	}
	return inventoryTreeReader{
		entries: entries,
		blobs:   blobs,
	}
}
