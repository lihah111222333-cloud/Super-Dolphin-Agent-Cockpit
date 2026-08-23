package heteroci

import "testing"

const (
	executionUnitTestDurationMS int64 = 1000
	executionUnitTestCPUMilli   int64 = 500
	executionUnitTestMemory     int64 = 64 * 1024 * 1024
)

func TestExecutionUnitValidateAcceptsUbuntuAndWineTargetPairs(t *testing.T) {
	tests := []struct {
		name    string
		runtime RuntimeKind
		target  Platform
	}{
		{name: "ubuntu on linux", runtime: RuntimeUbuntuContainer, target: PlatformLinuxAMD64},
		{name: "windows through Ubuntu Wine", runtime: RuntimeUbuntuWineContainer, target: PlatformWindowsAMD64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unit := executionUnitTestUnit(tt.name, tt.runtime, tt.target)
			if err := unit.Validate(); err != nil {
				t.Fatalf("ExecutionUnit.Validate() unexpected error: %v", err)
			}
			if err := ValidateExecutionUnits([]ExecutionUnit{unit}); err != nil {
				t.Fatalf("ValidateExecutionUnits() unexpected error: %v", err)
			}
		})
	}
}

func TestExecutionUnitValidateRejectsMalformedAndCrossPlatformUnits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExecutionUnit)
	}{
		{name: "empty id", mutate: func(unit *ExecutionUnit) { unit.UnitID = "" }},
		{name: "trimmed id", mutate: func(unit *ExecutionUnit) { unit.UnitID = " unit-1" }},
		{name: "control id", mutate: func(unit *ExecutionUnit) { unit.UnitID = "unit\n1" }},
		{name: "unknown platform", mutate: func(unit *ExecutionUnit) { unit.TargetPlatform = Platform("plan9/amd64") }},
		{name: "unknown runtime", mutate: func(unit *ExecutionUnit) { unit.RequiredRuntime = RuntimeKind("unknown_runtime") }},
		{name: "zero duration", mutate: func(unit *ExecutionUnit) { unit.EstimatedDurationMS = 0 }},
		{name: "negative duration", mutate: func(unit *ExecutionUnit) { unit.EstimatedDurationMS = -1 }},
		{name: "zero cpu", mutate: func(unit *ExecutionUnit) { unit.CPUMilli = 0 }},
		{name: "negative cpu", mutate: func(unit *ExecutionUnit) { unit.CPUMilli = -1 }},
		{name: "zero memory", mutate: func(unit *ExecutionUnit) { unit.MemoryBytes = 0 }},
		{name: "negative memory", mutate: func(unit *ExecutionUnit) { unit.MemoryBytes = -1 }},
		{name: "ubuntu target windows", mutate: func(unit *ExecutionUnit) {
			unit.RequiredRuntime = RuntimeUbuntuContainer
			unit.TargetPlatform = PlatformWindowsAMD64
		}},
		{name: "Wine target linux", mutate: func(unit *ExecutionUnit) {
			unit.RequiredRuntime = RuntimeUbuntuWineContainer
			unit.TargetPlatform = PlatformLinuxAMD64
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unit := executionUnitTestUnit(tt.name, RuntimeUbuntuContainer, PlatformLinuxAMD64)
			tt.mutate(&unit)
			if err := unit.Validate(); err == nil {
				t.Fatal("ExecutionUnit.Validate() accepted invalid unit")
			}
			if err := ValidateExecutionUnits([]ExecutionUnit{unit}); err == nil {
				t.Fatal("ValidateExecutionUnits() accepted invalid unit")
			}
		})
	}
}

func TestExecutionUnitValidateRejectsEveryRuntimeTargetCrossPlatformPair(t *testing.T) {
	tests := []struct {
		runtime RuntimeKind
		invalid Platform
	}{
		{runtime: RuntimeUbuntuContainer, invalid: PlatformWindowsAMD64},
		{runtime: RuntimeUbuntuWineContainer, invalid: PlatformLinuxAMD64},
	}
	for _, tt := range tests {
		t.Run(string(tt.runtime)+"/"+string(tt.invalid), func(t *testing.T) {
			unit := executionUnitTestUnit("cross-platform", tt.runtime, tt.invalid)
			if err := unit.Validate(); err == nil {
				t.Fatalf("runtime %q accepted incompatible target %q", tt.runtime, tt.invalid)
			}
		})
	}
}

func TestValidateExecutionUnitsRejectsNilEmptyAndDuplicateIDs(t *testing.T) {
	for _, units := range [][]ExecutionUnit{nil, {}} {
		if err := ValidateExecutionUnits(units); err == nil {
			t.Errorf("ValidateExecutionUnits(%v) accepted missing units", units)
		}
	}

	first := executionUnitTestUnit("unit-1", RuntimeUbuntuContainer, PlatformLinuxAMD64)
	second := executionUnitTestUnit("unit-2", RuntimeUbuntuWineContainer, PlatformWindowsAMD64)
	second.UnitID = first.UnitID
	if err := ValidateExecutionUnits([]ExecutionUnit{first, second}); err == nil {
		t.Fatal("ValidateExecutionUnits() accepted duplicate unit IDs")
	}
}

func TestValidateExecutionUnitsAcceptsLegalInputInAnyOrder(t *testing.T) {
	units := executionUnitTestAllUnits()
	units[0], units[1] = units[1], units[0]
	if err := ValidateExecutionUnits(units); err != nil {
		t.Fatalf("ValidateExecutionUnits() rejected legal unordered input: %v", err)
	}
}

type executionUnitNodeCase struct {
	name string
	unit ExecutionUnit
	node NodeSnapshot
	want bool
}

func TestExecutionUnitCanRunOnNodeAcceptsMatchingRuntimeHostPairs(t *testing.T) {
	assertExecutionUnitNodeCases(t, []executionUnitNodeCase{
		{name: "ubuntu on linux", unit: executionUnitTestUnit("ubuntu", RuntimeUbuntuContainer, PlatformLinuxAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeUbuntuContainer), want: true},
		{name: "windows through Wine on Linux", unit: executionUnitTestUnit("windows-wine", RuntimeUbuntuWineContainer, PlatformWindowsAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeUbuntuWineContainer), want: true},
		{name: "resource fit is not part of matching", unit: ExecutionUnit{UnitID: "resource-agnostic", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: executionUnitTestDurationMS, CPUMilli: 1_000_000, MemoryBytes: 1 << 60}, node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeUbuntuContainer), want: true},
	})
}

func TestExecutionUnitCanRunOnNodeRejectsPhysicalWindowsAndRuntimeMismatches(t *testing.T) {
	assertExecutionUnitNodeCases(t, []executionUnitNodeCase{
		{name: "ubuntu on Windows host rejected", unit: executionUnitTestUnit("ubuntu-windows", RuntimeUbuntuContainer, PlatformLinuxAMD64), node: executionUnitTestLiveNode(PlatformWindowsAMD64, RuntimeUbuntuContainer), want: false},
		{name: "Wine on Windows host rejected", unit: executionUnitTestUnit("windows-wine-host", RuntimeUbuntuWineContainer, PlatformWindowsAMD64), node: executionUnitTestLiveNode(PlatformWindowsAMD64, RuntimeUbuntuWineContainer), want: false},
		{name: "Wine requires exact active runtime", unit: executionUnitTestUnit("windows-wine-runtime", RuntimeUbuntuWineContainer, PlatformWindowsAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeUbuntuContainer), want: false},
		{name: "offline node rejected", unit: executionUnitTestUnit("offline", RuntimeUbuntuContainer, PlatformLinuxAMD64), node: executionUnitTestOfflineNode(PlatformLinuxAMD64), want: false},
		{name: "unknown node platform", unit: executionUnitTestUnit("unknown-node-platform", RuntimeUbuntuContainer, PlatformLinuxAMD64), node: executionUnitTestLiveNode(Platform("plan9/amd64"), RuntimeUbuntuContainer), want: false},
	})
}

func assertExecutionUnitNodeCases(t *testing.T, tests []executionUnitNodeCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := executionUnitCanRunOnNode(tt.unit, tt.node); got != tt.want {
				t.Fatalf("executionUnitCanRunOnNode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func executionUnitTestUnit(id string, runtime RuntimeKind, target Platform) ExecutionUnit {
	return ExecutionUnit{UnitID: id, TargetPlatform: target, RequiredRuntime: runtime, EstimatedDurationMS: executionUnitTestDurationMS, CPUMilli: executionUnitTestCPUMilli, MemoryBytes: executionUnitTestMemory}
}

func executionUnitTestAllUnits() []ExecutionUnit {
	return []ExecutionUnit{
		executionUnitTestUnit("ubuntu", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		executionUnitTestUnit("windows-wine", RuntimeUbuntuWineContainer, PlatformWindowsAMD64),
	}
}

func executionUnitTestLiveNode(platform Platform, runtimes ...RuntimeKind) NodeSnapshot {
	active := make([]ActiveCapabilitySnapshot, len(runtimes))
	for index, runtime := range runtimes {
		active[index] = ActiveCapabilitySnapshot{Runtime: runtime}
	}
	return NodeSnapshot{Platform: platform, Status: NodeStatusLive, ActiveCapabilities: active, Total: ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1}, Available: ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1}}
}

func executionUnitTestOfflineNode(platform Platform) NodeSnapshot {
	return NodeSnapshot{Platform: platform, Status: NodeStatusOffline, ActiveCapabilities: []ActiveCapabilitySnapshot{}}
}
