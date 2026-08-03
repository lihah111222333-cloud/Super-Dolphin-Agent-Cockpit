package gate

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResultReceiptRejectsIncompleteOrDriftedShardClosure(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	valid := validResultReceipt(t, now)
	tests := []struct {
		name   string
		mutate func(*ResultReceipt)
	}{
		{name: "v1", mutate: func(receipt *ResultReceipt) { receipt.SchemaVersion = 1 }},
		{name: "missing shard", mutate: func(receipt *ResultReceipt) {
			removeResultReceiptShardForGate(t, receipt, GateIDBackendTestWithGuard)
		}},
		{name: "duplicate shard", mutate: func(receipt *ResultReceipt) {
			duplicateResultReceiptShardForGate(t, receipt, GateIDBackendTestWithGuard)
		}},
		{name: "gate aggregate drift", mutate: func(receipt *ResultReceipt) {
			resultReceiptGateResultForGate(t, receipt, GateIDBackendTestWithGuard).LogDigest = shardTestDigest('f')
		}},
		{name: "container aggregate drift", mutate: func(receipt *ResultReceipt) {
			receipt.Container.HostConfigDigest = shardTestDigest('f')
		}},
		{name: "unsigned non-passed status", mutate: func(receipt *ResultReceipt) {
			receipt.Status = ResultStatusFailed
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := cloneResultReceiptCurrent(t, valid)
			test.mutate(&receipt)
			if err := receipt.Validate(); err == nil {
				t.Fatal("invalid result receipt was accepted")
			}
		})
	}
}

func removeResultReceiptShardForGate(t *testing.T, receipt *ResultReceipt, gateID GateID) {
	t.Helper()
	target := shardReceiptForGate(t, receipt.ShardReceipts, gateID)
	identity := target.Shard.IdentityDigest
	filtered := make([]ContainerShardReceipt, 0, len(receipt.ShardReceipts)-1)
	removed := false
	for _, shardReceipt := range receipt.ShardReceipts {
		if shardReceipt.Shard.IdentityDigest == identity {
			if removed {
				t.Fatalf("shard identity %q appears more than once", identity)
			}
			removed = true
			continue
		}
		filtered = append(filtered, shardReceipt)
	}
	if !removed {
		t.Fatalf("shard identity %q was not removed", identity)
	}
	receipt.ShardReceipts = filtered
}

func duplicateResultReceiptShardForGate(t *testing.T, receipt *ResultReceipt, gateID GateID) {
	t.Helper()
	target := shardReceiptForGate(t, receipt.ShardReceipts, gateID)
	duplicate := cloneShardReceipts([]ContainerShardReceipt{*target})[0]
	receipt.ShardReceipts = append(receipt.ShardReceipts, duplicate)
}

func resultReceiptGateResultForGate(t *testing.T, receipt *ResultReceipt, gateID GateID) *GateResult {
	t.Helper()
	for index := range receipt.GateResults {
		if receipt.GateResults[index].GateID == string(gateID) {
			return &receipt.GateResults[index]
		}
	}
	t.Fatalf("gate %q does not belong to result receipt aggregate", gateID)
	return nil
}

func TestResultReceiptRejectsShardCountBindingDrift(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	target := shardReceiptForGate(t, receipt.ShardReceipts, GateIDBackendTestWithGuard)
	target.Shard.ShardsPerJob = 2
	identity, err := containerShardIdentityDigest(target.Shard)
	if err != nil {
		t.Fatal(err)
	}
	target.Shard.IdentityDigest = identity
	if err := receipt.Validate(); err == nil {
		t.Fatal("receipt accepted a shard count binding drift")
	}
}

func TestResultReceiptV4ValidatesDynamicShardCountAndWorkloadPlan(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	if receipt.SchemaVersion != ResultReceiptSchemaVersion || len(receipt.ShardReceipts) == 0 {
		t.Fatalf("schema=%d shards=%d, want schema=%d and dynamic shards", receipt.SchemaVersion, len(receipt.ShardReceipts), ResultReceiptSchemaVersion)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestResultReceiptRejectsMissingWorkloadPlan(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	receipt.WorkloadPlan = WorkloadExecutionPlan{}
	if err := receipt.Validate(); err == nil || !strings.Contains(err.Error(), "workload plan") {
		t.Fatalf("Validate() error = %v, want missing workload plan", err)
	}
}

func TestResultReceiptRejectsWorkloadPlanProjectionTampering(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	receipt.WorkloadPlan.ExecutionWorkloadIDs[0] = GateID("forged-workload")
	if err := receipt.Validate(); err == nil {
		t.Fatal("Validate() accepted tampered workload execution projection")
	}
}

func TestResultReceiptStrictJSONRequiresFrozenWorkloadPlan(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "workload_plan")
	missing, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ResultReceipt
	if err := DecodeStrictJSON(missing, &decoded); err == nil || !strings.Contains(err.Error(), "workload plan") {
		t.Fatalf("DecodeStrictJSON() error = %v, want missing workload plan", err)
	}
}

func TestResultReceiptStrictJSONRejectsUnknownNestedWorkloadPlanField(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	var workloadPlan map[string]json.RawMessage
	if err := json.Unmarshal(fields["workload_plan"], &workloadPlan); err != nil {
		t.Fatal(err)
	}
	workloadPlan["unknown_field"] = json.RawMessage(`true`)
	fields["workload_plan"], err = json.Marshal(workloadPlan)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ResultReceipt
	if err := DecodeStrictJSON(unknown, &decoded); err == nil || !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("DecodeStrictJSON() error = %v, want unknown nested field", err)
	}
}

func TestResultReceiptValidateStoredRejectsPreviousSchema(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	receipt.SchemaVersion = 3
	target := shardReceiptForGate(t, receipt.ShardReceipts, GateIDBackendTestWithGuard)
	plan, err := BuildGatePlan(target.Shard.Profile, receipt.Source)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.ValidateStored(plan); err == nil || !strings.Contains(err.Error(), "unsupported result receipt schema_version") {
		t.Fatalf("ValidateStored() error = %v", err)
	}
}

func TestResultReceiptSignatureCoversEveryShardExitedAt(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt = signResultReceiptCurrent(t, receipt, privateKey)
	if err := VerifyResultReceipt(receipt, publicKey); err != nil {
		t.Fatalf("valid result receipt verification failed: %v", err)
	}
	for _, shardReceipt := range receipt.ShardReceipts {
		identity := shardReceipt.Shard.IdentityDigest
		t.Run(identity, func(t *testing.T) {
			tampered := cloneResultReceiptCurrent(t, receipt)
			target := resultReceiptShardForIdentity(t, &tampered, identity)
			target.ExitedAt = target.ExitedAt.Add(time.Nanosecond)
			if err := tampered.Validate(); err != nil {
				t.Fatalf("isolated ExitedAt tamper should remain structurally valid: %v", err)
			}
			if err := VerifyResultReceipt(tampered, publicKey); err == nil || !strings.Contains(err.Error(), "signature verification failed") {
				t.Fatalf("ExitedAt tamper verification error = %v", err)
			}
		})
	}
}

func resultReceiptShardForIdentity(t *testing.T, receipt *ResultReceipt, identity string) *ContainerShardReceipt {
	t.Helper()
	for index := range receipt.ShardReceipts {
		if receipt.ShardReceipts[index].Shard.IdentityDigest == identity {
			return &receipt.ShardReceipts[index]
		}
	}
	t.Fatalf("shard identity %q does not belong to result receipt", identity)
	return nil
}

func signResultReceiptCurrent(t *testing.T, receipt ResultReceipt, privateKey ed25519.PrivateKey) ResultReceipt {
	t.Helper()
	payload, err := ResultReceiptSigningPayload(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return receipt
}

func cloneResultReceiptCurrent(t *testing.T, receipt ResultReceipt) ResultReceipt {
	t.Helper()
	cloned := receipt
	workloadPlan, err := cloneWorkloadExecutionPlan(receipt.WorkloadPlan)
	if err != nil {
		t.Fatal(err)
	}
	cloned.WorkloadPlan = workloadPlan
	cloned.GateResults = append([]GateResult(nil), receipt.GateResults...)
	cloned.ShardReceipts = cloneShardReceipts(receipt.ShardReceipts)
	cloned.Evidence = append([]Evidence(nil), receipt.Evidence...)
	return cloned
}
