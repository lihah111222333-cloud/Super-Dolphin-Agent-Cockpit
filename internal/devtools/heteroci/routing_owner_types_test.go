package heteroci

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestRoutingOwnerTypesValidateCanonicalValues(t *testing.T) {
	scope, ownerID, operationID := assertCanonicalOwnerStrings(t)
	epoch, revision := assertCanonicalOwnerNumbers(t)
	fence, err := NewOwnerFence(scope, ownerID, epoch, revision)
	if err != nil {
		t.Fatalf("NewOwnerFence: %v", err)
	}
	if fence.Scope != scope || fence.OwnerID != ownerID || fence.OwnerEpoch != epoch || fence.Revision != revision || operationID == "" {
		t.Fatalf("canonical values changed: fence=%#v operation=%q", fence, operationID)
	}
}

func assertCanonicalOwnerStrings(t *testing.T) (RoutingOwnerScope, RoutingOwnerID, OperationID) {
	t.Helper()
	scope, err := ParseRoutingOwnerScope("sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("ParseRoutingOwnerScope: %v", err)
	}
	ownerID, err := ParseRoutingOwnerID(strings.Repeat("b", RoutingOwnerIDHexLength))
	if err != nil {
		t.Fatalf("ParseRoutingOwnerID: %v", err)
	}
	operationID, err := ParseOperationID(strings.Repeat("c", OperationIDHexLength))
	if err != nil {
		t.Fatalf("ParseOperationID: %v", err)
	}
	return scope, ownerID, operationID
}

func assertCanonicalOwnerNumbers(t *testing.T) (RoutingOwnerEpoch, RoutingOwnerRevision) {
	t.Helper()
	epoch, err := NewRoutingOwnerEpoch(7)
	if err != nil {
		t.Fatalf("NewRoutingOwnerEpoch: %v", err)
	}
	revision, err := NewRoutingOwnerRevision(8)
	if err != nil {
		t.Fatalf("NewRoutingOwnerRevision: %v", err)
	}
	return epoch, revision
}

func TestRoutingOwnerTypesRejectInvalidValues(t *testing.T) {
	for _, raw := range []string{"", "a", "SHA256:" + strings.Repeat("a", 64), strings.Repeat("a", 64)} {
		if _, err := ParseRoutingOwnerScope(raw); err == nil {
			t.Errorf("ParseRoutingOwnerScope(%q) accepted invalid value", raw)
		}
	}
	for _, raw := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 64)} {
		if _, err := ParseRoutingOwnerID(raw); err == nil {
			t.Errorf("ParseRoutingOwnerID(%q) accepted invalid value", raw)
		}
		if _, err := ParseOperationID(raw); err == nil {
			t.Errorf("ParseOperationID(%q) accepted invalid value", raw)
		}
	}
	for _, value := range []int64{math.MinInt64, -1, 0, math.MaxInt64} {
		if _, err := NewRoutingOwnerEpoch(value); err == nil {
			t.Errorf("NewRoutingOwnerEpoch(%d) accepted invalid value", value)
		}
		if _, err := NewRoutingOwnerRevision(value); err == nil {
			t.Errorf("NewRoutingOwnerRevision(%d) accepted invalid value", value)
		}
	}
}

func TestOwnerFenceRejectsRevisionBeforeEpochAndPreservesLayout(t *testing.T) {
	scope := RoutingOwnerScope("sha256:" + strings.Repeat("a", 64))
	ownerID := RoutingOwnerID(strings.Repeat("b", 64))
	epoch := RoutingOwnerEpoch(4)
	if _, err := NewOwnerFence(scope, ownerID, epoch, RoutingOwnerRevision(3)); err == nil {
		t.Fatal("NewOwnerFence accepted revision before epoch")
	}
	typ := reflect.TypeFor[OwnerFence]()
	want := []string{"Scope", "OwnerID", "OwnerEpoch", "Revision"}
	if typ.NumField() != len(want) {
		t.Fatalf("OwnerFence fields = %d, want %d", typ.NumField(), len(want))
	}
	for index, name := range want {
		field := typ.Field(index)
		if field.Name != name {
			t.Errorf("OwnerFence field %d = %q, want %q", index, field.Name, name)
		}
	}
}
