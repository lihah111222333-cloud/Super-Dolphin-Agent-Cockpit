package rpcwire

import (
	"bytes"
	"errors"
	"testing"
)

func TestCanonicalJSONRoundTrip(t *testing.T) {
	submit := SubmitParams{SchemaVersion: SchemaVersion, Request: validRequest(t)}
	payload, err := MarshalCanonical(submit)
	if err != nil {
		t.Fatalf("MarshalCanonical(submit) error = %v", err)
	}
	decoded, err := DecodeSubmitParams(payload)
	if err != nil {
		t.Fatalf("DecodeSubmitParams() error = %v", err)
	}
	if decoded != submit {
		t.Fatalf("submit round trip = %+v, want %+v", decoded, submit)
	}

	query := QueryParams{SchemaVersion: SchemaVersion, Handle: validHandle(t)}
	payload, err = MarshalCanonical(query)
	if err != nil {
		t.Fatalf("MarshalCanonical(query) error = %v", err)
	}
	decodedQuery, err := DecodeQueryParams(payload)
	if err != nil {
		t.Fatalf("DecodeQueryParams() error = %v", err)
	}
	if decodedQuery != query {
		t.Fatalf("query round trip = %+v, want %+v", decodedQuery, query)
	}

	identity := Result{SchemaVersion: SchemaVersion, Authority: validAuthorityIdentity()}
	payload, err = MarshalCanonical(identity)
	if err != nil {
		t.Fatalf("MarshalCanonical(identity) error = %v", err)
	}
	decodedResult, err := DecodeResultForMethod(payload, MethodIdentity)
	if err != nil {
		t.Fatalf("DecodeResultForMethod() error = %v", err)
	}
	if decodedResult != identity {
		t.Fatalf("identity round trip = %+v, want %+v", decodedResult, identity)
	}
}

func TestCanonicalDecoderRejectsAmbiguousOrNonCanonicalJSON(t *testing.T) {
	valid, err := MarshalCanonical(QueryParams{SchemaVersion: SchemaVersion, Handle: validHandle(t)})
	if err != nil {
		t.Fatal(err)
	}
	unknown := append(bytes.TrimSuffix(valid, []byte("}")), []byte(`,"extra":true}`)...)
	duplicate := append([]byte(`{"schema_version":1,`), valid[1:]...)
	nestedNull := bytes.Replace(valid, []byte(`"handle":{`), []byte(`"handle":null,"discarded":{`), 1)
	tests := []struct {
		name    string
		payload []byte
	}{
		{"unknown", unknown},
		{"duplicate", duplicate},
		{"null", nestedNull},
		{"trailing", append(append([]byte{}, valid...), []byte(` {}`)...)},
		{"leading_space", append([]byte(" "), valid...)},
		{"noncanonical_number", bytes.Replace(valid, []byte(`"schema_version":1`), []byte(`"schema_version":1.0`), 1)},
		{"reordered_fields", append(append([]byte(`{"handle":`), bytes.TrimSuffix(bytes.SplitN(valid, []byte(`"handle":`), 2)[1], []byte("}"))...), []byte(`,"schema_version":1}`)...)},
		{"top_level_array", []byte(`[]`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, decodeErr := DecodeQueryParams(test.payload)
			if !errors.Is(decodeErr, ErrInvalidWire) {
				t.Fatalf("DecodeQueryParams(%s) error = %v, want ErrInvalidWire", test.payload, decodeErr)
			}
			assertErrorContainsNoSensitiveValue(t, decodeErr)
		})
	}
}

func TestCanonicalDecoderRejectsNestedDuplicateAndExplicitOptionalNull(t *testing.T) {
	request := validRequest(t)
	submit, err := MarshalCanonical(SubmitParams{SchemaVersion: SchemaVersion, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	needle := []byte(`"operation_id":"operation-1"`)
	nestedDuplicate := bytes.Replace(submit, needle, append(append([]byte{}, needle...), []byte(`,"operation_id":"operation-1"`)...), 1)
	if _, err := DecodeSubmitParams(nestedDuplicate); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("nested duplicate error = %v, want ErrInvalidWire", err)
	}

	identity, err := MarshalCanonical(Result{SchemaVersion: SchemaVersion, Authority: validAuthorityIdentity()})
	if err != nil {
		t.Fatal(err)
	}
	explicitNull := append(bytes.TrimSuffix(identity, []byte("}")), []byte(`,"failure":null}`)...)
	if _, err := DecodeResult(explicitNull); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("optional null error = %v, want ErrInvalidWire", err)
	}
}
