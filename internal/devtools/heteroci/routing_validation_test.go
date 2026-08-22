package heteroci

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type routingValidationFixture struct {
	now          time.Time
	heartbeatTTL time.Duration
	manifest     Manifest
	snapshot     SnapshotSet
	units        []ExecutionUnit
	plan         PackingPlan
	decision     RoutingDecision
}

type routingDecisionMutation struct {
	name   string
	mutate func(*routingValidationFixture)
}

func TestRoutingDecisionValidateAgainstAcceptsCanonicalLocalAssignments(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	if fixture.decision.SchemaVersion != "heteroci-routing-decision/v4" {
		t.Fatalf("routing decision schema = %q, want heteroci-routing-decision/v4", fixture.decision.SchemaVersion)
	}
	if fixture.decision.Authority != AuthorityLocalNonAuthoritative {
		t.Fatalf("routing decision authority = %q, want %q", fixture.decision.Authority, AuthorityLocalNonAuthoritative)
	}
	if err := fixture.decision.ValidateAgainst(
		fixture.manifest,
		fixture.snapshot,
		fixture.units,
		fixture.plan,
		fixture.now,
		fixture.heartbeatTTL,
	); err != nil {
		t.Fatalf("RoutingDecision.ValidateAgainst() unexpected error: %v", err)
	}
}

func TestRoutingDecisionValidateAgainstRejectsIdentityDrift(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	mutations := []routingDecisionMutation{
		{name: "schema empty", mutate: func(f *routingValidationFixture) { f.decision.SchemaVersion = "" }},
		{name: "schema old v1", mutate: func(f *routingValidationFixture) { f.decision.SchemaVersion = "heteroci-routing-decision/v1" }},
		{name: "schema old", mutate: func(f *routingValidationFixture) { f.decision.SchemaVersion = "heteroci-routing-decision/v0" }},
		{name: "schema unknown", mutate: func(f *routingValidationFixture) { f.decision.SchemaVersion = "heteroci-routing-decision/v99" }},
		{name: "authority empty", mutate: func(f *routingValidationFixture) { f.decision.Authority = "" }},
		{name: "authority remote", mutate: func(f *routingValidationFixture) { f.decision.Authority = "REMOTE_AUTHORITATIVE" }},
		{name: "capture zero", mutate: func(f *routingValidationFixture) { f.decision.SnapshotCapturedAtUnixMilli = 0 }},
		{name: "capture drift", mutate: func(f *routingValidationFixture) { f.decision.SnapshotCapturedAtUnixMilli++ }},
		{name: "packing digest drift", mutate: func(f *routingValidationFixture) { f.decision.PackingPlanDigest = "sha256:" + strings.Repeat("0", 64) }},
		{name: "decision digest drift", mutate: func(f *routingValidationFixture) { f.decision.DecisionDigest = "sha256:" + strings.Repeat("1", 64) }},
	}
	runRoutingDecisionMutations(t, fixture, mutations)
}

func TestRoutingDecisionValidateAgainstRejectsAssignmentsCoverageAndOrderDrift(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	mutations := []routingDecisionMutation{
		{name: "assignments nil", mutate: func(f *routingValidationFixture) { f.decision.Assignments = nil }},
		{name: "assignments count", mutate: func(f *routingValidationFixture) { f.decision.Assignments = f.decision.Assignments[:1] }},
		{name: "assignment missing", mutate: func(f *routingValidationFixture) { f.decision.Assignments = f.decision.Assignments[1:] }},
		{name: "assignment order", mutate: func(f *routingValidationFixture) {
			f.decision.Assignments[0], f.decision.Assignments[1] = f.decision.Assignments[1], f.decision.Assignments[0]
		}},
		{name: "assignment duplicate", mutate: func(f *routingValidationFixture) {
			f.decision.Assignments[1] = f.decision.Assignments[0]
		}},
		{name: "assignment unknown", mutate: func(f *routingValidationFixture) {
			f.decision.Assignments[0].UnitID = "unit-aaa-unknown"
		}},
	}
	runRoutingDecisionMutations(t, fixture, mutations)
}

func TestRoutingDecisionValidateAgainstRejectsLocalBoundaryDrift(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	mutations := []routingDecisionMutation{
		{name: "local node drift", mutate: func(f *routingValidationFixture) {
			f.decision.Assignments[1].NodeID = "heteroci-node-drift"
		}},
		{name: "local endpoint drift", mutate: func(f *routingValidationFixture) {
			f.decision.Assignments[1].Endpoint = "https://drift.invalid/runner"
		}},
		{name: "local probe identity drift", mutate: func(f *routingValidationFixture) {
			f.decision.Assignments[1].ProbeIdentity = "sha256:" + strings.Repeat("f", 64)
		}},
		{name: "local probe boot id drift", mutate: func(f *routingValidationFixture) {
			f.decision.Assignments[1].ProbeBootID = "boot-drift"
		}},
		{name: "local assignment drift", mutate: func(f *routingValidationFixture) {
			f.decision.Assignments[0].NodeID = "heteroci-forged"
		}},
	}
	runRoutingDecisionMutations(t, fixture, mutations)
}

func TestRoutingDecisionValidateAgainstRedactsSensitiveLocalIdentityDrift(t *testing.T) {
	cases := []struct {
		name      string
		field     string
		sentinel  string
		forbidden []string
		mutate    func(*routingValidationFixture, string)
	}{
		{
			name:      "endpoint URL token",
			field:     "endpoint",
			sentinel:  "https://runner.example.invalid/run?token=R9B_ENDPOINT_TOKEN",
			forbidden: []string{"https://runner.example.invalid/run", "R9B_ENDPOINT_TOKEN"},
			mutate: func(f *routingValidationFixture, sentinel string) {
				f.decision.Assignments[1].Endpoint = sentinel
			},
		},
		{
			name:      "probe identity secret",
			field:     "probe_identity",
			sentinel:  "sha256:R9B_PROBE_IDENTITY_SECRET",
			forbidden: []string{"R9B_PROBE_IDENTITY_SECRET"},
			mutate: func(f *routingValidationFixture, sentinel string) {
				f.decision.Assignments[1].ProbeIdentity = sentinel
			},
		},
		{
			name:      "probe boot secret",
			field:     "probe_boot_id",
			sentinel:  "boot:R9B_PROBE_BOOT_SECRET",
			forbidden: []string{"R9B_PROBE_BOOT_SECRET"},
			mutate: func(f *routingValidationFixture, sentinel string) {
				f.decision.Assignments[1].ProbeBootID = sentinel
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRoutingValidationMixedFixture(t)
			tc.mutate(&fixture, tc.sentinel)
			err := fixture.decision.ValidateAgainst(
				fixture.manifest,
				fixture.snapshot,
				fixture.units,
				fixture.plan,
				fixture.now,
				fixture.heartbeatTTL,
			)
			if err == nil {
				t.Fatal("sensitive local identity drift must be rejected")
			}
			message := err.Error()
			if !strings.Contains(message, "local") || !strings.Contains(message, tc.field) {
				t.Fatalf("error %q must retain local %s validation context", message, tc.field)
			}
			for _, forbidden := range append([]string{tc.sentinel}, tc.forbidden...) {
				if strings.Contains(message, forbidden) {
					t.Fatalf("error %q must not disclose sensitive sentinel %q", message, forbidden)
				}
			}
		})
	}
}

func TestRoutingDecisionUsesLocalAuthorityAndNoFallbackFields(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	if fixture.manifest.Authority != AuthorityLocalNonAuthoritative ||
		fixture.snapshot.Authority != AuthorityLocalNonAuthoritative ||
		fixture.plan.Authority != AuthorityLocalNonAuthoritative ||
		fixture.decision.Authority != AuthorityLocalNonAuthoritative {
		t.Fatal("routing fixture must keep every local artifact LOCAL_NON_AUTHORITATIVE")
	}
	raw, err := json.Marshal(fixture.decision)
	if err != nil {
		t.Fatalf("marshal routing decision: %v", err)
	}
	if strings.Contains(string(raw), "fallback") {
		t.Fatalf("routing decision wire unexpectedly contains fallback field or value: %s", raw)
	}
	assertRoutingDecisionWireAssignments(t, raw)
}

func assertRoutingDecisionWireAssignments(t *testing.T, raw []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode routing decision wire: %v", err)
	}
	if _, exists := fields["fallback"]; exists {
		t.Fatal("routing decision wire must not expose a fallback field")
	}
	var wire struct {
		Assignments []map[string]json.RawMessage `json:"assignments"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode routing assignments wire: %v", err)
	}
	if len(wire.Assignments) != 2 {
		t.Fatalf("wire assignments = %d, want 2", len(wire.Assignments))
	}
	for index, assignment := range wire.Assignments {
		if string(assignment["executor"]) != `"local_node"` || string(assignment["node_id"]) == `""` {
			t.Fatalf("assignment %d is not a local fixed-node binding: %v", index, assignment)
		}
	}
}

func TestRoutingDecisionValidateAgainstRejectsInvalidPlanBeforeIdentity(t *testing.T) {
	fixture := newRoutingValidationMixedFixture(t)
	fixture.plan.NodeShards[0].Waves[0].CPUMilli++
	fixture.decision.SchemaVersion = ""
	err := fixture.decision.ValidateAgainst(
		fixture.manifest,
		fixture.snapshot,
		fixture.units,
		fixture.plan,
		fixture.now,
		fixture.heartbeatTTL,
	)
	if err == nil || !strings.HasPrefix(err.Error(), "validate routing packing plan:") {
		t.Fatalf("invalid plan must be rejected before routing identity, err=%v", err)
	}
}

func newRoutingValidationMixedFixture(t *testing.T) routingValidationFixture {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	manifest := testManifest(2)
	snapshot := snapshotForManifest(manifest, now)
	units := []ExecutionUnit{
		executionUnitTestUnit("unit-local", RuntimeUbuntuContainer, PlatformLinuxAMD64),
		executionUnitTestUnit("unit-eci", RuntimeUbuntuContainer, PlatformLinuxAMD64),
	}
	plan := PackingPlan{
		SchemaVersion: PackingPlanSchemaVersion, Authority: AuthorityLocalNonAuthoritative, SnapshotCapturedAtUnixMilli: snapshot.CapturedAtUnixMilli,
		NodeShards:        []NodeShard{{NodeID: manifest.Nodes[0].NodeID, Waves: []ExecutionWave{{Wave: 0, UnitIDs: []string{"unit-eci", "unit-local"}, CPUMilli: units[0].CPUMilli + units[1].CPUMilli, MemoryBytes: units[0].MemoryBytes + units[1].MemoryBytes, Concurrency: 2, EstimatedDurationMS: units[0].EstimatedDurationMS}}, EstimatedMakespanMS: units[0].EstimatedDurationMS}},
		UnassignedUnitIDs: []string{},
	}
	packingDigest, err := plan.CanonicalDigest()
	if err != nil {
		t.Fatalf("compute packing digest: %v", err)
	}
	assignments := []RouteAssignment{
		{UnitID: "unit-eci", Executor: ExecutorKindLocalNode, NodeID: manifest.Nodes[0].NodeID, Endpoint: manifest.Nodes[0].Endpoint, ProbeIdentity: snapshot.Nodes[0].ProbeIdentity, ProbeBootID: snapshot.Nodes[0].ProbeBootID},
		{UnitID: "unit-local", Executor: ExecutorKindLocalNode, NodeID: manifest.Nodes[0].NodeID, Endpoint: manifest.Nodes[0].Endpoint, ProbeIdentity: snapshot.Nodes[0].ProbeIdentity, ProbeBootID: snapshot.Nodes[0].ProbeBootID},
	}
	decision := RoutingDecision{SchemaVersion: RoutingDecisionSchemaVersion, Authority: AuthorityLocalNonAuthoritative, SnapshotCapturedAtUnixMilli: snapshot.CapturedAtUnixMilli, PackingPlanDigest: packingDigest, Assignments: assignments}
	decision.DecisionDigest, err = decision.CanonicalDigest()
	if err != nil {
		t.Fatalf("compute decision digest: %v", err)
	}
	return routingValidationFixture{now: now, heartbeatTTL: time.Minute, manifest: manifest, snapshot: snapshot, units: units, plan: plan, decision: decision}
}

func cloneRoutingValidationFixture(source routingValidationFixture) routingValidationFixture {
	clone := source
	clone.manifest.Nodes = append([]NodeConfig(nil), source.manifest.Nodes...)
	for index := range clone.manifest.Nodes {
		clone.manifest.Nodes[index].Capabilities = append([]CapabilityConfig(nil), source.manifest.Nodes[index].Capabilities...)
	}
	clone.snapshot = cloneSnapshotSet(source.snapshot)
	clone.units = append([]ExecutionUnit(nil), source.units...)
	clone.plan = clonePackingPlan(source.plan)
	clone.decision.Assignments = append([]RouteAssignment(nil), source.decision.Assignments...)
	return clone
}

func runRoutingDecisionMutations(t *testing.T, fixture routingValidationFixture, mutations []routingDecisionMutation) {
	t.Helper()
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := cloneRoutingValidationFixture(fixture)
			mutation.mutate(&candidate)
			assertRoutingDecisionInvalid(t, candidate)
		})
	}
}

func assertRoutingDecisionInvalid(t *testing.T, fixture routingValidationFixture) {
	t.Helper()
	err := fixture.decision.ValidateAgainst(
		fixture.manifest,
		fixture.snapshot,
		fixture.units,
		fixture.plan,
		fixture.now,
		fixture.heartbeatTTL,
	)
	if err == nil {
		t.Fatalf("RoutingDecision.ValidateAgainst() unexpectedly accepted invalid fixture: %+v", fixture.decision)
	}
}
