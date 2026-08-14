package gate

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExitCodesAreStable(t *testing.T) {
	t.Parallel()

	want := []ExitCode{0, 2, 10, 11, 12, 13, 14, 15, 16}
	got := []ExitCode{ExitOK, ExitProtocol, ExitGateViolation, ExitEvidenceIncomplete, ExitSourceMismatch, ExitInfrastructure, ExitRegistryInvariant, ExitCancelled, ExitTimeout}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exit codes = %v, want %v", got, want)
	}
	for _, code := range got {
		if err := code.Validate(); err != nil {
			t.Fatalf("ExitCode(%d).Validate() error = %v", code, err)
		}
	}
}

func TestGateRegistryIsCanonicalAndIsolated(t *testing.T) {
	t.Parallel()

	registry := GateRegistry()
	if err := validateGateRegistry(registry); err != nil {
		t.Fatalf("validateGateRegistry() error = %v", err)
	}
	digest, err := GateRegistryDigest()
	assertCanonicalRegistryDigest(t, digest, err)
	registry[0].ExecutionOwner = "tampered"
	registry[0].Argv[0] = "host-shell"
	registry[0].Profiles[0] = ProfileRelease
	registry[0].RequiredProfiles[0] = ProfileRelease
	fresh := GateRegistry()[0]
	assertGateRegistryCloneUnmodified(t, fresh)
	assertContainerWorkerCommand(t, fresh)
}

func assertCanonicalRegistryDigest(t *testing.T, digest string, err error) {
	t.Helper()
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("GateRegistryDigest() = %q, %v", digest, err)
	}
}

func assertGateRegistryCloneUnmodified(t *testing.T, fresh GateSpec) {
	t.Helper()
	if fresh.ExecutionOwner == "tampered" || fresh.Argv[0] == "host-shell" || fresh.Profiles[0] == ProfileRelease || fresh.RequiredProfiles[0] == ProfileRelease {
		t.Fatal("GateRegistry() leaked nested mutable canonical state")
	}
}

func assertContainerWorkerCommand(t *testing.T, fresh GateSpec) {
	t.Helper()
	if fresh.ExecutionOwner != containerExecutionOwner || fresh.Argv[0] != containerGateBinary ||
		fresh.Argv[1] != containerWorkerNamespace {
		t.Fatalf("gate command is not container-owned: %#v", fresh)
	}
}

func TestBuildGatePlanFiltersCanonicalRequiredProfiles(t *testing.T) {
	t.Parallel()

	local := mustBuildPlan(t, ProfileLocalFast)
	push := mustBuildPlan(t, ProfilePush)
	remote := mustBuildPlan(t, ProfileRemoteRequired)
	promotion := mustBuildPlan(t, ProfilePromotion)
	release := mustBuildPlan(t, ProfileRelease)
	assertGatePlanIDSetEqual(t, remote, promotion)
	remoteOnly := gateIDSet(remote.Gates)
	for id := range gateIDSet(local.Gates) {
		delete(remoteOnly, id)
	}
	if !reflect.DeepEqual(remoteOnly, map[GateID]bool{GateIDFrontendPerformanceVerify: true, GateIDFrontendPreflight: true}) {
		t.Fatalf("remote-only gates = %v", remoteOnly)
	}
	pushOnlyRemoved := gateIDSet(local.Gates)
	for id := range gateIDSet(push.Gates) {
		delete(pushOnlyRemoved, id)
	}
	if !reflect.DeepEqual(pushOnlyRemoved, map[GateID]bool{
		GateIDFrontendTest:        true,
		GateIDFrontendBuild:       true,
		GateIDFrontendEmbedVerify: true,
	}) {
		t.Fatalf("local gates omitted from push = %v", pushOnlyRemoved)
	}

	releaseOnly := gateIDSet(release.Gates)
	for id := range gateIDSet(push.Gates) {
		delete(releaseOnly, id)
	}
	if !reflect.DeepEqual(releaseOnly, map[GateID]bool{
		GateIDFrontendBuild:             true,
		GateIDFrontendE2E:               true,
		GateIDFrontendEmbedVerify:       true,
		GateIDFrontendPerformanceVerify: true,
		GateIDFrontendPreflight:         true,
		GateIDFrontendFullTest:          true,
		GateIDBackendTestGuardWithRace:  true,
		GateIDBackendNilness:            true,
		GateIDReleaseLayeredCheck:       true,
	}) {
		t.Fatalf("release-only gates = %v", releaseOnly)
	}
	for _, spec := range local.Gates {
		if !slices.Contains(spec.RequiredProfiles, ProfileLocalFast) {
			t.Fatalf("local plan contains optional gate %q", spec.ID)
		}
	}
}

func TestFrontendTestGateProfileContract(t *testing.T) {
	t.Parallel()

	frontendPreflight := findGateSpec(t, GateIDFrontendPreflight)
	if frontendPreflight.ID != GateID("frontend:preflight") {
		t.Fatalf("frontend preflight id = %q", frontendPreflight.ID)
	}
	wantPreflightProfiles := []Profile{ProfileRemoteRequired, ProfilePromotion, ProfileRelease}
	if !slices.Equal(frontendPreflight.RequiredProfiles, wantPreflightProfiles) {
		t.Fatalf("frontend preflight required profiles = %v, want %v", frontendPreflight.RequiredProfiles, wantPreflightProfiles)
	}
	if !slices.Equal(frontendPreflight.Profiles, wantPreflightProfiles) {
		t.Fatalf("frontend preflight profiles = %v, want %v", frontendPreflight.Profiles, wantPreflightProfiles)
	}

	frontendTest := findGateSpec(t, GateIDFrontendTest)
	if frontendTest.ID != GateID("frontend:test") {
		t.Fatalf("frontend test id = %q", frontendTest.ID)
	}
	wantFrontendProfiles := []Profile{ProfileLocalFast, ProfileRemoteRequired, ProfilePromotion}
	if !slices.Equal(frontendTest.RequiredProfiles, wantFrontendProfiles) {
		t.Fatalf("frontend test required profiles = %v, want %v", frontendTest.RequiredProfiles, wantFrontendProfiles)
	}
	if !slices.Equal(frontendTest.Profiles, wantFrontendProfiles) {
		t.Fatalf("frontend test profiles = %v, want %v", frontendTest.Profiles, wantFrontendProfiles)
	}

	frontendFullTest := findGateSpec(t, GateIDFrontendFullTest)
	if frontendFullTest.ID != GateID("frontend:test-full") {
		t.Fatalf("frontend full test id = %q", frontendFullTest.ID)
	}
	if !slices.Equal(frontendFullTest.RequiredProfiles, []Profile{ProfileRelease}) {
		t.Fatalf("frontend full test required profiles = %v, want release only", frontendFullTest.RequiredProfiles)
	}
	if !slices.Equal(frontendFullTest.Profiles, []Profile{ProfileRelease}) {
		t.Fatalf("frontend full test profiles = %v, want release only", frontendFullTest.Profiles)
	}

	for _, test := range []struct {
		profile               Profile
		wantFrontendPreflight bool
		wantFrontendTest      bool
		wantFullTest          bool
	}{
		{ProfileLocalFast, false, true, false},
		{ProfilePush, false, false, false},
		{ProfileRemoteRequired, true, true, false},
		{ProfilePromotion, true, true, false},
		{ProfileRelease, true, false, true},
	} {
		t.Run(string(test.profile), func(t *testing.T) {
			gates := gateIDSet(mustBuildPlan(t, test.profile).Gates)
			if gates[GateIDFrontendPreflight] != test.wantFrontendPreflight || gates[GateIDFrontendTest] != test.wantFrontendTest || gates[GateIDFrontendFullTest] != test.wantFullTest {
				t.Fatalf("%s frontend test gates = preflight:%t regular:%t full:%t, want preflight:%t regular:%t full:%t", test.profile, gates[GateIDFrontendPreflight], gates[GateIDFrontendTest], gates[GateIDFrontendFullTest], test.wantFrontendPreflight, test.wantFrontendTest, test.wantFullTest)
			}
		})
	}
}

func TestBuildGatePlanRejectsUnknownProfile(t *testing.T) {
	t.Parallel()

	if _, err := BuildGatePlan(Profile("unknown"), registryTestSource()); err == nil {
		t.Fatal("unknown profile passed BuildGatePlan")
	}
}

func TestBuildGatePlanDoesNotLeakNestedGateSlices(t *testing.T) {
	t.Parallel()

	plan := mustBuildPlan(t, ProfilePush)
	plan.Gates[0].Argv[0] = "host-shell"
	plan.Gates[0].Profiles[0] = ProfileRelease
	plan.Gates[0].RequiredProfiles[0] = ProfileRelease
	fresh := mustBuildPlan(t, ProfilePush).Gates[0]
	if fresh.Argv[0] == "host-shell" || fresh.Profiles[0] == ProfileRelease || fresh.RequiredProfiles[0] == ProfileRelease {
		t.Fatal("BuildGatePlan() leaked nested gate slices")
	}
}

func TestBuildGatePlanProducesStrictDigestBoundJSON(t *testing.T) {
	t.Parallel()

	plan, err := BuildGatePlan(ProfileLocalFast, registryTestSource())
	if err != nil {
		t.Fatalf("BuildGatePlan() error = %v", err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded GatePlan
	if err := DecodeStrictJSON(encoded, &decoded); err != nil {
		t.Fatalf("DecodeStrictJSON() error = %v", err)
	}
	tampered := plan
	tampered.Gates = append([]GateSpec(nil), plan.Gates...)
	tampered.Gates[0].ExecutionOwner = "host-shell"
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered gate plan passed validation")
	}
}

func TestGatePlanValidateStoredAcceptsIntactHistoricalRegistry(t *testing.T) {
	plan := mustBuildPlan(t, ProfileLocalFast)
	all := allProfiles()
	plan.Gates = append(plan.Gates, newGateSpec(GateIDLSPChangedDiagnostics, all, all))
	digest, err := plan.digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.PlanDigest = digest

	if err := plan.ValidateStored(); err != nil {
		t.Fatalf("ValidateStored() error = %v", err)
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate() accepted a historical plan as current")
	}
}

func TestGatePlanValidateStoredRejectsRetiredExecutorIdentity(t *testing.T) {
	plan := mustBuildPlan(t, ProfileLocalFast)
	for index := range plan.Gates {
		id := string(plan.Gates[index].ID)
		plan.Gates[index].ExecutionOwner = "container-executor"
		plan.Gates[index].CommandIdentity = "container-executor/v1/" + id
		plan.Gates[index].Argv = []string{"/usr/local/bin/super-dolphin-gate-executor", "run", "--gate", id}
	}
	digest, err := plan.digest()
	if err != nil {
		t.Fatal(err)
	}
	plan.PlanDigest = digest

	if err := plan.ValidateStored(); err == nil {
		t.Fatal("ValidateStored() accepted a retired executor plan")
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate() accepted a retired executor plan as current")
	}

	plan.Gates[0].CommandIdentity = "container-executor/v2/" + string(plan.Gates[0].ID)
	plan.PlanDigest, err = plan.digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateStored(); err == nil {
		t.Fatal("ValidateStored() accepted a mixed retired executor identity")
	}
}

func TestGatePlanRequiredFieldsFailClosed(t *testing.T) {
	t.Parallel()

	plan, err := BuildGatePlan(ProfilePush, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := JSONFieldNames(reflect.TypeFor[GatePlan]())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			var document map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			delete(document, field)
			missing, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			var decoded GatePlan
			if err := DecodeStrictJSON(missing, &decoded); err == nil {
				t.Fatalf("missing required field %q passed validation", field)
			}
		})
	}
}

func TestGateSpecRequiredFieldsFailClosed(t *testing.T) {
	t.Parallel()

	spec := GateRegistry()[0]
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := JSONFieldNames(reflect.TypeFor[GateSpec]())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			var document map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			delete(document, field)
			missing, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			var decoded GateSpec
			if err := DecodeStrictJSON(missing, &decoded); err == nil {
				t.Fatalf("missing required field %q passed validation", field)
			}
		})
	}
}

func registryTestSource() SourceSpec {
	return SourceSpec{
		Kind:          SourceKindCommit,
		ObjectFormat:  GitObjectFormatSHA1,
		Commit:        &CommitSource{SHA: testCommitSHA},
		SourceTreeSHA: testTreeSHA,
	}
}

func mustBuildPlan(t *testing.T, profile Profile) GatePlan {
	t.Helper()
	plan, err := BuildGatePlan(profile, registryTestSource())
	if err != nil {
		t.Fatalf("BuildGatePlan(%q) error = %v", profile, err)
	}
	return plan
}

func assertGatePlanIDSetEqual(t *testing.T, left, right GatePlan) {
	t.Helper()
	if !reflect.DeepEqual(gateIDSet(left.Gates), gateIDSet(right.Gates)) {
		t.Fatalf("%s gates differ from %s gates", left.Profile, right.Profile)
	}
}

func gateIDSet(specs []GateSpec) map[GateID]bool {
	set := make(map[GateID]bool, len(specs))
	for _, spec := range specs {
		set[spec.ID] = true
	}
	return set
}

func findGateSpec(t *testing.T, id GateID) GateSpec {
	t.Helper()
	for _, spec := range GateRegistry() {
		if spec.ID == id {
			return spec
		}
	}
	t.Fatalf("canonical registry does not contain gate %q", id)
	return GateSpec{}
}
