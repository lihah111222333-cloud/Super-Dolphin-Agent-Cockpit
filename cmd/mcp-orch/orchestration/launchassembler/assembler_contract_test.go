package launchassembler

import (
	"context"
	"testing"
)

// TestLaunchAssemblerContract verifies fail-fast admission for malformed launch intents.
func TestLaunchAssemblerContract(t *testing.T) {
	t.Parallel()
	a, err := New(nil, nil)
	if err == nil || a != nil {
		t.Fatalf("New(nil,nil) = (%v,%v), want invalid", a, err)
	}
	if err := validate(Request{}); err == nil {
		t.Fatal("validate(empty) succeeded")
	}
	valid := Request{CommandID: "cmd", CallerPrincipalRef: "caller", ScopeRef: "scope", IdempotencyNamespace: "ns", IdempotencyDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", RequestDigest: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210", DigestKeyID: "key", ContentRef: "ref", SourceFenceRef: "fence", OperatorReservationID: "res", OperatorSourceID: "src", OperatorGeneration: 1, ContentKind: "operator_content_ref", Profile: "profile"}
	if err := validate(valid); err != nil {
		t.Fatalf("validate(valid) = %v", err)
	}
	if _, err := (&Assembler{}).Assemble(context.Background(), valid); err == nil {
		t.Fatal("Assemble without stores succeeded")
	}
}
