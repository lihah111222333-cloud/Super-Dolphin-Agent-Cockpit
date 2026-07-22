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
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("GateRegistryDigest() = %q, %v", digest, err)
	}
	registry[0].ExecutionOwner = "tampered"
	registry[0].Argv[0] = "host-shell"
	registry[0].Profiles[0] = ProfileRelease
	registry[0].RequiredProfiles[0] = ProfileRelease
	fresh := GateRegistry()[0]
	if fresh.ExecutionOwner == "tampered" || fresh.Argv[0] == "host-shell" || fresh.Profiles[0] == ProfileRelease || fresh.RequiredProfiles[0] == ProfileRelease {
		t.Fatal("GateRegistry() leaked nested mutable canonical state")
	}
	if fresh.ExecutionOwner != containerExecutionOwner || fresh.Argv[0] != containerExecutorBinary {
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
	assertGatePlanIDSetEqual(t, local, push)
	assertGatePlanIDSetEqual(t, local, remote)
	assertGatePlanIDSetEqual(t, local, promotion)

	releaseOnly := gateIDSet(release.Gates)
	for id := range gateIDSet(push.Gates) {
		delete(releaseOnly, id)
	}
	if !reflect.DeepEqual(releaseOnly, map[GateID]bool{
		GateIDFrontendFullTest:         true,
		GateIDBackendTestGuardWithRace: true,
		GateIDBackendNilness:           true,
		GateIDReleaseLayeredCheck:      true,
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

	frontendTest := findGateSpec(t, GateIDFrontendTest)
	if frontendTest.ID != GateID("frontend:test") {
		t.Fatalf("frontend test id = %q", frontendTest.ID)
	}
	wantFrontendProfiles := []Profile{ProfileLocalFast, ProfilePush, ProfileRemoteRequired, ProfilePromotion}
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
		profile          Profile
		wantFrontendTest bool
		wantFullTest     bool
	}{
		{ProfileLocalFast, true, false},
		{ProfilePush, true, false},
		{ProfileRemoteRequired, true, false},
		{ProfilePromotion, true, false},
		{ProfileRelease, false, true},
	} {
		t.Run(string(test.profile), func(t *testing.T) {
			gates := gateIDSet(mustBuildPlan(t, test.profile).Gates)
			if gates[GateIDFrontendTest] != test.wantFrontendTest || gates[GateIDFrontendFullTest] != test.wantFullTest {
				t.Fatalf("%s frontend test gates = regular:%t full:%t, want regular:%t full:%t", test.profile, gates[GateIDFrontendTest], gates[GateIDFrontendFullTest], test.wantFrontendTest, test.wantFullTest)
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

func assertStrictGateSubset(t *testing.T, subset, superset GatePlan) {
	t.Helper()
	want := gateIDSet(superset.Gates)
	if len(subset.Gates) >= len(superset.Gates) {
		t.Fatalf("%s gate count %d is not a strict subset of %s count %d", subset.Profile, len(subset.Gates), superset.Profile, len(superset.Gates))
	}
	for _, spec := range subset.Gates {
		if !want[spec.ID] {
			t.Fatalf("%s gate %q is missing from %s", subset.Profile, spec.ID, superset.Profile)
		}
	}
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
