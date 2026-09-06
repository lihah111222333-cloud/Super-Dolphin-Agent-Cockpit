package authorityhttp

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/httpclient"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/rpcwire"
)

func TestHTTPClientIntegrationIdentitySubmitQueryExact(t *testing.T) {
	request := testSubmissionRequest(t)
	handle := testSubmissionHandle(t, request)
	pending := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusPending}
	backend := &stubClient{
		submit: func(_ context.Context, _ recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
			return pending, nil
		},
		query: func(_ context.Context, _ recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
			return pending, nil
		},
	}
	server := httptest.NewServer(newTestHandler(t, backend, 64<<10, 2))
	t.Cleanup(server.Close)
	client := newIntegrationHTTPClient(t, server.URL, testBearerToken, testIdentity())

	if err := client.ProbeIdentity(t.Context()); err != nil {
		t.Fatalf("ProbeIdentity() error = %v", err)
	}
	submitted, err := client.Submit(t.Context(), request)
	if err != nil || !reflect.DeepEqual(submitted, pending) {
		t.Fatalf("Submit() = (%+v, %v), want %+v/nil", submitted, err, pending)
	}
	queried, err := client.Query(t.Context(), handle)
	if err != nil || !reflect.DeepEqual(queried, pending) {
		t.Fatalf("Query() = (%+v, %v), want %+v/nil", queried, err, pending)
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

func TestHTTPClientIntegrationRejectsWrongToken(t *testing.T) {
	server := httptest.NewServer(newTestHandler(t, &stubClient{}, 64<<10, 1))
	t.Cleanup(server.Close)
	client := newIntegrationHTTPClient(t, server.URL, "wrong-authority-token", testIdentity())
	if err := client.ProbeIdentity(t.Context()); !errors.Is(err, recoverycustody.ErrCustodyUnavailable) {
		t.Fatalf("ProbeIdentity() error = %v, want ErrCustodyUnavailable", err)
	}
}

func TestHTTPClientIntegrationRejectsIdentityDrift(t *testing.T) {
	server := httptest.NewServer(newTestHandler(t, &stubClient{}, 64<<10, 1))
	t.Cleanup(server.Close)
	expected := testIdentity()
	expected.InstanceID = "authority-instance-drifted"
	client := newIntegrationHTTPClient(t, server.URL, testBearerToken, expected)
	if err := client.ProbeIdentity(t.Context()); !errors.Is(err, recoverycustody.ErrCustodyUnavailable) {
		t.Fatalf("ProbeIdentity() error = %v, want ErrCustodyUnavailable", err)
	}
}

func newIntegrationHTTPClient(t *testing.T, endpoint, token string, identity rpcwire.AuthorityIdentity) *httpclient.Client {
	t.Helper()
	credential, err := httpclient.NewBearerCredential(token)
	if err != nil {
		t.Fatalf("httpclient.NewBearerCredential() error = %v", err)
	}
	client, err := httpclient.NewClient(httpclient.Config{
		Endpoint: endpoint, Credential: credential, ExpectedIdentity: identity,
		MaxRequestBytes: 64 << 10, MaxResponseBytes: 64 << 10,
		ReceiptVerifier: recoverycustody.RejectAllReceiptVerifier{},
	})
	if err != nil {
		t.Fatalf("httpclient.NewClient() error = %v", err)
	}
	return client
}
