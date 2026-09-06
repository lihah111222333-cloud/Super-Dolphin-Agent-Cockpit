package issuer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime"
)

type receiptVerifierFixture struct {
	calls int
	err   error
}

func (v *receiptVerifierFixture) VerifyPrepared(context.Context, managedruntime.PrepareRequest, managedruntime.PreparedReceipt) error {
	v.calls++
	return v.err
}

func TestEndpointAndProcessReceiptBindingRoundTrip(t *testing.T) {
	prepare := validPrepare(t, "bind")
	prepared := validPrepared(t, prepare, "bind")
	endpoint := validEndpoint(t, "bind")
	request := validBindRequest(t, endpoint, prepared, "bind")
	receipt := validBindingReceipt(t, request, prepared, "bind")
	verifier := &receiptVerifierFixture{}
	if _, err := ValidateBind(context.Background(), verifier, request, prepare, prepared, receipt); err != nil {
		t.Fatalf("ValidateBind() error = %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("receipt verifier calls = %d, want 1", verifier.calls)
	}
	proof, err := ValidateBind(context.Background(), verifier, request, prepare, prepared, receipt)
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedProof(t, proof, prepare, prepared)
	assertJSONRoundTrip(t, endpoint)
	endpointPayload, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	decodedEndpoint, err := DecodeEndpoint(endpointPayload)
	if err != nil || !reflect.DeepEqual(decodedEndpoint, endpoint) {
		t.Fatalf("DecodeEndpoint()=(%#v,%v), want exact endpoint", decodedEndpoint, err)
	}
	assertJSONRoundTrip(t, request)
	assertJSONRoundTrip(t, receipt)
	receiptPayload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	decodedReceipt, err := DecodeBindingReceipt(receiptPayload)
	if err != nil || !reflect.DeepEqual(decodedReceipt, receipt) {
		t.Fatalf("DecodeBindingReceipt()=(%#v,%v), want exact receipt", decodedReceipt, err)
	}
}

// assertPreparedProof 检查 verified marker 只暴露安全副本且格式化不泄露进程身份。
func assertPreparedProof(t *testing.T, proof PreparedReceiptProof, prepare managedruntime.PrepareRequest, prepared managedruntime.PreparedReceipt) {
	t.Helper()
	gotPrepare, err := proof.PrepareRequest()
	if err != nil || !reflect.DeepEqual(gotPrepare, prepare) {
		t.Fatalf("proof PrepareRequest()=(%#v,%v), want exact request", gotPrepare, err)
	}
	gotPrepared, err := proof.PreparedReceipt()
	if err != nil || !reflect.DeepEqual(gotPrepared, prepared) {
		t.Fatalf("proof PreparedReceipt()=(%#v,%v), want exact receipt", gotPrepared, err)
	}
	for _, rendered := range []string{proof.String(), fmt.Sprintf("%v", proof), fmt.Sprintf("%#v", proof)} {
		if strings.Contains(rendered, "opaque://") || strings.Contains(rendered, "/verified/") {
			t.Fatalf("proof formatting leaked process identity: %q", rendered)
		}
	}
}

func TestEndpointRejectsCredentialBearingOrNonLoopbackURLs(t *testing.T) {
	for _, raw := range []string{
		"https://127.0.0.1:43123/mcp", "http://127.0.0.1:43123/mcp?token=secret",
		"http://127.0.0.1:43123/mcp?", "http://127.0.0.1:43123/mcp#",
		"http://user:pass@127.0.0.1:43123/mcp", "http://127.0.0.1:43123/other",
		"http://localhost:43123/mcp", "http://127.0.0.2:43123/mcp", "http://127.0.0.1:0/mcp",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := NewEndpoint(raw, "runtime-bind"); !errors.Is(err, ErrInvalidEndpoint) {
				t.Fatalf("NewEndpoint(%q) error = %v, want ErrInvalidEndpoint", raw, err)
			}
		})
	}
	for _, instanceID := range []string{"", " runtime", "runtime/child", "runtime#fragment"} {
		t.Run("instance/"+instanceID, func(t *testing.T) {
			if _, err := NewEndpoint("http://127.0.0.1:43123/mcp", instanceID); !errors.Is(err, ErrInvalidEndpoint) {
				t.Fatalf("NewEndpoint(instance=%q) error = %v, want ErrInvalidEndpoint", instanceID, err)
			}
		})
	}
	if _, err := NewEndpoint("http://127.0.0.1:43123/mcp"+strings.Repeat("x", endpointURLTextLimit), "runtime-bind"); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("NewEndpoint(oversized URL) error = %v, want ErrInvalidEndpoint", err)
	}
}

func TestBindRejectsForeignProcessReceiptEndpointCredentialAndProvider(t *testing.T) {
	prepare := validPrepare(t, "foreign")
	prepared := validPrepared(t, prepare, "foreign")
	endpoint := validEndpoint(t, "foreign")
	request := validBindRequest(t, endpoint, prepared, "foreign")
	receipt := validBindingReceipt(t, request, prepared, "foreign")
	cases := []struct {
		name   string
		mutate func(*BindRequest, *managedruntime.PreparedReceipt, *BindingReceipt)
	}{
		{name: "endpoint", mutate: func(req *BindRequest, _ *managedruntime.PreparedReceipt, _ *BindingReceipt) {
			other := validEndpoint(t, "other")
			req.Endpoint = other
			*req = mustFinalizeBind(t, *req)
		}},
		{name: "credential", mutate: func(req *BindRequest, _ *managedruntime.PreparedReceipt, _ *BindingReceipt) {
			req.CredentialDigest = testDigest("foreign-credential")
			*req = mustFinalizeBind(t, *req)
		}},
		{name: "provider", mutate: func(req *BindRequest, _ *managedruntime.PreparedReceipt, _ *BindingReceipt) {
			req.Provider = "grok"
			*req = mustFinalizeBind(t, *req)
		}},
		{name: "provider profile changed on both sides", mutate: func(req *BindRequest, _ *managedruntime.PreparedReceipt, receipt *BindingReceipt) {
			foreign := testDigest("foreign-profile")
			req.ProviderProfileDigest, receipt.ProviderProfileDigest = foreign, foreign
			*req = mustFinalizeBind(t, *req)
			*receipt = mustFinalizeBinding(t, *receipt)
		}},
		{name: "prepared receipt", mutate: func(_ *BindRequest, prepared *managedruntime.PreparedReceipt, _ *BindingReceipt) {
			prepared.ProviderSessionID = "foreign-session"
			*prepared = mustFinalizePrepared(t, *prepared)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidateRequest, candidatePrepared, candidateReceipt := request, prepared, receipt
			tc.mutate(&candidateRequest, &candidatePrepared, &candidateReceipt)
			if _, err := ValidateBind(context.Background(), &receiptVerifierFixture{}, candidateRequest, prepare, candidatePrepared, candidateReceipt); err == nil {
				t.Fatal("ValidateBind() accepted a foreign binding")
			}
		})
	}
}

func TestValidateBindRequiresReadOnlyVerifierBeforeBinding(t *testing.T) {
	prepare := validPrepare(t, "verifier")
	prepared := validPrepared(t, prepare, "verifier")
	request := validBindRequest(t, validEndpoint(t, "verifier"), prepared, "verifier")
	receipt := validBindingReceipt(t, request, prepared, "verifier")
	var typedNil *receiptVerifierFixture
	for name, verifier := range map[string]ProcessReceiptVerifier{
		"nil":       nil,
		"typed nil": typedNil,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateBind(context.Background(), verifier, request, prepare, prepared, receipt); !errors.Is(err, ErrReceiptVerifierUnavailable) {
				t.Fatalf("ValidateBind() error = %v, want ErrReceiptVerifierUnavailable", err)
			}
		})
	}
	verifier := &receiptVerifierFixture{err: errors.New("secret process path")}
	if _, err := ValidateBind(context.Background(), verifier, request, prepare, prepared, receipt); !errors.Is(err, ErrReceiptVerificationFailed) || strings.Contains(err.Error(), "secret process path") {
		t.Fatalf("rejected receipt error = %v, want redacted ErrReceiptVerificationFailed", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("rejected receipt verifier calls = %d, want 1", verifier.calls)
	}
	malformed := request
	malformed.CredentialDigest = ""
	malformed = mustFinalizeBind(t, malformed)
	unused := &receiptVerifierFixture{}
	if _, err := ValidateBind(context.Background(), unused, malformed, prepare, prepared, receipt); !errors.Is(err, ErrInvalidBindRequest) || unused.calls != 0 {
		t.Fatalf("malformed request validation error=%v calls=%d, want fail before verifier", err, unused.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	verifier = &receiptVerifierFixture{}
	if _, err := ValidateBind(ctx, verifier, request, prepare, prepared, receipt); !errors.Is(err, ErrReceiptVerifierUnavailable) || verifier.calls != 0 {
		t.Fatalf("cancelled ValidateBind() error=%v calls=%d, want unavailable and zero calls", err, verifier.calls)
	}
}

// TestPreparedReceiptProofCannotBeForgedByZeroValue 锁定 verifier marker 的 fail-closed 语义。
func TestPreparedReceiptProofCannotBeForgedByZeroValue(t *testing.T) {
	var proof PreparedReceiptProof
	if !errors.Is(proof.Validate(), ErrReceiptVerifierUnavailable) {
		t.Fatalf("zero PreparedReceiptProof.Validate() = %v, want ErrReceiptVerifierUnavailable", proof.Validate())
	}
}

func TestLifecycleTransitionMatrixIsFailClosed(t *testing.T) {
	cases := []struct {
		from    State
		trigger Trigger
		want    State
		ok      bool
	}{
		{StateCandidate, TriggerPrepare, StatePrepared, true},
		{StatePrepared, TriggerBind, StateBound, true},
		{StateBound, TriggerRevoke, StateRevoked, true},
		{StatePrepared, TriggerAbort, StateAborted, true},
		{StateCandidate, TriggerAbort, "", false},
		{StateBound, TriggerAbort, "", false},
		{StateOutcomeUnknown, TriggerBind, "", false},
		{StateRevoked, TriggerRevoke, "", false},
		{StateAborted, TriggerPrepare, "", false},
	}
	for _, tc := range cases {
		t.Run(string(tc.from)+"/"+string(tc.trigger), func(t *testing.T) {
			got, err := Transition(tc.from, tc.trigger)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("Transition()=(%q,%v), want (%q,nil)", got, err, tc.want)
				}
				return
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("Transition() error = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestAbortAndRevokeRequireExactReceiptAndClosureEvidence(t *testing.T) {
	prepare := validPrepare(t, "close")
	prepared := validPrepared(t, prepare, "close")
	endpoint := validEndpoint(t, "close")
	request := validBindRequest(t, endpoint, prepared, "close")
	binding := validBindingReceipt(t, request, prepared, "close")
	abort := mustFinalizeAbort(t, AbortRequest{SchemaVersion: SchemaVersion, OperationID: "abort-close", PreparedOperationID: prepared.OperationID, PreparedRequestDigest: prepared.RequestDigest, PreparedReceiptRef: prepared.ReceiptRef, PreparedReceiptDigest: prepared.ReceiptDigest, CleanupEvidenceRef: "evidence://abort/close", CleanupEvidenceDigest: testDigest("abort-evidence")})
	aborted := mustFinalizeClosed(t, ClosedReceipt{SchemaVersion: SchemaVersion, OperationID: abort.OperationID, PreparedOperationID: prepared.OperationID, PreparedRequestDigest: prepared.RequestDigest, ExpectedReceiptRef: prepared.ReceiptRef, ExpectedReceiptDigest: prepared.ReceiptDigest, State: StateAborted, OperationDigest: abort.RequestDigest, ClosureEvidenceRef: abort.CleanupEvidenceRef, ClosureEvidenceDigest: abort.CleanupEvidenceDigest, ReceiptRef: "receipt://abort/close"})
	proof, err := VerifyPreparedReceipt(context.Background(), &receiptVerifierFixture{}, prepare, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAbort(abort, proof, aborted); err != nil {
		t.Fatalf("ValidateAbort() error = %v", err)
	}
	closedPayload, err := json.Marshal(aborted)
	if err != nil {
		t.Fatal(err)
	}
	decodedClosed, err := DecodeClosedReceipt(closedPayload)
	if err != nil || !reflect.DeepEqual(decodedClosed, aborted) {
		t.Fatalf("DecodeClosedReceipt()=(%#v,%v), want exact receipt", decodedClosed, err)
	}
	wrongAbortState := aborted
	wrongAbortState.State = StateRevoked
	wrongAbortState = mustFinalizeClosed(t, wrongAbortState)
	if err := ValidateAbort(abort, proof, wrongAbortState); !errors.Is(err, ErrInvalidClosure) {
		t.Fatalf("ValidateAbort(wrong state) error = %v, want ErrInvalidClosure", err)
	}
	wrongAbortBinding := aborted
	wrongAbortBinding.BindingOperationID = "bind-close"
	wrongAbortBinding = mustFinalizeClosed(t, wrongAbortBinding)
	if err := ValidateAbort(abort, proof, wrongAbortBinding); !errors.Is(err, ErrInvalidClosure) {
		t.Fatalf("ValidateAbort(binding operation) error = %v, want ErrInvalidClosure", err)
	}
	revoke := mustFinalizeRevoke(t, RevokeRequest{SchemaVersion: SchemaVersion, OperationID: "revoke-close", BindingOperationID: binding.OperationID, ExpectedReceiptRef: binding.ReceiptRef, ExpectedReceiptDigest: binding.ReceiptDigest, ClosureEvidenceRef: "evidence://revoke/close", ClosureEvidenceDigest: testDigest("revoke-evidence")})
	revoked := mustFinalizeClosed(t, ClosedReceipt{SchemaVersion: SchemaVersion, OperationID: revoke.OperationID, BindingOperationID: binding.OperationID, PreparedOperationID: prepared.OperationID, PreparedRequestDigest: prepared.RequestDigest, ExpectedReceiptRef: binding.ReceiptRef, ExpectedReceiptDigest: binding.ReceiptDigest, State: StateRevoked, OperationDigest: revoke.RequestDigest, ClosureEvidenceRef: revoke.ClosureEvidenceRef, ClosureEvidenceDigest: revoke.ClosureEvidenceDigest, ReceiptRef: "receipt://revoke/close"})
	if err := ValidateRevoke(revoke, binding, revoked); err != nil {
		t.Fatalf("ValidateRevoke() error = %v", err)
	}
	revoked.ExpectedReceiptDigest = testDigest("foreign-receipt")
	revoked = mustFinalizeClosed(t, revoked)
	if err := ValidateRevoke(revoke, binding, revoked); !errors.Is(err, ErrInvalidClosure) {
		t.Fatalf("ValidateRevoke(foreign receipt) error = %v, want ErrInvalidClosure", err)
	}
}

func TestQueryResultOneHotAndUnknownIsQueryOnly(t *testing.T) {
	base := QueryResult{SchemaVersion: SchemaVersion, OperationID: "bind-query", OperationDigest: testDigest("bind-query"), State: StateBound, BindingReceiptRef: "receipt://binding/query", BindingReceiptDigest: testDigest("binding-query")}
	base = mustFinalizeQuery(t, base)
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	queryPayload, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	decodedQuery, err := DecodeQueryResult(queryPayload)
	if err != nil || !reflect.DeepEqual(decodedQuery, base) {
		t.Fatalf("DecodeQueryResult()=(%#v,%v), want exact result", decodedQuery, err)
	}
	unknown := QueryResult{SchemaVersion: SchemaVersion, OperationID: base.OperationID, OperationDigest: base.OperationDigest, State: StateOutcomeUnknown, UncertaintyReceiptRef: "receipt://unknown/query", UncertaintyDigest: testDigest("unknown-query")}
	unknown = mustFinalizeQuery(t, unknown)
	if err := unknown.Validate(); err != nil {
		t.Fatal(err)
	}
	unknown.BindingReceiptRef = base.BindingReceiptRef
	unknown = mustFinalizeQuery(t, unknown)
	if !errors.Is(unknown.Validate(), ErrInvalidQuery) {
		t.Fatal("query accepted outcome_unknown with a bound receipt branch")
	}
	if _, err := Transition(StateOutcomeUnknown, TriggerRevoke); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Transition(outcome_unknown,revoke) = %v, want ErrInvalidTransition", err)
	}
	malformedRef := base
	malformedRef.BindingReceiptRef = " receipt://binding/query"
	malformedRef = mustFinalizeQuery(t, malformedRef)
	if !errors.Is(malformedRef.Validate(), ErrInvalidQuery) {
		t.Fatal("query accepted non-canonical receipt reference")
	}
}

func TestStrictBindWireDecoderRejectsUnknownDuplicateNullTrailingAndNonCanonicalJSON(t *testing.T) {
	prepare := validPrepare(t, "wire")
	request := validBindRequest(t, validEndpoint(t, "wire"), validPrepared(t, prepare, "wire"), "wire")
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBindRequest(payload)
	if err != nil || !reflect.DeepEqual(decoded, request) {
		t.Fatalf("DecodeBindRequest()=(%#v,%v), want exact request", decoded, err)
	}
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "unknown", mutate: func(value []byte) []byte {
			result := append([]byte{}, value[:len(value)-1]...)
			return append(result, []byte(`,"unknown":true}`)...)
		}},
		{name: "duplicate", mutate: func(value []byte) []byte {
			needle := []byte(`"operation_id":"bind-wire"`)
			replacement := []byte(`"operation_id":"bind-wire","operation_id":"foreign"`)
			return bytes.Replace(value, needle, replacement, 1)
		}},
		{name: "null", mutate: func(value []byte) []byte {
			endpointJSON, marshalErr := json.Marshal(request.Endpoint)
			if marshalErr != nil {
				t.Fatalf("marshal endpoint: %v", marshalErr)
			}
			needle := append([]byte(`"endpoint":`), endpointJSON...)
			return bytes.Replace(value, needle, []byte(`"endpoint":null`), 1)
		}},
		{name: "trailing", mutate: func(value []byte) []byte { return append(append([]byte{}, value...), []byte(`{}`)...) }},
		{name: "noncanonical whitespace", mutate: func(value []byte) []byte { return append([]byte{' '}, value...) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := tc.mutate(payload)
			if _, err := DecodeBindRequest(candidate); !errors.Is(err, ErrInvalidBindRequest) {
				t.Fatalf("DecodeBindRequest() error = %v, want ErrInvalidBindRequest", err)
			}
		})
	}
}

// TestValidateSelfDigestRejectsTypedInputsWithoutPanicking 锁定错误类型 fail-closed。
func TestValidateSelfDigestRejectsTypedInputsWithoutPanicking(t *testing.T) {
	for _, value := range []any{nil, &BindRequest{}, map[string]any{}, []string{"unexpected"}} {
		if err := validateSelfDigest(ErrInvalidBindRequest, bindDigestDomain, testDigest("wrong"), value); !errors.Is(err, ErrInvalidBindRequest) {
			t.Fatalf("validateSelfDigest(%T) error = %v, want ErrInvalidBindRequest", value, err)
		}
	}
}

// TestCandidateJSONFieldRegistryLocksWireSurface 锁定 candidate DTO 的完整 JSON 字段集合。
func TestCandidateJSONFieldRegistryLocksWireSurface(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		want []string
	}{
		{"endpoint", reflect.TypeFor[Endpoint](), []string{"schema_version", "url", "instance_id", "digest"}},
		{"bind", reflect.TypeFor[BindRequest](), []string{"schema_version", "operation_id", "prepared_operation_id", "prepared_request_digest", "credential_lease_id", "credential_digest", "provider_profile_ref", "provider_profile_digest", "parent_execution_incarnation_id", "parent_fence_digest", "connection_nonce_digest", "endpoint", "provider", "request_digest"}},
		{"binding", reflect.TypeFor[BindingReceipt](), []string{"schema_version", "operation_id", "binding_request_digest", "prepared_operation_id", "prepared_request_digest", "credential_lease_id", "credential_digest", "provider_profile_ref", "provider_profile_digest", "parent_execution_incarnation_id", "parent_fence_digest", "connection_nonce_digest", "endpoint_digest", "endpoint_instance_id", "provider", "provider_thread_id", "provider_session_id", "runtime_owner_id", "runtime_generation", "process_generation", "runtime_writer_epoch", "prepared_receipt_ref", "prepared_receipt_digest", "state", "receipt_ref", "receipt_digest"}},
		{"abort", reflect.TypeFor[AbortRequest](), []string{"schema_version", "operation_id", "prepared_operation_id", "prepared_request_digest", "prepared_receipt_ref", "prepared_receipt_digest", "cleanup_evidence_ref", "cleanup_evidence_digest", "request_digest"}},
		{"revoke", reflect.TypeFor[RevokeRequest](), []string{"schema_version", "operation_id", "binding_operation_id", "expected_receipt_ref", "expected_receipt_digest", "closure_evidence_ref", "closure_evidence_digest", "request_digest"}},
		{"closed", reflect.TypeFor[ClosedReceipt](), []string{"schema_version", "operation_id", "binding_operation_id", "prepared_operation_id", "prepared_request_digest", "expected_receipt_ref", "expected_receipt_digest", "state", "operation_digest", "closure_evidence_ref", "closure_evidence_digest", "receipt_ref", "receipt_digest"}},
		{"query request", reflect.TypeFor[QueryRequest](), []string{"schema_version", "operation_id", "operation_digest"}},
		{"query result", reflect.TypeFor[QueryResult](), []string{"schema_version", "operation_id", "operation_digest", "state", "prepared_receipt_ref", "prepared_receipt_digest", "binding_receipt_ref", "binding_receipt_digest", "closed_receipt_ref", "closed_receipt_digest", "uncertainty_receipt_ref", "uncertainty_digest", "result_digest"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jsonFields(tc.typ)
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("JSON fields=%v want=%v", got, tc.want)
			}
		})
	}
}

// jsonFields 读取一个 DTO 的 exact JSON tag，拒绝 omitempty 漂移。
func jsonFields(typ reflect.Type) []string {
	fields := make([]string, 0, typ.NumField())
	for index := range typ.NumField() {
		field := typ.Field(index)
		tag := field.Tag.Get("json")
		if tag == "" || strings.Contains(tag, ",") {
			continue
		}
		fields = append(fields, tag)
	}
	return fields
}

func TestCandidateSurfaceHasNoRawBearerOperatorOrProductionWiring(t *testing.T) {
	assertCandidateFields(t)
	for _, name := range productionImports(t) {
		if strings.Contains(name, "/internal/provider") || strings.Contains(name, "/internal/store") || strings.Contains(name, "go.uber.org/fx") {
			t.Fatalf("candidate imports production owner %q", name)
		}
	}
}

// assertCandidateFields 确认 candidate wire surface 不暴露 raw bearer 或 operator principal。
func assertCandidateFields(t *testing.T) {
	t.Helper()
	types := []reflect.Type{reflect.TypeFor[Endpoint](), reflect.TypeFor[BindRequest](), reflect.TypeFor[BindingReceipt](), reflect.TypeFor[AbortRequest](), reflect.TypeFor[RevokeRequest](), reflect.TypeFor[ClosedReceipt](), reflect.TypeFor[QueryRequest](), reflect.TypeFor[QueryResult]()}
	for _, typ := range types {
		assertTypeFieldsSafe(t, typ)
	}
}

// assertTypeFieldsSafe 检查单个 DTO 的字段命名，不允许 token、Principal 或 Operator。
func assertTypeFieldsSafe(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := range typ.NumField() {
		field := typ.Field(index)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if strings.Contains(strings.ToLower(field.Name), "token") || strings.Contains(strings.ToLower(jsonName), "token") ||
			strings.EqualFold(field.Name, "Principal") || strings.EqualFold(field.Name, "Operator") {
			t.Fatalf("%s exposes forbidden authorizing field %s/%s", typ.Name(), field.Name, jsonName)
		}
	}
}

func productionImports(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(".", entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			seen[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func validEndpoint(t *testing.T, suffix string) Endpoint {
	t.Helper()
	endpoint, err := NewEndpoint("http://127.0.0.1:43123/mcp", "runtime-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func validPrepare(t *testing.T, suffix string) managedruntime.PrepareRequest {
	t.Helper()
	request := managedruntime.PrepareRequest{SchemaVersion: managedruntime.SchemaVersion, OperationID: "prepare-" + suffix, BootstrapOperationID: "bootstrap-" + suffix, BootstrapOperationDigest: testDigest("bootstrap-" + suffix), ProviderDigest: testDigest("provider-" + suffix), ExecutableDigest: testDigest("executable-" + suffix), WorkingDirectoryDigest: testDigest("cwd-" + suffix), ArgumentVectorDigest: testDigest("argv-" + suffix), EnvironmentDigest: testDigest("env-" + suffix), ExpectedRuntimeWriterEpoch: "writer-" + suffix}
	request, err := managedruntime.FinalizePrepareRequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func validPrepared(t *testing.T, request managedruntime.PrepareRequest, suffix string) managedruntime.PreparedReceipt {
	t.Helper()
	receipt := managedruntime.PreparedReceipt{SchemaVersion: managedruntime.SchemaVersion, OperationID: request.OperationID, RequestDigest: request.RequestDigest, State: managedruntime.StatePrepared, OpaqueHandleRef: "opaque://" + suffix, OpaqueHandleDigest: testDigest("opaque-" + suffix), Process: managedruntime.StableProcessIdentity{PID: 4242, ProcessStartToken: "start-" + suffix, ExecutableIdentity: "/verified/" + suffix, ExecutableDigest: request.ExecutableDigest}, ProviderThreadID: "thread-" + suffix, ProviderSessionID: "session-" + suffix, ExternalEpoch: "external-" + suffix, RuntimeOwnerID: "owner-" + suffix, RuntimeGeneration: 3, ProcessGeneration: 4, RuntimeWriterEpoch: request.ExpectedRuntimeWriterEpoch, ReceiptRef: "receipt://prepared/" + suffix}
	result, err := managedruntime.FinalizePreparedReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func validBindRequest(t *testing.T, endpoint Endpoint, prepared managedruntime.PreparedReceipt, suffix string) BindRequest {
	t.Helper()
	profileDigest, err := ComputeProviderProfileDigest(ProviderCodex, prepared)
	if err != nil {
		t.Fatal(err)
	}
	request, err := FinalizeBindRequestDigest(BindRequest{SchemaVersion: SchemaVersion, OperationID: "bind-" + suffix, PreparedOperationID: prepared.OperationID, PreparedRequestDigest: prepared.RequestDigest, CredentialLeaseID: "lease-" + suffix, CredentialDigest: testDigest("credential-" + suffix), ProviderProfileRef: "profile/" + suffix, ProviderProfileDigest: profileDigest, ParentExecutionIncarnationID: "parent-incarnation-" + suffix, ParentFenceDigest: testDigest("parent-fence-" + suffix), ConnectionNonceDigest: testDigest("nonce-" + suffix), Endpoint: endpoint, Provider: ProviderCodex})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func validBindingReceipt(t *testing.T, request BindRequest, prepared managedruntime.PreparedReceipt, suffix string) BindingReceipt {
	t.Helper()
	receipt, err := FinalizeBindingReceiptDigest(BindingReceipt{SchemaVersion: SchemaVersion, OperationID: request.OperationID, BindingRequestDigest: request.RequestDigest, PreparedOperationID: prepared.OperationID, PreparedRequestDigest: prepared.RequestDigest, CredentialLeaseID: request.CredentialLeaseID, CredentialDigest: request.CredentialDigest, ProviderProfileRef: request.ProviderProfileRef, ProviderProfileDigest: request.ProviderProfileDigest, ParentExecutionIncarnationID: request.ParentExecutionIncarnationID, ParentFenceDigest: request.ParentFenceDigest, ConnectionNonceDigest: request.ConnectionNonceDigest, EndpointDigest: request.Endpoint.Digest, EndpointInstanceID: request.Endpoint.InstanceID, Provider: request.Provider, ProviderThreadID: prepared.ProviderThreadID, ProviderSessionID: prepared.ProviderSessionID, RuntimeOwnerID: prepared.RuntimeOwnerID, RuntimeGeneration: prepared.RuntimeGeneration, ProcessGeneration: prepared.ProcessGeneration, RuntimeWriterEpoch: prepared.RuntimeWriterEpoch, PreparedReceiptRef: prepared.ReceiptRef, PreparedReceiptDigest: prepared.ReceiptDigest, State: StateBound, ReceiptRef: "receipt://binding/" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func mustFinalizeBinding(t *testing.T, receipt BindingReceipt) BindingReceipt {
	t.Helper()
	result, err := FinalizeBindingReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustFinalizeBind(t *testing.T, request BindRequest) BindRequest {
	t.Helper()
	result, err := FinalizeBindRequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustFinalizePrepared(t *testing.T, receipt managedruntime.PreparedReceipt) managedruntime.PreparedReceipt {
	t.Helper()
	result, err := managedruntime.FinalizePreparedReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustFinalizeAbort(t *testing.T, request AbortRequest) AbortRequest {
	t.Helper()
	result, err := FinalizeAbortRequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustFinalizeRevoke(t *testing.T, request RevokeRequest) RevokeRequest {
	t.Helper()
	result, err := FinalizeRevokeRequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustFinalizeClosed(t *testing.T, receipt ClosedReceipt) ClosedReceipt {
	t.Helper()
	result, err := FinalizeClosedReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustFinalizeQuery(t *testing.T, result QueryResult) QueryResult {
	t.Helper()
	finalized, err := FinalizeQueryResultDigest(result)
	if err != nil {
		t.Fatal(err)
	}
	return finalized
}

func assertJSONRoundTrip(t *testing.T, want any) {
	t.Helper()
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got := reflect.New(reflect.TypeOf(want))
	if err := json.Unmarshal(encoded, got.Interface()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Elem().Interface(), want) {
		t.Fatalf("round trip mismatch: got=%#v want=%#v", got.Elem().Interface(), want)
	}
}

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
