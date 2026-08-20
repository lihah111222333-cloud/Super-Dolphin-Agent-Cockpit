package heteroci

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProbeSchemaVersionsAreFrozen(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "request", got: ProbeRequestSchemaVersion, want: "heteroci-probe-request/v1"},
		{name: "response", got: ProbeResponseSchemaVersion, want: "heteroci-probe-response/v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("probe %s schema version = %q, want %q", test.name, test.got, test.want)
			}
		})
	}
}

func TestRoutingOwnerEpochUsesInt64UnderlyingType(t *testing.T) {
	typ := reflect.TypeFor[RoutingOwnerEpoch]()
	if typ.Kind() != reflect.Int64 || typ.Bits() != 64 {
		t.Fatalf("RoutingOwnerEpoch kind/bits = %s/%d, want int64/64", typ.Kind(), typ.Bits())
	}
}

func TestProbeRequestFieldLayoutAndJSONTagsAreFrozen(t *testing.T) {
	assertProbeStructLayout(t, reflect.TypeFor[ProbeRequest](), []probeFieldSpec{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), jsonTag: "schema_version"},
		{name: "NodeID", typ: reflect.TypeFor[string](), jsonTag: "node_id"},
		{name: "ProbeIdentity", typ: reflect.TypeFor[string](), jsonTag: "probe_identity"},
		{name: "OwnerEpoch", typ: reflect.TypeFor[RoutingOwnerEpoch](), jsonTag: "owner_epoch"},
	})
}

func TestProbeResponseFieldLayoutAndJSONTagsAreFrozen(t *testing.T) {
	assertProbeStructLayout(t, reflect.TypeFor[ProbeResponse](), []probeFieldSpec{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), jsonTag: "schema_version"},
		{name: "OwnerEpoch", typ: reflect.TypeFor[RoutingOwnerEpoch](), jsonTag: "owner_epoch"},
		{name: "CapacityBasis", typ: reflect.TypeFor[SnapshotCapacityBasis](), jsonTag: "capacity_basis"},
		{name: "NodeID", typ: reflect.TypeFor[string](), jsonTag: "node_id"},
		{name: "Endpoint", typ: reflect.TypeFor[string](), jsonTag: "endpoint"},
		{name: "Platform", typ: reflect.TypeFor[Platform](), jsonTag: "platform"},
		{name: "ProbeIdentity", typ: reflect.TypeFor[string](), jsonTag: "probe_identity"},
		{name: "ProbeBootID", typ: reflect.TypeFor[string](), jsonTag: "probe_boot_id"},
		{name: "ActiveCapabilities", typ: reflect.TypeFor[[]ActiveCapabilitySnapshot](), jsonTag: "active_capabilities"},
		{name: "Status", typ: reflect.TypeFor[NodeStatus](), jsonTag: "status"},
		{name: "HeartbeatUnixMilli", typ: reflect.TypeFor[int64](), jsonTag: "heartbeat_unix_milli"},
		{name: "Total", typ: reflect.TypeFor[ResourceCapacity](), jsonTag: "total"},
		{name: "Available", typ: reflect.TypeFor[ResourceCapacity](), jsonTag: "available"},
	})
}

type probeFieldSpec struct {
	name    string
	typ     reflect.Type
	jsonTag string
}

func assertProbeStructLayout(t *testing.T, typ reflect.Type, want []probeFieldSpec) {
	t.Helper()
	if typ.NumField() != len(want) {
		t.Fatalf("%s field count = %d, want %d", typ.Name(), typ.NumField(), len(want))
	}
	for index, spec := range want {
		field := typ.Field(index)
		if field.Name != spec.name {
			t.Errorf("field %d name = %s, want %s", index, field.Name, spec.name)
		}
		if field.Type != spec.typ {
			t.Errorf("field %s type = %s, want %s", field.Name, field.Type, spec.typ)
		}
		if got := field.Tag.Get("json"); got != spec.jsonTag {
			t.Errorf("field %s json tag = %q, want %q", field.Name, got, spec.jsonTag)
		}
		if strings.Contains(field.Tag.Get("json"), ",") {
			t.Errorf("field %s json tag has options: %q", field.Name, field.Tag.Get("json"))
		}
	}
}

type probeTransportStub struct{}

type probeResponseBodyStub struct{}

func (probeResponseBodyStub) Read(context.Context, []byte) (int, error) {
	return 0, nil
}

func (probeResponseBodyStub) Close() error {
	return nil
}

func (probeTransportStub) Probe(context.Context, string, []byte) (ProbeResponseBody, error) {
	return probeResponseBodyStub{}, nil
}

var _ ProbeResponseBody = probeResponseBodyStub{}
var _ ProbeTransport = probeTransportStub{}

func TestProbeResponseBodySignatureIsFrozen(t *testing.T) {
	typ := reflect.TypeFor[ProbeResponseBody]()
	if typ.NumMethod() != 2 {
		t.Fatalf("ProbeResponseBody method count = %d, want 2", typ.NumMethod())
	}
	want := map[string]reflect.Type{
		"Read":  reflect.TypeOf(func(context.Context, []byte) (int, error) { return 0, nil }),
		"Close": reflect.TypeOf(func() error { return nil }),
	}
	for name, wantType := range want {
		method, ok := typ.MethodByName(name)
		if !ok {
			t.Fatalf("ProbeResponseBody.%s method is missing", name)
		}
		if method.Type != wantType {
			t.Fatalf("ProbeResponseBody.%s type = %s, want %s", name, method.Type, wantType)
		}
	}
}

func TestProbeTransportSignatureIsFrozen(t *testing.T) {
	typ := reflect.TypeFor[ProbeTransport]()
	if typ.NumMethod() != 1 {
		t.Fatalf("ProbeTransport method count = %d, want 1", typ.NumMethod())
	}
	method, ok := typ.MethodByName("Probe")
	if !ok {
		t.Fatal("ProbeTransport.Probe method is missing")
	}
	want := reflect.TypeOf(func(context.Context, string, []byte) (ProbeResponseBody, error) {
		return nil, nil
	})
	if method.Type != want {
		t.Fatalf("ProbeTransport.Probe type = %s, want %s", method.Type, want)
	}
}

func TestProbeTypedErrorsRedactSensitiveCauseAndUnwrap(t *testing.T) {
	sentinel := errors.New("endpoint=https://probe.invalid/?token=endpoint-secret probe=probe-secret body=body-secret")
	cases := []struct {
		name string
		err  error
		as   func(error) bool
	}{
		{name: "argument", err: ProbeArgumentError{Field: "endpoint", Cause: sentinel}, as: func(err error) bool {
			var target ProbeArgumentError
			return errors.As(err, &target)
		}},
		{name: "transport", err: ProbeTransportError{NodeID: "node-1", Cause: sentinel}, as: func(err error) bool {
			var target ProbeTransportError
			return errors.As(err, &target)
		}},
		{name: "malformed", err: ProbeMalformedError{NodeID: "node-1", Cause: sentinel}, as: func(err error) bool {
			var target ProbeMalformedError
			return errors.As(err, &target)
		}},
		{name: "protocol", err: ProbeProtocolError{NodeID: "node-1", Field: "body", Cause: sentinel}, as: func(err error) bool {
			var target ProbeProtocolError
			return errors.As(err, &target)
		}},
		{name: "identity drift", err: ProbeIdentityDriftError{NodeID: "node-1", Field: "probe_identity", Cause: sentinel}, as: func(err error) bool {
			var target ProbeIdentityDriftError
			return errors.As(err, &target)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if !errors.Is(test.err, sentinel) {
				t.Fatalf("errors.Is(%T, sentinel) = false", test.err)
			}
			if !test.as(test.err) {
				t.Fatalf("errors.As(%T) = false", test.err)
			}
			message := test.err.Error()
			for _, forbidden := range []string{"https://probe.invalid", "endpoint-secret", "probe-secret", "body-secret"} {
				if strings.Contains(message, forbidden) {
					t.Fatalf("%T error leaks %q: %q", test.err, forbidden, message)
				}
			}
		})
	}
}

func TestProbeZeroValuesRemainExplicitInJSON(t *testing.T) {
	tests := []struct {
		name  string
		value any
		keys  []string
	}{
		{name: "request", value: ProbeRequest{}, keys: []string{
			"schema_version", "node_id", "probe_identity", "owner_epoch",
		}},
		{name: "response", value: ProbeResponse{}, keys: []string{
			"schema_version", "owner_epoch", "capacity_basis", "node_id", "endpoint", "platform",
			"probe_identity", "probe_boot_id", "active_capabilities", "status", "heartbeat_unix_milli", "total", "available",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.value)
			if err != nil {
				t.Fatalf("json.Marshal(%T): %v", test.value, err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &object); err != nil {
				t.Fatalf("json.Unmarshal(%T): %v; JSON=%s", test.value, err, encoded)
			}
			if len(object) != len(test.keys) {
				t.Fatalf("%T JSON field count = %d, want %d; JSON=%s", test.value, len(object), len(test.keys), encoded)
			}
			for _, key := range test.keys {
				if _, ok := object[key]; !ok {
					t.Errorf("%T JSON omits zero field %q; JSON=%s", test.value, key, encoded)
				}
			}
		})
	}
}
