package heteroci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRoutingDecisionCanonicalDigestMatchesIndependentWireOracleAndIsStable(t *testing.T) {
	decision := routingDecisionDigestFixture()
	want := routingDecisionDigestOracle(t, decision)

	for iteration := range 4 {
		got, err := decision.CanonicalDigest()
		if err != nil {
			t.Fatalf("CanonicalDigest() iteration %d: %v", iteration, err)
		}
		if got != want {
			t.Fatalf("CanonicalDigest() iteration %d = %q, want independent oracle %q", iteration, got, want)
		}
	}
	if !strings.HasPrefix(want, "sha256:") {
		t.Fatalf("canonical digest = %q, want sha256: prefix", want)
	}
	encoded := strings.TrimPrefix(want, "sha256:")
	if len(encoded) != sha256.Size*2 {
		t.Fatalf("canonical digest hex length = %d, want %d", len(encoded), sha256.Size*2)
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		t.Fatalf("canonical digest hex payload %q: %v", encoded, err)
	}
}

func TestRoutingDecisionCanonicalDigestUsesFrozenWireOrderAndBytes(t *testing.T) {
	decision := routingDecisionDigestFixture()
	encoded := routingDecisionDigestWireJSON(t, decision)
	wantJSON := `{"schema_version":"` + RoutingDecisionSchemaVersion + `","authority":"LOCAL_NON_AUTHORITATIVE","snapshot_captured_at_unix_milli":1712345678901,"packing_plan_digest":"sha256:` +
		strings.Repeat("1", sha256.Size*2) +
		`","assignments":[{"unit_id":"unit-a","executor":"local_node","node_id":"node-linux-1","endpoint":"http://node-linux-1:9100","probe_identity":"probe-linux-1","probe_boot_id":"boot-linux-1"},{"unit_id":"unit-b","executor":"local_node","node_id":"node-linux-2","endpoint":"http://node-linux-2:9100","probe_identity":"probe-linux-2","probe_boot_id":"boot-linux-2"}],"decision_digest":""}`
	if string(encoded) != wantJSON {
		t.Fatalf("canonical wire JSON changed:\n got=%s\nwant=%s", encoded, wantJSON)
	}
	wantSum := sha256.Sum256([]byte(wantJSON))
	wantDigest := "sha256:" + hex.EncodeToString(wantSum[:])
	got, err := decision.CanonicalDigest()
	if err != nil {
		t.Fatalf("CanonicalDigest(): %v", err)
	}
	if got != wantDigest {
		t.Fatalf("canonical digest = %q, want fixed wire digest %q", got, wantDigest)
	}
}

func TestRoutingDecisionCanonicalDigestIgnoresOwnDigestAndDoesNotMutateDecision(t *testing.T) {
	original := routingDecisionDigestFixture()
	decision := original
	decision.Assignments = append([]RouteAssignment(nil), original.Assignments...)

	got, err := decision.CanonicalDigest()
	if err != nil {
		t.Fatalf("CanonicalDigest(): %v", err)
	}
	if !reflect.DeepEqual(decision, original) {
		t.Fatalf("CanonicalDigest mutated decision:\n got=%#v\nwant=%#v", decision, original)
	}

	changedDigest := original
	changedDigest.DecisionDigest = "sha256:" + strings.Repeat("b", sha256.Size*2)
	changed, err := changedDigest.CanonicalDigest()
	if err != nil {
		t.Fatalf("CanonicalDigest() with changed DecisionDigest: %v", err)
	}
	if changed != got {
		t.Fatalf("DecisionDigest changed canonical digest: empty=%q changed=%q", got, changed)
	}
}

func TestRoutingDecisionCanonicalDigestChangesForEveryCanonicalField(t *testing.T) {
	baselineDecision := routingDecisionDigestFixture()
	baseline := routingDecisionDigestOracle(t, baselineDecision)
	cases := []struct {
		name   string
		mutate func(*RoutingDecision)
	}{
		{name: "schema_version", mutate: func(decision *RoutingDecision) { decision.SchemaVersion = "heteroci-routing/v2" }},
		{name: "authority", mutate: func(decision *RoutingDecision) { decision.Authority = "REMOTE" }},
		{name: "snapshot_captured_at_unix_milli", mutate: func(decision *RoutingDecision) { decision.SnapshotCapturedAtUnixMilli++ }},
		{name: "packing_plan_digest", mutate: func(decision *RoutingDecision) {
			decision.PackingPlanDigest = "sha256:" + strings.Repeat("2", sha256.Size*2)
		}},
		{name: "assignment_unit_id", mutate: func(decision *RoutingDecision) { decision.Assignments[0].UnitID = "unit-a-changed" }},
		{name: "assignment_executor", mutate: func(decision *RoutingDecision) { decision.Assignments[0].Executor = ExecutorKind("future_executor") }},
		{name: "assignment_node_id", mutate: func(decision *RoutingDecision) { decision.Assignments[0].NodeID = "node-linux-2" }},
		{name: "assignment_endpoint", mutate: func(decision *RoutingDecision) { decision.Assignments[0].Endpoint = "http://node-linux-2:9100" }},
		{name: "assignment_probe_identity", mutate: func(decision *RoutingDecision) { decision.Assignments[0].ProbeIdentity = "probe-linux-2" }},
		{name: "assignment_probe_boot_id", mutate: func(decision *RoutingDecision) { decision.Assignments[0].ProbeBootID = "boot-linux-2" }},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			decision := routingDecisionDigestFixture()
			testCase.mutate(&decision)
			got, err := decision.CanonicalDigest()
			if err != nil {
				t.Fatalf("CanonicalDigest(): %v", err)
			}
			if got == baseline {
				t.Fatalf("changing %s did not change canonical digest %q", testCase.name, got)
			}
			if got != routingDecisionDigestOracle(t, decision) {
				t.Fatalf("changing %s produced digest inconsistent with independent oracle", testCase.name)
			}
		})
	}
}

func TestRoutingDecisionCanonicalDigestChangesWhenAssignmentOrderChanges(t *testing.T) {
	decision := routingDecisionDigestFixture()
	baseline, err := decision.CanonicalDigest()
	if err != nil {
		t.Fatalf("CanonicalDigest() baseline: %v", err)
	}
	decision.Assignments = append([]RouteAssignment(nil), decision.Assignments...)
	decision.Assignments[0], decision.Assignments[1] = decision.Assignments[1], decision.Assignments[0]

	got, err := decision.CanonicalDigest()
	if err != nil {
		t.Fatalf("CanonicalDigest() reordered assignments: %v", err)
	}
	if got == baseline {
		t.Fatalf("reordering assignments left canonical digest unchanged: %q", got)
	}
	if got != routingDecisionDigestOracle(t, decision) {
		t.Fatalf("reordered assignments produced digest inconsistent with independent oracle")
	}
}

func routingDecisionDigestFixture() RoutingDecision {
	return RoutingDecision{
		SchemaVersion:               RoutingDecisionSchemaVersion,
		Authority:                   "LOCAL_NON_AUTHORITATIVE",
		SnapshotCapturedAtUnixMilli: 1712345678901,
		PackingPlanDigest:           "sha256:" + strings.Repeat("1", sha256.Size*2),
		Assignments: []RouteAssignment{
			{
				UnitID:        "unit-a",
				Executor:      ExecutorKindLocalNode,
				NodeID:        "node-linux-1",
				Endpoint:      "http://node-linux-1:9100",
				ProbeIdentity: "probe-linux-1",
				ProbeBootID:   "boot-linux-1",
			},
			{
				UnitID:        "unit-b",
				Executor:      ExecutorKindLocalNode,
				NodeID:        "node-linux-2",
				Endpoint:      "http://node-linux-2:9100",
				ProbeIdentity: "probe-linux-2",
				ProbeBootID:   "boot-linux-2",
			},
		},
		DecisionDigest: "sha256:" + strings.Repeat("a", sha256.Size*2),
	}
}

type routingDecisionDigestWire struct {
	SchemaVersion               string                        `json:"schema_version"`
	Authority                   string                        `json:"authority"`
	SnapshotCapturedAtUnixMilli int64                         `json:"snapshot_captured_at_unix_milli"`
	PackingPlanDigest           string                        `json:"packing_plan_digest"`
	Assignments                 []routingAssignmentDigestWire `json:"assignments"`
	DecisionDigest              string                        `json:"decision_digest"`
}

type routingAssignmentDigestWire struct {
	UnitID        string `json:"unit_id"`
	Executor      string `json:"executor"`
	NodeID        string `json:"node_id"`
	Endpoint      string `json:"endpoint"`
	ProbeIdentity string `json:"probe_identity"`
	ProbeBootID   string `json:"probe_boot_id"`
}

func routingDecisionDigestOracle(t *testing.T, decision RoutingDecision) string {
	t.Helper()
	encoded := routingDecisionDigestWireJSON(t, decision)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func routingDecisionDigestWireJSON(t *testing.T, decision RoutingDecision) []byte {
	t.Helper()
	var assignments []routingAssignmentDigestWire
	if decision.Assignments != nil {
		assignments = make([]routingAssignmentDigestWire, len(decision.Assignments))
		for index, assignment := range decision.Assignments {
			assignments[index] = routingAssignmentDigestWire{
				UnitID:        assignment.UnitID,
				Executor:      string(assignment.Executor),
				NodeID:        assignment.NodeID,
				Endpoint:      assignment.Endpoint,
				ProbeIdentity: assignment.ProbeIdentity,
				ProbeBootID:   assignment.ProbeBootID,
			}
		}
	}
	material := routingDecisionDigestWire{
		SchemaVersion:               decision.SchemaVersion,
		Authority:                   decision.Authority,
		SnapshotCapturedAtUnixMilli: decision.SnapshotCapturedAtUnixMilli,
		PackingPlanDigest:           decision.PackingPlanDigest,
		Assignments:                 assignments,
		DecisionDigest:              "",
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		t.Fatalf("json.Marshal independent routing decision wire oracle: %v", err)
	}
	return encoded
}
