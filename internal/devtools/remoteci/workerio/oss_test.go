package workerio

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestDownloadUsesIMDSv2CredentialsAndOSSV1Signature(t *testing.T) {
	now := workerIOTestNow()
	objectServer := httptest.NewTLSServer(signedObjectHandler{t: t, now: now})
	t.Cleanup(objectServer.Close)
	metadataServer := httptest.NewServer(imdsV2Handler{t: t})
	t.Cleanup(metadataServer.Close)
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: objectServer.URL, Bucket: "bucket-name", Key: "path/to/object.txt", MaxBytes: 1024}, Dependencies{HTTPClient: objectServer.Client(), MetadataBaseURL: metadataServer.URL, Clock: func() time.Time { return now }})
	var destination bytes.Buffer
	size, err := client.Download(context.Background(), &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	assertDownloadedObject(t, size, destination.String(), "bundle-data")
}

func TestUploadUsesCreateOnlyHeaderAndTreatsCollisionAsFailure(t *testing.T) {
	now := workerIOTestNow()
	metadataServer := httptest.NewServer(imdsV2Handler{t: t})
	t.Cleanup(metadataServer.Close)
	for name, status := range map[string]int{
		"success":             http.StatusOK,
		"conflict":            http.StatusConflict,
		"precondition failed": http.StatusPreconditionFailed,
	} {
		t.Run(name, func(t *testing.T) {
			attempts := 0
			objectServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				attempts++
				if request.Method != http.MethodPut || request.Header.Get("x-oss-forbid-overwrite") != "true" {
					t.Fatalf("request=%s headers=%v", request.Method, request.Header)
				}
				credentials := temporaryCredentials{AccessKeyID: "test-access-key", AccessKeySecret: "test-secret", SecurityToken: "test-session-token"}
				want := ossAuthorizationWithHeaders(http.MethodPut, now.UTC().Format(http.TimeFormat), "/bucket-name/object", credentials, "x-oss-forbid-overwrite:true\n")
				if request.Header.Get("Authorization") != want {
					t.Fatalf("authorization=%q want=%q", request.Header.Get("Authorization"), want)
				}
				writer.WriteHeader(status)
			}))
			t.Cleanup(objectServer.Close)
			client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: objectServer.URL, Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: objectServer.Client(), MetadataBaseURL: metadataServer.URL, Clock: func() time.Time { return now }})
			client.wait = noWait
			_, err := client.Upload(context.Background(), strings.NewReader("bundle"))
			if status == http.StatusOK && err != nil {
				t.Fatalf("Upload() error=%v", err)
			}
			if status != http.StatusOK {
				if err == nil {
					t.Fatal("Upload() collision unexpectedly succeeded")
				}
				if attempts != 1 {
					t.Fatalf("Upload() attempts=%d, want 1 for status %d", attempts, status)
				}
			}
		})
	}
}

func TestDownloadRejectsExpiredCredentialsWithoutRequestingOSS(t *testing.T) {
	metadataServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/latest/api/token" {
			_, _ = writer.Write([]byte("token"))
			return
		}
		_, _ = writer.Write([]byte(`{"AccessKeyId":"id","AccessKeySecret":"secret","SecurityToken":"token","Expiration":"2026-07-26T23:59:59Z","Code":"Success","LastUpdated":"2026-07-26T23:00:00Z"}`))
	}))
	defer metadataServer.Close()
	objectServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("OSS must not be called with expired credentials")
	}))
	defer objectServer.Close()
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: objectServer.URL, Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: objectServer.Client(), MetadataBaseURL: metadataServer.URL, Clock: func() time.Time { return time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC) }})
	_, err := client.Download(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("Download() error = %v, want expired credentials", err)
	}
}

func TestDownloadRejectsInvalidCredentialsResponse(t *testing.T) {
	for name, body := range map[string]string{
		"failure code":  `{"AccessKeyId":"id","AccessKeySecret":"not-for-errors","SecurityToken":"not-for-errors","Expiration":"2026-07-27T01:00:00Z","Code":"Failure","LastUpdated":"2026-07-27T00:00:00Z"}`,
		"unknown field": `{"AccessKeyId":"id","AccessKeySecret":"not-for-errors","SecurityToken":"not-for-errors","Expiration":"2026-07-27T01:00:00Z","Code":"Success","LastUpdated":"2026-07-27T00:00:00Z","Unexpected":"value"}`,
	} {
		t.Run(name, func(t *testing.T) {
			metadataServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/latest/api/token" {
					_, _ = writer.Write([]byte("token"))
					return
				}
				_, _ = writer.Write([]byte(body))
			}))
			defer metadataServer.Close()
			client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal.example", Bucket: "bucket-name", Key: "object", MaxBytes: 1}, Dependencies{MetadataBaseURL: metadataServer.URL, Clock: func() time.Time { return time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC) }})
			_, err := client.Download(context.Background(), &bytes.Buffer{})
			if err == nil || strings.Contains(err.Error(), "not-for-errors") {
				t.Fatalf("Download() error = %v", err)
			}
		})
	}
}

func TestDownloadRejectsMetadataErrorsAndOversizedObjects(t *testing.T) {
	t.Run("metadata status", testPermanentMetadataStatus)
	t.Run("object exceeds configured maximum", testOversizedObject)
	t.Run("object redirect", testObjectRedirect)
}

func TestDownloadRetriesTransientOSSStatusWithoutAppendingPartialData(t *testing.T) {
	now := time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)
	metadataServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/latest/api/token" {
			_, _ = writer.Write([]byte("token"))
			return
		}
		_, _ = writer.Write([]byte(`{"AccessKeyId":"id","AccessKeySecret":"secret","SecurityToken":"token","Expiration":"2026-07-27T01:00:00Z","Code":"Success","LastUpdated":"2026-07-27T00:00:00Z"}`))
	}))
	defer metadataServer.Close()
	attempts := 0
	objectServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write([]byte("complete-object"))
	}))
	defer objectServer.Close()
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: objectServer.URL, Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: objectServer.Client(), MetadataBaseURL: metadataServer.URL, Clock: func() time.Time { return now }})
	client.wait = noWait
	var destination bytes.Buffer
	size, err := client.Download(context.Background(), &destination)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if attempts != 2 || size != int64(len("complete-object")) || destination.String() != "complete-object" {
		t.Fatalf("attempts=%d size=%d destination=%q", attempts, size, destination.String())
	}
}

func TestDownloadStopsWhenContextIsCanceled(t *testing.T) {
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal.example", Bucket: "bucket-name", Key: "object", MaxBytes: 1}, Dependencies{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Download(ctx, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "attempt 1") {
		t.Fatalf("Download() error = %v", err)
	}
}

func TestDownloadRetriesSingleRequestDeadlineWhileParentIsActive(t *testing.T) {
	for name, timeoutPath := range map[string]string{
		"metadata token":  "/latest/api/token",
		"STS credentials": "/latest/meta-data/ram/security-credentials/worker-role",
	} {
		t.Run(name, func(t *testing.T) {
			testSingleRequestDeadlineRetry(t, timeoutPath)
		})
	}
}

func TestRetryDelayMatchesCloudRetryContract(t *testing.T) {
	want := expectedRetryDelays()
	for index, delay := range want {
		attempt := index + 1
		if got := retryDelay(attempt); got != delay {
			t.Fatalf("retryDelay(%d) = %s, want %s", attempt, got, delay)
		}
	}
}

func TestRetryableDownloadErrorDistinguishesTransientAndPermanentFailures(t *testing.T) {
	if !retryableDownloadError(context.Background(), io.ErrUnexpectedEOF) {
		t.Fatal("unexpected EOF must be retried")
	}
	tlsTimeout := &url.Error{Op: http.MethodGet, URL: "https://oss.internal", Err: timeoutNetworkError("TLS handshake timeout")}
	if !retryableDownloadError(context.Background(), tlsTimeout) {
		t.Fatal("TLS handshake timeout must be retried")
	}
	certificateError := &url.Error{Op: http.MethodGet, URL: "https://oss.internal", Err: x509.UnknownAuthorityError{Cert: &x509.Certificate{}}}
	if retryableDownloadError(context.Background(), certificateError) {
		t.Fatal("unknown certificate authority must not be retried")
	}
}

func TestDownloadSucceedsOnTwelfthTransientAttempt(t *testing.T) {
	for _, stage := range transientRetryStages() {
		t.Run(stage.name, func(t *testing.T) {
			testTwelfthTransientAttempt(t, stage)
		})
	}
}

func TestDownloadReportsTwelveAttemptExhaustion(t *testing.T) {
	tokenAttempts := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		tokenAttempts++
		return nil, context.DeadlineExceeded
	})
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal", Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: &http.Client{Transport: transport}, MetadataBaseURL: "http://metadata.internal"})
	var delays []time.Duration
	client.wait = func(ctx context.Context, delay time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		delays = append(delays, delay)
		return nil
	}
	var destination bytes.Buffer
	_, err := client.Download(context.Background(), &destination)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "failed after 12 attempt(s)") {
		t.Fatalf("Download() error = %v", err)
	}
	if tokenAttempts != maxDownloadAttempts || destination.Len() != 0 {
		t.Fatalf("token attempts=%d destination=%q", tokenAttempts, destination.String())
	}
	assertRetryDelays(t, delays)
}

func TestDownloadStopsWhenParentIsCanceledDuringRetryWait(t *testing.T) {
	tokenAttempts := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		tokenAttempts++
		return nil, context.DeadlineExceeded
	})
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal", Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: &http.Client{Transport: transport}, MetadataBaseURL: "http://metadata.internal"})
	ctx, cancel := context.WithCancel(context.Background())
	waitAttempts := 0
	client.wait = func(waitContext context.Context, delay time.Duration) error {
		waitAttempts++
		cancel()
		<-waitContext.Done()
		return waitContext.Err()
	}
	_, err := client.Download(ctx, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "retry wait after attempt 1") {
		t.Fatalf("Download() error = %v", err)
	}
	if tokenAttempts != 1 || waitAttempts != 1 {
		t.Fatalf("token attempts=%d wait attempts=%d", tokenAttempts, waitAttempts)
	}
}

func TestDownloadStopsOnParentDeadlineWithoutRetry(t *testing.T) {
	parent := newControlledDeadlineContext()
	started := make(chan struct{})
	attempts := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal", Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: &http.Client{Transport: transport}, MetadataBaseURL: "http://metadata.internal"})
	result := make(chan error, 1)
	safego.Go(parent, nil, "workerio.test-download-parent-deadline", func(context.Context) {
		_, err := client.Download(parent, &bytes.Buffer{})
		result <- err
	})
	<-started
	parent.expire()
	err := <-result
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 || !strings.Contains(err.Error(), "parent context ended during attempt 1") {
		t.Fatalf("Download() attempts=%d error=%v", attempts, err)
	}
}

func TestDownloadAllowsVirtualLongObjectBodyWithinMaterializeContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	transport := &virtualLongBodyTransport{started: started, release: release}
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal", Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: &http.Client{Transport: transport}, MetadataBaseURL: "http://metadata.internal", Clock: func() time.Time { return time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC) }})
	if client.ossHTTPClient.Timeout != 0 {
		t.Fatalf("OSS client timeout = %s, want no total-body timeout", client.ossHTTPClient.Timeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var destination bytes.Buffer
	result := make(chan error, 1)
	safego.Go(ctx, nil, "workerio.test-download-virtual-long-body", func(context.Context) {
		_, err := client.Download(ctx, &destination)
		result <- err
	})
	<-started
	if transport.requestHasShortDeadline {
		t.Fatal("OSS request unexpectedly has a short total deadline")
	}
	assertDownloadPending(t, result)
	close(release)
	assertVirtualLongBodyResult(t, result, &destination)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type gatedReadCloser struct {
	started chan<- struct{}
	release <-chan struct{}
	data    []byte
	read    bool
}

func (reader *gatedReadCloser) Read(destination []byte) (int, error) {
	if !reader.read {
		reader.read = true
		close(reader.started)
		<-reader.release
	}
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	count := copy(destination, reader.data)
	reader.data = reader.data[count:]
	return count, nil
}

func (*gatedReadCloser) Close() error { return nil }

func textResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func noWait(context.Context, time.Duration) error { return nil }

type timeoutNetworkError string

func (err timeoutNetworkError) Error() string {
	return string(err)
}

func (timeoutNetworkError) Timeout() bool {
	return true
}

func (timeoutNetworkError) Temporary() bool {
	return true
}

func expectedRetryDelays() []time.Duration {
	return []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
	}
}

func assertRetryDelays(t *testing.T, got []time.Duration) {
	t.Helper()
	want := expectedRetryDelays()
	if len(got) != len(want) {
		t.Fatalf("retry delay count = %d, want %d: %v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("retry delay %d = %s, want %s", index+1, got[index], want[index])
		}
	}
}

type controlledDeadlineContext struct {
	context.Context
	done     chan struct{}
	deadline time.Time
}

func newControlledDeadlineContext() *controlledDeadlineContext {
	return &controlledDeadlineContext{Context: context.Background(), done: make(chan struct{}), deadline: time.Now().Add(time.Hour)}
}

func (ctx *controlledDeadlineContext) Deadline() (time.Time, bool) {
	return ctx.deadline, true
}

func (ctx *controlledDeadlineContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *controlledDeadlineContext) Err() error {
	select {
	case <-ctx.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (ctx *controlledDeadlineContext) expire() {
	close(ctx.done)
}

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	valid := Config{RoleName: "worker-role", Endpoint: "https://oss.internal.example", Bucket: "bucket-name", Key: "object", MaxBytes: 1}
	for name, mutate := range map[string]func(*Config){
		"role":     func(config *Config) { config.RoleName = "../role" },
		"endpoint": func(config *Config) { config.Endpoint = "http://oss.internal.example" },
		"bucket":   func(config *Config) { config.Bucket = "Bad_Bucket" },
		"key":      func(config *Config) { config.Key = "/object" },
		"size":     func(config *Config) { config.MaxBytes = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if _, err := NewClient(config, Dependencies{}); err == nil {
				t.Fatal("NewClient() error = nil")
			}
		})
	}
}

func newTestClient(t *testing.T, config Config, dependencies Dependencies) *Client {
	t.Helper()
	client, err := NewClient(config, dependencies)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
