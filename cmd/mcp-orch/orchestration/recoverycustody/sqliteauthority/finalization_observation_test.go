package sqliteauthority

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestFinalizationObservationContract 锁定封闭状态、JSON 形状与全部 envelope 字段的 fail-fast 校验。
func TestFinalizationObservationContract(t *testing.T) {
	t.Run("state_matrix", testFinalizationObservationStateMatrix)
	t.Run("json_golden", testFinalizationObservationJSONGolden)
	t.Run("envelope_one_hot", testFinalizationObservationEnvelopeOneHot)
}

// testFinalizationObservationStateMatrix 覆盖 absent、inflight 与 finalized 的 evidence 互斥矩阵。
func testFinalizationObservationStateMatrix(t *testing.T) {
	_, expected, finalization := threeLayerDigestFixture(t)
	evidence, err := finalizedEvidenceFixture(authorityTestConfig(t, "observation-state"), expected)
	if err != nil {
		t.Fatalf("finalizedEvidenceFixture() error = %v", err)
	}
	tests := []struct {
		name     string
		state    FinalizationQueryState
		evidence *FinalizedEvidence
		wantErr  bool
	}{
		{name: "absent", state: FinalizationQueryAbsent},
		{name: "inflight", state: FinalizationQueryInflight},
		{name: "finalized", state: FinalizationQueryFinalized, evidence: &evidence},
		{name: "absent_with_evidence", state: FinalizationQueryAbsent, evidence: &evidence, wantErr: true},
		{name: "inflight_with_evidence", state: FinalizationQueryInflight, evidence: &evidence, wantErr: true},
		{name: "finalized_without_evidence", state: FinalizationQueryFinalized, wantErr: true},
		{name: "unknown", state: FinalizationQueryState("unknown"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := finalizationObservationFixture(finalization, test.state, test.evidence)
			err := observation.Validate(finalization)
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

// testFinalizationObservationJSONGolden 锁定 observation 的完整有序协议字段，防止静默删改。
func testFinalizationObservationJSONGolden(t *testing.T) {
	_, _, finalization := threeLayerDigestFixture(t)
	observation := finalizationObservationFixture(finalization, FinalizationQueryAbsent, nil)
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want := `{"SchemaVersion":1,"State":"absent","FinalizationOperationID":"finalization-golden",` +
		`"FinalizationRequestDigest":"09f042e7d959b29a8e85b92a884b259d0c712d6df6999418cb4f8c61485f9391",` +
		`"ObservationGeneration":1,"ReadFenceDigest":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",` +
		`"Evidence":null}`
	if string(encoded) != want {
		t.Fatalf("observation JSON = %s, want %s", encoded, want)
	}
}

// testFinalizationObservationEnvelopeOneHot 逐字段破坏 observation envelope 并要求严格拒绝。
func testFinalizationObservationEnvelopeOneHot(t *testing.T) {
	_, _, finalization := threeLayerDigestFixture(t)
	mutations := []struct {
		name   string
		mutate func(*FinalizationObservation)
	}{
		{name: "schema_version", mutate: func(o *FinalizationObservation) { o.SchemaVersion = 0 }},
		{name: "state", mutate: func(o *FinalizationObservation) { o.State = "unknown" }},
		{name: "operation_id", mutate: func(o *FinalizationObservation) { o.FinalizationOperationID += "-other" }},
		{name: "request_digest", mutate: func(o *FinalizationObservation) { o.FinalizationRequestDigest = schemaTestDigest("e") }},
		{name: "generation", mutate: func(o *FinalizationObservation) { o.ObservationGeneration = 0 }},
		{name: "read_fence", mutate: func(o *FinalizationObservation) { o.ReadFenceDigest = "not-a-digest" }},
		{name: "evidence", mutate: func(o *FinalizationObservation) { o.Evidence = &FinalizedEvidence{} }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			observation := finalizationObservationFixture(finalization, FinalizationQueryAbsent, nil)
			mutation.mutate(&observation)
			if err := observation.Validate(finalization); !errors.Is(err, ErrInvalidEvidence) {
				t.Fatalf("Validate() error = %v, want ErrInvalidEvidence", err)
			}
		})
	}
}
