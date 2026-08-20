package heteroci

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type routingLeaseMutation struct {
	name   string
	mutate func(*RoutingLease)
}

type routingLeaseSecretMutation struct {
	name   string
	field  string
	mutate func(*RoutingLease)
}

type routingLeaseWireField struct {
	name string
	typ  reflect.Type
	tag  string
}

func TestRoutingLeaseValidateAgainstAcceptsLocalAndECILifecycleStates(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	identities := []struct {
		name string
		unit int
	}{
		{name: "ECI", unit: 0},
		{name: "local", unit: 1},
	}
	for _, identity := range identities {
		runRoutingLeaseLifecycle(t, fixture, identity)
	}
}

func runRoutingLeaseLifecycle(t *testing.T, fixture routingValidationFixture, identity struct {
	name string
	unit int
}) {
	t.Helper()
	states := []struct {
		name  string
		state RoutingLeaseState
	}{
		{name: "planned", state: LeaseStatePlanned},
		{name: "started", state: LeaseStateStarted},
		{name: "succeeded", state: LeaseStateSucceeded},
		{name: "failed", state: LeaseStateFailed},
		{name: "cancelled", state: LeaseStateCancelled},
	}
	for _, lifecycle := range states {
		t.Run(identity.name+"/"+lifecycle.name, func(t *testing.T) {
			lease := validRoutingLease(fixture, identity.unit, lifecycle.state,
				fixture.now.UnixMilli()-5_000, stateStartedAt(fixture.now, lifecycle.state),
				stateFinishedAt(fixture.now, lifecycle.state))
			assertRoutingLeaseAssignmentIdentity(t, lease, fixture.decision.Assignments[identity.unit])
			if err := lease.ValidateAgainst(fixture.decision, fixture.now); err != nil {
				t.Fatalf("RoutingLease.ValidateAgainst() unexpected error: %v", err)
			}
		})
	}
}

func assertRoutingLeaseAssignmentIdentity(t *testing.T, lease RoutingLease, assignment RouteAssignment) {
	t.Helper()
	if lease.Executor == ExecutorKindLocalNode {
		assertNonEmptyRoutingLeaseIdentity(t, "node", assignment.NodeID)
		assertNonEmptyRoutingLeaseIdentity(t, "endpoint", assignment.Endpoint)
		assertNonEmptyRoutingLeaseIdentity(t, "probe_identity", assignment.ProbeIdentity)
		assertNonEmptyRoutingLeaseIdentity(t, "probe_boot_id", assignment.ProbeBootID)
		return
	}
	if assignment.NodeID != "" || assignment.Endpoint != "" || assignment.ProbeIdentity != "" || assignment.ProbeBootID != "" {
		t.Fatalf("ECI assignment must keep local identity fields empty: %+v", assignment)
	}
}

func assertNonEmptyRoutingLeaseIdentity(t *testing.T, name, value string) {
	t.Helper()
	if value == "" {
		t.Fatalf("local assignment %s identity must be non-empty", name)
	}
}

func TestRoutingLeaseValidateAgainstRejectsIdentityDrift(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	local := validRoutingLease(fixture, 1, LeaseStateStarted, fixture.now.UnixMilli()-5_000, fixture.now.UnixMilli()-3_000, 0)
	mutations := []routingLeaseMutation{
		{name: "schema empty", mutate: func(lease *RoutingLease) { lease.SchemaVersion = "" }},
		{name: "schema v1", mutate: func(lease *RoutingLease) { lease.SchemaVersion = "heteroci-routing-lease/v1" }},
		{name: "schema old", mutate: func(lease *RoutingLease) { lease.SchemaVersion = "heteroci-routing-lease/v0" }},
		{name: "authority empty", mutate: func(lease *RoutingLease) { lease.Authority = "" }},
		{name: "authority remote", mutate: func(lease *RoutingLease) { lease.Authority = "REMOTE_AUTHORITATIVE" }},
		{name: "run id empty", mutate: func(lease *RoutingLease) { lease.RunID = "" }},
		{name: "run id whitespace", mutate: func(lease *RoutingLease) { lease.RunID = " run-valid" }},
		{name: "decision digest empty", mutate: func(lease *RoutingLease) { lease.DecisionDigest = "" }},
		{name: "decision digest drift", mutate: func(lease *RoutingLease) { lease.DecisionDigest = "sha256:" + strings.Repeat("0", 64) }},
		{name: "unknown unit", mutate: func(lease *RoutingLease) { lease.UnitID = "unit-unknown" }},
		{name: "unknown executor", mutate: func(lease *RoutingLease) { lease.Executor = ExecutorKind("fallback") }},
		{name: "local executor drift", mutate: func(lease *RoutingLease) { lease.Executor = ExecutorKindAliyunECI }},
		{name: "local node empty", mutate: func(lease *RoutingLease) { lease.NodeID = "" }},
		{name: "local node drift", mutate: func(lease *RoutingLease) { lease.NodeID = "node-drift" }},
		{name: "local endpoint empty", mutate: func(lease *RoutingLease) { lease.Endpoint = "" }},
		{name: "local endpoint drift", mutate: func(lease *RoutingLease) { lease.Endpoint = "https://drift.invalid/runner" }},
		{name: "local probe identity empty", mutate: func(lease *RoutingLease) { lease.ProbeIdentity = "" }},
		{name: "local probe identity drift", mutate: func(lease *RoutingLease) { lease.ProbeIdentity = "probe-drift" }},
		{name: "local probe boot id empty", mutate: func(lease *RoutingLease) { lease.ProbeBootID = "" }},
		{name: "local probe boot id drift", mutate: func(lease *RoutingLease) { lease.ProbeBootID = "boot-drift" }},
		{name: "local tier populated", mutate: func(lease *RoutingLease) { lease.ECITier = ECITier2C4GiB }},
	}
	runRoutingLeaseMutations(t, local, mutations, fixture)

	eci := validRoutingLease(fixture, 0, LeaseStateStarted, fixture.now.UnixMilli()-5_000, fixture.now.UnixMilli()-3_000, 0)
	runRoutingLeaseMutations(t, eci, []routingLeaseMutation{
		{name: "ECI executor drift", mutate: func(lease *RoutingLease) { lease.Executor = ExecutorKindLocalNode }},
		{name: "ECI node populated", mutate: func(lease *RoutingLease) { lease.NodeID = "node-local" }},
		{name: "ECI endpoint populated", mutate: func(lease *RoutingLease) { lease.Endpoint = "https://eci.invalid/runner" }},
		{name: "ECI probe identity populated", mutate: func(lease *RoutingLease) { lease.ProbeIdentity = "probe-eci" }},
		{name: "ECI probe boot id populated", mutate: func(lease *RoutingLease) { lease.ProbeBootID = "boot-eci" }},
		{name: "ECI tier empty", mutate: func(lease *RoutingLease) { lease.ECITier = "" }},
		{name: "ECI tier drift", mutate: func(lease *RoutingLease) { lease.ECITier = ECITier8C16GiB }},
		{name: "ECI tier unknown", mutate: func(lease *RoutingLease) { lease.ECITier = ECIResourceTier("eci/unknown") }},
	}, fixture)
}

func TestRoutingLeaseValidateAgainstErrorsDoNotLeakIdentitySecrets(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	base := validRoutingLease(fixture, 1, LeaseStateStarted, fixture.now.UnixMilli()-5_000, fixture.now.UnixMilli()-3_000, 0)
	endpointURL := "https://routing-lease.invalid/runner"
	endpointToken := "endpoint-token-sentinel"
	probeIdentitySecret := "probe-identity-secret-sentinel"
	probeBootIDSecret := "probe-boot-secret-sentinel"
	forbidden := []string{endpointURL, endpointToken, probeIdentitySecret, probeBootIDSecret}
	mutations := []routingLeaseSecretMutation{
		{name: "endpoint URL and token", field: "endpoint", mutate: func(lease *RoutingLease) {
			lease.Endpoint = endpointURL + "?token=" + endpointToken
		}},
		{name: "probe identity secret", field: "probe_identity", mutate: func(lease *RoutingLease) {
			lease.ProbeIdentity = probeIdentitySecret
		}},
		{name: "probe boot secret", field: "probe_boot_id", mutate: func(lease *RoutingLease) {
			lease.ProbeBootID = probeBootIDSecret
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			assertRoutingLeaseErrorOmitsSecrets(t, base, mutation, forbidden, fixture)
		})
	}
}

func assertRoutingLeaseErrorOmitsSecrets(t *testing.T, base RoutingLease, mutation routingLeaseSecretMutation, forbidden []string, fixture routingValidationFixture) {
	t.Helper()
	lease := base
	mutation.mutate(&lease)
	err := lease.ValidateAgainst(fixture.decision, fixture.now)
	if err == nil {
		t.Fatalf("secret-bearing %s mutation unexpectedly accepted", mutation.name)
	}
	message := err.Error()
	if !strings.Contains(message, mutation.field) {
		t.Fatalf("validation error %q lacks safe field context %q", message, mutation.field)
	}
	for _, secret := range forbidden {
		if strings.Contains(message, secret) {
			t.Fatalf("validation error leaks forbidden sentinel %q: %q", secret, message)
		}
	}
}

func TestRoutingLeaseValidateAgainstRejectsTimestampMatrix(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	states := []RoutingLeaseState{LeaseStatePlanned, LeaseStateStarted, LeaseStateSucceeded, LeaseStateFailed, LeaseStateCancelled}
	for _, state := range states {
		for _, unit := range []int{0, 1} {
			t.Run(string(state)+"/"+fixture.units[unit].UnitID, func(t *testing.T) {
				base := validRoutingLease(fixture, unit, state, fixture.now.UnixMilli()-5_000,
					stateStartedAt(fixture.now, state), stateFinishedAt(fixture.now, state))
				cases := []routingLeaseMutation{
					{name: "reserved zero", mutate: func(lease *RoutingLease) { lease.ReservedAtUnixMilli = 0 }},
					{name: "reserved future", mutate: func(lease *RoutingLease) { lease.ReservedAtUnixMilli = fixture.now.UnixMilli() + 1 }},
					{name: "started future", mutate: func(lease *RoutingLease) { lease.StartedAtUnixMilli = fixture.now.UnixMilli() + 1 }},
					{name: "finished future", mutate: func(lease *RoutingLease) { lease.FinishedAtUnixMilli = fixture.now.UnixMilli() + 1 }},
				}
				if state == LeaseStatePlanned {
					cases = append(cases,
						routingLeaseMutation{name: "planned started", mutate: func(lease *RoutingLease) { lease.StartedAtUnixMilli = fixture.now.UnixMilli() - 1 }},
						routingLeaseMutation{name: "planned finished", mutate: func(lease *RoutingLease) { lease.FinishedAtUnixMilli = fixture.now.UnixMilli() - 1 }})
				}
				if state == LeaseStateStarted {
					cases = append(cases,
						routingLeaseMutation{name: "started missing timestamp", mutate: func(lease *RoutingLease) { lease.StartedAtUnixMilli = 0 }},
						routingLeaseMutation{name: "started finished", mutate: func(lease *RoutingLease) { lease.FinishedAtUnixMilli = fixture.now.UnixMilli() - 1 }})
				}
				if state == LeaseStateSucceeded || state == LeaseStateFailed || state == LeaseStateCancelled {
					if state == LeaseStateCancelled {
						cases = append(cases,
							routingLeaseMutation{name: "cancelled started", mutate: func(lease *RoutingLease) { lease.StartedAtUnixMilli = lease.ReservedAtUnixMilli }},
							routingLeaseMutation{name: "cancelled missing finished", mutate: func(lease *RoutingLease) { lease.FinishedAtUnixMilli = 0 }},
							routingLeaseMutation{name: "cancelled finish before reserved", mutate: func(lease *RoutingLease) { lease.FinishedAtUnixMilli = lease.ReservedAtUnixMilli - 1 }})
					} else {
						cases = append(cases,
							routingLeaseMutation{name: "terminal missing started", mutate: func(lease *RoutingLease) { lease.StartedAtUnixMilli = 0 }},
							routingLeaseMutation{name: "terminal missing finished", mutate: func(lease *RoutingLease) { lease.FinishedAtUnixMilli = 0 }},
							routingLeaseMutation{name: "finished before started", mutate: func(lease *RoutingLease) { lease.FinishedAtUnixMilli = lease.StartedAtUnixMilli - 1 }},
							routingLeaseMutation{name: "finished before reserved", mutate: func(lease *RoutingLease) {
								lease.StartedAtUnixMilli = lease.ReservedAtUnixMilli + 2
								lease.FinishedAtUnixMilli = lease.ReservedAtUnixMilli - 1
							}},
							routingLeaseMutation{name: "started before reserved", mutate: func(lease *RoutingLease) { lease.StartedAtUnixMilli = lease.ReservedAtUnixMilli - 1 }})
					}
				}
				runRoutingLeaseMutations(t, base, cases, fixture)
			})
		}
	}

}

func TestRoutingLeaseValidateAgainstRejectsUnknownState(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	lease := validRoutingLease(fixture, 0, LeaseStateStarted, fixture.now.UnixMilli()-5_000, fixture.now.UnixMilli()-3_000, 0)
	lease.State = RoutingLeaseState("running")
	if err := lease.ValidateAgainst(fixture.decision, fixture.now); err == nil {
		t.Fatal("unknown lease state unexpectedly accepted")
	}
}

func TestRoutingLeaseValidateAgainstRejectsEmptyDecisionDigest(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	lease := validRoutingLease(fixture, 0, LeaseStatePlanned, fixture.now.UnixMilli()-5_000, 0, 0)
	fixture.decision.DecisionDigest = ""
	if err := lease.ValidateAgainst(fixture.decision, fixture.now); err == nil {
		t.Fatal("empty decision digest unexpectedly accepted")
	}
}

func TestRoutingLeaseWireHasExactFieldsAndNoFallbackOrRetry(t *testing.T) {
	typ := reflect.TypeFor[RoutingLease]()
	wantFields := routingLeaseWireFields()
	assertRoutingLeaseReflection(t, typ, wantFields)
	fields, raw := marshalRoutingLeaseWire(t)
	assertRoutingLeaseWireFields(t, fields, wantFields)
	assertNoForbiddenRoutingLeaseFields(t, fields, raw)
}

func routingLeaseWireFields() []routingLeaseWireField {
	return []routingLeaseWireField{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), tag: "schema_version"},
		{name: "Authority", typ: reflect.TypeFor[string](), tag: "authority"},
		{name: "RunID", typ: reflect.TypeFor[string](), tag: "run_id"},
		{name: "DecisionDigest", typ: reflect.TypeFor[string](), tag: "decision_digest"},
		{name: "UnitID", typ: reflect.TypeFor[string](), tag: "unit_id"},
		{name: "Executor", typ: reflect.TypeFor[ExecutorKind](), tag: "executor"},
		{name: "NodeID", typ: reflect.TypeFor[string](), tag: "node_id"},
		{name: "Endpoint", typ: reflect.TypeFor[string](), tag: "endpoint"},
		{name: "ProbeIdentity", typ: reflect.TypeFor[string](), tag: "probe_identity"},
		{name: "ProbeBootID", typ: reflect.TypeFor[string](), tag: "probe_boot_id"},
		{name: "ECITier", typ: reflect.TypeFor[ECIResourceTier](), tag: "eci_tier"},
		{name: "State", typ: reflect.TypeFor[RoutingLeaseState](), tag: "state"},
		{name: "ReservedAtUnixMilli", typ: reflect.TypeFor[int64](), tag: "reserved_at_unix_milli"},
		{name: "StartedAtUnixMilli", typ: reflect.TypeFor[int64](), tag: "started_at_unix_milli"},
		{name: "FinishedAtUnixMilli", typ: reflect.TypeFor[int64](), tag: "finished_at_unix_milli"},
	}
}

func assertRoutingLeaseReflection(t *testing.T, typ reflect.Type, wantFields []routingLeaseWireField) {
	t.Helper()
	if typ.NumField() != len(wantFields) {
		t.Fatalf("RoutingLease field count = %d, want %d", typ.NumField(), len(wantFields))
	}
	for index, want := range wantFields {
		field := typ.Field(index)
		if field.Name != want.name || field.Type != want.typ {
			t.Fatalf("RoutingLease field %d = %s/%s, want %s/%s", index, field.Name, field.Type, want.name, want.typ)
		}
		got, ok := field.Tag.Lookup("json")
		if !ok || got != want.tag {
			t.Fatalf("RoutingLease.%s json tag = %q, want %q", field.Name, got, want.tag)
		}
	}
}

func marshalRoutingLeaseWire(t *testing.T) (map[string]json.RawMessage, []byte) {
	t.Helper()
	lease := RoutingLease{SchemaVersion: RoutingLeaseSchemaVersion, Authority: AuthorityLocalNonAuthoritative, RunID: "run-valid", State: LeaseStatePlanned}
	raw, err := json.Marshal(lease)
	if err != nil {
		t.Fatalf("marshal RoutingLease: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode RoutingLease: %v", err)
	}
	return fields, raw
}

func assertRoutingLeaseWireFields(t *testing.T, fields map[string]json.RawMessage, wantFields []routingLeaseWireField) {
	t.Helper()
	if len(fields) != len(wantFields) {
		t.Fatalf("RoutingLease wire field count = %d, want %d", len(fields), len(wantFields))
	}
	for _, field := range wantFields {
		if _, exists := fields[field.tag]; !exists {
			t.Fatalf("RoutingLease wire omitted %q", field.tag)
		}
	}
}

func assertNoForbiddenRoutingLeaseFields(t *testing.T, fields map[string]json.RawMessage, raw []byte) {
	t.Helper()
	for _, forbidden := range []string{"fallback", "retry"} {
		if _, exists := fields[forbidden]; exists || strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Fatalf("RoutingLease wire exposes forbidden %q field: %s", forbidden, raw)
		}
	}
}

func validRoutingLease(fixture routingValidationFixture, assignmentIndex int, state RoutingLeaseState, reserved, started, finished int64) RoutingLease {
	assignment := fixture.decision.Assignments[assignmentIndex]
	return RoutingLease{
		SchemaVersion:       RoutingLeaseSchemaVersion,
		Authority:           AuthorityLocalNonAuthoritative,
		RunID:               "run-valid",
		DecisionDigest:      fixture.decision.DecisionDigest,
		UnitID:              assignment.UnitID,
		Executor:            assignment.Executor,
		NodeID:              assignment.NodeID,
		Endpoint:            assignment.Endpoint,
		ProbeIdentity:       assignment.ProbeIdentity,
		ProbeBootID:         assignment.ProbeBootID,
		ECITier:             assignment.ECITier,
		State:               state,
		ReservedAtUnixMilli: reserved,
		StartedAtUnixMilli:  started,
		FinishedAtUnixMilli: finished,
	}
}

func runRoutingLeaseMutations(t *testing.T, base RoutingLease, mutations []routingLeaseMutation, fixture routingValidationFixture) {
	t.Helper()
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			lease := base
			mutation.mutate(&lease)
			if err := lease.ValidateAgainst(fixture.decision, fixture.now); err == nil {
				t.Fatalf("invalid lease mutation unexpectedly accepted: %+v", lease)
			}
		})
	}
}

func stateStartedAt(now time.Time, state RoutingLeaseState) int64 {
	if state == LeaseStatePlanned || state == LeaseStateCancelled {
		return 0
	}
	return now.UnixMilli() - 3_000
}

func stateFinishedAt(now time.Time, state RoutingLeaseState) int64 {
	if state == LeaseStateSucceeded || state == LeaseStateFailed || state == LeaseStateCancelled {
		return now.UnixMilli() - 1_000
	}
	return 0
}
