package gateclosure

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestRuntimeDepsRegistryAnonymousBearerV2ChallengeDoesNotFetchToken(t *testing.T) {
	var tokenRequests, v2Requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v2/":
			v2Requests++
			writer.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
			writer.Header().Set("WWW-Authenticate", testRuntimeDepsBearerChallenge(request, "super-dolphin/runtime-deps"))
			writer.WriteHeader(http.StatusUnauthorized)
		case "/token":
			tokenRequests++
			t.Error("registry prerequisite probe fetched a repository token")
			writer.WriteHeader(http.StatusInternalServerError)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	registry := testRuntimeDepsRegistry(t, server)
	if err := validateRuntimeDepsRegistryClient(registry); err != nil {
		t.Fatalf("anonymous Bearer V2 probe: %v", err)
	}
	if tokenRequests != 0 || v2Requests != 1 {
		t.Fatalf("token requests = %d, V2 requests = %d", tokenRequests, v2Requests)
	}
}

func TestRuntimeDepsRegistryRejectsBearerScopeAndTokenShape(t *testing.T) {
	registry := runtimeDepsRegistry{baseURL: "https://ghcr.io", repository: "owner/runtime", auditURL: func(*url.URL) error { return nil }}
	challenge, err := registry.parseBearerChallenge(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope=""`)
	if err != nil || challenge.scope != registry.pullScope() {
		t.Fatalf("empty registry-root scope challenge = %#v, %v", challenge, err)
	}
	if _, err := registry.parseBearerChallenge(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:owner/other:pull"`); err == nil {
		t.Fatal("wrong Bearer scope unexpectedly passed")
	}
	if _, err := decodeRegistryBearerToken(strings.NewReader(`{"token":"good","unexpected":true}`)); err == nil {
		t.Fatal("unknown token field unexpectedly passed")
	}
	if _, err := decodeRegistryBearerToken(strings.NewReader(`{"token":"bad\ncontrol"}`)); err == nil {
		t.Fatal("control character token unexpectedly passed")
	}
	if _, err := decodeRegistryBearerToken(strings.NewReader(strings.Repeat("x", runtimeDepsTokenLimit+1))); err == nil {
		t.Fatal("oversized token response unexpectedly passed")
	}
}

func TestRuntimeDepsRegistryAcceptsJSONWhitespaceAroundTokenResponse(t *testing.T) {
	token, err := decodeRegistryBearerToken(strings.NewReader(" \n{\"token\":\"anonymous-token\"}\n\t"))
	if err != nil {
		t.Fatalf("decode token response with JSON whitespace: %v", err)
	}
	if token != "anonymous-token" {
		t.Fatalf("token = %q", token)
	}
	if _, err := decodeRegistryBearerToken(strings.NewReader("{\"token\":\"anonymous-token\"}\n{\"token\":\"second\"}")); err == nil {
		t.Fatal("trailing token document unexpectedly passed")
	}
}

func TestRuntimeDepsRegistryPingAcceptsOnlyShapedPlaceholderScope(t *testing.T) {
	registry := runtimeDepsRegistry{baseURL: "https://ghcr.io", repository: "owner/runtime", auditURL: func(*url.URL) error { return nil }}
	challenge, err := registry.parseRegistryPingChallengeHeaders([]string{`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:user/image:pull"`})
	if err != nil || challenge.scope != "repository:user/image:pull" {
		t.Fatalf("registry ping challenge = %#v, %v", challenge, err)
	}
	if _, err := registry.parseRegistryPingChallengeHeaders([]string{`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="admin"`}); err == nil {
		t.Fatal("unshaped registry ping scope unexpectedly passed")
	}
}

func TestRuntimeDepsRegistryDoesNotFollowManifestRedirect(t *testing.T) {
	var tokenRequests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			tokenRequests++
			fmt.Fprint(writer, `{"token":"must-not-be-used"}`)
			return
		}
		writer.Header().Set("Location", "https://example.invalid/manifest")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	registry := testRuntimeDepsRegistry(t, server)
	if _, err := registry.readManifest("locked"); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("manifest redirect error = %v", err)
	}
	if tokenRequests != 0 {
		t.Fatalf("manifest read requested %d anonymous tokens", tokenRequests)
	}
}

func TestRuntimeDepsRegistryRejectsDuplicateBearerChallenges(t *testing.T) {
	var tokenRequests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			tokenRequests++
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Add("WWW-Authenticate", testRuntimeDepsBearerChallenge(request, "super-dolphin/runtime-deps"))
		writer.Header().Add("WWW-Authenticate", testRuntimeDepsBearerChallenge(request, "super-dolphin/runtime-deps"))
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	err := validateRuntimeDepsRegistryClient(testRuntimeDepsRegistry(t, server))
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("duplicate Bearer challenges error = %v", err)
	}
	if tokenRequests != 0 {
		t.Fatalf("duplicate Bearer challenges requested %d tokens", tokenRequests)
	}
}

func TestRuntimeDepsRegistryBlobRedirectHostAllowlist(t *testing.T) {
	registry := runtimeDepsRegistry{baseURL: "https://ghcr.io", auditURL: func(*url.URL) error { return nil }}
	for _, target := range []string{
		"https://ghcr.io/v2/blob",
		"https://pkg-containers.githubusercontent.com/blob",
		"https://githubusercontent.com/blob",
	} {
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.auditRedirect(parsed); err != nil {
			t.Fatalf("allowed redirect %s: %v", target, err)
		}
	}
	for _, target := range []string{
		"https://example.com/blob",
		"https://githubusercontent.com.example.com/blob",
		"https://evilgithubusercontent.com/blob",
	} {
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.auditRedirect(parsed); err == nil {
			t.Fatalf("unapproved redirect %s unexpectedly passed", target)
		}
	}
}

func TestRuntimeDepsRegistryFollowsOneAuditedBlobRedirectWithoutAuthorization(t *testing.T) {
	payload := []byte(`{"architecture":"amd64"}`)
	digest := digestBytes(payload)
	var cdnAuthorization string
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cdnAuthorization = request.Header.Get("Authorization")
		writer.Write(payload)
	}))
	defer cdn.Close()
	registryServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/token" {
			fmt.Fprint(writer, `{"token":"anonymous-token"}`)
			return
		}
		if request.Header.Get("Authorization") != "Bearer anonymous-token" {
			writer.Header().Set("WWW-Authenticate", testRuntimeDepsBearerChallenge(request, "super-dolphin/runtime-deps"))
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Location", cdn.URL+"/blob")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer registryServer.Close()
	registry := testRuntimeDepsRegistry(t, registryServer)
	registry.client = insecureRuntimeDepsTestClient()
	registry.auditURL = func(target *url.URL) error {
		switch target.String() {
		case registryServer.URL + "/token", cdn.URL + "/blob":
			return nil
		default:
			return fmt.Errorf("unexpected audited target %s", target)
		}
	}
	data, err := registry.readConfigBlob(digest, int64(len(payload)))
	if err != nil {
		t.Fatalf("one-hop blob redirect: %v", err)
	}
	if string(data) != string(payload) || cdnAuthorization != "" {
		t.Fatalf("blob = %q, CDN Authorization = %q", data, cdnAuthorization)
	}
}

func TestRuntimeDepsDialRejectsReservedAddressesAndPinsResolvedIP(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "224.0.0.1", "2001:db8::1"} {
		t.Run(address, func(t *testing.T) {
			_, err := dialRuntimeDepsAddress(context.Background(), "tcp", "registry.example:443", testRuntimeDepsLookup(address), testRuntimeDepsDial)
			if err == nil || !strings.Contains(err.Error(), "prohibited") {
				t.Fatalf("reserved address error = %v", err)
			}
		})
	}
	var dialed string
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	dial := func(_ context.Context, _ string, address string) (net.Conn, error) {
		dialed = address
		return nil, errors.New("stop")
	}
	_, _ = dialRuntimeDepsAddress(context.Background(), "tcp", "registry.example:443", lookup, dial)
	if dialed != "8.8.8.8:443" {
		t.Fatalf("dialed address = %q", dialed)
	}
	dialed = ""
	_, _ = dialRuntimeDepsAddress(context.Background(), "tcp", "ghcr.io:443", testRuntimeDepsLookup("198.18.0.124"), dial)
	if dialed != "198.18.0.124:443" {
		t.Fatalf("GHCR proxy dialed address = %q", dialed)
	}
}

func testRuntimeDepsRegistry(t *testing.T, server *httptest.Server) runtimeDepsRegistry {
	t.Helper()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect") }
	return runtimeDepsRegistry{
		baseURL: server.URL, repository: "super-dolphin/runtime-deps", client: client,
		auditURL: func(*url.URL) error { return nil },
	}
}

func testRuntimeDepsBearerChallenge(request *http.Request, repository string) string {
	return fmt.Sprintf(`Bearer realm="https://%s/token",service="%s",scope="repository:%s:pull"`, request.Host, request.Host, repository)
}

func insecureRuntimeDepsTestClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func testRuntimeDepsLookup(address string) runtimeDepsLookupIP {
	return func(context.Context, string, string) ([]netip.Addr, error) {
		parsed := netip.MustParseAddr(address)
		return []netip.Addr{parsed}, nil
	}
}

func testRuntimeDepsDial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("dial should not run")
}
