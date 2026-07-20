package gate

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestResultReceiptV2RejectsIncompleteOrDriftedShardClosure(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	valid := validResultReceipt(t, now)
	tests := []struct {
		name   string
		mutate func(*ResultReceipt)
	}{
		{name: "v1", mutate: func(receipt *ResultReceipt) { receipt.SchemaVersion = 1 }},
		{name: "missing shard", mutate: func(receipt *ResultReceipt) {
			receipt.ShardReceipts = receipt.ShardReceipts[:2]
		}},
		{name: "duplicate shard", mutate: func(receipt *ResultReceipt) {
			receipt.ShardReceipts[1].Shard = receipt.ShardReceipts[0].Shard
		}},
		{name: "gate aggregate drift", mutate: func(receipt *ResultReceipt) {
			receipt.GateResults[0].LogDigest = shardTestDigest('f')
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
			receipt := cloneResultReceiptV2(valid)
			test.mutate(&receipt)
			if err := receipt.Validate(); err == nil {
				t.Fatal("invalid v2 result receipt was accepted")
			}
		})
	}
}

func TestResultReceiptV2SignatureCoversEveryShardExitedAt(t *testing.T) {
	receipt := validResultReceipt(t, time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt = signResultReceiptV2(t, receipt, privateKey)
	if err := VerifyResultReceipt(receipt, publicKey); err != nil {
		t.Fatalf("valid v2 receipt verification failed: %v", err)
	}
	for shardIndex := range receipt.ShardReceipts {
		t.Run(receipt.ShardReceipts[shardIndex].Shard.IdentityDigest, func(t *testing.T) {
			tampered := cloneResultReceiptV2(receipt)
			tampered.ShardReceipts[shardIndex].ExitedAt = tampered.ShardReceipts[shardIndex].ExitedAt.Add(time.Nanosecond)
			if err := tampered.Validate(); err != nil {
				t.Fatalf("isolated ExitedAt tamper should remain structurally valid: %v", err)
			}
			if err := VerifyResultReceipt(tampered, publicKey); err == nil || !strings.Contains(err.Error(), "signature verification failed") {
				t.Fatalf("ExitedAt tamper verification error = %v", err)
			}
		})
	}
}

func signResultReceiptV2(t *testing.T, receipt ResultReceipt, privateKey ed25519.PrivateKey) ResultReceipt {
	t.Helper()
	payload, err := ResultReceiptSigningPayload(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return receipt
}

func cloneResultReceiptV2(receipt ResultReceipt) ResultReceipt {
	cloned := receipt
	cloned.GateResults = append([]GateResult(nil), receipt.GateResults...)
	cloned.ShardReceipts = cloneShardReceipts(receipt.ShardReceipts)
	cloned.Evidence = append([]Evidence(nil), receipt.Evidence...)
	return cloned
}
