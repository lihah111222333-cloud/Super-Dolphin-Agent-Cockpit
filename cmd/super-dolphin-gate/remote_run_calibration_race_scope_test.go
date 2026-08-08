package main

import (
	"slices"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteCalibrationRunnableRacePackageTargetCountsUniquePackages(t *testing.T) {
	identities := []remoteCalibrationWorkloadIdentity{
		remoteRacePackageIdentity(t, gatecontract.AtomicArchtestPackageTarget, "TestRaceOne"),
		remoteRacePackageIdentity(t, gatecontract.AtomicArchtestPackageTarget, "TestRaceTwo"),
		remoteRacePackageIdentity(t, gatecontract.AtomicCodexAppPackageTarget, "TestCodexRace"),
	}
	seen := make(map[string]struct{})
	for _, identity := range identities {
		packageTarget, runnable, err := remoteCalibrationRunnableRacePackageTarget(identity)
		if err != nil {
			t.Fatal(err)
		}
		if runnable {
			seen[packageTarget] = struct{}{}
		}
	}
	got := make([]string, 0, len(seen))
	for packageTarget := range seen {
		got = append(got, packageTarget)
	}
	slices.Sort(got)
	want := []string{gatecontract.AtomicArchtestPackageTarget, gatecontract.AtomicCodexAppPackageTarget}
	if !slices.Equal(got, want) {
		t.Fatalf("runnable race package count targets = %v, want %v", got, want)
	}
}

func remoteRacePackageIdentity(t *testing.T, packageTarget, name string) remoteCalibrationWorkloadIdentity {
	t.Helper()
	workload, err := gatecontract.NewGoTestWorkload(gatecontract.GateIDBackendTestGuardWithRace, packageTarget, name, 1)
	if err != nil {
		t.Fatal(err)
	}
	return remoteCalibrationWorkloadIdentity{id: workload.ID, kind: workload.Kind, shardable: true}
}
