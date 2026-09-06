package trustcandidate

import (
	"reflect"
	"strings"
	"testing"
)

// TestJSONFieldGuardEnumeratesProducerFields 从反射动态枚举所有 wire DTO，拒绝漏 tag/duplicate。
func TestJSONFieldGuardEnumeratesProducerFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[Provenance](), reflect.TypeFor[ContentRef](), reflect.TypeFor[MetadataInfo](),
		reflect.TypeFor[LinkMetadata](), reflect.TypeFor[TrustEnvelope](), reflect.TypeFor[ContentTicket](),
		reflect.TypeFor[ContentTicketInput](), reflect.TypeFor[ProviderTextInput](), reflect.TypeFor[MetadataInput](),
		reflect.TypeFor[LinkInput](), reflect.TypeFor[ImageInput](), reflect.TypeFor[CanonicalEvent](),
		reflect.TypeFor[CanonicalControl](), reflect.TypeFor[SinkDecision](), reflect.TypeFor[UserConsent](),
	} {
		fields := jsonFields(t, typ)
		if len(fields) == 0 {
			t.Fatalf("%s has no JSON fields", typ.Name())
		}
	}
}

// TestEnvelopeDigestFieldGuard 确认每个 envelope producer 字段都被 self-digest 覆盖。
func TestEnvelopeDigestFieldGuard(t *testing.T) {
	base, _ := providerFixture(t, "field guard")
	want := base.EnvelopeDigest
	value := reflect.ValueOf(&base).Elem()
	typ := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := typ.Field(index)
		if field.Name == "EnvelopeDigest" {
			continue
		}
		candidate := base
		fieldValue := reflect.ValueOf(&candidate).Elem().Field(index)
		switch fieldValue.Kind() {
		case reflect.String:
			fieldValue.SetString(fieldValue.String() + "-changed")
		case reflect.Int64:
			fieldValue.SetInt(fieldValue.Int() + 1)
		case reflect.Bool:
			fieldValue.SetBool(!fieldValue.Bool())
		case reflect.Struct:
			mutateNestedEnvelopeField(t, &candidate, field.Name)
		default:
			t.Fatalf("unsupported envelope field %s kind=%s", field.Name, fieldValue.Kind())
		}
		got, err := ComputeEnvelopeDigest(candidate)
		if err != nil {
			continue
		}
		if got == want {
			t.Fatalf("TrustEnvelope.%s is absent from digest", field.Name)
		}
	}
}

func mutateNestedEnvelopeField(t *testing.T, envelope *TrustEnvelope, field string) {
	t.Helper()
	switch field {
	case "Provenance":
		envelope.Provenance.SourceSubject += "-changed"
	case "ContentRef":
		envelope.ContentRef.Ref += "-changed"
	case "Metadata":
		// Provider text has zero metadata; replacing it creates a shape error and is still fail-closed.
		envelope.Metadata.MetadataKind = MetadataMCPToolsList
	case "Link":
		envelope.Link.URI = "https://example.com/changed"
	default:
		t.Fatalf("unsupported nested field %s", field)
	}
}

func jsonFields(t *testing.T, typ reflect.Type) map[string]reflect.StructField {
	t.Helper()
	fields := make(map[string]reflect.StructField, typ.NumField())
	for _, field := range reflect.VisibleFields(typ) {
		if field.PkgPath != "" {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" {
			t.Fatalf("%s.%s has no JSON tag", typ.Name(), field.Name)
		}
		if name == "-" {
			continue
		}
		if _, exists := fields[name]; exists {
			t.Fatalf("%s duplicate JSON field %q", typ.Name(), name)
		}
		fields[name] = field
	}
	return fields
}
