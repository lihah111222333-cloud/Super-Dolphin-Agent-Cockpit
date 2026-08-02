package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/workerio"
)

func TestACRAuthorizationRequestSignsSTSAndRejectsExpiredCredentials(t *testing.T) {
	now := time.Date(2026, time.August, 2, 1, 2, 3, 0, time.UTC)
	request := remoteci.OCIBaselineBuilderRequest{ACRInstanceID: "cri-example", ACRRegionID: "cn-hangzhou", RegistryRepository: "registry.cn-hangzhou.aliyuncs.com/team/gate"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		values := incoming.URL.Query()
		if incoming.Method != http.MethodGet || values.Get("Action") != "GetAuthorizationToken" || values.Get("Version") != acrAuthorizationVersion || values.Get("InstanceId") != request.ACRInstanceID || values.Get("RegionId") != request.ACRRegionID || values.Get("SecurityToken") != "sts-token" {
			t.Fatalf("unexpected signed request: %s", incoming.URL.String())
		}
		if signature := values.Get("Signature"); signature == "" || signature != acrRPCSignature(values, "secret") {
			t.Fatal("ACR request signature is absent or invalid")
		}
		_, _ = writer.Write([]byte(`{"AuthorizationToken":"token","Code":"Success","ExpireTime":1785632524000,"IsSuccess":true,"RequestId":"req-1","TempUsername":"user"}`))
	}))
	defer server.Close()
	endpoint := "https://cr-vpc.cn-hangzhou.aliyuncs.com/"
	endpointURL, _ := url.Parse(server.URL)
	client := server.Client()
	client.Transport = rewriteTransport{target: endpointURL, base: client.Transport}
	credentials := workerio.RAMCredentials{AccessKeyID: "key", AccessKeySecret: "secret", SecurityToken: "sts-token", Expiration: now.Add(time.Minute)}
	actual, err := requestACRAuthorizationToken(context.Background(), request, credentials, client, func() time.Time { return now }, func() (string, error) { return "nonce", nil }, endpoint)
	if err != nil || actual.Username != "user" || actual.Token != "token" {
		t.Fatalf("requestACRAuthorizationToken() = %#v, %v", actual, err)
	}
	credentials.Expiration = now
	if _, err := requestACRAuthorizationToken(context.Background(), request, credentials, client, func() time.Time { return now }, func() (string, error) { return "nonce", nil }, endpoint); err == nil {
		t.Fatal("expired RAM credentials were accepted")
	}
}

func TestACRAuthorizationStrictResponseAndPermissionFailure(t *testing.T) {
	now := time.Now().UTC()
	for name, response := range map[string]string{
		"unknown field":     `{"AuthorizationToken":"token","Code":"Success","ExpireTime":9999999999999,"IsSuccess":true,"RequestId":"r","TempUsername":"user","Extra":true}`,
		"expired token":     `{"AuthorizationToken":"token","Code":"Success","ExpireTime":1,"IsSuccess":true,"RequestId":"r","TempUsername":"user"}`,
		"permission denied": `{"AuthorizationToken":"","Code":"NoPermission","ExpireTime":9999999999999,"IsSuccess":false,"RequestId":"r","TempUsername":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			var decoded acrAuthorizationResponse
			if err := decodeStrictACRAuthorization(strings.NewReader(response), &decoded); err == nil && name == "unknown field" {
				t.Fatal("unknown ACR response field was accepted")
			}
			if name == "permission denied" || name == "expired token" {
				if _, err := validateACRAuthorization(decoded, "registry.cn-hangzhou.aliyuncs.com/team/gate", now); err == nil {
					t.Fatalf("ACR %s was accepted", name)
				}
			}
		})
	}
}

func TestACRDockerConfigIsPrivateAndWorkerCleanupRemovesIt(t *testing.T) {
	home, err := os.MkdirTemp("", "acr-auth-test-")
	if err != nil {
		t.Fatal(err)
	}
	config, err := writeACRDockerConfig(home, acrPushCredentials{Registry: "registry.cn-hangzhou.aliyuncs.com", Username: "user", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(config)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, err = %v", info.Mode(), err)
	}
	data, err := os.ReadFile(config)
	if err != nil || !strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte("user:token"))) {
		t.Fatalf("config auth was not encoded: %v", err)
	}
	removeRemoteOCIWorkspace(home)
	if _, err := os.Stat(filepath.Join(home, ".docker", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("temporary ACR config still exists: %v", err)
	}
}

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.URL.Scheme, copy.URL.Host = transport.target.Scheme, transport.target.Host
	return transport.base.RoundTrip(copy)
}
