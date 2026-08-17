package remoteci

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

func TestCoordinatorRejectsCustomNormalClassBeforeRemoteSideEffects(t *testing.T) {
	policy := testRemoteResourcePolicy()
	policy.Classes[0].ID = "custom"
	policy.Bootstrap = shardresource.BootstrapClasses{Guard: "custom", NodeTest: "custom", GoTest: "custom"}
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	_, err := NewCoordinator(CoordinatorConfig{
		Bucket: "ci-bucket", SourcePrefix: "baseline-artifacts/source-bundles/",
		InternalOSSEndpoint: "oss-cn-shenzhen-internal.aliyuncs.com", WorkerRoleName: "worker-role",
		ImageCacheSnapshotID: "snap-refreshed-runtime", WorkerTimeout: 10 * time.Minute,
		PollInterval: time.Millisecond, CleanupTimeout: time.Second, ResourcePolicy: policy,
	}, store, runtime)
	if err == nil {
		t.Fatal("NewCoordinator() accepted a custom normal class ID with a canonical tuple")
	}
	if len(store.uploads) != 0 || len(runtime.creates) != 0 {
		t.Fatalf("invalid resource policy caused remote side effects: uploads=%d creates=%d", len(store.uploads), len(runtime.creates))
	}
}

func TestBindRemoteShardResourcesRejectsCustomNormalClassWithCanonicalTuple(t *testing.T) {
	t.Parallel()

	class := shardresource.Class{ID: "custom", VCPU: 2, MemoryGiB: 4}
	request := ShardRequest{
		Profile: gate.ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("a", 64),
		ShardIdentity: "sha256:" + strings.Repeat("b", 64), SourceTreeSHA: strings.Repeat("c", 40),
		GateIDs: []gate.GateID{gate.GateIDWhitespaceCheck}, ResourceClass: class,
	}
	manifestDigest, err := request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatalf("ComputeShardExecutionManifestDigest() error = %v", err)
	}
	request.ShardExecutionManifestDigest = manifestDigest
	results := []ShardResult{{ShardIdentity: request.ShardIdentity, Resources: eci.Resources{CPU: class.VCPU, MemoryGiB: class.MemoryGiB}}}
	err = bindRemoteShardResources(results, []shardresource.Class{class}, []ShardRequest{request})
	if err == nil || !strings.Contains(err.Error(), "normal shard request resources") {
		t.Fatalf("bindRemoteShardResources() error = %v, want canonical normal identity rejection", err)
	}
	if results[0].ResourceClass != "" {
		t.Fatalf("invalid custom resource class was persisted: %#v", results[0])
	}
}

func TestBindRemoteShardResourcesRejectsForgedProviderClassReceipt(t *testing.T) {
	request := testSourceBundleShardRequest(t)
	results := []ShardResult{{
		ShardIdentity: request.ShardIdentity, ResourceClass: "custom",
		Resources: eci.Resources{CPU: request.ResourceClass.VCPU, MemoryGiB: request.ResourceClass.MemoryGiB},
	}}
	if err := bindRemoteShardResources(results, []shardresource.Class{request.ResourceClass}, []ShardRequest{request}); err == nil {
		t.Fatal("bindRemoteShardResources accepted forged provider resource class receipt")
	}
}

func TestCreateRequestCarriesResourceClassTupleAndExecutionMode(t *testing.T) {
	coordinator := &Coordinator{config: CoordinatorConfig{WorkerRoleName: "worker-role", InternalOSSEndpoint: "internal.eci"}}
	resources := shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8}
	shard := gate.ContainerShard{Index: 2, IdentityDigest: "sha256:" + strings.Repeat("a", 64), Profile: gate.ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("b", 64)}
	request := coordinator.createRequest("job-resource-fields", shard, resources, "bootstrap-key", "bootstrap-digest", "full-key", "full-digest", "manifest-digest", RunInput{Calibration: true})
	if request.ResourceClassID != resources.ID || request.ExecutionMode != gate.DurationExecutionModeCalibration || request.Resources.CPU != resources.VCPU || request.Resources.MemoryGiB != resources.MemoryGiB {
		t.Fatalf("createRequest resource identity = class=%q mode=%q resources=%+v, want class=%q mode=%q resources=%+v", request.ResourceClassID, request.ExecutionMode, request.Resources, resources.ID, gate.DurationExecutionModeCalibration, resources)
	}
}

// TestRemoteResourcePlanFieldContract 锁定计划资源字段及其到 ECI 选择器的完整映射。
func TestRemoteResourcePlanFieldContract(t *testing.T) {
	t.Parallel()
	planType := reflect.TypeFor[gate.PlannedWorkload]()
	selectorType := reflect.TypeFor[shardresource.Workload]()
	for _, fieldName := range []string{"ResourceCPU", "ResourceMemoryGiB"} {
		planField, ok := planType.FieldByName(fieldName)
		if !ok || planField.Tag.Get("json") == "" {
			t.Fatalf("planned workload must persist %s with a JSON identity", fieldName)
		}
		if _, ok := selectorType.FieldByName(fieldName); !ok {
			t.Fatalf("resource selector workload must consume planned field %s", fieldName)
		}
	}

	policy := shardresource.Policy{
		Classes: []shardresource.Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "medium", VCPU: 4, MemoryGiB: 8},
			{ID: "maximum", VCPU: 8, MemoryGiB: 16},
		},
		Bootstrap: shardresource.BootstrapClasses{
			Guard: "small", NodeTest: "small", GoTest: "small",
		},
		CalibrationResource:         shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
		FastWorkloadMaxDurationMS:   5_000,
		MediumWorkloadMaxDurationMS: 70_000,
		HeadroomPercent:             25,
		MinSamplesToDownsize:        5,
	}
	planned := gate.PlannedWorkload{
		Workload:            gate.Workload{ID: "guard:heavy", Kind: gate.WorkloadKindGuard},
		EstimatedDurationMS: 90_000,
		ResourceCPU:         2, ResourceMemoryGiB: 4,
	}
	selected, err := selectRemoteShardResources(
		policy,
		nil,
		map[string]gate.PlannedWorkload{planned.Workload.ID: planned},
		gate.ContainerShard{IdentityDigest: "sha256:" + strings.Repeat("a", 64), GateIDs: []gate.GateID{gate.GateID(planned.Workload.ID)}},
		shardresource.Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)},
	)
	if err != nil {
		t.Fatalf("select remote shard resources: %v", err)
	}
	if selected.VCPU != 2 || selected.MemoryGiB != 4 {
		t.Fatalf("90s bootstrap workload must retain persisted 2C/4GiB identity, got %+v", selected)
	}
}
