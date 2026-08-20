package heteroci

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDecodeProbeResponseRoundTripsCanonicalResponse(t *testing.T) {
	want := probeCodecCanonicalResponse()
	raw := probeCodecMarshal(t, want)
	got, err := DecodeProbeResponse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeProbeResponse(valid response): %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded response differs from fixture:\n got %#v\nwant %#v", got, want)
	}
	if fields := probeCodecJSONFields(t, raw, reflect.TypeFor[ProbeResponse]()); len(fields) != 13 {
		t.Fatalf("ProbeResponse canonical root fields = %d, want 13 (%v)", len(fields), fields)
	}
}

func TestDecodeProbeResponseAllowsActiveCapabilityWithoutQualification(t *testing.T) {
	response := probeCodecCanonicalResponse()
	response.ActiveCapabilities[0].Runtime = RuntimeUbuntuContainer
	response.ActiveCapabilities[0].Qualification = nil
	if _, err := DecodeProbeResponse(bytes.NewReader(probeCodecMarshal(t, response))); err != nil {
		t.Fatalf("DecodeProbeResponse(ubuntu capability without qualification): %v", err)
	}
}

func TestDecodeProbeResponseRejectsNilEmptyAndOversizedReaders(t *testing.T) {
	valid := probeCodecMarshal(t, probeCodecCanonicalResponse())
	if len(valid) >= 1<<20 {
		t.Fatalf("canonical probe response unexpectedly exceeds 1 MiB: %d bytes", len(valid))
	}
	oversized := append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), (1<<20+1)-len(valid))...)
	cases := []struct {
		name   string
		reader io.Reader
	}{
		{name: "nil", reader: nil},
		{name: "typed nil", reader: func() io.Reader { var reader *bytes.Reader; return reader }()},
		{name: "empty", reader: strings.NewReader("")},
		{name: "oversized", reader: bytes.NewReader(oversized)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			probeCodecAssertMalformedReader(t, test.reader)
		})
	}
}

func TestDecodeProbeResponsePropagatesReaderError(t *testing.T) {
	const readerSecret = "probe-reader-secret"
	sentinel := errors.New("reader failed: " + readerSecret)
	response, err := DecodeProbeResponse(probeCodecErrorReader{err: sentinel})
	if err == nil {
		t.Fatal("DecodeProbeResponse unexpectedly succeeded")
	}
	if !reflect.DeepEqual(response, ProbeResponse{}) {
		t.Fatalf("reader-error response = %#v, want zero ProbeResponse", response)
	}
	if _, ok := errors.AsType[ProbeTransportError](err); !ok {
		t.Fatalf("error = %T %v, want ProbeTransportError", err, err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want errors.Is sentinel", err)
	}
	if _, ok := errors.AsType[ProbeMalformedError](err); ok {
		t.Fatalf("error = %T %v, unexpectedly matches ProbeMalformedError", err, err)
	}
	if strings.Contains(err.Error(), readerSecret) {
		t.Fatalf("reader error leaks secret %q: %v", readerSecret, err)
	}
}

type probeCodecErrorReader struct {
	err error
}

func (reader probeCodecErrorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func TestDecodeProbeResponseSanitizesReadErrorTrees(t *testing.T) {
	for _, test := range probeCodecReadErrorCases() {
		t.Run(test.name, func(t *testing.T) {
			probeCodecAssertReadError(t, test)
		})
	}
}

type probeCodecReadErrorCase struct {
	name       string
	cause      error
	target     error
	wantTarget bool
	secrets    []string
}

func probeCodecReadErrorCases() []probeCodecReadErrorCase {
	ordinary := errors.New("probe reader sentinel: ordinary-reader-secret")
	deepTail := errors.New("probe reader deep tail: deep-reader-secret")
	wideTail := errors.New("probe reader wide tail: wide-reader-secret")
	return []probeCodecReadErrorCase{
		{name: "ordinary sentinel", cause: ordinary, target: ordinary, wantTarget: true, secrets: []string{"ordinary-reader-secret"}},
		{name: "self-unwrapping cycle", cause: &probeCodecSelfUnwrapError{secret: "cycle-reader-secret"}, secrets: []string{"cycle-reader-secret"}},
		{name: "deep wrapper chain", cause: probeCodecDeepReadError(deepTail), target: deepTail, secrets: []string{"deep-reader-secret"}},
		{name: "wide join tree", cause: probeCodecWideReadError(wideTail), target: wideTail, secrets: []string{"wide-reader-secret"}},
		{name: "panic unwrapping", cause: &probeCodecPanicUnwrapError{secret: "panic-reader-secret"}, secrets: []string{"panic-reader-secret"}},
	}
}

func probeCodecAssertReadError(t *testing.T, test probeCodecReadErrorCase) {
	t.Helper()
	response, err := probeCodecDecodeReadError(t, test.cause)
	if err == nil {
		t.Fatal("DecodeProbeResponse unexpectedly succeeded")
	}
	if !reflect.DeepEqual(response, ProbeResponse{}) {
		t.Fatalf("reader-error response = %#v, want zero ProbeResponse", response)
	}
	if !probeCodecRunErrorQuery(t, test.name+"/transport", func() bool {
		_, ok := errors.AsType[ProbeTransportError](err)
		return ok
	}) {
		t.Fatalf("error = %T %v, want ProbeTransportError", err, err)
	}
	if probeCodecRunErrorQuery(t, test.name+"/malformed", func() bool {
		_, ok := errors.AsType[ProbeMalformedError](err)
		return ok
	}) {
		t.Fatalf("error = %T %v, unexpectedly matches ProbeMalformedError", err, err)
	}
	if test.target != nil {
		got := probeCodecRunErrorQuery(t, test.name+"/target", func() bool { return errors.Is(err, test.target) })
		if got != test.wantTarget {
			t.Fatalf("errors.Is(target) = %v, want %v", got, test.wantTarget)
		}
	}
	if probeCodecRunErrorQuery(t, test.name+"/unmatched", func() bool {
		return errors.Is(err, errors.New("unmatched probe reader sentinel"))
	}) {
		t.Fatal("errors.Is(unmatched sentinel) = true")
	}
	for _, secret := range test.secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("reader error leaks secret %q: %v", secret, err)
		}
	}
}

func probeCodecDecodeReadError(t *testing.T, cause error) (ProbeResponse, error) {
	t.Helper()
	type outcome struct {
		response ProbeResponse
		err      error
		panicVal any
	}
	result := make(chan outcome, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		got := outcome{}
		defer func() {
			got.panicVal = recover()
			result <- got
		}()
		got.response, got.err = DecodeProbeResponse(probeCodecErrorReader{err: cause})
	})
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case got := <-result:
		waitGroup.Wait()
		if got.panicVal != nil {
			t.Fatalf("DecodeProbeResponse panicked: %v", got.panicVal)
		}
		return got.response, got.err
	case <-timer.C:
		t.Fatalf("DecodeProbeResponse did not return within 1s")
		return ProbeResponse{}, nil
	}
}

func probeCodecRunErrorQuery(t *testing.T, name string, query func() bool) bool {
	t.Helper()
	type outcome struct {
		value    bool
		panicVal any
	}
	result := make(chan outcome, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		got := outcome{}
		defer func() {
			got.panicVal = recover()
			result <- got
		}()
		got.value = query()
	})
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case got := <-result:
		waitGroup.Wait()
		if got.panicVal != nil {
			t.Fatalf("%s panicked: %v", name, got.panicVal)
		}
		return got.value
	case <-timer.C:
		t.Fatalf("%s did not return within 1s", name)
		return false
	}
}

type probeCodecSelfUnwrapError struct {
	secret string
}

func (err *probeCodecSelfUnwrapError) Error() string {
	return "probe reader cycle: " + err.secret
}

func (err *probeCodecSelfUnwrapError) Unwrap() error {
	return err
}

type probeCodecPanicUnwrapError struct {
	secret string
}

func (err *probeCodecPanicUnwrapError) Error() string {
	return "probe reader panic: " + err.secret
}

func (err *probeCodecPanicUnwrapError) Unwrap() error {
	// archguard:ignore panic_count -- 测试错误树遍历必须覆盖 Unwrap panic
	panic("probe reader unwrap panic: " + err.secret)
}

func probeCodecDeepReadError(tail error) error {
	err := tail
	for range 65 {
		err = fmt.Errorf("probe reader deep wrapper: %w", err)
	}
	return err
}

func probeCodecWideReadError(tail error) error {
	causes := make([]error, 65)
	for index := range causes {
		if index == len(causes)-1 {
			causes[index] = tail
			continue
		}
		causes[index] = fmt.Errorf("probe reader wide branch %d", index)
	}
	return errors.Join(causes...)
}

func TestDecodeProbeResponseRejectsStrictJSONShapes(t *testing.T) {
	valid := probeCodecMarshal(t, probeCodecCanonicalResponse())
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "malformed", raw: []byte(`{"schema_version":`)},
		{name: "trailing", raw: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "null root", raw: []byte("null")},
		{name: "unknown root", raw: probeCodecSetRootField(t, valid, "unknown_root", json.RawMessage(`true`))},
		{name: "unknown active", raw: probeCodecSetActiveField(t, valid, "unknown_active", json.RawMessage(`true`))},
		{name: "unknown resource", raw: probeCodecSetResourceField(t, valid, "total", "unknown_resource", json.RawMessage(`true`))},
		{name: "unknown qualification", raw: probeCodecSetQualificationField(t, valid, "unknown_qualification", json.RawMessage(`true`))},
		{name: "duplicate root", raw: probeCodecDuplicateRootField(t, valid, "schema_version")},
		{name: "duplicate active", raw: probeCodecDuplicateActiveField(t, valid, "runtime")},
		{name: "duplicate resource", raw: probeCodecDuplicateResourceField(t, valid, "total", "cpu_milli")},
		{name: "duplicate qualification", raw: probeCodecDuplicateQualificationField(t, valid, "digest")},
		{name: "null active", raw: probeCodecSetActiveValue(t, valid, json.RawMessage(`null`))},
		{name: "null resource", raw: probeCodecSetRootField(t, valid, "total", json.RawMessage(`null`))},
		{name: "null qualification", raw: probeCodecSetActiveField(t, valid, "qualification", json.RawMessage(`null`))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probeCodecAssertMalformed(t, test.raw)
		})
	}
}

func TestDecodeProbeResponseRejectsMissingRootFields(t *testing.T) {
	valid := probeCodecMarshal(t, probeCodecCanonicalResponse())
	root := probeCodecObject(t, valid, "probe response root")
	for _, field := range probeCodecJSONFields(t, valid, reflect.TypeFor[ProbeResponse]()) {
		if _, ok := root[field]; !ok {
			t.Fatalf("canonical probe response is missing root field %q", field)
		}
		t.Run("missing_"+field, func(t *testing.T) {
			probeCodecAssertMalformed(t, probeCodecRemoveRootField(t, valid, field))
		})
	}
}

func TestDecodeProbeResponseRejectsMissingNestedRequiredFields(t *testing.T) {
	valid := probeCodecMarshal(t, probeCodecCanonicalResponse())
	checks := []struct {
		name string
		typ  reflect.Type
		make func(string) []byte
	}{
		{name: "active", typ: reflect.TypeFor[ActiveCapabilitySnapshot](), make: func(field string) []byte {
			return probeCodecRemoveActiveField(t, valid, field)
		}},
		{name: "total", typ: reflect.TypeFor[ResourceCapacity](), make: func(field string) []byte {
			return probeCodecRemoveResourceField(t, valid, "total", field)
		}},
		{name: "available", typ: reflect.TypeFor[ResourceCapacity](), make: func(field string) []byte {
			return probeCodecRemoveResourceField(t, valid, "available", field)
		}},
		{name: "qualification", typ: reflect.TypeFor[CapabilityQualificationReceipt](), make: func(field string) []byte {
			return probeCodecRemoveQualificationField(t, valid, field)
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			for _, field := range probeCodecJSONFields(t, probeCodecNestedObject(t, valid, check.name), check.typ) {
				t.Run("missing_"+field, func(t *testing.T) {
					probeCodecAssertMalformed(t, check.make(field))
				})
			}
		})
	}
}

func probeCodecCanonicalResponse() ProbeResponse {
	return ProbeResponse{
		SchemaVersion: ProbeResponseSchemaVersion,
		OwnerEpoch:    RoutingOwnerEpoch(7),
		CapacityBasis: SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		NodeID:        "probe-node-1",
		Endpoint:      "https://probe-endpoint-secret.invalid/runner",
		Platform:      PlatformLinuxAMD64,
		ProbeIdentity: "probe-identity-secret",
		ProbeBootID:   "probe-boot-secret",
		ActiveCapabilities: []ActiveCapabilitySnapshot{{
			Runtime:     RuntimeDarlingCompatRuntime,
			ImageDigest: "sha256:probe-image-secret",
			Qualification: &CapabilityQualificationReceipt{
				SchemaVersion:        CapabilityQualificationSchemaVersion,
				NodeID:               "probe-node-1",
				Runtime:              RuntimeDarlingCompatRuntime,
				ProbeIdentity:        "probe-identity-secret",
				ProbeBootID:          "probe-boot-secret",
				ImageDigest:          "sha256:qualification-image",
				SourceRevision:       "probe-response-body-secret",
				QualificationSuite:   "darling-attestation/v1",
				ImmutableIdentity:    "immutable-probe-secret",
				CompletedAtUnixMilli: 1_700_000_000_000,
				Digest:               "sha256:" + strings.Repeat("a", 64),
			},
		}},
		Status:             NodeStatusLive,
		HeartbeatUnixMilli: 1_700_000_000_000,
		Total:              ResourceCapacity{CPUMilli: 8_000, MemoryBytes: 16 << 30, MaxConcurrency: 8},
		Available:          ResourceCapacity{CPUMilli: 7_500, MemoryBytes: 15 << 30, MaxConcurrency: 7},
	}
}

func probeCodecMarshal(t *testing.T, response ProbeResponse) []byte {
	t.Helper()
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal ProbeResponse: %v", err)
	}
	return raw
}

func probeCodecAssertMalformedReader(t *testing.T, reader io.Reader) {
	t.Helper()
	response, err := DecodeProbeResponse(reader)
	if err == nil {
		t.Fatal("DecodeProbeResponse unexpectedly succeeded")
	}
	probeCodecAssertMalformedError(t, response, err)
}

func probeCodecAssertMalformed(t *testing.T, raw []byte) {
	t.Helper()
	response, err := DecodeProbeResponse(bytes.NewReader(raw))
	if err == nil {
		t.Fatalf("DecodeProbeResponse unexpectedly accepted %s", raw)
	}
	probeCodecAssertMalformedError(t, response, err)
}

func probeCodecAssertMalformedError(t *testing.T, response ProbeResponse, err error) {
	t.Helper()
	var malformed ProbeMalformedError
	if !errors.As(err, &malformed) {
		var malformedPointer *ProbeMalformedError
		if !errors.As(err, &malformedPointer) {
			t.Fatalf("error = %T %v, want ProbeMalformedError", err, err)
		}
		malformed = *malformedPointer
	}
	if malformed.Cause == nil {
		t.Fatal("ProbeMalformedError.Cause = nil")
	}
	if !reflect.DeepEqual(response, ProbeResponse{}) {
		t.Fatalf("malformed response = %#v, want zero ProbeResponse", response)
	}
	for _, secret := range probeCodecSecrets() {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("malformed error leaks response secret %q: %v", secret, err)
		}
	}
}

func probeCodecSecrets() []string {
	response := probeCodecCanonicalResponse()
	active := response.ActiveCapabilities[0]
	qualification := active.Qualification
	return []string{
		response.Endpoint,
		response.ProbeIdentity,
		response.ProbeBootID,
		active.ImageDigest,
		qualification.ImageDigest,
		qualification.SourceRevision,
		qualification.ImmutableIdentity,
	}
}

func probeCodecJSONFields(t *testing.T, raw []byte, typ reflect.Type) []string {
	t.Helper()
	object := probeCodecObject(t, raw, "canonical object")
	fields := make([]string, 0)
	for field := range typ.Fields() {
		if field.PkgPath != "" {
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")
		name := tag[0]
		if name == "" {
			name = field.Name
		}
		if name == "-" {
			continue
		}
		if probeCodecHasJSONOption(tag[1:], "omitempty") {
			continue
		}
		if _, present := object[name]; !present {
			t.Fatalf("canonical %s object is missing field %q", typ, name)
		}
		fields = append(fields, name)
	}
	return fields
}

func probeCodecHasJSONOption(options []string, want string) bool {
	return slices.Contains(options, want)
}

func probeCodecNestedObject(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	switch name {
	case "active":
		return probeCodecActiveObject(t, raw)
	case "total", "available":
		return probeCodecResourceObject(t, raw, name)
	case "qualification":
		return probeCodecQualificationObject(t, raw)
	default:
		t.Fatalf("unknown probe nested object %q", name)
		return nil
	}
}

func probeCodecObject(t *testing.T, raw []byte, context string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode %s: %v", context, err)
	}
	if object == nil {
		t.Fatalf("decode %s: null object", context)
	}
	return object
}

func probeCodecActiveObject(t *testing.T, raw []byte) []byte {
	t.Helper()
	root := probeCodecObject(t, raw, "probe root")
	var active []json.RawMessage
	if err := json.Unmarshal(root["active_capabilities"], &active); err != nil || len(active) == 0 {
		t.Fatalf("decode active_capabilities: err=%v len=%d", err, len(active))
	}
	return active[0]
}

func probeCodecResourceObject(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	root := probeCodecObject(t, raw, "probe root")
	return root[name]
}

func probeCodecQualificationObject(t *testing.T, raw []byte) []byte {
	t.Helper()
	active := probeCodecObject(t, probeCodecActiveObject(t, raw), "active capability")
	return active["qualification"]
}

func probeCodecSetRootField(t *testing.T, raw []byte, name string, value json.RawMessage) []byte {
	t.Helper()
	root := probeCodecObject(t, raw, "probe root")
	root[name] = value
	return probeCodecMustMarshal(t, root)
}

func probeCodecRemoveRootField(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	root := probeCodecObject(t, raw, "probe root")
	delete(root, name)
	return probeCodecMustMarshal(t, root)
}

func probeCodecSetActiveField(t *testing.T, raw []byte, name string, value json.RawMessage) []byte {
	t.Helper()
	return probeCodecUpdateActive(t, raw, func(active map[string]json.RawMessage) { active[name] = value })
}

func probeCodecRemoveActiveField(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	return probeCodecUpdateActive(t, raw, func(active map[string]json.RawMessage) { delete(active, name) })
}

func probeCodecSetResourceField(t *testing.T, raw []byte, resource, name string, value json.RawMessage) []byte {
	t.Helper()
	return probeCodecUpdateResource(t, raw, resource, func(object map[string]json.RawMessage) { object[name] = value })
}

func probeCodecRemoveResourceField(t *testing.T, raw []byte, resource, name string) []byte {
	t.Helper()
	return probeCodecUpdateResource(t, raw, resource, func(object map[string]json.RawMessage) { delete(object, name) })
}

func probeCodecSetQualificationField(t *testing.T, raw []byte, name string, value json.RawMessage) []byte {
	t.Helper()
	return probeCodecUpdateQualification(t, raw, func(object map[string]json.RawMessage) { object[name] = value })
}

func probeCodecRemoveQualificationField(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	return probeCodecUpdateQualification(t, raw, func(object map[string]json.RawMessage) { delete(object, name) })
}

func probeCodecSetActiveValue(t *testing.T, raw []byte, value json.RawMessage) []byte {
	t.Helper()
	root := probeCodecObject(t, raw, "probe root")
	var active []json.RawMessage
	if err := json.Unmarshal(root["active_capabilities"], &active); err != nil || len(active) == 0 {
		t.Fatalf("decode active_capabilities: err=%v len=%d", err, len(active))
	}
	active[0] = value
	root["active_capabilities"] = probeCodecMustMarshal(t, active)
	return probeCodecMustMarshal(t, root)
}

func probeCodecUpdateActive(t *testing.T, raw []byte, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	return probeCodecUpdateActiveRaw(t, raw, func(value []byte) []byte {
		object := probeCodecObject(t, value, "active capability")
		mutate(object)
		return probeCodecMustMarshal(t, object)
	})
}

func probeCodecUpdateActiveRaw(t *testing.T, raw []byte, mutate func([]byte) []byte) []byte {
	t.Helper()
	root := probeCodecObject(t, raw, "probe root")
	var active []json.RawMessage
	if err := json.Unmarshal(root["active_capabilities"], &active); err != nil || len(active) == 0 {
		t.Fatalf("decode active_capabilities: err=%v len=%d", err, len(active))
	}
	active[0] = mutate(active[0])
	root["active_capabilities"] = probeCodecMustMarshal(t, active)
	return probeCodecMustMarshal(t, root)
}

func probeCodecUpdateResource(t *testing.T, raw []byte, resource string, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	root := probeCodecObject(t, raw, "probe root")
	object := probeCodecObject(t, root[resource], resource)
	mutate(object)
	root[resource] = probeCodecMustMarshal(t, object)
	return probeCodecMustMarshal(t, root)
}

func probeCodecUpdateQualification(t *testing.T, raw []byte, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	return probeCodecUpdateQualificationRaw(t, raw, func(value []byte) []byte {
		object := probeCodecObject(t, value, "qualification")
		mutate(object)
		return probeCodecMustMarshal(t, object)
	})
}

func probeCodecUpdateQualificationRaw(t *testing.T, raw []byte, mutate func([]byte) []byte) []byte {
	t.Helper()
	return probeCodecUpdateActive(t, raw, func(active map[string]json.RawMessage) {
		active["qualification"] = mutate(active["qualification"])
	})
}

func probeCodecDuplicateRootField(t *testing.T, raw []byte, field string) []byte {
	t.Helper()
	return probeCodecAppendDuplicate(t, raw, field)
}

func probeCodecDuplicateActiveField(t *testing.T, raw []byte, field string) []byte {
	t.Helper()
	return probeCodecUpdateActiveRaw(t, raw, func(value []byte) []byte { return probeCodecAppendDuplicate(t, value, field) })
}

func probeCodecDuplicateResourceField(t *testing.T, raw []byte, resource, field string) []byte {
	t.Helper()
	root := probeCodecObject(t, raw, "probe root")
	root[resource] = probeCodecAppendDuplicate(t, root[resource], field)
	return probeCodecMustMarshal(t, root)
}

func probeCodecDuplicateQualificationField(t *testing.T, raw []byte, field string) []byte {
	t.Helper()
	return probeCodecUpdateQualificationRaw(t, raw, func(value []byte) []byte { return probeCodecAppendDuplicate(t, value, field) })
}

func probeCodecAppendDuplicate(t *testing.T, raw []byte, field string) []byte {
	t.Helper()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[len(trimmed)-1] != '}' {
		t.Fatalf("JSON object %q is not closed", field)
	}
	result := append([]byte(nil), trimmed[:len(trimmed)-1]...)
	result = append(result, fmt.Appendf(nil, ",%q:null}", field)...)
	return result
}

func probeCodecMustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal probe fixture: %v", err)
	}
	return raw
}
