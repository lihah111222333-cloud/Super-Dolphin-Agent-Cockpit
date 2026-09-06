package common

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCallerAttributionConstructorAndContextSnapshot(t *testing.T) {
	attribution := validCallerAttribution(t)
	ctx, err := BindCallerAttribution(context.Background(), attribution)
	if err != nil {
		t.Fatalf("BindCallerAttribution() error = %v", err)
	}

	attribution.TaskID = "task/changed-after-bind"
	got, err := RequireCallerAttribution(ctx)
	if err != nil {
		t.Fatalf("RequireCallerAttribution() error = %v", err)
	}
	if got.TaskID != "agent/source-1" || got.TaskID != got.Source.AgentID {
		t.Fatalf("bound attribution task/source = %q/%q, want immutable matching snapshot", got.TaskID, got.Source.AgentID)
	}
}

func TestCallerAttributionFixedContractAndClosedIssuerKind(t *testing.T) {
	valid := validCallerAttribution(t)
	cases := map[string]CallerAttribution{
		"schema":       mutateAttribution(t, valid, "schema_version", int64(2)),
		"subject kind": mutateAttribution(t, valid, "subject_kind", "agent"),
		"task mismatch": func() CallerAttribution {
			value := valid
			value.TaskID = "agent/other"
			return value
		}(),
		"audience":    mutateAttribution(t, valid, "audience", "mcp-orch/legacy"),
		"issuer kind": mutateAttribution(t, valid, "issuer.kind", "controller_connection"),
	}
	for name, attribution := range cases {
		t.Run(name, func(t *testing.T) {
			if err := attribution.Validate(); !errors.Is(err, ErrCallerAttributionInvalid) {
				t.Fatalf("Validate() error = %v, want ErrCallerAttributionInvalid", err)
			}
		})
	}

	for _, kind := range []CallerAttributionIssuerKind{
		CallerAttributionIssuerProviderBridge,
		CallerAttributionIssuerManagedRuntime,
		CallerAttributionIssuerA2AGateway,
	} {
		attribution := valid
		attribution.Issuer.Kind = kind
		if err := attribution.Validate(); err != nil {
			t.Errorf("Validate() issuer kind %q error = %v", kind, err)
		}
	}
}

func TestCallerAttributionEveryTextFieldRejectsNonCanonicalValueOneHot(t *testing.T) {
	valid := validCallerAttribution(t)
	for _, path := range attributionLeafPaths(t, reflect.TypeOf(valid), reflect.String) {
		t.Run(path, func(t *testing.T) {
			attribution := mutateAttribution(t, valid, path, stringValueAtPath(t, valid, path)+" ")
			if err := attribution.Validate(); !errors.Is(err, ErrCallerAttributionInvalid) {
				t.Fatalf("Validate() error = %v, want ErrCallerAttributionInvalid", err)
			}
		})
	}
}

func TestCallerAttributionEveryVersionAndGenerationMustBePositiveOneHot(t *testing.T) {
	valid := validCallerAttribution(t)
	paths := attributionLeafPaths(t, reflect.TypeOf(valid), reflect.Int, reflect.Int64)
	if len(paths) != 5 {
		t.Fatalf("numeric field paths = %v, want schema plus four fence generations/versions", paths)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			attribution := mutateAttribution(t, valid, path, int64(0))
			if err := attribution.Validate(); !errors.Is(err, ErrCallerAttributionInvalid) {
				t.Fatalf("Validate() error = %v, want ErrCallerAttributionInvalid", err)
			}
		})
	}
}

func TestCallerAttributionDigestFieldsUseCanonicalLowercaseSHA256OneHot(t *testing.T) {
	valid := validCallerAttribution(t)
	var digestPaths []string
	for _, path := range attributionLeafPaths(t, reflect.TypeOf(valid), reflect.String) {
		if strings.HasSuffix(path, "digest") {
			digestPaths = append(digestPaths, path)
		}
	}
	if !reflect.DeepEqual(digestPaths, []string{"source.binding_digest", "issuer.digest"}) {
		t.Fatalf("digest paths = %v, want source and issuer digest exact set", digestPaths)
	}
	for _, path := range digestPaths {
		t.Run(path, func(t *testing.T) {
			attribution := mutateAttribution(t, valid, path, strings.Repeat("A", callerAttributionDigestSize))
			if err := attribution.Validate(); !errors.Is(err, ErrCallerAttributionInvalid) {
				t.Fatalf("Validate() uppercase digest error = %v, want ErrCallerAttributionInvalid", err)
			}
		})
	}
}

func TestCallerAttributionDTOFieldRegistryAndForbiddenSecrets(t *testing.T) {
	want := []string{
		"schema_version",
		"subject_kind",
		"task_id",
		"source.agent_id",
		"source.reservation_id",
		"source.reservation_generation",
		"source.incarnation_id",
		"source.incarnation_generation",
		"source.binding_version",
		"source.binding_digest",
		"source.source_fence_ref",
		"issuer.kind",
		"issuer.binding_ref",
		"issuer.generation",
		"issuer.digest",
		"audience",
	}
	got := attributionLeafPaths(t, reflect.TypeOf(CallerAttribution{}))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CallerAttribution JSON fields = %v, want %v", got, want)
	}
	for _, path := range got {
		normalized := strings.ToLower(strings.ReplaceAll(path, "_", ""))
		for _, forbidden := range []string{"credential", "token", "jti"} {
			if strings.Contains(normalized, forbidden) {
				t.Errorf("CallerAttribution field %q contains forbidden secret-bearing name %q", path, forbidden)
			}
		}
	}

	encoded, err := json.Marshal(validCallerAttribution(t))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, forbidden := range []string{"credential", "token", "jti"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Errorf("serialized CallerAttribution contains forbidden %q field: %s", forbidden, encoded)
		}
	}
}

func TestCallerAttributionContextFailsClosedOnMissingForgeryAndOverride(t *testing.T) {
	if _, err := RequireCallerAttribution(context.Background()); !errors.Is(err, ErrCallerAttributionUnavailable) {
		t.Fatalf("RequireCallerAttribution(missing) error = %v, want ErrCallerAttributionUnavailable", err)
	}
	if _, err := RequireCallerAttribution(nil); !errors.Is(err, ErrCallerAttributionUnavailable) {
		t.Fatalf("RequireCallerAttribution(nil) error = %v, want ErrCallerAttributionUnavailable", err)
	}

	type forgedCallerAttributionContextKey struct{}
	forged := context.WithValue(context.Background(), forgedCallerAttributionContextKey{}, validCallerAttribution(t))
	if _, err := RequireCallerAttribution(forged); !errors.Is(err, ErrCallerAttributionUnavailable) {
		t.Fatalf("RequireCallerAttribution(forged key) error = %v, want unavailable", err)
	}

	wrongType := context.WithValue(context.Background(), callerAttributionContextKey{}, "forged")
	if _, err := RequireCallerAttribution(wrongType); !errors.Is(err, ErrCallerAttributionInvalid) {
		t.Fatalf("RequireCallerAttribution(wrong type) error = %v, want invalid", err)
	}

	bound, err := BindCallerAttribution(context.Background(), validCallerAttribution(t))
	if err != nil {
		t.Fatalf("BindCallerAttribution() error = %v", err)
	}
	other := validCallerAttribution(t)
	other.Issuer.BindingRef = "issuer/other"
	overridden, err := BindCallerAttribution(bound, other)
	if !errors.Is(err, ErrCallerAttributionInvalid) {
		t.Fatalf("BindCallerAttribution(override) error = %v, want invalid", err)
	}
	got, requireErr := RequireCallerAttribution(overridden)
	if requireErr != nil {
		t.Fatalf("RequireCallerAttribution(after override) error = %v", requireErr)
	}
	if got.Issuer.BindingRef != "issuer/provider-1" {
		t.Fatalf("issuer ref after rejected override = %q, want original", got.Issuer.BindingRef)
	}
}

func validCallerAttribution(t *testing.T) CallerAttribution {
	t.Helper()
	attribution, err := NewCallerAttribution(
		"agent/source-1",
		ExecutionFence{
			AgentID:               "agent/source-1",
			ReservationID:         "reservation/source-1",
			ReservationGeneration: 3,
			IncarnationID:         "incarnation/source-1",
			IncarnationGeneration: 5,
			BindingVersion:        7,
			BindingDigest:         strings.Repeat("a", callerAttributionDigestSize),
			SourceFenceRef:        "source-fence/provider-1",
		},
		IssuerBinding{
			Kind:       CallerAttributionIssuerProviderBridge,
			BindingRef: "issuer/provider-1",
			Generation: 11,
			Digest:     strings.Repeat("b", callerAttributionDigestSize),
		},
	)
	if err != nil {
		t.Fatalf("NewCallerAttribution() error = %v", err)
	}
	return attribution
}

func attributionLeafPaths(t *testing.T, typ reflect.Type, kinds ...reflect.Kind) []string {
	t.Helper()
	allowed := make(map[reflect.Kind]struct{}, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = struct{}{}
	}
	var paths []string
	var walk func(reflect.Type, string)
	walk = func(current reflect.Type, prefix string) {
		for index := 0; index < current.NumField(); index++ {
			field := current.Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				t.Fatalf("field %s.%s lacks a canonical JSON tag", current.Name(), field.Name)
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if field.Type.Kind() == reflect.Struct {
				walk(field.Type, path)
				continue
			}
			switch field.Type.Kind() {
			case reflect.String, reflect.Int, reflect.Int64:
			default:
				t.Fatalf("field %s has unguarded kind %s", path, field.Type.Kind())
			}
			if len(allowed) > 0 {
				if _, ok := allowed[field.Type.Kind()]; !ok {
					continue
				}
			}
			paths = append(paths, path)
		}
	}
	walk(typ, "")
	return paths
}

func mutateAttribution(t *testing.T, original CallerAttribution, path string, replacement any) CallerAttribution {
	t.Helper()
	value := reflect.ValueOf(&original).Elem()
	for _, segment := range strings.Split(path, ".") {
		typ := value.Type()
		matched := false
		for index := 0; index < value.NumField(); index++ {
			field := typ.Field(index)
			if strings.Split(field.Tag.Get("json"), ",")[0] != segment {
				continue
			}
			value = value.Field(index)
			matched = true
			break
		}
		if !matched {
			t.Fatalf("unknown CallerAttribution field path: %s", path)
		}
	}
	replacementValue := reflect.ValueOf(replacement)
	if replacementValue.Type().ConvertibleTo(value.Type()) {
		value.Set(replacementValue.Convert(value.Type()))
	} else {
		t.Fatalf("incompatible CallerAttribution field replacement: %s", path)
	}
	return original
}

func stringValueAtPath(t *testing.T, attribution CallerAttribution, path string) string {
	t.Helper()
	value := reflect.ValueOf(attribution)
	for _, segment := range strings.Split(path, ".") {
		typ := value.Type()
		matched := false
		for index := 0; index < value.NumField(); index++ {
			field := typ.Field(index)
			if strings.Split(field.Tag.Get("json"), ",")[0] != segment {
				continue
			}
			value = value.Field(index)
			matched = true
			break
		}
		if !matched {
			t.Fatalf("unknown CallerAttribution field path: %s", path)
		}
	}
	return value.String()
}
