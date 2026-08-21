package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestProjectRepositoryWorkloadBootstrapExecutionUnitsUsesManifestResources(t *testing.T) {
	manifest := routingManifestFixture(t)
	manifest.Workloads[0].BootstrapEstimateMS = 2_000
	manifest.Workloads[0].CPUMilli = 1_500
	manifest.Workloads[0].MemoryBytes = 2 << 30
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	manifest, err = DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeRepositoryWorkloadManifest() error = %v", err)
	}
	units, err := ProjectRepositoryWorkloadBootstrapExecutionUnits(manifest)
	if err != nil {
		t.Fatalf("ProjectRepositoryWorkloadBootstrapExecutionUnits() error = %v", err)
	}
	if len(units) != 2 || units[0].EstimatedDurationMS != 2_000 || units[0].CPUMilli != 1_500 || units[0].MemoryBytes != 2<<30 {
		t.Fatalf("bootstrap units = %#v", units)
	}
}

func TestProjectRepositoryWorkloadExecutionUnitsBindsPlatformRuntimeAndDynamicResources(t *testing.T) {
	manifest := routingManifestFixture(t)
	planned := []gate.PlannedWorkload{
		routingPlannedWorkload("linux", 12_345, 2, 4),
		routingPlannedWorkload("windows", 67_890, 4, 8),
	}
	units, err := ProjectRepositoryWorkloadExecutionUnits(manifest, planned)
	if err != nil {
		t.Fatalf("ProjectRepositoryWorkloadExecutionUnits() error = %v", err)
	}
	want := []heteroci.ExecutionUnit{
		{UnitID: "linux", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 12_345, CPUMilli: 2_000, MemoryBytes: 4 * bytesPerGiB},
		{UnitID: "windows", TargetPlatform: heteroci.PlatformWindowsAMD64, RequiredRuntime: heteroci.RuntimeWindowsNative, EstimatedDurationMS: 67_890, CPUMilli: 4_000, MemoryBytes: 8 * bytesPerGiB},
	}
	if len(units) != len(want) {
		t.Fatalf("unit count = %d, want %d", len(units), len(want))
	}
	for index := range want {
		if units[index] != want[index] {
			t.Fatalf("unit[%d] = %#v, want %#v", index, units[index], want[index])
		}
	}
}

func TestProjectRepositoryWorkloadExecutionUnitsRejectsSetAndResourceDrift(t *testing.T) {
	manifest := routingManifestFixture(t)
	tests := []struct {
		name    string
		planned []gate.PlannedWorkload
		want    string
	}{
		{name: "missing manifest workload", planned: []gate.PlannedWorkload{routingPlannedWorkload("unknown", 1, 1, 1), routingPlannedWorkload("windows", 1, 1, 1)}, want: "missing from repository manifest"},
		{name: "stale manifest workload", planned: []gate.PlannedWorkload{routingPlannedWorkload("linux", 1, 1, 1)}, want: "sets differ"},
		{name: "duplicate planned workload", planned: []gate.PlannedWorkload{routingPlannedWorkload("linux", 1, 1, 1), routingPlannedWorkload("linux", 1, 1, 1)}, want: "duplicated"},
		{name: "zero duration", planned: []gate.PlannedWorkload{routingPlannedWorkload("linux", 0, 1, 1), routingPlannedWorkload("windows", 1, 1, 1)}, want: "estimated duration"},
		{name: "fractional millicore", planned: []gate.PlannedWorkload{routingPlannedWorkload("linux", 1, 0.0001, 1), routingPlannedWorkload("windows", 1, 1, 1)}, want: "represented exactly"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ProjectRepositoryWorkloadExecutionUnits(manifest, test.planned)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func routingManifestFixture(t *testing.T) RepositoryWorkloadManifest {
	t.Helper()
	manifest := repositoryWorkloadManifestFixture()
	manifest.Workloads[0].ID = "linux"
	manifest.Workloads = append(manifest.Workloads, RepositoryWorkload{
		ID: "windows", Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".",
		Package: "./internal/devtools/remoteci", Name: "TestWindows",
		Platform: RepositoryWorkloadPlatformWindowsAMD64, Runtime: RepositoryWorkloadRuntimeWindowsNative,
		BootstrapEstimateMS: 1_000, CPUMilli: 1_000, MemoryBytes: 1 << 30,
		ExecutionPolicy: RepositoryWorkloadExecutionPolicyNormal,
	})
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

func routingPlannedWorkload(id string, duration int64, cpu, memoryGiB float64) gate.PlannedWorkload {
	return gate.PlannedWorkload{
		Workload: gate.Workload{ID: id}, EstimatedDurationMS: duration,
		ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB,
	}
}
