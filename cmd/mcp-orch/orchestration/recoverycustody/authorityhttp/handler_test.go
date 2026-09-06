package authorityhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

const testBearerToken = "opaque-authority-token-1"

type stubClient struct {
	submit func(context.Context, recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error)
	query  func(context.Context, recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error)

	mu             sync.Mutex
	submitRequests []recoverycustody.SubmissionRequest
	queryHandles   []recoverycustody.SubmissionHandle
}

// acceptingTestVerifier 仅用于 transport contract fixture；它不是生产 authority verifier。
type acceptingTestVerifier struct{}

func (acceptingTestVerifier) VerifyReceipt(context.Context, recoverycustody.SubmissionHandle, recoverycustody.ExternalReceipt) error {
	return nil
}

func TestAuthorityHTTPContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "config", run: TestNewHandlerRejectsMissingOrAmbiguousDependencies},
		{name: "credential_redaction", run: TestBearerCredentialRedactsFormattingJSONAndErrors},
		{name: "closed_methods", run: TestHandlerDispatchesClosedMethodsWithWholeDTOs},
		{name: "identity_params", run: TestHandlerIdentityRequiresOmittedParams},
		{name: "strict_protocol", run: TestHandlerRejectsExpandedOrAmbiguousJSONRPCSurface},
		{name: "transport_limits", run: TestHandlerTransportAuthenticationAndBodyLimits},
		{name: "outcome_error", run: TestHandlerPreservesDomainOutcomeAndError},
		{name: "known_error", run: TestHandlerMapsKnownDomainError},
		{name: "unknown_error", run: TestHandlerCollapsesUnknownImplementationError},
		{name: "invalid_outcome", run: TestHandlerCollapsesInvalidSuccessOutcome},
		{name: "concurrency_cancel", run: TestHandlerConcurrencyIsBoundedAndCancellationReachesBackend},
		{name: "httpclient_exact", run: TestHTTPClientIntegrationIdentitySubmitQueryExact},
		{name: "httpclient_token", run: TestHTTPClientIntegrationRejectsWrongToken},
		{name: "httpclient_identity", run: TestHTTPClientIntegrationRejectsIdentityDrift},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func (s *stubClient) Submit(ctx context.Context, request recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
	s.mu.Lock()
	s.submitRequests = append(s.submitRequests, request)
	s.mu.Unlock()
	if s.submit == nil {
		return recoverycustody.Outcome{}, errors.New("unexpected submit")
	}
	return s.submit(ctx, request)
}

func (s *stubClient) Query(ctx context.Context, handle recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
	s.mu.Lock()
	s.queryHandles = append(s.queryHandles, handle)
	s.mu.Unlock()
	if s.query == nil {
		return recoverycustody.Outcome{}, errors.New("unexpected query")
	}
	return s.query(ctx, handle)
}

func TestNewHandlerRejectsMissingOrAmbiguousDependencies(t *testing.T) {
	identity := testIdentity()
	backend := &stubClient{}
	credential := testBearerCredential(t)
	var typedNil *stubClient
	tests := []struct {
		name   string
		config Config
	}{
		{name: "missing_backend", config: Config{Identity: identity, BearerToken: credential, MaxBodyBytes: 1024, MaxConcurrent: 1}},
		{name: "typed_nil_backend", config: Config{Backend: typedNil, Identity: identity, BearerToken: credential, MaxBodyBytes: 1024, MaxConcurrent: 1}},
		{name: "unverified_backend", config: Config{Backend: backend, Identity: identity, BearerToken: credential, MaxBodyBytes: 1024, MaxConcurrent: 1}},
		{name: "invalid_identity", config: Config{Backend: backend, BearerToken: credential, MaxBodyBytes: 1024, MaxConcurrent: 1}},
		{name: "missing_token", config: Config{Backend: backend, Identity: identity, MaxBodyBytes: 1024, MaxConcurrent: 1}},
		{name: "noncanonical_token", config: Config{Backend: backend, Identity: identity, BearerToken: BearerCredential{value: " token "}, MaxBodyBytes: 1024, MaxConcurrent: 1}},
		{name: "missing_body_limit", config: Config{Backend: backend, Identity: identity, BearerToken: credential, MaxConcurrent: 1}},
		{name: "missing_concurrency_limit", config: Config{Backend: backend, Identity: identity, BearerToken: credential, MaxBodyBytes: 1024}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if handler, err := NewHandler(test.config); err == nil || handler != nil {
				t.Fatalf("NewHandler() = (%T, %v), want nil/error", handler, err)
			}
		})
	}
}

func TestBearerCredentialRedactsFormattingJSONAndErrors(t *testing.T) {
	credential := testBearerCredential(t)
	config := Config{BearerToken: credential}
	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal(Config) error = %v", err)
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("json.Marshal(BearerCredential) error = %v", err)
	}
	views := []string{
		credential.String(), credential.GoString(), fmt.Sprint(credential), fmt.Sprintf("%+v", credential),
		fmt.Sprintf("%#v", credential), fmt.Sprintf("%+v", config), fmt.Sprintf("%#v", config),
		string(configJSON), string(credentialJSON),
	}
	for _, view := range views {
		if strings.Contains(view, testBearerToken) || !strings.Contains(view, redactedCredential) {
			t.Fatalf("credential view is not redacted: %q", view)
		}
	}
	invalid := " token=unsafe "
	if _, err := NewBearerCredential(invalid); err == nil || strings.Contains(err.Error(), invalid) || strings.Contains(err.Error(), "token=unsafe") {
		t.Fatalf("NewBearerCredential() error leaked input: %v", err)
	}
}

func TestHandlerDispatchesClosedMethodsWithWholeDTOs(t *testing.T) {
	request := testSubmissionRequest(t)
	handle := testSubmissionHandle(t, request)
	assertProducerFieldsPopulated(t, testIdentity())
	assertProducerFieldsPopulated(t, request)
	assertProducerFieldsPopulated(t, handle)
	pending := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusPending}
	backend := &stubClient{
		submit: func(context.Context, recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
			return pending, nil
		},
		query: func(context.Context, recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
			return pending, nil
		},
	}
	handler := newTestHandler(t, backend, 1<<20, 2)

	identityResponse := serveRPC(t, handler, canonicalRequest(t, 1, rpcwire.MethodIdentity, nil))
	identityResult := decodeResult(t, identityResponse, rpcwire.MethodIdentity)
	if !reflect.DeepEqual(identityResult.Authority, testIdentity()) {
		t.Fatalf("identity = %+v, want %+v", identityResult.Authority, testIdentity())
	}

	submitParams := mustCanonical(t, rpcwire.SubmitParams{SchemaVersion: rpcwire.SchemaVersion, Request: request})
	submitResult := decodeResult(t, serveRPC(t, handler, canonicalRequest(t, 2, rpcwire.MethodSubmit, submitParams)), rpcwire.MethodSubmit)
	if submitResult.Outcome == nil || !reflect.DeepEqual(*submitResult.Outcome, pending) {
		t.Fatalf("submit outcome = %+v, want %+v", submitResult.Outcome, pending)
	}

	queryParams := mustCanonical(t, rpcwire.QueryParams{SchemaVersion: rpcwire.SchemaVersion, Handle: handle})
	queryResult := decodeResult(t, serveRPC(t, handler, canonicalRequest(t, 3, rpcwire.MethodQuery, queryParams)), rpcwire.MethodQuery)
	if queryResult.Outcome == nil || !reflect.DeepEqual(*queryResult.Outcome, pending) {
		t.Fatalf("query outcome = %+v, want %+v", queryResult.Outcome, pending)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.submitRequests) != 1 || !reflect.DeepEqual(backend.submitRequests[0], request) {
		t.Fatalf("submit requests = %+v, want exact request", backend.submitRequests)
	}
	if len(backend.queryHandles) != 1 || !reflect.DeepEqual(backend.queryHandles[0], handle) {
		t.Fatalf("query handles = %+v, want exact handle", backend.queryHandles)
	}
}

func TestHandlerIdentityRequiresOmittedParams(t *testing.T) {
	handler := newTestHandler(t, &stubClient{}, 1<<20, 1)
	withoutParams := []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity"}`)
	result := decodeResult(t, serveRPC(t, handler, withoutParams), rpcwire.MethodIdentity)
	if !reflect.DeepEqual(result.Authority, testIdentity()) {
		t.Fatalf("identity = %+v, want %+v", result.Authority, testIdentity())
	}

	tests := []struct {
		name string
		body []byte
		code jrpc2.Code
	}{
		{name: "object", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity","params":{}}`), code: jrpc2.InvalidParams},
		{name: "null", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity","params":null}`), code: jrpc2.InvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errorValue := decodeRPCError(t, serveRPC(t, handler, test.body))
			if errorValue.Code != test.code {
				t.Fatalf("error code = %d, want %d", errorValue.Code, test.code)
			}
		})
	}
}

func TestHandlerRejectsExpandedOrAmbiguousJSONRPCSurface(t *testing.T) {
	backend := &stubClient{}
	handler := newTestHandler(t, backend, 1<<20, 1)
	request := testSubmissionRequest(t)
	params := mustCanonical(t, rpcwire.SubmitParams{SchemaVersion: rpcwire.SchemaVersion, Request: request})
	unknownParam := bytes.Replace(params, []byte(`"request":{`), []byte(`"extra":true,"request":{`), 1)
	duplicateNested := bytes.Replace(params, []byte(`"operation_id":"operation-1"`), []byte(`"operation_id":"operation-1","operation_id":"operation-1"`), 1)
	tests := []struct {
		name string
		body []byte
		code jrpc2.Code
	}{
		{name: "malformed", body: []byte(`{"jsonrpc":`), code: jrpc2.ParseError},
		{name: "batch", body: []byte(`[]`), code: jrpc2.InvalidRequest},
		{name: "notification", body: []byte(`{"jsonrpc":"2.0","method":"recovery-custody/identity","params":{}}`), code: jrpc2.InvalidRequest},
		{name: "array_params", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity","params":[]}`), code: jrpc2.InvalidParams},
		{name: "identity_object_params", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity","params":{}}`), code: jrpc2.InvalidParams},
		{name: "duplicate_top_field", body: []byte(`{"jsonrpc":"2.0","id":1,"id":2,"method":"recovery-custody/identity","params":{}}`), code: jrpc2.InvalidRequest},
		{name: "unknown_top_field", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity","params":{},"extra":true}`), code: jrpc2.InvalidRequest},
		{name: "null_id", body: []byte(`{"jsonrpc":"2.0","id":null,"method":"recovery-custody/identity","params":{}}`), code: jrpc2.InvalidRequest},
		{name: "nested_null", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity","params":{"extra":null}}`), code: jrpc2.InvalidRequest},
		{name: "unknown_param", body: canonicalRequest(t, 1, rpcwire.MethodSubmit, unknownParam), code: jrpc2.InvalidParams},
		{name: "duplicate_nested", body: canonicalRequest(t, 1, rpcwire.MethodSubmit, duplicateNested), code: jrpc2.InvalidRequest},
		{name: "noncanonical_whitespace", body: []byte(` {"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity","params":{}}`), code: jrpc2.InvalidRequest},
		{name: "noncanonical_order", body: []byte(`{"id":1,"jsonrpc":"2.0","method":"recovery-custody/identity","params":{}}`), code: jrpc2.InvalidRequest},
		{name: "noncanonical_number", body: []byte(`{"jsonrpc":"2.0","id":1.0,"method":"recovery-custody/identity","params":{}}`), code: jrpc2.InvalidRequest},
		{name: "unknown_method", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/resolve","params":{}}`), code: jrpc2.MethodNotFound},
		{name: "mcp_initialize", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`), code: jrpc2.MethodNotFound},
		{name: "mcp_tools", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`), code: jrpc2.MethodNotFound},
		{name: "resume", body: []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/resume","params":{}}`), code: jrpc2.MethodNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := serveRPC(t, handler, test.body)
			errorValue := decodeRPCError(t, response)
			if errorValue.Code != test.code {
				t.Fatalf("error code = %d, want %d; body=%s", errorValue.Code, test.code, response.Body.String())
			}
			assertNoInternalDetails(t, response.Body.String())
		})
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.submitRequests) != 0 || len(backend.queryHandles) != 0 {
		t.Fatalf("rejected methods reached backend: submit=%d query=%d", len(backend.submitRequests), len(backend.queryHandles))
	}
}

func TestHandlerTransportAuthenticationAndBodyLimits(t *testing.T) {
	handler := newTestHandler(t, &stubClient{}, 64, 1)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"recovery-custody/identity"}`)
	tests := []struct {
		name   string
		alter  func(*http.Request)
		status int
	}{
		{name: "non_post", alter: func(request *http.Request) { request.Method = http.MethodGet }, status: http.StatusMethodNotAllowed},
		{name: "missing_authorization", alter: func(request *http.Request) { request.Header.Del("Authorization") }, status: http.StatusUnauthorized},
		{name: "wrong_authorization", alter: func(request *http.Request) { request.Header.Set("Authorization", "Bearer wrong") }, status: http.StatusUnauthorized},
		{name: "wrong_scheme", alter: func(request *http.Request) { request.Header.Set("Authorization", "Basic opaque-authority-token-1") }, status: http.StatusUnauthorized},
		{name: "duplicate_authorization", alter: func(request *http.Request) { request.Header.Add("Authorization", "Bearer forged") }, status: http.StatusBadRequest},
		{name: "missing_content_type", alter: func(request *http.Request) { request.Header.Del("Content-Type") }, status: http.StatusUnsupportedMediaType},
		{name: "non_json_content_type", alter: func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") }, status: http.StatusUnsupportedMediaType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := authorizedRequest(body)
			test.alter(request)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%q", response.Code, test.status, response.Body.String())
			}
		})
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authorizedRequest(bytes.Repeat([]byte("x"), 65)))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandlerPreservesDomainOutcomeAndError(t *testing.T) {
	request := testSubmissionRequest(t)
	params := mustCanonical(t, rpcwire.SubmitParams{SchemaVersion: rpcwire.SchemaVersion, Request: request})
	body := canonicalRequest(t, 1, rpcwire.MethodSubmit, params)
	backend := &stubClient{submit: func(ctx context.Context, request recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
		return recoverycustody.NewUnavailableClient().Submit(ctx, request)
	}}
	result := decodeResult(t, serveRPC(t, newTestHandler(t, backend, 1<<20, 1), body), rpcwire.MethodSubmit)
	if result.Outcome == nil || result.Outcome.Status != recoverycustody.StatusIndeterminate || result.Failure == nil || result.Failure.Code != rpcwire.FailureAuthorityUnavailable {
		t.Fatalf("result = %+v, want indeterminate outcome plus authority_unavailable", result)
	}
}

func TestHandlerMapsKnownDomainError(t *testing.T) {
	body := testSubmitRPCBody(t)
	backend := &stubClient{submit: func(context.Context, recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
		return recoverycustody.Outcome{}, fmt.Errorf("wrapped: %w", recoverycustody.ErrCustodyConflict)
	}}
	result := decodeResult(t, serveRPC(t, newTestHandler(t, backend, 1<<20, 1), body), rpcwire.MethodSubmit)
	if result.Outcome != nil || result.Failure == nil || result.Failure.Code != rpcwire.FailureOperationConflict {
		t.Fatalf("result = %+v, want operation_conflict only", result)
	}
}

func TestHandlerCollapsesUnknownImplementationError(t *testing.T) {
	backend := &stubClient{submit: func(context.Context, recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
		return recoverycustody.Outcome{}, errors.New("open /secret/authority.db: token=unsafe")
	}}
	response := serveRPC(t, newTestHandler(t, backend, 1<<20, 1), testSubmitRPCBody(t))
	result := decodeResult(t, response, rpcwire.MethodSubmit)
	if result.Outcome != nil || result.Failure == nil || result.Failure.Code != rpcwire.FailureAuthorityUnavailable {
		t.Fatalf("result = %+v, want authority_unavailable only", result)
	}
	assertNoInternalDetails(t, response.Body.String())
}

func TestHandlerCollapsesInvalidSuccessOutcome(t *testing.T) {
	request := testSubmissionRequest(t)
	handle := testSubmissionHandle(t, request)
	backend := &stubClient{submit: func(context.Context, recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
		return recoverycustody.Outcome{Handle: handle, Status: "implementation-secret"}, nil
	}}
	response := serveRPC(t, newTestHandler(t, backend, 1<<20, 1), testSubmitRPCBodyForRequest(t, request))
	result := decodeResult(t, response, rpcwire.MethodSubmit)
	if result.Outcome != nil || result.Failure == nil || result.Failure.Code != rpcwire.FailureAuthorityUnavailable {
		t.Fatalf("result = %+v, want authority_unavailable only", result)
	}
	assertNoInternalDetails(t, response.Body.String())
}

func TestHandlerConcurrencyIsBoundedAndCancellationReachesBackend(t *testing.T) {
	request := testSubmissionRequest(t)
	params := mustCanonical(t, rpcwire.SubmitParams{SchemaVersion: rpcwire.SchemaVersion, Request: request})
	body := canonicalRequest(t, 1, rpcwire.MethodSubmit, params)
	started := make(chan struct{})
	finished := make(chan struct{})
	backend := &stubClient{submit: func(ctx context.Context, _ recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return recoverycustody.Outcome{}, ctx.Err()
	}}
	handler := newTestHandler(t, backend, 1<<20, 1)

	ctx, cancel := context.WithCancel(context.Background())
	firstRequest := authorizedRequest(body).WithContext(ctx)
	firstResponse := httptest.NewRecorder()
	served := make(chan struct{})
	runtimesafe.SafeGo(t.Context(), nil, "authorityhttp.test.cancelled-request", func(context.Context) {
		handler.ServeHTTP(firstResponse, firstRequest)
		close(served)
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("backend did not start")
	}
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, authorizedRequest([]byte(`{"jsonrpc":"2.0","id":2,"method":"recovery-custody/identity"}`)))
	if secondResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want %d", secondResponse.Code, http.StatusServiceUnavailable)
	}

	cancel()
	for name, channel := range map[string]<-chan struct{}{"backend": finished, "handler": served} {
		select {
		case <-channel:
		case <-time.After(time.Second):
			t.Fatalf("%s did not exit after cancellation", name)
		}
	}
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("cancelled response status = %d, want JSON-RPC result", firstResponse.Code)
	}
}

func newTestHandler(t *testing.T, backend recoverycustody.Client, maxBodyBytes int64, maxConcurrent int) http.Handler {
	t.Helper()
	verifiedBackend, err := recoverycustody.NewVerifiedClient(backend, acceptingTestVerifier{})
	if err != nil {
		t.Fatalf("NewVerifiedClient() error = %v", err)
	}
	handler, err := NewHandler(Config{
		Backend: verifiedBackend, Identity: testIdentity(), BearerToken: testBearerCredential(t),
		MaxBodyBytes: maxBodyBytes, MaxConcurrent: maxConcurrent,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func authorizedRequest(body []byte) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://authority.invalid/rpc", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testBearerToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func testBearerCredential(t *testing.T) BearerCredential {
	t.Helper()
	credential, err := NewBearerCredential(testBearerToken)
	if err != nil {
		t.Fatalf("NewBearerCredential() error = %v", err)
	}
	return credential
}

func serveRPC(t *testing.T, handler http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authorizedRequest(body))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	return response
}

func canonicalRequest(t *testing.T, id int64, method rpcwire.Method, params []byte) []byte {
	t.Helper()
	payload, err := json.Marshal(requestEnvelope{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprintf("%d", id)), Method: method, Params: params})
	if err != nil {
		t.Fatalf("marshal request error = %v", err)
	}
	return payload
}

func testSubmitRPCBody(t *testing.T) []byte {
	t.Helper()
	return testSubmitRPCBodyForRequest(t, testSubmissionRequest(t))
}

func testSubmitRPCBodyForRequest(t *testing.T, request recoverycustody.SubmissionRequest) []byte {
	t.Helper()
	params := mustCanonical(t, rpcwire.SubmitParams{SchemaVersion: rpcwire.SchemaVersion, Request: request})
	return canonicalRequest(t, 1, rpcwire.MethodSubmit, params)
}

func mustCanonical(t *testing.T, value interface{ Validate() error }) []byte {
	t.Helper()
	payload, err := rpcwire.MarshalCanonical(value)
	if err != nil {
		t.Fatalf("MarshalCanonical() error = %v", err)
	}
	return payload
}

func decodeResult(t *testing.T, response *httptest.ResponseRecorder, method rpcwire.Method) rpcwire.Result {
	t.Helper()
	var envelope responseEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response error = %v; body=%s", err, response.Body.String())
	}
	assertCanonicalResponse(t, response, envelope)
	if envelope.Error != nil || len(envelope.Result) == 0 {
		t.Fatalf("response = %+v, want result", envelope)
	}
	result, err := rpcwire.DecodeResultForMethod(envelope.Result, method)
	if err != nil {
		t.Fatalf("DecodeResultForMethod() error = %v; result=%s", err, envelope.Result)
	}
	return result
}

func decodeRPCError(t *testing.T, response *httptest.ResponseRecorder) *jrpc2.Error {
	t.Helper()
	var envelope responseEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response error = %v; body=%s", err, response.Body.String())
	}
	assertCanonicalResponse(t, response, envelope)
	if envelope.Error == nil || len(envelope.Result) != 0 {
		t.Fatalf("response = %+v, want error", envelope)
	}
	return envelope.Error
}

func assertCanonicalResponse(t *testing.T, response *httptest.ResponseRecorder, envelope responseEnvelope) {
	t.Helper()
	canonical, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal response error = %v", err)
	}
	if !bytes.Equal(canonical, response.Body.Bytes()) {
		t.Fatalf("response is not canonical: %s", response.Body.String())
	}
}

func testIdentity() rpcwire.AuthorityIdentity {
	return rpcwire.AuthorityIdentity{
		InstanceID: "authority-instance-1", AuthorityID: "authority-1", AuthorityClusterID: "cluster-1",
		AuthorityProfile: "external-quorum-v1", AuthorityGeneration: 3, AuthorityProfileGeneration: 5,
		AuthorityProfileDigest: testDigest(40), SecurityDomainID: "security-domain-1",
	}
}

func testSubmissionRequest(t *testing.T) recoverycustody.SubmissionRequest {
	t.Helper()
	request := recoverycustody.SubmissionRequest{
		SchemaVersion: 1, OperationID: "operation-1", SecurityDomainID: "security-domain-1",
		AuthorityProfile: "external-quorum-v1", CommandID: "command-1", CommandVersion: 1,
		CommandProfileDigest: testDigest(1), AuthorizationDigest: testDigest(2), IdentityFenceDigest: testDigest(3),
		ContentSnapshotDigest: testDigest(4), ResourceSnapshotDigest: testDigest(5), BatchDigest: testDigest(6),
		IdempotencyKeyDigest: testDigest(7),
	}
	finalized, err := recoverycustody.FinalizeRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeRequestDigest() error = %v", err)
	}
	return finalized
}

func testSubmissionHandle(t *testing.T, request recoverycustody.SubmissionRequest) recoverycustody.SubmissionHandle {
	t.Helper()
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}
	return handle
}

func testDigest(seed int) string { return fmt.Sprintf("%064x", seed) }

func assertNoInternalDetails(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"/secret/", "token=", testBearerToken, "implementation-secret", "unexpected submit", "unexpected query"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
}

// assertProducerFieldsPopulated 动态枚举生产 DTO 字段，新增字段未进入端到端 fixture 时立即失败。
func assertProducerFieldsPopulated(t *testing.T, producer any) {
	t.Helper()
	value := reflect.ValueOf(producer)
	typeOfProducer := value.Type()
	for index := range value.NumField() {
		field := typeOfProducer.Field(index)
		if value.Field(index).IsZero() {
			t.Fatalf("producer %s field %s is zero; whole-DTO field guard fixture must cover it", typeOfProducer, field.Name)
		}
	}
}

var _ recoverycustody.Client = (*stubClient)(nil)
