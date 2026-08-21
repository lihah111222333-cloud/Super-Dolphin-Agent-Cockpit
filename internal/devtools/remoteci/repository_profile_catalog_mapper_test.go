package remoteci

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRepositoryProfileCatalogMapper 覆盖 canonical execution、规划/环境分离、fail-fast 与字段守卫。
func TestRepositoryProfileCatalogMapper(t *testing.T) {
	assertMapRepositoryWorkloadProfileBuildsAuthoritativeCatalog(t)
	assertMapRepositoryWorkloadProfileMapsGoPackage(t)
	assertMapRepositoryWorkloadProfileSeparatesPlanningAndEnvironment(t)
	assertMapRepositoryWorkloadProfileRejectsInvalidMappings(t)
	assertRepositoryWorkloadMapperFieldGuard(t)
}

// TestMapRepositoryWorkloadProfileBuildsAuthoritativeCatalog 锁定 profile 到唯一 canonical catalog 的投影边界。
func assertMapRepositoryWorkloadProfileBuildsAuthoritativeCatalog(t *testing.T) {
	t.Helper()
	manifest := decodedRepositoryProfileMapperFixture(t)
	mapping, err := MapRepositoryWorkloadProfile(manifest, gate.ProfileLocalFast)
	if err != nil {
		t.Fatalf("MapRepositoryWorkloadProfile() error = %v", err)
	}
	assertRepositoryProfileCatalog(t, mapping)
	assertRepositoryProfilePlanning(t, mapping)
	assertRepositoryProfileEnvironment(t, mapping)
	assertRepositoryProfileDoesNotExpand(t, mapping)
}

func assertMapRepositoryWorkloadProfileMapsGoPackage(t *testing.T) {
	t.Helper()
	manifest := decodedRepositoryProfileMapperFixture(t)
	mapping, err := MapRepositoryWorkloadProfile(manifest, gate.ProfilePush)
	if err != nil {
		t.Fatalf("MapRepositoryWorkloadProfile(push) error = %v", err)
	}
	if len(mapping.Catalog.Workloads) != 1 {
		t.Fatalf("push catalog workload count = %d", len(mapping.Catalog.Workloads))
	}
	assertRepositoryPortablePackageTarget(t, mapping.Catalog.Workloads[0])
}

func assertRepositoryPortablePackageTarget(t *testing.T, workload gate.Workload) {
	t.Helper()
	_, kind, payload, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		t.Fatalf("ParseWorkloadID(push) error = %v", err)
	}
	if kind != gate.WorkloadTargetPortableGoPackage || !targeted {
		t.Fatalf("push workload kind/targeted = (%q, %t)", kind, targeted)
	}
	target, err := gate.ParsePortableGoPackageTarget(payload)
	if err != nil {
		t.Fatalf("ParsePortableGoPackageTarget() error = %v", err)
	}
	if target.ModuleRoot != "." || target.Package != "./internal/other" || target.Name != "" ||
		target.ExecutionPolicy != gate.PortableGoExecutionPolicyRace {
		t.Fatalf("portable package target = %#v", target)
	}
}

func assertRepositoryProfileCatalog(t *testing.T, mapping RepositoryProfileCatalogMapping) {
	t.Helper()
	if !mapping.Catalog.Authoritative || len(mapping.Catalog.Workloads) != 1 {
		t.Fatalf("catalog authority/workloads = (%t, %d)", mapping.Catalog.Authoritative, len(mapping.Catalog.Workloads))
	}
	if err := gate.ValidateWorkloadCatalog(mapping.Catalog); err != nil {
		t.Fatalf("ValidateWorkloadCatalog() error = %v", err)
	}
	digest, err := gate.WorkloadCatalogDigest(mapping.Catalog)
	if err != nil || digest != mapping.CatalogDigest {
		t.Fatalf("WorkloadCatalogDigest() = (%q, %v), mapping=%q", digest, err, mapping.CatalogDigest)
	}
	assertRepositoryProfileCatalogDigest(t, mapping.Catalog.Workloads[0])
	assertRepositoryProfilePortableTarget(t, mapping.Catalog.Workloads[0])
	assertRepositoryProfileExecutionDomains(t, mapping)
}

func assertRepositoryProfileExecutionDomains(t *testing.T, mapping RepositoryProfileCatalogMapping) {
	t.Helper()
	if len(mapping.Execution) != 1 {
		t.Fatalf("execution mapping count = %d", len(mapping.Execution))
	}
	execution := mapping.Execution[0]
	if execution.SourceWorkloadID != "a-portable-test" || execution.WorkloadID != mapping.Catalog.Workloads[0].ID {
		t.Fatalf("execution mapping identity = %#v", execution)
	}
	if !strings.HasPrefix(execution.DeclaredCommandDigest, "sha256:") ||
		execution.CanonicalCommandDigest != mapping.Catalog.Workloads[0].CommandDigest ||
		execution.DeclaredCommandDigest == execution.CanonicalCommandDigest {
		t.Fatalf("execution digest domains collapsed: %#v", execution)
	}
}

func assertRepositoryProfileCatalogDigest(t *testing.T, workload gate.Workload) {
	t.Helper()
	if digest, err := gate.WorkloadExecutionDigest(workload.ID); err != nil || digest != workload.CommandDigest {
		t.Fatalf("catalog digest round trip = (%q, %v), want %q", digest, err, workload.CommandDigest)
	}
}

func assertRepositoryProfilePortableTarget(t *testing.T, workload gate.Workload) {
	t.Helper()
	_, kind, payload, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		t.Fatalf("ParseWorkloadID() error = %v", err)
	}
	if kind != gate.WorkloadTargetPortableGoTest || !targeted {
		t.Fatalf("catalog workload kind/targeted = (%q, %t)", kind, targeted)
	}
	target, err := gate.ParsePortableGoTestTarget(payload)
	if err != nil {
		t.Fatalf("ParsePortableGoTestTarget() error = %v", err)
	}
	if target.ModuleRoot != "tools" || target.Package != "./internal/check" || target.Name != "TestPortable" {
		t.Fatalf("portable target = %#v", target)
	}
}

func assertRepositoryProfilePlanning(t *testing.T, mapping RepositoryProfileCatalogMapping) {
	t.Helper()
	workload := mapping.Catalog.Workloads[0]
	if len(mapping.Planning) != 1 || mapping.Planning[0].SourceWorkloadID != "a-portable-test" ||
		mapping.Planning[0].WorkloadID != workload.ID || mapping.Planning[0].CPUMilli != 1_250 ||
		mapping.Planning[0].MemoryBytes != 2<<30 || mapping.Planning[0].BootstrapEstimateMS != 1_500 {
		t.Fatalf("planning projection = %#v", mapping.Planning)
	}
}

func assertRepositoryProfileEnvironment(t *testing.T, mapping RepositoryProfileCatalogMapping) {
	t.Helper()
	workload := mapping.Catalog.Workloads[0]
	if len(mapping.Environment) != 1 || mapping.Environment[0].SourceWorkloadID != "a-portable-test" ||
		mapping.Environment[0].WorkloadID != workload.ID ||
		mapping.Environment[0].Platform != RepositoryWorkloadPlatformLinuxAMD64 ||
		mapping.Environment[0].Runtime != RepositoryWorkloadRuntimeUbuntuContainer {
		t.Fatalf("environment binding = %#v", mapping.Environment)
	}
}

func assertRepositoryProfileDoesNotExpand(t *testing.T, mapping RepositoryProfileCatalogMapping) {
	t.Helper()
	if slices.ContainsFunc(mapping.Planning, func(item RepositoryWorkloadPlanningProjection) bool {
		return item.SourceWorkloadID == "z-unselected-package"
	}) {
		t.Fatal("mapper expanded a workload outside profile.required_workload_ids")
	}
}

// TestMapRepositoryWorkloadProfileSeparatesPlanningAndEnvironment 锁定资源、平台与执行 identity 的分离。
func assertMapRepositoryWorkloadProfileSeparatesPlanningAndEnvironment(t *testing.T) {
	t.Helper()
	base := decodedRepositoryProfileMapperFixture(t)
	baseMapping := mustMapRepositoryProfile(t, base)
	changed := repositoryProfileMapperFixture()
	changed.Workloads[0].BootstrapEstimateMS = 9_000
	changed.Workloads[0].CPUMilli = 4_000
	changed.Workloads[0].MemoryBytes = 8 << 30
	changed.Workloads[0].Platform = RepositoryWorkloadPlatformWindowsAMD64
	changed.Workloads[0].Runtime = RepositoryWorkloadRuntimeWindowsNative
	changedMapping := mustMapRepositoryProfile(t, mustDecodeRepositoryProfileMapperManifest(t, changed))
	baseWorkload, changedWorkload := baseMapping.Catalog.Workloads[0], changedMapping.Catalog.Workloads[0]
	if baseWorkload.ID != changedWorkload.ID || baseWorkload.CommandDigest != changedWorkload.CommandDigest {
		t.Fatalf("planning/environment data changed execution identity: base=%#v changed=%#v", baseWorkload, changedWorkload)
	}
	if reflect.DeepEqual(baseMapping.Planning, changedMapping.Planning) || reflect.DeepEqual(baseMapping.Environment, changedMapping.Environment) {
		t.Fatal("planning or environment projection did not change")
	}
}

func mustMapRepositoryProfile(t *testing.T, manifest RepositoryWorkloadManifest) RepositoryProfileCatalogMapping {
	t.Helper()
	mapping, err := MapRepositoryWorkloadProfile(manifest, gate.ProfileLocalFast)
	if err != nil {
		t.Fatalf("MapRepositoryWorkloadProfile() error = %v", err)
	}
	return mapping
}

// TestMapRepositoryWorkloadProfileRejectsInvalidMappings 覆盖未校验、缺 profile、重复、stale 与 orphan 映射。
func assertMapRepositoryWorkloadProfileRejectsInvalidMappings(t *testing.T) {
	t.Helper()
	if _, err := MapRepositoryWorkloadProfile(repositoryProfileMapperFixture(), gate.ProfileLocalFast); err == nil {
		t.Fatal("mapper accepted a manifest without strict digest validation")
	}
	manifest := decodedRepositoryProfileMapperFixture(t)
	if _, err := MapRepositoryWorkloadProfile(manifest, gate.ProfileRelease); err == nil {
		t.Fatal("mapper accepted an absent profile")
	}
	if _, err := MapRepositoryWorkloadProfile(manifest, gate.Profile("unknown")); err == nil {
		t.Fatal("mapper accepted an unknown profile")
	}

	duplicate := repositoryProfileMapperFixture()
	duplicate.Workloads[1] = duplicate.Workloads[0]
	duplicate.Workloads[1].ID = "b-same-execution"
	duplicate.Profiles[0].RequiredWorkloadIDs = []string{"a-portable-test", "b-same-execution"}
	duplicate.Profiles[1].RequiredWorkloadIDs = []string{"b-same-execution"}
	if _, err := MapRepositoryWorkloadProfile(mustDecodeRepositoryProfileMapperManifest(t, duplicate), gate.ProfileLocalFast); err == nil {
		t.Fatal("mapper accepted duplicate canonical execution mappings")
	}

	mapping, err := MapRepositoryWorkloadProfile(manifest, gate.ProfileLocalFast)
	if err != nil {
		t.Fatalf("MapRepositoryWorkloadProfile() error = %v", err)
	}
	stale := mapping
	stale.Catalog.Workloads = slices.Clone(mapping.Catalog.Workloads)
	stale.Catalog.Workloads[0].CommandDigest = strings.Repeat("0", 64)
	stale.CatalogDigest = mustMappedCatalogDigest(t, stale.Catalog)
	if err := validateRepositoryProfileCatalogMapping(stale, []string{"a-portable-test"}); err == nil {
		t.Fatal("mapping validator accepted stale execution digest")
	}
	orphan := mapping
	orphan.Planning = nil
	if err := validateRepositoryProfileCatalogMapping(orphan, []string{"a-portable-test"}); err == nil {
		t.Fatal("mapping validator accepted orphan catalog workload")
	}
}

func mustMappedCatalogDigest(t *testing.T, catalog gate.WorkloadCatalog) string {
	t.Helper()
	digest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatalf("WorkloadCatalogDigest() error = %v", err)
	}
	return digest
}

// TestRepositoryWorkloadMapperFieldGuard 动态枚举 producer 字段并拒绝 missing/stale 分类。
func assertRepositoryWorkloadMapperFieldGuard(t *testing.T) {
	t.Helper()
	producer := reflect.TypeFor[RepositoryWorkload]()
	coverage := map[string]string{
		"ID": "identity", "Kind": "execution", "ModuleRoot": "execution", "Package": "execution", "Name": "execution",
		"Platform": "environment", "Runtime": "environment", "BootstrapEstimateMS": "planning", "CPUMilli": "planning",
		"MemoryBytes": "planning", "ExecutionPolicy": "execution", "CommandDigest": "execution",
	}
	if producer.NumField() != len(coverage) {
		t.Fatalf("RepositoryWorkload producer fields=%d mapper registry=%d", producer.NumField(), len(coverage))
	}
	seen := make(map[string]struct{}, producer.NumField())
	for field := range producer.Fields() {
		category, ok := coverage[field.Name]
		if !ok || category == "" {
			t.Fatalf("producer field %s has no mapper coverage", field.Name)
		}
		seen[field.Name] = struct{}{}
	}
	for field := range coverage {
		if _, ok := seen[field]; !ok {
			t.Fatalf("mapper field registry contains stale field %s", field)
		}
	}
}

func decodedRepositoryProfileMapperFixture(t *testing.T) RepositoryWorkloadManifest {
	t.Helper()
	return mustDecodeRepositoryProfileMapperManifest(t, repositoryProfileMapperFixture())
}

func mustDecodeRepositoryProfileMapperManifest(t *testing.T, manifest RepositoryWorkloadManifest) RepositoryWorkloadManifest {
	t.Helper()
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	decoded, err := DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeRepositoryWorkloadManifest() error = %v", err)
	}
	return decoded
}

func repositoryProfileMapperFixture() RepositoryWorkloadManifest {
	return RepositoryWorkloadManifest{
		SchemaVersion: PortableWorkloadManifestSchemaVersion,
		RepositoryID:  "example.test/portable-target",
		Modules: []RepositoryWorkloadModule{
			{Root: ".", ModulePath: "example.test/portable-target"},
			{Root: "tools", ModulePath: "example.test/portable-target/tools"},
		},
		ExcludedNestedModules: []string{},
		Profiles: []RepositoryWorkloadProfile{
			{Profile: gate.ProfileLocalFast, RequiredWorkloadIDs: []string{"a-portable-test"}},
			{Profile: gate.ProfilePush, RequiredWorkloadIDs: []string{"z-unselected-package"}},
		},
		Workloads: []RepositoryWorkload{
			{
				ID: "a-portable-test", Kind: RepositoryWorkloadKindGoTest, ModuleRoot: "tools",
				Package: "./internal/check", Name: "TestPortable", Platform: RepositoryWorkloadPlatformLinuxAMD64,
				Runtime: RepositoryWorkloadRuntimeUbuntuContainer, BootstrapEstimateMS: 1_500, CPUMilli: 1_250,
				MemoryBytes: 2 << 30, ExecutionPolicy: RepositoryWorkloadExecutionPolicyNormal,
			},
			{
				ID: "z-unselected-package", Kind: RepositoryWorkloadKindGoPackage, ModuleRoot: ".",
				Package: "./internal/other", Platform: RepositoryWorkloadPlatformLinuxAMD64,
				Runtime: RepositoryWorkloadRuntimeUbuntuContainer, BootstrapEstimateMS: 2_500, CPUMilli: 2_000,
				MemoryBytes: 3 << 30, ExecutionPolicy: RepositoryWorkloadExecutionPolicyRace,
			},
		},
	}
}
