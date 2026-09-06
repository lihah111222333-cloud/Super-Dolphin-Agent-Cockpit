package managedruntime

import (
	"fmt"
	"reflect"
	"testing"
)

func TestManagedRuntimeDigestDomainsAreUnique(t *testing.T) {
	domains := []string{
		PrepareRequestDigestDomain, PreparedReceiptDigestDomain, CommitRequestDigestDomain,
		AbortRequestDigestDomain, ClosedReceiptDigestDomain, QueryResultDigestDomain,
	}
	seen := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		if domain == "" {
			t.Fatal("digest domain must not be empty")
		}
		if _, duplicate := seen[domain]; duplicate {
			t.Fatalf("digest domain %q is duplicated", domain)
		}
		seen[domain] = struct{}{}
	}
}

func TestManagedRuntimeDigestNonSelfFieldsOneHot(t *testing.T) {
	prepare := validPrepareRequest(t, "one-hot")
	prepared := validPreparedReceipt(t, prepare, "one-hot")
	commit := validCommitRequest(t, prepared, "one-hot")
	abort := validAbortRequest(t, prepared, "one-hot")
	closed := validClosedReceipt(t, prepared, commit.OperationID, commit.RequestDigest, StateCommitted, "one-hot")
	query := mustFinalizeQueryResult(t, QueryResult{
		SchemaVersion: SchemaVersion, OperationID: prepare.OperationID, OperationDigest: prepare.RequestDigest,
		State: StateCommitted, PreparedReceiptRef: prepared.ReceiptRef, PreparedReceiptDigest: prepared.ReceiptDigest,
		ClosedReceiptRef: closed.ReceiptRef, ClosedReceiptDigest: closed.ReceiptDigest,
	})

	tests := []digestOneHotCase{
		{name: "prepare", value: prepare, selfField: "RequestDigest", compute: computePrepareAny},
		{name: "prepared", value: prepared, selfField: "ReceiptDigest", compute: computePreparedAny},
		{name: "commit", value: commit, selfField: "RequestDigest", compute: computeCommitAny},
		{name: "abort", value: abort, selfField: "RequestDigest", compute: computeAbortAny},
		{name: "closed", value: closed, selfField: "ReceiptDigest", compute: computeClosedAny},
		{name: "query", value: query, selfField: "ResultDigest", compute: computeQueryAny},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { assertDigestOneHot(t, test) })
	}
}

type digestOneHotCase struct {
	name      string
	value     any
	selfField string
	compute   func(any) (string, error)
}

func assertDigestOneHot(t *testing.T, test digestOneHotCase) {
	t.Helper()
	base, err := test.compute(test.value)
	if err != nil {
		t.Fatalf("compute base digest: %v", err)
	}
	value := reflect.ValueOf(test.value)
	paths := structLeafPaths(value.Type(), nil, test.selfField)
	for _, path := range paths {
		candidate := reflect.New(value.Type()).Elem()
		candidate.Set(value)
		if err := mutateLeaf(candidate.FieldByIndex(path)); err != nil {
			t.Fatalf("mutate field %v: %v", path, err)
		}
		got, computeErr := test.compute(candidate.Interface())
		if computeErr != nil {
			t.Fatalf("compute mutation %v: %v", path, computeErr)
		}
		if got == base {
			t.Errorf("field path %v is not covered by digest", path)
		}
	}
	selfChanged := reflect.New(value.Type()).Elem()
	selfChanged.Set(value)
	selfChanged.FieldByName(test.selfField).SetString(testDigest("self-field-change"))
	got, err := test.compute(selfChanged.Interface())
	if err != nil {
		t.Fatalf("compute self-digest mutation: %v", err)
	}
	if got != base {
		t.Fatalf("self field %s participates in its own digest", test.selfField)
	}
}

func structLeafPaths(valueType reflect.Type, prefix []int, skipTopLevel string) [][]int {
	paths := make([][]int, 0)
	for index := range valueType.NumField() {
		field := valueType.Field(index)
		if len(prefix) == 0 && field.Name == skipTopLevel {
			continue
		}
		path := append(append([]int(nil), prefix...), index)
		if field.Type.Kind() == reflect.Struct {
			paths = append(paths, structLeafPaths(field.Type, path, skipTopLevel)...)
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

func mutateLeaf(value reflect.Value) error {
	switch value.Kind() {
	case reflect.String:
		value.SetString(value.String() + "-changed")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(value.Int() + 1)
	default:
		return fmt.Errorf("managed runtime digest fixture contains unsupported field kind %s", value.Kind())
	}
	return nil
}

func computePrepareAny(value any) (string, error) {
	return ComputePrepareRequestDigest(value.(PrepareRequest))
}

func computePreparedAny(value any) (string, error) {
	return ComputePreparedReceiptDigest(value.(PreparedReceipt))
}

func computeCommitAny(value any) (string, error) {
	return ComputeCommitRequestDigest(value.(CommitRequest))
}

func computeAbortAny(value any) (string, error) {
	return ComputeAbortRequestDigest(value.(AbortRequest))
}

func computeClosedAny(value any) (string, error) {
	return ComputeClosedReceiptDigest(value.(ClosedReceipt))
}

func computeQueryAny(value any) (string, error) {
	return ComputeQueryResultDigest(value.(QueryResult))
}
