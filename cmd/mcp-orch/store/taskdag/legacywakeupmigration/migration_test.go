package legacywakeupmigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func row(id int64, kind, target, payload string) Row {
	return Row{ID: id, WakeupKind: kind, TargetAgentID: target, PromptPayload: []byte(payload), IdempotencyKey: "old-" + string(rune('a'+id)), UpdatedAt: 7}
}

func versionedRow(id int64, kind, target, payload string) Row {
	r := row(id, kind, target, payload)
	r.SourcePayloadSchemaVersion = SchemaVersion
	return r
}

func TestBuildPlanMigratesDeterministicLegacyLaunch(t *testing.T) {
	p := BuildPlan([]Row{row(2, WakeupKindStart, "agent-a", `{"AgentID":"agent-a","Prompt":"hello","Cwd":"/tmp"}`)})
	m := requireMigration(t, p)
	var got map[string]any
	if err := json.Unmarshal(m.NewPayload, &got); err != nil {
		t.Fatal(err)
	}
	assertLaunchFields(t, got)
}

func TestBuildPlanAcceptsCanonicalLegacyOptionalFields(t *testing.T) {
	p := BuildPlan([]Row{row(4, WakeupKindStart, "agent-a", `{"agent_id":"agent-a","prompt":"hello","cwd":"/tmp","command":["echo","ok"],"env":["A=B"]}`)})
	m := requireMigration(t, p)
	var got map[string]any
	if err := json.Unmarshal(m.NewPayload, &got); err != nil {
		t.Fatal(err)
	}
	if got["cwd"] != "/tmp" || !strings.Contains(string(m.NewPayload), `"command":["echo","ok"]`) || !strings.Contains(string(m.NewPayload), `"env":["A=B"]`) {
		t.Fatalf("canonical optional fields were not preserved: %#v", got)
	}
}

func requireMigration(t *testing.T, p Plan) *Mutation {
	t.Helper()
	if len(p.Actions) != 1 || p.Actions[0].Disposition != DispositionMigrate {
		t.Fatalf("plan = %#v", p)
	}
	m := p.Actions[0].Mutation
	if m == nil || m.NewKind != KindLegacyLaunch || m.NewPayloadSchemaVersion != SchemaVersion || m.ExpectedUpdatedAt != 7 || m.ExpectedPayloadDigest == "" || m.IdempotencyKey == "" {
		t.Fatalf("mutation = %#v", m)
	}
	return m
}

func assertLaunchFields(t *testing.T, got map[string]any) {
	t.Helper()
	if got["wakeup_payload_kind"] != KindLegacyLaunch || got["schema_version"] != float64(1) || got["agent_id"] != "agent-a" || got["cwd"] != "/tmp" {
		t.Fatalf("payload = %#v", got)
	}
}

func TestBuildPlanMigratesCamelCaseDAG(t *testing.T) {
	run := int64(9)
	r := row(3, WakeupKindNodeStart, "agent-b", `{"agentId":"agent-b","upstreamOutputs":[{"nodeKey":"a","Path":"dag/a/output.json"}]}`)
	r.DagKey, r.NodeKey, r.RunID = "dag-1", "b", &run
	p := BuildPlan([]Row{r})
	if p.Actions[0].Disposition != DispositionMigrate {
		t.Fatalf("action = %#v", p.Actions[0])
	}
	var got map[string]any
	if err := json.Unmarshal(p.Actions[0].Mutation.NewPayload, &got); err != nil {
		t.Fatal(err)
	}
	if got["wakeup_payload_kind"] != KindDAGDownstream {
		t.Fatalf("payload = %#v", got)
	}
}

func TestBuildPlanQuarantinesAmbiguousAndUnsafeRows(t *testing.T) {
	run := int64(1)
	cases := []struct {
		name   string
		r      Row
		reason QuarantineReason
	}{
		{"unknown wakeup", row(1, "mystery", "a", `{ "agent_id":"a" }`), ReasonUnknownWakeupKind},
		{"target mismatch", row(2, WakeupKindStart, "b", `{ "agent_id":"a" }`), ReasonTargetMismatch},
		{"duplicate key", row(3, WakeupKindStart, "a", `{ "agent_id":"a", "agent_id":"b" }`), ReasonMixedUnion},
		{"unknown field", row(4, WakeupKindStart, "a", `{ "agent_id":"a", "promt":"x" }`), ReasonUnknownFields},
		{"mixed union", row(5, WakeupKindStart, "a", `{ "agent_id":"a", "upstream_outputs":[] }`), ReasonMixedUnion},
		{"null field", row(9, WakeupKindStart, "a", `{ "agent_id":"a", "prompt":null }`), ReasonMissingRequiredField},
		{"dag identity", func() Row { r := row(6, WakeupKindNodeStart, "a", `{ "agent_id":"a" }`); r.RunID = &run; return r }(), ReasonDAGIdentityMismatch},
		{"malformed", row(7, WakeupKindStart, "a", `{`), ReasonMalformedJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := BuildPlan([]Row{tc.r}).Actions[0]
			if a.Disposition != DispositionQuarantine || a.Quarantine == nil || a.Quarantine.Reason != tc.reason {
				t.Fatalf("action = %#v, want %s", a, tc.reason)
			}
		})
	}
	tagged := versionedRow(12, WakeupKindStart, "a", `{"wakeup_payload_kind":"legacy_launch","schema_version":2,"agent_id":"a"}`)
	a := BuildPlan([]Row{tagged}).Actions[0]
	if a.Disposition != DispositionQuarantine || a.Quarantine.Reason != ReasonUnsupportedVersion {
		t.Fatalf("unsupported version = %#v", a)
	}
}

func TestBuildPlanTaggedValidationAndRepeatability(t *testing.T) {
	r := versionedRow(8, WakeupKindStart, "a", `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"a"}`)
	one, two := BuildPlan([]Row{r}), BuildPlan([]Row{r})
	if one.RunDigest != two.RunDigest {
		t.Fatalf("run digest changed: %s vs %s", one.RunDigest, two.RunDigest)
	}
	if one.Actions[0].Disposition != DispositionUnchanged {
		t.Fatalf("tagged action = %#v", one.Actions[0])
	}
	r.PromptPayload = []byte(`{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"other"}`)
	a := BuildPlan([]Row{r}).Actions[0]
	if a.Disposition != DispositionQuarantine || a.Quarantine.Reason != ReasonTargetMismatch {
		t.Fatalf("tagged mismatch = %#v", a)
	}
	r.PromptPayload = []byte(`{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"a","unknown":true}`)
	a = BuildPlan([]Row{r}).Actions[0]
	if a.Disposition != DispositionQuarantine || a.Quarantine.Reason != ReasonUnknownFields {
		t.Fatalf("tagged unknown = %#v", a)
	}
	r.PromptPayload = []byte(`{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"a"}`)
	a = BuildPlan([]Row{r}).Actions[0]
	if a.Disposition != DispositionQuarantine || a.Quarantine.Reason != ReasonMixedUnion {
		t.Fatalf("tagged kind mismatch = %#v", a)
	}
}

func TestBuildPlanQuarantinesTaggedOversizedPayload(t *testing.T) {
	r := versionedRow(9, WakeupKindStart, "a", `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"a","prompt":"`+strings.Repeat("x", maxPromptBytes+1)+`"}`)
	action := BuildPlan([]Row{r}).Actions[0]
	if action.Disposition != DispositionQuarantine || action.Quarantine == nil {
		t.Fatalf("oversized tagged action = %#v, want quarantine", action)
	}
}

func TestBuildPlanEnforcesSourcePayloadSchemaVersion(t *testing.T) {
	legacy := row(20, WakeupKindStart, "a", `{"agent_id":"a","prompt":"x"}`)
	if action := BuildPlan([]Row{legacy}).Actions[0]; action.Disposition != DispositionMigrate {
		t.Fatalf("version 0 legacy action = %#v", action)
	}
	canonical := versionedRow(21, WakeupKindStart, "a", `{"wakeup_payload_kind":"legacy_launch","schema_version":1,"agent_id":"a"}`)
	if action := BuildPlan([]Row{canonical}).Actions[0]; action.Disposition != DispositionUnchanged {
		t.Fatalf("version 1 canonical action = %#v", action)
	}
	legacy.SourcePayloadSchemaVersion = SchemaVersion
	if action := BuildPlan([]Row{legacy}).Actions[0]; action.Quarantine == nil || action.Quarantine.Reason != ReasonMissingDiscriminator {
		t.Fatalf("version 1 legacy action = %#v", action)
	}
	canonical.SourcePayloadSchemaVersion = 0
	if action := BuildPlan([]Row{canonical}).Actions[0]; action.Quarantine == nil || action.Quarantine.Reason != ReasonUnsupportedVersion {
		t.Fatalf("version 0 canonical action = %#v", action)
	}
	unknown := row(22, WakeupKindStart, "a", `{"agent_id":"a"}`)
	unknown.SourcePayloadSchemaVersion = 2
	action := BuildPlan([]Row{unknown}).Actions[0]
	if action.Quarantine == nil || action.Quarantine.Reason != ReasonUnsupportedVersion || action.Quarantine.SourcePayloadSchemaVersion != 2 {
		t.Fatalf("unsupported source version action = %#v", action)
	}
}

func TestLegacyOptionalNullAndReferenceTypesQuarantine(t *testing.T) {
	optional := []string{"name", "prompt", "instructions", "parent_id", "parent_thread_id", "context_mode", "agent_type", "agent_key", "prompt_key", "memory_scope", "cwd", "language", "command", "env"}
	for i, field := range optional {
		r := row(int64(30+i), WakeupKindStart, "a", `{"agent_id":"a","`+field+`":null}`)
		action := BuildPlan([]Row{r}).Actions[0]
		if action.Quarantine == nil || action.Quarantine.Reason != ReasonMissingRequiredField {
			t.Fatalf("field %s action = %#v", field, action)
		}
	}
	r := row(50, WakeupKindNodeStart, "a", `{"agent_id":"a","upstream_outputs":[{"node_key":null,"path":"p"}]}`)
	r.DagKey, r.NodeKey = "d", "n"
	run := int64(1)
	r.RunID = &run
	if action := BuildPlan([]Row{r}).Actions[0]; action.Quarantine == nil || action.Quarantine.Reason != ReasonMissingRequiredField {
		t.Fatalf("null reference action = %#v", action)
	}
	r.PromptPayload = []byte(`{"agent_id":"a","upstream_outputs":[null]}`)
	if action := BuildPlan([]Row{r}).Actions[0]; action.Quarantine == nil || action.Quarantine.Reason != ReasonMissingRequiredField {
		t.Fatalf("null reference entry action = %#v", action)
	}
	r.PromptPayload = []byte(`{"agent_id":"a","command":"not-an-array"}`)
	r.WakeupKind = WakeupKindStart
	r.DagKey, r.NodeKey, r.RunID = "", "", nil
	if action := BuildPlan([]Row{r}).Actions[0]; action.Quarantine == nil {
		t.Fatalf("wrong optional type action = %#v", action)
	}
}

func TestRowDigestIgnoresExternalDigestAndRunDigestBindsSourceAndMutation(t *testing.T) {
	r := row(60, WakeupKindStart, "a", `{"agent_id":"a","prompt":"x"}`)
	r.PromptPayloadDigest = "attacker-supplied-digest"
	p := BuildPlan([]Row{r})
	expected := sha256.Sum256(r.PromptPayload)
	if got := p.Actions[0].Mutation.ExpectedPayloadDigest; got != hex.EncodeToString(expected[:]) {
		t.Fatalf("expected raw payload digest %q, got %q", hex.EncodeToString(expected[:]), got)
	}
	for name, mutate := range map[string]func(*Row){
		"dag":     func(v *Row) { v.DagKey = "dag" },
		"node":    func(v *Row) { v.NodeKey = "node" },
		"run":     func(v *Row) { n := int64(2); v.RunID = &n },
		"kind":    func(v *Row) { v.WakeupKind = WakeupKindManualDispatch },
		"target":  func(v *Row) { v.TargetAgentID = "b" },
		"updated": func(v *Row) { v.UpdatedAt = 8 },
		"source":  func(v *Row) { v.SourcePayloadSchemaVersion = SchemaVersion },
	} {
		v := r
		mutate(&v)
		if got := BuildPlan([]Row{v}).RunDigest; got == p.RunDigest {
			t.Errorf("run digest did not change for %s", name)
		}
	}
	v := r
	v.PromptPayloadDigest = "different-external-digest"
	if got := BuildPlan([]Row{v}).RunDigest; got != p.RunDigest {
		t.Fatalf("external digest changed run digest: %s vs %s", got, p.RunDigest)
	}
}

type fakeApplier struct {
	mutations   []Mutation
	quarantines []QuarantineRecord
	err         error
}

func (f *fakeApplier) ApplyMutation(_ context.Context, m Mutation) error {
	if f.err != nil {
		return f.err
	}
	f.mutations = append(f.mutations, m)
	return nil
}
func (f *fakeApplier) RecordQuarantine(_ context.Context, q QuarantineRecord) error {
	if f.err != nil {
		return f.err
	}
	f.quarantines = append(f.quarantines, q)
	return nil
}

func TestApplyFailFastAndTypedRecords(t *testing.T) {
	good := row(10, WakeupKindStart, "a", `{"agent_id":"a","prompt":"x"}`)
	bad := row(11, "unknown", "a", `{}`)
	p := BuildPlan([]Row{good, bad})
	f := &fakeApplier{}
	summary, err := p.Apply(context.Background(), f)
	if err != nil || summary.Migrated != 1 || summary.Quarantined != 1 || len(f.mutations) != 1 || len(f.quarantines) != 1 {
		t.Fatalf("summary=%#v err=%v mutations=%d quarantines=%d", summary, err, len(f.mutations), len(f.quarantines))
	}
	f.err = errors.New("CAS conflict")
	if _, err := p.Apply(context.Background(), f); err == nil {
		t.Fatal("expected fail-fast applier error")
	}
	if _, err := p.Apply(context.Background(), nil); err == nil {
		t.Fatal("expected nil applier error")
	}
}

func TestApplyRejectsImplicitPayloadSchemaVersion(t *testing.T) {
	p := Plan{Actions: []Action{{
		Disposition: DispositionMigrate,
		RowID:       1,
		Mutation:    &Mutation{RowID: 1, NewPayloadSchemaVersion: 0},
	}}}
	if _, err := p.Apply(context.Background(), &fakeApplier{}); err == nil || !strings.Contains(err.Error(), "payload schema version") {
		t.Fatalf("Apply() error = %v, want explicit schema-version rejection", err)
	}
}

func TestApplyRejectsTamperedQuarantineRecord(t *testing.T) {
	r := row(70, "unknown", "a", `{}`)
	action := BuildPlan([]Row{r}).Actions[0]
	if action.Quarantine == nil || action.Quarantine.SourcePayloadSchemaVersion != 0 {
		t.Fatalf("quarantine = %#v", action.Quarantine)
	}
	action.Quarantine.SourceDigest = "tampered"
	if _, err := (Plan{Actions: []Action{action}}).Apply(context.Background(), &fakeApplier{}); err == nil || !strings.Contains(err.Error(), "source digest") {
		t.Fatalf("Apply() error = %v, want source digest validation", err)
	}
}

func TestApplyRejectsNonLegacyQuarantineSourceVersion(t *testing.T) {
	r := row(71, "unknown", "a", `{}`)
	r.SourcePayloadSchemaVersion = 2
	action := BuildPlan([]Row{r}).Actions[0]
	if action.Quarantine == nil || action.Quarantine.SourcePayloadSchemaVersion != 2 {
		t.Fatalf("quarantine = %#v", action.Quarantine)
	}
	if _, err := (Plan{Actions: []Action{action}}).Apply(context.Background(), &fakeApplier{}); err == nil || !strings.Contains(err.Error(), "legacy-only") {
		t.Fatalf("Apply() error = %v, want legacy-only quarantine rejection", err)
	}
}
