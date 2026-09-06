package childissuer

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestChildIssuerJSONFieldsEnumerateEveryPublicField keeps the candidate
// request/result schema explicit and rejects silent field additions.
func TestChildIssuerJSONFieldsEnumerateEveryPublicField(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want []string
	}{
		{name: "request", typ: reflect.TypeFor[Request](), want: []string{
			"child_binding", "child_incarnation", "child_reservation", "issuer", "issuer_nonce_digest",
		}},
		{name: "issue_request", typ: reflect.TypeFor[IssueRequest](), want: []string{
			"child_binding", "child_incarnation", "child_reservation", "issuer", "issuer_nonce_digest", "parent",
		}},
		{name: "candidate", typ: reflect.TypeFor[Candidate](), want: []string{
			"child_agent_id", "child_external_epoch", "child_external_identity_ref", "child_fence",
			"child_identity_kind", "child_incarnation_id", "child_reservation_id", "child_scope_ref", "child_source_kind",
			"issuer", "issuer_nonce_digest", "parent_agent_id", "parent_auth_context_digest", "parent_fence",
			"parent_principal_ref", "parent_profile", "parent_scope_ref",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := publicJSONFieldNames(t, test.typ)
			want := append([]string(nil), test.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("JSON fields = %v, want %v", got, want)
			}
		})
	}
}

// TestChildIssuerPublicTypesCannotCarryRawCredentials rejects secret-bearing
// fields in the candidate contract; the delegate receives identity metadata
// only and bearer material remains in taskcredential's separate owner.
func TestChildIssuerPublicTypesCannotCarryRawCredentials(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[Request](), reflect.TypeFor[IssueRequest](), reflect.TypeFor[Candidate]()} {
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
			for _, forbidden := range []string{"bearer", "token", "password", "secret", "credential"} {
				if strings.Contains(name, forbidden) && field.Name != "IssuerNonceDigest" {
					t.Fatalf("%s carries forbidden secret field %q", typ.Name(), field.Name)
				}
			}
		}
	}
}

func publicJSONFieldNames(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	names := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("%s.%s lacks an explicit JSON field name", typ.Name(), field.Name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
