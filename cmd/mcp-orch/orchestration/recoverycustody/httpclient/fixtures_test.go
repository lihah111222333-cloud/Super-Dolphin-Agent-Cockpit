package httpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
)

func assertUnavailableOutcome(
	t *testing.T,
	outcome recoverycustody.Outcome,
	err error,
	handle recoverycustody.SubmissionHandle,
) {
	t.Helper()
	if !errors.Is(err, recoverycustody.ErrCustodyUnavailable) {
		t.Fatalf("error = %v, want ErrCustodyUnavailable", err)
	}
	if outcome.Handle != handle || outcome.Status != recoverycustody.StatusIndeterminate || outcome.Receipt != nil {
		t.Fatalf("outcome = %+v, want same-handle indeterminate", outcome)
	}
	if validateErr := outcome.Validate(); validateErr != nil {
		t.Fatalf("outcome.Validate() error = %v", validateErr)
	}
}

func newTestClient(
	t *testing.T,
	endpoint string,
	credential BearerCredential,
	identity rpcwire.AuthorityIdentity,
	verifier recoverycustody.ReceiptVerifier,
	maxRequestBytes int64,
	maxResponseBytes int64,
) *Client {
	t.Helper()
	client, err := NewClient(Config{
		Endpoint: endpoint, Credential: credential, ExpectedIdentity: identity,
		MaxRequestBytes: maxRequestBytes, MaxResponseBytes: maxResponseBytes, ReceiptVerifier: verifier,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func testCredential(t *testing.T) BearerCredential {
	t.Helper()
	credential, err := NewBearerCredential("test-only-opaque-custody-credential")
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func testIdentity() rpcwire.AuthorityIdentity {
	return rpcwire.AuthorityIdentity{
		InstanceID: "authority-instance-1", AuthorityID: "authority-1", AuthorityClusterID: "cluster-1",
		AuthorityProfile: "external-quorum-v1", AuthorityGeneration: 3, AuthorityProfileGeneration: 5,
		AuthorityProfileDigest: testDigest(40), SecurityDomainID: "security-domain-1",
	}
}

func testRequest(t *testing.T) recoverycustody.SubmissionRequest {
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
		t.Fatal(err)
	}
	return finalized
}

func testHandle(t *testing.T, request recoverycustody.SubmissionRequest) recoverycustody.SubmissionHandle {
	t.Helper()
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func testTerminalOutcome(
	t *testing.T,
	identity rpcwire.AuthorityIdentity,
	handle recoverycustody.SubmissionHandle,
) recoverycustody.Outcome {
	t.Helper()
	receipt := recoverycustody.ExternalReceipt{
		SchemaVersion: 1, Kind: recoverycustody.ReceiptAvailability, ReceiptID: "receipt-1",
		AuthorityID: identity.AuthorityID, AuthorityClusterID: identity.AuthorityClusterID,
		AuthorityProfile: identity.AuthorityProfile, AuthorityGeneration: identity.AuthorityGeneration,
		OwnerID: "owner-1", OwnerGeneration: 2, AuthorityProfileGeneration: identity.AuthorityProfileGeneration,
		AuthorityProfileDigest: identity.AuthorityProfileDigest, SecurityDomainID: identity.SecurityDomainID,
		HeadID: "head-1", HeadRevision: 4, HeadRootDigest: testDigest(41), OperationID: handle.OperationID,
		RequestDigest: handle.RequestDigest, BatchDigest: handle.BatchDigest,
		SecurityTimeLineageDigest: testDigest(42), SecurityTimeGeneration: 2, SecurityTimeRevision: 3,
		AntiRollbackGeneration: 4, AntiRollbackAnchorDigest: testDigest(43),
		QuorumAttestationDigest: testDigest(44), QuorumAttestationRef: "quorum://external/" + testDigest(44),
		SignatureDigest: testDigest(45), SignatureRef: "signature://external/" + testDigest(45),
		AuthorityTime: time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC), RetentionPolicyDigest: testDigest(46),
		RetentionUntil: time.Date(2027, 9, 2, 1, 2, 3, 0, time.UTC), GCNotBeforeAnchorGeneration: 5,
	}
	finalized, err := recoverycustody.FinalizeReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	outcome := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusAvailable, Receipt: &finalized}
	if err := outcome.Validate(); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func testDigest(seed int) string { return fmt.Sprintf("%064x", seed) }

func assertRequestHeaders(t *testing.T, request *http.Request, credential string) {
	t.Helper()
	if request.Method != http.MethodPost || request.Header.Get("Content-Type") != mediaTypeJSON ||
		request.Header.Get("Accept") != mediaTypeJSON ||
		request.Header.Get("Authorization") != authorizationPrefix+credential {
		t.Errorf("request headers/method are invalid")
	}
}

func readRequestEnvelope(t *testing.T, request *http.Request) requestEnvelope {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope requestEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func canonicalResult(t *testing.T, result rpcwire.Result) []byte {
	t.Helper()
	payload, err := rpcwire.MarshalCanonical(result)
	if err != nil {
		t.Fatalf("MarshalCanonical(result) error = %v", err)
	}
	return payload
}

func responseJSON(t *testing.T, id string, result []byte) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: jsonRPCVersion, ID: id, Result: result})
	if err != nil {
		t.Fatalf("marshal response envelope: %v", err)
	}
	return payload
}

func writeResult(t *testing.T, writer http.ResponseWriter, id string, result rpcwire.Result) {
	t.Helper()
	writer.Header().Set("Content-Type", mediaTypeJSON)
	if _, err := writer.Write(responseJSON(t, id, canonicalResult(t, result))); err != nil {
		t.Errorf("write response error = %v", err)
	}
}

func serveExactWireRequest(
	t *testing.T,
	writer http.ResponseWriter,
	incoming *http.Request,
	credential string,
	identity rpcwire.AuthorityIdentity,
	request recoverycustody.SubmissionRequest,
	handle recoverycustody.SubmissionHandle,
) {
	t.Helper()
	assertRequestHeaders(t, incoming, credential)
	envelope := readRequestEnvelope(t, incoming)
	if envelope.JSONRPC != jsonRPCVersion {
		t.Errorf("jsonrpc = %q", envelope.JSONRPC)
	}
	outcome, ok := exactWireOutcome(t, envelope, request, handle)
	if !ok {
		return
	}
	writeResult(t, writer, envelope.ID, rpcwire.Result{
		SchemaVersion: rpcwire.SchemaVersion, Authority: identity, Outcome: &outcome,
	})
}

func exactWireOutcome(
	t *testing.T,
	envelope requestEnvelope,
	request recoverycustody.SubmissionRequest,
	handle recoverycustody.SubmissionHandle,
) (recoverycustody.Outcome, bool) {
	t.Helper()
	switch envelope.Method {
	case rpcwire.MethodSubmit:
		assertSubmitParams(t, envelope.Params, request)
		return recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusPending}, true
	case rpcwire.MethodQuery:
		assertQueryParams(t, envelope.Params, handle)
		return recoverycustody.Outcome{
			Handle: handle, Status: recoverycustody.StatusIndeterminate, ReasonCode: "authority_processing",
		}, true
	default:
		t.Errorf("method = %q", envelope.Method)
		return recoverycustody.Outcome{}, false
	}
}

func assertSubmitParams(t *testing.T, payload []byte, request recoverycustody.SubmissionRequest) {
	t.Helper()
	got, err := rpcwire.DecodeSubmitParams(payload)
	if err != nil || got != (rpcwire.SubmitParams{SchemaVersion: rpcwire.SchemaVersion, Request: request}) {
		t.Errorf("submit params = (%+v, %v)", got, err)
	}
}

func assertQueryParams(t *testing.T, payload []byte, handle recoverycustody.SubmissionHandle) {
	t.Helper()
	got, err := rpcwire.DecodeQueryParams(payload)
	if err != nil || got != (rpcwire.QueryParams{SchemaVersion: rpcwire.SchemaVersion, Handle: handle}) {
		t.Errorf("query params = (%+v, %v)", got, err)
	}
}

func rawResponseServer(t *testing.T, status int, contentType string, body []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			writer.Header().Set("Content-Type", contentType)
		}
		writer.WriteHeader(status)
		if _, err := writer.Write(body); err != nil {
			t.Errorf("write response error = %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
