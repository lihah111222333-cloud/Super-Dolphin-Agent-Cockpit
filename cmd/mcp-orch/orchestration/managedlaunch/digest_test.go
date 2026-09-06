package managedlaunch

import (
	"reflect"
	"testing"
)

func TestManagedLaunchDigestsAreDomainSeparatedAndOneHot(t *testing.T) {
	request := bootstrapFixture(t)
	assertEveryFieldAffectsDigest(t, request, "BootstrapRequestDigest", ComputeBootstrapRequestDigest)
	result := bootstrapResultFixture(t)
	assertEveryFieldAffectsDigest(t, result, "ResultDigest", ComputeBootstrapResultDigest)
	commit := commitFixture(t)
	assertEveryFieldAffectsDigest(t, commit, "CommitOperationDigest", ComputeCommitOperationDigest)
	abort := abortFixture(t)
	assertEveryFieldAffectsDigest(t, abort, "AbortOperationDigest", ComputeAbortOperationDigest)

	domains := []string{
		BootstrapRequestDigestDomain, BootstrapResultDigestDomain,
		CommitOperationDigestDomain, AbortOperationDigestDomain,
	}
	seen := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		if _, duplicate := seen[domain]; duplicate {
			t.Fatalf("digest domain %q is duplicated", domain)
		}
		seen[domain] = struct{}{}
	}
	base := struct {
		Value string `json:"value"`
	}{Value: "same-payload"}
	for left := range domains {
		leftDigest, err := digestCanonical(domains[left], base)
		assertNoError(t, err)
		for right := left + 1; right < len(domains); right++ {
			rightDigest, digestErr := digestCanonical(domains[right], base)
			assertNoError(t, digestErr)
			if leftDigest == rightDigest {
				t.Fatalf("domains %q and %q produced the same digest", domains[left], domains[right])
			}
		}
	}
}

func assertEveryFieldAffectsDigest[T any](t *testing.T, base T, selfField string, digest func(T) (string, error)) {
	t.Helper()
	baseDigest, err := digest(base)
	assertNoError(t, err)
	typeOf := reflect.TypeOf(base)
	for _, leaf := range digestLeafFields(typeOf, nil, "") {
		leaf := leaf
		t.Run(typeOf.Name()+"."+leaf.name, func(t *testing.T) {
			mutated := base
			value := reflect.ValueOf(&mutated).Elem().FieldByIndex(leaf.index)
			mutateDigestField(t, value, leaf.name)
			got, digestErr := digest(mutated)
			assertNoError(t, digestErr)
			if leaf.name == selfField {
				if got != baseDigest {
					t.Fatalf("self field %s changed digest", leaf.name)
				}
				return
			}
			if got == baseDigest {
				t.Fatalf("field %s does not affect digest", leaf.name)
			}
		})
	}
}

type digestLeaf struct {
	name  string
	index []int
}

func digestLeafFields(typeOf reflect.Type, prefix []int, namePrefix string) []digestLeaf {
	var leaves []digestLeaf
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		path := append(append([]int(nil), prefix...), index)
		name := field.Name
		if namePrefix != "" {
			name = namePrefix + "." + name
		}
		if field.Type.Kind() == reflect.Struct {
			leaves = append(leaves, digestLeafFields(field.Type, path, name)...)
			continue
		}
		leaves = append(leaves, digestLeaf{name: name, index: path})
	}
	return leaves
}

func mutateDigestField(t *testing.T, value reflect.Value, name string) {
	t.Helper()
	switch value.Kind() {
	case reflect.String:
		value.SetString(value.String() + "-one-hot")
	case reflect.Int64:
		value.SetInt(value.Int() + 1)
	default:
		t.Fatalf("%s has unsupported mutation kind %s", name, value.Kind())
	}
}
