package httpclient

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
)

type verifierFixture struct {
	err   error
	calls atomic.Int64
}

func (v *verifierFixture) VerifyReceipt(
	context.Context,
	recoverycustody.SubmissionHandle,
	recoverycustody.ExternalReceipt,
) error {
	v.calls.Add(1)
	return v.err
}

func TestHTTPClientContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"exact_submit_query", TestClientSubmitAndQueryTransmitExactWireFields},
		{"identity", TestClientProbeIdentityAndRejectsDrift},
		{"close_lifecycle", TestClientCloseIsIdempotentAndRejectsNewCalls},
		{"unavailable_no_retry", TestClientUnavailablePathsKeepOriginalHandleAndNeverRetry},
		{"redirect", TestClientRejectsRedirectWithoutSendingCredentialToTarget},
		{"limits_headers", TestClientRejectsOversizeBodyHeadersAndContentType},
		{"strict_envelope", TestClientRejectsAmbiguousUnknownAndMismatchedEnvelopes},
		{"identity_handle_drift", TestClientRejectsIdentityAndHandleDriftWithOriginalHandle},
		{"failure_mapping", TestClientMapsClosedFailureCodes},
		{"terminal_verifier", TestClientValidatesEveryTerminalReceiptAndRejectsVerifierFailure},
		{"credential_redaction", TestClientNeverLeaksCredentialThroughFormattingJSONOrErrors},
		{"explicit_config", TestNewClientValidatesEachExplicitConfigFieldAndDisablesProxy},
		{"config_field_guard", TestConfigFieldsAreDynamicallyCoveredByValidationMutations},
		{"request_limit", TestClientRejectsRequestOverLimitBeforeNetwork},
		{"import_boundary", TestHTTPClientPackageHasNoForbiddenRuntimeDependencies},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func TestClientSubmitAndQueryTransmitExactWireFields(t *testing.T) {
	identity := testIdentity()
	credential := testCredential(t)
	request := testRequest(t)
	handle := testHandle(t, request)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		calls.Add(1)
		serveExactWireRequest(t, writer, incoming, credential.value, identity, request, handle)
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL, credential, identity, new(verifierFixture), 64<<10, 64<<10)
	submitted, err := client.Submit(t.Context(), request)
	if err != nil || submitted.Handle != handle || submitted.Status != recoverycustody.StatusPending {
		t.Fatalf("Submit() = (%+v, %v)", submitted, err)
	}
	queried, err := client.Query(t.Context(), handle)
	if err != nil || queried.Handle != handle || queried.Status != recoverycustody.StatusIndeterminate {
		t.Fatalf("Query() = (%+v, %v)", queried, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("HTTP calls = %d, want 2", calls.Load())
	}
}

func TestClientProbeIdentityAndRejectsDrift(t *testing.T) {
	identity := testIdentity()
	var drift atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		envelope := readRequestEnvelope(t, incoming)
		if envelope.Method != rpcwire.MethodIdentity || len(envelope.Params) != 0 {
			t.Errorf("identity request = %+v", envelope)
		}
		got := identity
		if drift.Load() {
			got.InstanceID = "authority-instance-drifted"
		}
		writeResult(t, writer, envelope.ID, rpcwire.Result{SchemaVersion: rpcwire.SchemaVersion, Authority: got})
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL, testCredential(t), identity, new(verifierFixture), 64<<10, 64<<10)
	if err := client.ProbeIdentity(t.Context()); err != nil {
		t.Fatalf("ProbeIdentity() error = %v", err)
	}
	drift.Store(true)
	if err := client.ProbeIdentity(t.Context()); !errors.Is(err, recoverycustody.ErrCustodyUnavailable) {
		t.Fatalf("drifted ProbeIdentity() error = %v", err)
	}
}

func TestClientCloseIsIdempotentAndRejectsNewCalls(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:1", testCredential(t), testIdentity(), new(verifierFixture), 64<<10, 64<<10)
	var lifecycle Lifecycle = client
	if err := lifecycle.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := lifecycle.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	request := testRequest(t)
	handle := testHandle(t, request)
	if _, err := client.Submit(t.Context(), request); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit() after Close() error = %v, want ErrClosed", err)
	}
	if _, err := client.Query(t.Context(), handle); !errors.Is(err, ErrClosed) {
		t.Fatalf("Query() after Close() error = %v, want ErrClosed", err)
	}
	if err := client.ProbeIdentity(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("ProbeIdentity() after Close() error = %v, want ErrClosed", err)
	}
}

func TestClientUnavailablePathsKeepOriginalHandleAndNeverRetry(t *testing.T) {
	request := testRequest(t)
	handle := testHandle(t, request)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		timeout time.Duration
	}{
		{
			name: "status_500",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusInternalServerError)
			},
		},
		{
			name: "eof",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				hijacker, ok := writer.(http.Hijacker)
				if !ok {
					t.Error("ResponseWriter does not support Hijacker")
					return
				}
				connection, _, err := hijacker.Hijack()
				if err != nil {
					t.Errorf("Hijack() error = %v", err)
					return
				}
				_ = connection.Close()
			},
		},
		{
			name: "timeout",
			handler: func(_ http.ResponseWriter, incoming *http.Request) {
				select {
				case <-incoming.Context().Done():
				case <-time.After(250 * time.Millisecond):
				}
			},
			timeout: 25 * time.Millisecond,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
				calls.Add(1)
				test.handler(writer, incoming)
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server.URL, testCredential(t), testIdentity(), new(verifierFixture), 64<<10, 64<<10)
			ctx := t.Context()
			if test.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.timeout)
				t.Cleanup(cancel)
			}
			outcome, err := client.Submit(ctx, request)
			assertUnavailableOutcome(t, outcome, err, handle)
			if calls.Load() != 1 {
				t.Fatalf("HTTP calls = %d, want exactly 1", calls.Load())
			}
		})
	}
}

func TestClientRejectsRedirectWithoutSendingCredentialToTarget(t *testing.T) {
	var sourceCalls atomic.Int64
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalls.Add(1)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		sourceCalls.Add(1)
		http.Redirect(writer, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	request := testRequest(t)
	handle := testHandle(t, request)
	client := newTestClient(t, source.URL, testCredential(t), testIdentity(), new(verifierFixture), 64<<10, 64<<10)
	outcome, err := client.Submit(t.Context(), request)
	assertUnavailableOutcome(t, outcome, err, handle)
	if sourceCalls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatalf("redirect calls = source:%d target:%d, want 1/0", sourceCalls.Load(), targetCalls.Load())
	}
}

func TestClientRejectsOversizeBodyHeadersAndContentType(t *testing.T) {
	request := testRequest(t)
	handle := testHandle(t, request)
	identity := testIdentity()
	tests := []struct {
		name    string
		maxResp int64
		write   func(*testing.T, http.ResponseWriter, requestEnvelope)
	}{
		{
			name:    "body",
			maxResp: 128,
			write: func(_ *testing.T, writer http.ResponseWriter, _ requestEnvelope) {
				writer.Header().Set("Content-Type", mediaTypeJSON)
				_, _ = writer.Write([]byte(strings.Repeat("x", 129)))
			},
		},
		{
			name:    "headers",
			maxResp: 512,
			write: func(_ *testing.T, writer http.ResponseWriter, _ requestEnvelope) {
				writer.Header().Set("Content-Type", mediaTypeJSON)
				writer.Header().Set("X-Oversize", strings.Repeat("x", 2048))
				_, _ = writer.Write([]byte(`{}`))
			},
		},
		{
			name:    "missing_content_type",
			maxResp: 64 << 10,
			write: func(_ *testing.T, writer http.ResponseWriter, _ requestEnvelope) {
				writer.Header()["Content-Type"] = nil
				_, _ = writer.Write([]byte(`{}`))
			},
		},
		{
			name:    "parameterized_content_type",
			maxResp: 64 << 10,
			write: func(_ *testing.T, writer http.ResponseWriter, _ requestEnvelope) {
				writer.Header().Set("Content-Type", "application/json; charset=utf-8")
				_, _ = writer.Write([]byte(`{}`))
			},
		},
		{
			name:    "duplicate_content_type",
			maxResp: 64 << 10,
			write: func(_ *testing.T, writer http.ResponseWriter, _ requestEnvelope) {
				writer.Header().Add("Content-Type", mediaTypeJSON)
				writer.Header().Add("Content-Type", mediaTypeJSON)
				_, _ = writer.Write([]byte(`{}`))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
				test.write(t, writer, readRequestEnvelope(t, incoming))
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server.URL, testCredential(t), identity, new(verifierFixture), 64<<10, test.maxResp)
			outcome, err := client.Submit(t.Context(), request)
			assertUnavailableOutcome(t, outcome, err, handle)
		})
	}
}

func TestClientRejectsAmbiguousUnknownAndMismatchedEnvelopes(t *testing.T) {
	request := testRequest(t)
	handle := testHandle(t, request)
	identity := testIdentity()
	pending := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusPending}
	result := canonicalResult(t, rpcwire.Result{
		SchemaVersion: rpcwire.SchemaVersion, Authority: identity, Outcome: &pending,
	})
	validID := requestID(rpcwire.MethodSubmit, handle)
	valid := responseJSON(t, validID, result)
	identityOnly := canonicalResult(t, rpcwire.Result{SchemaVersion: rpcwire.SchemaVersion, Authority: identity})
	both := []byte(`{"jsonrpc":"2.0","id":"` + validID + `","result":` + string(result) +
		`,"error":{"code":-32603,"message":"failed"}}`)
	tests := []struct {
		name string
		body []byte
	}{
		{"duplicate", bytes.Replace(valid, []byte(`"jsonrpc":"2.0"`), []byte(`"jsonrpc":"2.0","jsonrpc":"2.0"`), 1)},
		{"nested_duplicate", bytes.Replace(valid, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)},
		{"unknown", bytes.Replace(valid, []byte(`{"jsonrpc"`), []byte(`{"unknown":true,"jsonrpc"`), 1)},
		{"wrong_id", responseJSON(t, "wrong-id", result)},
		{"numeric_id", bytes.Replace(valid, []byte(`"id":"`+validID+`"`), []byte(`"id":1`), 1)},
		{"wrong_version", bytes.Replace(valid, []byte(`"jsonrpc":"2.0"`), []byte(`"jsonrpc":"1.0"`), 1)},
		{"both_result_and_error", both},
		{"method_result_mismatch", responseJSON(t, validID, identityOnly)},
		{"trailing", append(append([]byte{}, valid...), []byte(` {}`)...)},
		{"empty", nil},
		{"explicit_null", bytes.Replace(valid, []byte(`"result":{`), []byte(`"result":null,"discarded":{`), 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := rawResponseServer(t, http.StatusOK, mediaTypeJSON, test.body)
			client := newTestClient(t, server.URL, testCredential(t), identity, new(verifierFixture), 64<<10, 64<<10)
			outcome, err := client.Submit(t.Context(), request)
			assertUnavailableOutcome(t, outcome, err, handle)
		})
	}
}

func TestClientRejectsIdentityAndHandleDriftWithOriginalHandle(t *testing.T) {
	request := testRequest(t)
	handle := testHandle(t, request)
	identity := testIdentity()
	driftedHandle := handle
	driftedHandle.OperationID = "operation-drifted"
	digest, err := recoverycustody.ComputeHandleDigest(driftedHandle)
	if err != nil {
		t.Fatal(err)
	}
	driftedHandle.HandleDigest = digest
	tests := []struct {
		name      string
		authority rpcwire.AuthorityIdentity
		handle    recoverycustody.SubmissionHandle
	}{
		{"identity", func() rpcwire.AuthorityIdentity { got := identity; got.InstanceID += "-drifted"; return got }(), handle},
		{"handle", identity, driftedHandle},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
				envelope := readRequestEnvelope(t, incoming)
				outcome := recoverycustody.Outcome{Handle: test.handle, Status: recoverycustody.StatusPending}
				writeResult(t, writer, envelope.ID, rpcwire.Result{
					SchemaVersion: rpcwire.SchemaVersion, Authority: test.authority, Outcome: &outcome,
				})
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server.URL, testCredential(t), identity, new(verifierFixture), 64<<10, 64<<10)
			outcome, err := client.Submit(t.Context(), request)
			assertUnavailableOutcome(t, outcome, err, handle)
		})
	}
}

func TestClientMapsClosedFailureCodes(t *testing.T) {
	tests := []struct {
		code rpcwire.FailureCode
		want error
	}{
		{rpcwire.FailureInvalidRequest, recoverycustody.ErrInvalidRequest},
		{rpcwire.FailureInvalidHandle, recoverycustody.ErrInvalidHandle},
		{rpcwire.FailureOperationConflict, recoverycustody.ErrCustodyConflict},
		{rpcwire.FailureReceiptRejected, recoverycustody.ErrReceiptRejected},
		{rpcwire.FailureResolutionConflict, recoverycustody.ErrResolutionConflict},
		{rpcwire.FailureOperationNotFound, recoverycustody.ErrOperationNotFound},
		{rpcwire.FailureAuthorityUnavailable, recoverycustody.ErrCustodyUnavailable},
		{rpcwire.FailureAuthorityReadOnly, recoverycustody.ErrCustodyUnavailable},
		{rpcwire.FailureLeaseRejected, recoverycustody.ErrCustodyUnavailable},
		{rpcwire.FailureCommitOutcomeUnknown, recoverycustody.ErrCustodyUnavailable},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			request := testRequest(t)
			handle := testHandle(t, request)
			identity := testIdentity()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
				envelope := readRequestEnvelope(t, incoming)
				failure := rpcwire.Failure{Code: test.code}
				writeResult(t, writer, envelope.ID, rpcwire.Result{
					SchemaVersion: rpcwire.SchemaVersion, Authority: identity, Failure: &failure,
				})
			}))
			t.Cleanup(server.Close)
			client := newTestClient(t, server.URL, testCredential(t), identity, new(verifierFixture), 64<<10, 64<<10)
			outcome, err := client.Submit(t.Context(), request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Submit() error = %v, want %v", err, test.want)
			}
			if outcome.Handle != handle || outcome.Status != recoverycustody.StatusIndeterminate {
				t.Fatalf("failure outcome = %+v", outcome)
			}
		})
	}
}

func TestClientValidatesEveryTerminalReceiptAndRejectsVerifierFailure(t *testing.T) {
	request := testRequest(t)
	handle := testHandle(t, request)
	identity := testIdentity()
	terminal := testTerminalOutcome(t, identity, handle)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		envelope := readRequestEnvelope(t, incoming)
		writeResult(t, writer, envelope.ID, rpcwire.Result{
			SchemaVersion: rpcwire.SchemaVersion, Authority: identity, Outcome: &terminal,
		})
	}))
	t.Cleanup(server.Close)

	accepting := new(verifierFixture)
	client := newTestClient(t, server.URL, testCredential(t), identity, accepting, 64<<10, 64<<10)
	for index := 0; index < 2; index++ {
		outcome, err := client.Query(t.Context(), handle)
		if err != nil || outcome.Status != recoverycustody.StatusAvailable {
			t.Fatalf("Query(%d) = (%+v, %v)", index, outcome, err)
		}
	}
	if accepting.calls.Load() != 2 {
		t.Fatalf("verifier calls = %d, want 2", accepting.calls.Load())
	}

	rejecting := &verifierFixture{err: errors.New("verifier-internal secret-marker")}
	client = newTestClient(t, server.URL, testCredential(t), identity, rejecting, 64<<10, 64<<10)
	outcome, err := client.Query(t.Context(), handle)
	assertUnavailableOutcome(t, outcome, err, handle)
	if strings.Contains(err.Error(), "secret-marker") {
		t.Fatalf("verifier detail leaked in error: %v", err)
	}
}

func TestClientNeverLeaksCredentialThroughFormattingJSONOrErrors(t *testing.T) {
	secret := "opaque-client-secret-marker"
	credential, err := NewBearerCredential(secret)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Credential: credential}
	values := []string{
		fmt.Sprintf("%v", credential), fmt.Sprintf("%+v", credential), fmt.Sprintf("%#v", credential),
		fmt.Sprintf("%v", config), fmt.Sprintf("%+v", config), fmt.Sprintf("%#v", config),
	}
	encodedCredential, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	encodedConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	values = append(values, string(encodedCredential), string(encodedConfig))
	if _, err := NewBearerCredential(secret + "\nheader-injection"); err != nil {
		values = append(values, err.Error())
	}
	assertStringsOmitSecret(t, secret, values)

	request := testRequest(t)
	handle := testHandle(t, request)
	errorBody := []byte(`{"jsonrpc":"2.0","id":"` + requestID(rpcwire.MethodSubmit, handle) +
		`","error":{"code":-32603,"message":"opaque-client-secret-marker"}}`)
	server := rawResponseServer(t, http.StatusOK, mediaTypeJSON, errorBody)
	client := newTestClient(t, server.URL, credential, testIdentity(), new(verifierFixture), 64<<10, 64<<10)
	encodedClient, marshalErr := json.Marshal(client)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	assertStringsOmitSecret(t, secret, []string{
		fmt.Sprintf("%v", client), fmt.Sprintf("%+v", client), fmt.Sprintf("%#v", client), string(encodedClient),
	})
	_, err = client.Submit(t.Context(), request)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Submit() error leaked server secret: %v", err)
	}
}

func assertStringsOmitSecret(t *testing.T, secret string, values []string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, secret) {
			t.Fatalf("credential leaked through representation %q", value)
		}
	}
}

func TestNewClientValidatesEachExplicitConfigFieldAndDisablesProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	credential := testCredential(t)
	identity := testIdentity()
	verifier := new(verifierFixture)
	valid := Config{
		Endpoint: server.URL, Credential: credential, ExpectedIdentity: identity,
		MaxRequestBytes: 64 << 10, MaxResponseBytes: 64 << 10, ReceiptVerifier: verifier,
	}
	var typedNil *verifierFixture
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"endpoint_empty", func(config *Config) { config.Endpoint = "" }},
		{"endpoint_hostname", func(config *Config) { config.Endpoint = "http://localhost:1234" }},
		{"endpoint_non_loopback", func(config *Config) { config.Endpoint = "http://192.0.2.1:1234" }},
		{"endpoint_https", func(config *Config) { config.Endpoint = "https://127.0.0.1:1234" }},
		{"endpoint_no_port", func(config *Config) { config.Endpoint = "http://127.0.0.1" }},
		{"endpoint_service_port", func(config *Config) { config.Endpoint = "http://127.0.0.1:http" }},
		{"endpoint_userinfo", func(config *Config) { config.Endpoint = "http://user@127.0.0.1:1234" }},
		{"endpoint_query", func(config *Config) { config.Endpoint = "http://127.0.0.1:1234/?x=1" }},
		{"endpoint_oversize", func(config *Config) {
			config.Endpoint = "http://127.0.0.1:1234/" + strings.Repeat("x", maxEndpointBytes)
		}},
		{"credential", func(config *Config) { config.Credential = BearerCredential{} }},
		{"expected_identity", func(config *Config) { config.ExpectedIdentity = rpcwire.AuthorityIdentity{} }},
		{"max_request", func(config *Config) { config.MaxRequestBytes = 0 }},
		{"max_response", func(config *Config) { config.MaxResponseBytes = 0 }},
		{"max_response_unbounded", func(config *Config) { config.MaxResponseBytes = maxConfiguredBytes + 1 }},
		{"verifier_nil", func(config *Config) { config.ReceiptVerifier = nil }},
		{"verifier_typed_nil", func(config *Config) { config.ReceiptVerifier = typedNil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if client, err := NewClient(config); err == nil || client != nil {
				t.Fatalf("NewClient() = (%+v, %v), want failure", client, err)
			}
		})
	}

	client, err := NewClient(valid)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("HTTP transport proxy = %#v, want nil", transport)
	}
	if client.httpClient.CheckRedirect == nil {
		t.Fatal("redirect policy is not explicit")
	}
}

func TestConfigFieldsAreDynamicallyCoveredByValidationMutations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	valid := testConfig(t, server.URL)
	mutations := configValidationMutations()
	typeOfConfig := reflect.TypeFor[Config]()
	if len(mutations) != typeOfConfig.NumField() {
		t.Fatalf("config validation registry fields = %d, producer fields = %d", len(mutations), typeOfConfig.NumField())
	}
	for index := 0; index < typeOfConfig.NumField(); index++ {
		field := typeOfConfig.Field(index)
		mutation, ok := mutations[field.Name]
		if !ok {
			t.Fatalf("Config.%s has no fail-fast validation mutation", field.Name)
		}
		t.Run(field.Name, func(t *testing.T) {
			config := valid
			mutation(&config)
			if client, err := NewClient(config); err == nil || client != nil {
				t.Fatalf("Config.%s invalid mutation was accepted", field.Name)
			}
		})
	}
}

func configValidationMutations() map[string]func(*Config) {
	var typedNil *verifierFixture
	return map[string]func(*Config){
		"Endpoint":         func(config *Config) { config.Endpoint = "" },
		"Credential":       func(config *Config) { config.Credential = BearerCredential{} },
		"ExpectedIdentity": func(config *Config) { config.ExpectedIdentity = rpcwire.AuthorityIdentity{} },
		"MaxRequestBytes":  func(config *Config) { config.MaxRequestBytes = 0 },
		"MaxResponseBytes": func(config *Config) { config.MaxResponseBytes = 0 },
		"ReceiptVerifier":  func(config *Config) { config.ReceiptVerifier = typedNil },
	}
}

func testConfig(t *testing.T, endpoint string) Config {
	t.Helper()
	return Config{
		Endpoint: endpoint, Credential: testCredential(t), ExpectedIdentity: testIdentity(),
		MaxRequestBytes: 64 << 10, MaxResponseBytes: 64 << 10, ReceiptVerifier: new(verifierFixture),
	}
}

func TestClientRejectsRequestOverLimitBeforeNetwork(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(server.Close)
	credential := testCredential(t)
	client := newTestClient(t, server.URL, credential, testIdentity(), new(verifierFixture), int64(len(authorizationPrefix+credential.value)), 64<<10)
	request := testRequest(t)
	handle := testHandle(t, request)
	outcome, err := client.Submit(t.Context(), request)
	assertUnavailableOutcome(t, outcome, err, handle)
	if calls.Load() != 0 {
		t.Fatalf("HTTP calls = %d, want 0", calls.Load())
	}
}
