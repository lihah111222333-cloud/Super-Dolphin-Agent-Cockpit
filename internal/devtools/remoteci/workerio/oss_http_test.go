package workerio

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const successfulCredentialsJSON = `{"AccessKeyId":"id","AccessKeySecret":"secret","SecurityToken":"token","Expiration":"2026-07-27T01:00:00Z","Code":"Success","LastUpdated":"2026-07-27T00:00:00Z"}`

func workerIOTestNow() time.Time {
	return time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)
}

type signedObjectHandler struct {
	t   *testing.T
	now time.Time
}

// ServeHTTP 断言 OSS V1 签名、临时令牌、日期和对象路径。
func (handler signedObjectHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.t.Helper()
	if request.URL.Path != "/bucket-name/path/to/object.txt" {
		handler.t.Fatalf("object path = %q", request.URL.Path)
	}
	if request.Header.Get("x-oss-security-token") != "test-session-token" {
		handler.t.Fatal("missing security token")
	}
	if request.Header.Get("Date") != handler.now.Format(http.TimeFormat) {
		handler.t.Fatalf("Date = %q", request.Header.Get("Date"))
	}
	const wantAuthorization = "OSS test-access-key:GcrTkKed9YRPmKdoFPXgkkTp5SM="
	if request.Header.Get("Authorization") != wantAuthorization {
		handler.t.Fatalf("Authorization = %q, want %q", request.Header.Get("Authorization"), wantAuthorization)
	}
	_, _ = writer.Write([]byte("bundle-data"))
}

type imdsV2Handler struct{ t *testing.T }

// ServeHTTP 断言 IMDSv2 token 与 RAM 凭据请求合同。
func (handler imdsV2Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.t.Helper()
	switch request.URL.Path {
	case "/latest/api/token":
		handler.writeToken(writer, request)
	case "/latest/meta-data/ram/security-credentials/worker-role":
		handler.writeCredentials(writer, request)
	default:
		handler.t.Fatalf("unexpected metadata path %q", request.URL.Path)
	}
}

func (handler imdsV2Handler) writeToken(writer http.ResponseWriter, request *http.Request) {
	handler.t.Helper()
	if request.Method != http.MethodPut {
		handler.t.Fatalf("token method = %s", request.Method)
	}
	if request.Header.Get("X-aliyun-ecs-metadata-token-ttl-seconds") != "21600" {
		handler.t.Fatal("missing IMDSv2 TTL header")
	}
	_, _ = writer.Write([]byte("imds-token"))
}

func (handler imdsV2Handler) writeCredentials(writer http.ResponseWriter, request *http.Request) {
	handler.t.Helper()
	if request.Method != http.MethodGet {
		handler.t.Fatalf("credentials method = %s", request.Method)
	}
	if request.Header.Get("X-aliyun-ecs-metadata-token") != "imds-token" {
		handler.t.Fatal("missing IMDSv2 token")
	}
	const credentials = `{"AccessKeyId":"test-access-key","AccessKeySecret":"test-secret","SecurityToken":"test-session-token","Expiration":"2026-07-27T01:00:00Z","Code":"Success","LastUpdated":"2026-07-27T00:00:00Z"}`
	_, _ = writer.Write([]byte(credentials))
}

type metadataSuccessHandler struct{}

func (metadataSuccessHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/latest/api/token" {
		_, _ = writer.Write([]byte("token"))
		return
	}
	_, _ = writer.Write([]byte(successfulCredentialsJSON))
}

func testPermanentMetadataStatus(t *testing.T) {
	attempts := 0
	metadataServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte("secret response body"))
	}))
	t.Cleanup(metadataServer.Close)
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal.example", Bucket: "bucket-name", Key: "object", MaxBytes: 1}, Dependencies{MetadataBaseURL: metadataServer.URL})
	client.wait = noWait
	_, err := client.Download(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("Download() error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("metadata attempts = %d, want 1 for permanent status", attempts)
	}
}

func testOversizedObject(t *testing.T) {
	metadataServer := httptest.NewServer(metadataSuccessHandler{})
	t.Cleanup(metadataServer.Close)
	objectServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("too-large"))
	}))
	t.Cleanup(objectServer.Close)
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: objectServer.URL, Bucket: "bucket-name", Key: "object", MaxBytes: 3}, Dependencies{HTTPClient: objectServer.Client(), MetadataBaseURL: metadataServer.URL, Clock: workerIOTestNow})
	var destination bytes.Buffer
	_, err := client.Download(context.Background(), &destination)
	if err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Fatalf("Download() error = %v", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("destination received %q", destination.String())
	}
}

func testObjectRedirect(t *testing.T) {
	metadataServer := httptest.NewServer(metadataSuccessHandler{})
	t.Cleanup(metadataServer.Close)
	redirectTarget := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("redirect target must not receive RAM credentials")
	}))
	t.Cleanup(redirectTarget.Close)
	objectServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, redirectTarget.URL, http.StatusFound)
	}))
	t.Cleanup(objectServer.Close)
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: objectServer.URL, Bucket: "bucket-name", Key: "object", MaxBytes: 3}, Dependencies{HTTPClient: objectServer.Client(), MetadataBaseURL: metadataServer.URL, Clock: workerIOTestNow})
	_, err := client.Download(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("Download() error = %v", err)
	}
}

type deadlineRetryTransport struct {
	timeoutPath string
	attempts    int
}

func (transport *deadlineRetryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	switch request.URL.Host {
	case "metadata.internal":
		return transport.metadataResponse(request)
	case "oss.internal":
		return textResponse("object"), nil
	default:
		return nil, errors.New("unexpected host")
	}
}

func (transport *deadlineRetryTransport) metadataResponse(request *http.Request) (*http.Response, error) {
	if request.URL.Path == transport.timeoutPath {
		transport.attempts++
		if transport.attempts == 1 {
			return nil, context.DeadlineExceeded
		}
	}
	if request.URL.Path == "/latest/api/token" {
		return textResponse("token"), nil
	}
	return textResponse(successfulCredentialsJSON), nil
}

func testSingleRequestDeadlineRetry(t *testing.T, timeoutPath string) {
	transport := &deadlineRetryTransport{timeoutPath: timeoutPath}
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal", Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: &http.Client{Transport: transport}, MetadataBaseURL: "http://metadata.internal", Clock: workerIOTestNow})
	client.wait = noWait
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	var destination bytes.Buffer
	size, err := client.Download(ctx, &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if transport.attempts != 2 {
		t.Fatalf("timeout attempts = %d, want 2", transport.attempts)
	}
	assertDownloadedObject(t, size, destination.String(), "object")
}

type transientRetryStage struct {
	name string
	host string
	path string
}

func transientRetryStages() []transientRetryStage {
	return []transientRetryStage{
		{name: "IMDS token", host: "metadata.internal", path: "/latest/api/token"},
		{name: "RAM credentials", host: "metadata.internal", path: "/latest/meta-data/ram/security-credentials/worker-role"},
		{name: "OSS object", host: "oss.internal", path: "/bucket-name/object"},
	}
}

type transientRetryTransport struct {
	stage    transientRetryStage
	attempts int
}

func (transport *transientRetryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host == transport.stage.host && request.URL.Path == transport.stage.path {
		transport.attempts++
		if transport.attempts < maxDownloadAttempts {
			return nil, context.DeadlineExceeded
		}
	}
	return successfulWorkerResponse(request)
}

func successfulWorkerResponse(request *http.Request) (*http.Response, error) {
	switch request.URL.Host {
	case "metadata.internal":
		if request.URL.Path == "/latest/api/token" {
			return textResponse("token"), nil
		}
		return textResponse(successfulCredentialsJSON), nil
	case "oss.internal":
		return textResponse("object"), nil
	default:
		return nil, errors.New("unexpected host")
	}
}

type retryDelayRecorder struct{ delays []time.Duration }

func (recorder *retryDelayRecorder) wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	recorder.delays = append(recorder.delays, delay)
	return nil
}

func testTwelfthTransientAttempt(t *testing.T, stage transientRetryStage) {
	transport := &transientRetryTransport{stage: stage}
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal", Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: &http.Client{Transport: transport}, MetadataBaseURL: "http://metadata.internal", Clock: workerIOTestNow})
	recorder := &retryDelayRecorder{}
	client.wait = recorder.wait
	var destination bytes.Buffer
	size, err := client.Download(context.Background(), &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if transport.attempts != maxDownloadAttempts {
		t.Fatalf("stage attempts = %d, want %d", transport.attempts, maxDownloadAttempts)
	}
	assertDownloadedObject(t, size, destination.String(), "object")
	assertRetryDelays(t, recorder.delays)
}

type virtualLongBodyTransport struct {
	started                 chan<- struct{}
	release                 <-chan struct{}
	requestHasShortDeadline bool
}

// RoundTrip 为长对象正文提供可控释放点并记录错误的短总期限。
func (transport *virtualLongBodyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host == "metadata.internal" {
		return successfulWorkerResponse(request)
	}
	if request.URL.Host != "oss.internal" {
		return nil, errors.New("unexpected host")
	}
	transport.requestHasShortDeadline = hasShortRequestDeadline(request)
	body := &gatedReadCloser{started: transport.started, release: transport.release, data: []byte("large-object")}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
}

func hasShortRequestDeadline(request *http.Request) bool {
	deadline, ok := request.Context().Deadline()
	return ok && time.Until(deadline) <= remoteHTTPTimeout
}

func assertDownloadedObject(t *testing.T, size int64, got, want string) {
	t.Helper()
	if size != int64(len(want)) || got != want {
		t.Fatalf("downloaded %d bytes %q, want %d bytes %q", size, got, len(want), want)
	}
}

func assertDownloadPending(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("Download() returned before virtual long body was released: %v", err)
	default:
	}
}

func assertVirtualLongBodyResult(t *testing.T, result <-chan error, destination *bytes.Buffer) {
	t.Helper()
	if err := <-result; err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if destination.String() != "large-object" {
		t.Fatalf("destination = %q", destination.String())
	}
}
