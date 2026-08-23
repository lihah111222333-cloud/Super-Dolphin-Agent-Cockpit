package remoteportable

import (
	"strings"
	"testing"
)

func TestPortableWorkloadCatalogDigestBindsCanonicalWorkloadsAndPlatformCounts(t *testing.T) {
	linux := portableCatalogTestWorkload(t, "linux", RepositoryWorkloadPlatformLinuxAMD64, RepositoryWorkloadRuntimeUbuntuContainer)
	windows := portableCatalogTestWorkload(t, "windows", RepositoryWorkloadPlatformWindowsAMD64, RepositoryWorkloadRuntimeUbuntuWineContainer)
	first, err := DerivePortableWorkloadCatalogFacts([]PortableManifestWorkload{windows, linux})
	if err != nil {
		t.Fatal(err)
	}
	second, err := DerivePortableWorkloadCatalogFacts([]PortableManifestWorkload{linux, windows})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.WorkloadCount != 2 || first.LinuxWorkloadCount != 1 || first.WindowsWorkloadCount != 1 {
		t.Fatalf("catalog facts are not canonical: first=%#v second=%#v", first, second)
	}
}

func TestPortableWorkloadCatalogDigestBindsWireFieldAndCountDrift(t *testing.T) {
	linux := portableCatalogTestWorkload(t, "linux", RepositoryWorkloadPlatformLinuxAMD64, RepositoryWorkloadRuntimeUbuntuContainer)
	windows := portableCatalogTestWorkload(t, "windows", RepositoryWorkloadPlatformWindowsAMD64, RepositoryWorkloadRuntimeUbuntuWineContainer)
	first, err := DerivePortableWorkloadCatalogFacts([]PortableManifestWorkload{linux, windows})
	if err != nil {
		t.Fatal(err)
	}
	drifted := windows
	drifted.MemoryBytes++
	drifted.ExecutionDigest, err = DerivePortableWorkloadExecutionDigest(drifted)
	if err != nil {
		t.Fatal(err)
	}
	driftedFacts, err := DerivePortableWorkloadCatalogFacts([]PortableManifestWorkload{linux, drifted})
	if err != nil {
		t.Fatal(err)
	}
	if driftedFacts.CatalogDigest == first.CatalogDigest {
		t.Fatal("catalog digest ignored a workload wire-field change")
	}
	if err := ValidatePortableWorkloadCatalogFacts(PortableWorkloadCatalogFacts{CatalogDigest: first.CatalogDigest, WorkloadCount: 2, LinuxWorkloadCount: 2, WindowsWorkloadCount: 1}); err == nil {
		t.Fatal("catalog facts accepted one-hot platform count drift")
	}
}

func portableCatalogTestWorkload(t *testing.T, unit string, platform RepositoryWorkloadPlatform, runtime RepositoryWorkloadRuntime) PortableManifestWorkload {
	t.Helper()
	workload := PortableManifestWorkload{
		UnitID: unit, CommandDigest: "sha256:" + strings.Repeat("a", 64), TargetPlatform: string(platform), RuntimeCapability: string(runtime),
		CPUMilli: 1, MemoryBytes: 1, HistoricalEstimateMS: 1, Kind: RepositoryWorkloadKindGoPackage,
		ModuleRoot: ".", Package: ".", ExecutionPolicy: string(RepositoryWorkloadExecutionPolicyNormal),
	}
	digest, err := DerivePortableWorkloadExecutionDigest(workload)
	if err != nil {
		t.Fatal(err)
	}
	workload.ExecutionDigest = digest
	return workload
}
