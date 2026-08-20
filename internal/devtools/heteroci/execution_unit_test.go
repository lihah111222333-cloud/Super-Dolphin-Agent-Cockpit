package heteroci

import "testing"

const (
	executionUnitTestDurationMS int64 = 1000
	executionUnitTestCPUMilli   int64 = 500
	executionUnitTestMemory     int64 = 64 * 1024 * 1024
)

func TestExecutionUnitValidateAcceptsFiveRuntimeTargetPairs(t *testing.T) {
	tests := []struct {
		name    string
		runtime RuntimeKind
		target  Platform
	}{
		{name: "ubuntu on linux", runtime: RuntimeUbuntuContainer, target: PlatformLinuxAMD64},
		{name: "darling cross target", runtime: RuntimeDarlingCompatRuntime, target: PlatformDarwinAMD64},
		{name: "windows native", runtime: RuntimeWindowsNative, target: PlatformWindowsAMD64},
		{name: "darwin native", runtime: RuntimeDarwinNative, target: PlatformDarwinARM64},
		{name: "docker osx cross target", runtime: RuntimeDockerOSXExperimental, target: PlatformDarwinAMD64},
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

func TestExecutionUnitValidateAcceptsBothDarwinNativeTargets(t *testing.T) {
	for _, target := range []Platform{PlatformDarwinAMD64, PlatformDarwinARM64} {
		unit := executionUnitTestUnit(string(target), RuntimeDarwinNative, target)
		if err := unit.Validate(); err != nil {
			t.Errorf("Darwin native target %q rejected: %v", target, err)
		}
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
		{name: "darling target linux", mutate: func(unit *ExecutionUnit) {
			unit.RequiredRuntime = RuntimeDarlingCompatRuntime
			unit.TargetPlatform = PlatformLinuxAMD64
		}},
		{name: "windows target linux", mutate: func(unit *ExecutionUnit) {
			unit.RequiredRuntime = RuntimeWindowsNative
			unit.TargetPlatform = PlatformLinuxAMD64
		}},
		{name: "darwin native target linux", mutate: func(unit *ExecutionUnit) {
			unit.RequiredRuntime = RuntimeDarwinNative
			unit.TargetPlatform = PlatformLinuxAMD64
		}},
		{name: "docker osx target darwin arm64", mutate: func(unit *ExecutionUnit) {
			unit.RequiredRuntime = RuntimeDockerOSXExperimental
			unit.TargetPlatform = PlatformDarwinARM64
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
		invalid []Platform
	}{
		{runtime: RuntimeUbuntuContainer, invalid: []Platform{PlatformWindowsAMD64, PlatformDarwinAMD64, PlatformDarwinARM64}},
		{runtime: RuntimeDarlingCompatRuntime, invalid: []Platform{PlatformLinuxAMD64, PlatformWindowsAMD64, PlatformDarwinARM64}},
		{runtime: RuntimeWindowsNative, invalid: []Platform{PlatformLinuxAMD64, PlatformDarwinAMD64, PlatformDarwinARM64}},
		{runtime: RuntimeDarwinNative, invalid: []Platform{PlatformLinuxAMD64, PlatformWindowsAMD64}},
		{runtime: RuntimeDockerOSXExperimental, invalid: []Platform{PlatformLinuxAMD64, PlatformWindowsAMD64, PlatformDarwinARM64}},
	}
	for _, tt := range tests {
		for _, target := range tt.invalid {
			t.Run(string(tt.runtime)+"/"+string(target), func(t *testing.T) {
				unit := executionUnitTestUnit("cross-platform", tt.runtime, target)
				if err := unit.Validate(); err == nil {
					t.Fatalf("runtime %q accepted incompatible target %q", tt.runtime, target)
				}
			})
		}
	}
}

func TestValidateExecutionUnitsRejectsNilEmptyAndDuplicateIDs(t *testing.T) {
	for _, units := range [][]ExecutionUnit{nil, {}} {
		if err := ValidateExecutionUnits(units); err == nil {
			t.Errorf("ValidateExecutionUnits(%v) accepted missing units", units)
		}
	}

	first := executionUnitTestUnit("unit-1", RuntimeUbuntuContainer, PlatformLinuxAMD64)
	second := executionUnitTestUnit("unit-2", RuntimeWindowsNative, PlatformWindowsAMD64)
	second.UnitID = first.UnitID
	if err := ValidateExecutionUnits([]ExecutionUnit{first, second}); err == nil {
		t.Fatal("ValidateExecutionUnits() accepted duplicate unit IDs")
	}
}

func TestValidateExecutionUnitsAcceptsLegalInputInAnyOrder(t *testing.T) {
	units := executionUnitTestAllUnits()
	units[0], units[4] = units[4], units[0]
	units[1], units[3] = units[3], units[1]
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
		{name: "darling target darwin on linux host", unit: executionUnitTestUnit("darling", RuntimeDarlingCompatRuntime, PlatformDarwinAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeDarlingCompatRuntime), want: true},
		{name: "windows on windows", unit: executionUnitTestUnit("windows", RuntimeWindowsNative, PlatformWindowsAMD64), node: executionUnitTestLiveNode(PlatformWindowsAMD64, RuntimeWindowsNative), want: true},
		{name: "darwin native arm64", unit: executionUnitTestUnit("darwin-arm64", RuntimeDarwinNative, PlatformDarwinARM64), node: executionUnitTestLiveNode(PlatformDarwinARM64, RuntimeDarwinNative), want: true},
		{name: "docker osx on linux host", unit: executionUnitTestUnit("docker-osx", RuntimeDockerOSXExperimental, PlatformDarwinAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeDockerOSXExperimental), want: true},
		{name: "resource fit is not part of matching", unit: ExecutionUnit{UnitID: "resource-agnostic", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: executionUnitTestDurationMS, CPUMilli: 1_000_000, MemoryBytes: 1 << 60}, node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeUbuntuContainer), want: true},
	})
}

func TestExecutionUnitCanRunOnNodeRejectsDarlingAndWindowsMismatches(t *testing.T) {
	assertExecutionUnitNodeCases(t, []executionUnitNodeCase{
		{name: "ubuntu on windows host rejected", unit: executionUnitTestUnit("ubuntu-windows", RuntimeUbuntuContainer, PlatformLinuxAMD64), node: executionUnitTestLiveNode(PlatformWindowsAMD64, RuntimeUbuntuContainer), want: false},
		{name: "darling native darwin host rejected", unit: executionUnitTestUnit("darling-native-host", RuntimeDarlingCompatRuntime, PlatformDarwinAMD64), node: executionUnitTestLiveNode(PlatformDarwinAMD64, RuntimeDarlingCompatRuntime), want: false},
		{name: "darling requires exact active runtime", unit: executionUnitTestUnit("darling-runtime", RuntimeDarlingCompatRuntime, PlatformDarwinAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeUbuntuContainer), want: false},
		{name: "darling near miss runtime rejected", unit: executionUnitTestUnit("darling-near-miss", RuntimeDarlingCompatRuntime, PlatformDarwinAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeKind("darling_compat_runtime-v2")), want: false},
		{name: "windows on linux host rejected", unit: executionUnitTestUnit("windows-linux", RuntimeWindowsNative, PlatformWindowsAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeWindowsNative), want: false},
		{name: "windows requires exact active runtime", unit: executionUnitTestUnit("windows-runtime", RuntimeWindowsNative, PlatformWindowsAMD64), node: executionUnitTestLiveNode(PlatformWindowsAMD64, RuntimeUbuntuContainer), want: false},
	})
}

func TestExecutionUnitCanRunOnNodeRejectsDarwinDockerAndUnavailableNodes(t *testing.T) {
	assertExecutionUnitNodeCases(t, []executionUnitNodeCase{
		{name: "darwin native cannot use linux host", unit: executionUnitTestUnit("darwin-linux", RuntimeDarwinNative, PlatformDarwinAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeDarwinNative), want: false},
		{name: "darwin native target must equal node", unit: executionUnitTestUnit("darwin-target", RuntimeDarwinNative, PlatformDarwinARM64), node: executionUnitTestLiveNode(PlatformDarwinAMD64, RuntimeDarwinNative), want: false},
		{name: "docker osx on darwin host rejected", unit: executionUnitTestUnit("docker-osx-darwin", RuntimeDockerOSXExperimental, PlatformDarwinAMD64), node: executionUnitTestLiveNode(PlatformDarwinAMD64, RuntimeDockerOSXExperimental), want: false},
		{name: "docker osx target arm64 rejected by unit contract", unit: executionUnitTestUnit("docker-osx-arm64", RuntimeDockerOSXExperimental, PlatformDarwinARM64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeDockerOSXExperimental), want: false},
		{name: "offline node rejected", unit: executionUnitTestUnit("offline", RuntimeUbuntuContainer, PlatformLinuxAMD64), node: executionUnitTestOfflineNode(PlatformLinuxAMD64), want: false},
		{name: "live node missing runtime rejected", unit: executionUnitTestUnit("missing-runtime", RuntimeDockerOSXExperimental, PlatformDarwinAMD64), node: executionUnitTestLiveNode(PlatformLinuxAMD64, RuntimeUbuntuContainer), want: false},
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
	return ExecutionUnit{
		UnitID:              id,
		TargetPlatform:      target,
		RequiredRuntime:     runtime,
		EstimatedDurationMS: executionUnitTestDurationMS,
		CPUMilli:            executionUnitTestCPUMilli,
		MemoryBytes:         executionUnitTestMemory,
	}
}

func executionUnitTestAllUnits() []ExecutionUnit {
	return []ExecutionUnit{
		executionUnitTestUnit("ubuntu", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		executionUnitTestUnit("darling", RuntimeDarlingCompatRuntime, PlatformDarwinAMD64),
		executionUnitTestUnit("windows", RuntimeWindowsNative, PlatformWindowsAMD64),
		executionUnitTestUnit("darwin", RuntimeDarwinNative, PlatformDarwinARM64),
		executionUnitTestUnit("docker-osx", RuntimeDockerOSXExperimental, PlatformDarwinAMD64),
	}
}

func executionUnitTestLiveNode(platform Platform, runtimes ...RuntimeKind) NodeSnapshot {
	active := make([]ActiveCapabilitySnapshot, len(runtimes))
	for index, runtime := range runtimes {
		active[index] = ActiveCapabilitySnapshot{Runtime: runtime}
	}
	return NodeSnapshot{
		Platform:           platform,
		Status:             NodeStatusLive,
		ActiveCapabilities: active,
		Total:              ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
		Available:          ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1},
	}
}

func executionUnitTestOfflineNode(platform Platform) NodeSnapshot {
	return NodeSnapshot{
		Platform:           platform,
		Status:             NodeStatusOffline,
		ActiveCapabilities: []ActiveCapabilitySnapshot{},
	}
}
