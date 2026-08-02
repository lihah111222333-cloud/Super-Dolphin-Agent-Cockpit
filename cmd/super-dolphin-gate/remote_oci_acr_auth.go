package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/workerio"
)

const acrAuthorizationVersion = "2018-12-01"

type acrAuthorizationResponse struct {
	AuthorizationToken string `json:"AuthorizationToken"`
	Code               string `json:"Code"`
	ExpireTime         int64  `json:"ExpireTime"`
	IsSuccess          bool   `json:"IsSuccess"`
	RequestID          string `json:"RequestId"`
	TempUsername       string `json:"TempUsername"`
}

type acrPushCredentials struct {
	Registry string
	Username string
	Token    string
}

// acquireACRPushCredentials exchanges the worker's ECI RAM role for one short-lived ACR token.
func acquireACRPushCredentials(ctx context.Context, request remoteci.OCIBaselineBuilderRequest) (acrPushCredentials, error) {
	roleName, ok := os.LookupEnv(remoteWorkerRoleEnv)
	if !ok || roleName == "" || strings.ContainsAny(roleName, "\x00\r\n") {
		return acrPushCredentials{}, errors.New("remote worker RAM role name is required for ACR authorization")
	}
	credentials, err := workerio.ReadRAMCredentials(ctx, roleName, workerio.Dependencies{})
	if err != nil {
		return acrPushCredentials{}, fmt.Errorf("read ECI RAM credentials for ACR: %w", err)
	}
	return requestACRAuthorizationToken(ctx, request, credentials, http.DefaultClient, time.Now, randomACRNonce, "")
}

func requestACRAuthorizationToken(ctx context.Context, request remoteci.OCIBaselineBuilderRequest, credentials workerio.RAMCredentials, client *http.Client, clock func() time.Time, nonce func() (string, error), endpoint string) (acrPushCredentials, error) {
	if client == nil || clock == nil || nonce == nil || credentials.AccessKeyID == "" || credentials.AccessKeySecret == "" || credentials.SecurityToken == "" || !credentials.Expiration.After(clock()) {
		return acrPushCredentials{}, errors.New("ACR authorization dependencies or RAM credentials are invalid")
	}
	if endpoint == "" {
		endpoint = "https://cr-vpc." + request.ACRRegionID + ".aliyuncs.com/"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return acrPushCredentials{}, errors.New("ACR authorization endpoint is invalid")
	}
	requestNonce, err := nonce()
	if err != nil {
		return acrPushCredentials{}, fmt.Errorf("generate ACR signature nonce: %w", err)
	}
	values := url.Values{
		"AccessKeyId":      {credentials.AccessKeyID},
		"Action":           {"GetAuthorizationToken"},
		"Format":           {"JSON"},
		"InstanceId":       {request.ACRInstanceID},
		"RegionId":         {request.ACRRegionID},
		"SecurityToken":    {credentials.SecurityToken},
		"SignatureMethod":  {"HMAC-SHA1"},
		"SignatureNonce":   {requestNonce},
		"SignatureVersion": {"1.0"},
		"Timestamp":        {clock().UTC().Format("2006-01-02T15:04:05Z")},
		"Version":          {acrAuthorizationVersion},
	}
	values.Set("Signature", acrRPCSignature(values, credentials.AccessKeySecret))
	parsed.RawQuery = values.Encode()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return acrPushCredentials{}, fmt.Errorf("create ACR authorization request: %w", err)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return acrPushCredentials{}, fmt.Errorf("request ACR authorization: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return acrPushCredentials{}, fmt.Errorf("ACR authorization returned HTTP %d", response.StatusCode)
	}
	var authorization acrAuthorizationResponse
	if err := decodeStrictACRAuthorization(response.Body, &authorization); err != nil {
		return acrPushCredentials{}, errors.New("ACR authorization response is invalid")
	}
	return validateACRAuthorization(authorization, request.RegistryRepository, clock())
}

func acrRPCSignature(values url.Values, secret string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "Signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		for _, value := range values[key] {
			parts = append(parts, percentEncodeACR(key)+"="+percentEncodeACR(value))
		}
	}
	stringToSign := "GET&%2F&" + percentEncodeACR(strings.Join(parts, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func percentEncodeACR(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func randomACRNonce() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func decodeStrictACRAuthorization(reader io.Reader, target *acrAuthorizationResponse) error {
	body, err := io.ReadAll(io.LimitReader(reader, 64<<10))
	if err != nil || len(body) == 64<<10 {
		return errors.New("ACR authorization response is oversized or unreadable")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("ACR authorization response has trailing JSON")
	}
	return nil
}

func validateACRAuthorization(authorization acrAuthorizationResponse, repository string, now time.Time) (acrPushCredentials, error) {
	if !authorization.IsSuccess || authorization.Code != "Success" || authorization.RequestID == "" || authorization.TempUsername == "" || authorization.ExpireTime <= now.UnixMilli() || !validACRRegistryRepository(repository) {
		return acrPushCredentials{}, errors.New("ACR authorization was denied, expired, or incomplete")
	}
	if strings.ContainsAny(authorization.TempUsername+authorization.AuthorizationToken, "\x00\r\n") || len(authorization.AuthorizationToken) > 4096 {
		return acrPushCredentials{}, errors.New("ACR authorization token is incomplete")
	}
	registry, _, _ := strings.Cut(repository, "/")
	return acrPushCredentials{Registry: registry, Username: authorization.TempUsername, Token: authorization.AuthorizationToken}, nil
}

func validACRRegistryRepository(value string) bool {
	registry, repository, ok := strings.Cut(value, "/")
	return ok && registry != "" && repository != "" && !strings.ContainsAny(value, "@ \t\r\n\\?#") && !strings.Contains(value, "://")
}

// writeACRDockerConfig materializes a single-use BuildKit auth file beneath a private HOME.
func writeACRDockerConfig(home string, credentials acrPushCredentials) (string, error) {
	if home == "" || credentials.Registry == "" || credentials.Username == "" || credentials.Token == "" {
		return "", errors.New("ACR Docker auth configuration is incomplete")
	}
	dockerHome := filepath.Join(home, ".docker")
	if err := os.MkdirAll(dockerHome, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dockerHome, 0o700); err != nil {
		return "", err
	}
	configPath := filepath.Join(dockerHome, "config.json")
	data, err := json.Marshal(struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}{Auths: map[string]struct {
		Auth string `json:"auth"`
	}{credentials.Registry: {Auth: base64.StdEncoding.EncodeToString([]byte(credentials.Username + ":" + credentials.Token))}}})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(configPath, 0o600); err != nil {
		return "", err
	}
	return configPath, nil
}
