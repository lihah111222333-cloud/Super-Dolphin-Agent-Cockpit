package workerio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUploadStreamsCreateOnlyAndReadbackVerifiesObject(t *testing.T) {
	const payload = "streamed-upload-payload"
	digestBytes := sha256.Sum256([]byte(payload))
	digest := hex.EncodeToString(digestBytes[:])
	now := workerIOTestNow()
	handler := &uploadReadbackHandler{t: t, now: now, payload: payload, digest: digest}
	objectServer := httptest.NewTLSServer(handler)
	t.Cleanup(objectServer.Close)
	metadataServer := httptest.NewServer(imdsV2Handler{t: t})
	t.Cleanup(metadataServer.Close)
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: objectServer.URL, Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: objectServer.Client(), MetadataBaseURL: metadataServer.URL, Clock: func() time.Time { return now }})
	if err := client.Upload(context.Background(), strings.NewReader(payload), int64(len(payload)), digest); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if string(handler.putBody) != payload {
		t.Fatalf("PUT body = %q", handler.putBody)
	}
	if got, want := strings.Join(handler.methods, ","), "PUT,HEAD,GET"; got != want {
		t.Fatalf("methods = %q, want %q", got, want)
	}
}

type uploadReadbackHandler struct {
	t       *testing.T
	now     time.Time
	payload string
	digest  string
	putBody []byte
	methods []string
}

func (handler *uploadReadbackHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.t.Helper()
	handler.methods = append(handler.methods, request.Method)
	if request.URL.Path != "/bucket-name/object" {
		handler.t.Errorf("object path = %q", request.URL.Path)
	}
	if request.Header.Get("x-oss-security-token") != "test-session-token" {
		handler.t.Errorf("missing security token")
	}
	switch request.Method {
	case http.MethodPut:
		handler.handlePut(writer, request)
	case http.MethodHead:
		handler.handleHead(writer)
	case http.MethodGet:
		handler.handleGet(writer)
	default:
		handler.t.Errorf("unexpected method %s", request.Method)
		writer.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (handler *uploadReadbackHandler) handlePut(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Date") != handler.now.Format(http.TimeFormat) {
		handler.t.Errorf("Date = %q", request.Header.Get("Date"))
	}
	wantAuthorization := ossAuthorizationWithHeaders(http.MethodPut, handler.now.Format(http.TimeFormat), "/bucket-name/object", temporaryCredentials{AccessKeyID: "test-access-key", AccessKeySecret: "test-secret", SecurityToken: "test-session-token"}, "x-oss-forbid-overwrite:true\nx-oss-meta-sha256:"+handler.digest+"\nx-oss-meta-size:23\n")
	if request.Header.Get("Authorization") != wantAuthorization {
		handler.t.Errorf("Authorization = %q, want %q", request.Header.Get("Authorization"), wantAuthorization)
	}
	if request.Header.Get("x-oss-forbid-overwrite") != "true" {
		handler.t.Errorf("forbid-overwrite = %q", request.Header.Get("x-oss-forbid-overwrite"))
	}
	if request.Header.Get("x-oss-meta-sha256") != handler.digest || request.Header.Get("x-oss-meta-size") != "23" {
		handler.t.Errorf("metadata sha=%q size=%q", request.Header.Get("x-oss-meta-sha256"), request.Header.Get("x-oss-meta-size"))
	}
	if request.ContentLength != int64(len(handler.payload)) {
		handler.t.Errorf("Content-Length = %d", request.ContentLength)
	}
	var err error
	handler.putBody, err = io.ReadAll(request.Body)
	if err != nil {
		handler.t.Errorf("read PUT body: %v", err)
	}
	writer.WriteHeader(http.StatusCreated)
}

func (handler *uploadReadbackHandler) handleHead(writer http.ResponseWriter) {
	writer.Header().Set("x-oss-meta-sha256", handler.digest)
	writer.Header().Set("x-oss-meta-size", "23")
	writer.Header().Set("Content-Length", "23")
	writer.WriteHeader(http.StatusOK)
}

func (handler *uploadReadbackHandler) handleGet(writer http.ResponseWriter) {
	writer.Header().Set("x-oss-meta-sha256", handler.digest)
	writer.Header().Set("x-oss-meta-size", "23")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(handler.payload))
}

func TestUploadRejectsCreateOnlyConflictWithoutRetry(t *testing.T) {
	var puts int
	objectServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			puts++
		}
		writer.WriteHeader(http.StatusPreconditionFailed)
	}))
	t.Cleanup(objectServer.Close)
	metadataServer := httptest.NewServer(imdsV2Handler{t: t})
	t.Cleanup(metadataServer.Close)
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: objectServer.URL, Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{HTTPClient: objectServer.Client(), MetadataBaseURL: metadataServer.URL, Clock: workerIOTestNow})
	err := client.Upload(context.Background(), strings.NewReader("payload"), 7, strings.Repeat("a", 64))
	if err == nil || !strings.Contains(err.Error(), "412") {
		t.Fatalf("Upload() error = %v, want HTTP 412", err)
	}
	if puts != 1 {
		t.Fatalf("PUT attempts = %d, want 1", puts)
	}
}

func TestUploadRejectsInvalidInputsBeforeCredentials(t *testing.T) {
	client := newTestClient(t, Config{RoleName: "worker-role", Endpoint: "https://oss.internal.example", Bucket: "bucket-name", Key: "object", MaxBytes: 1024}, Dependencies{})
	tests := []struct {
		name   string
		source io.Reader
		size   int64
		digest string
	}{
		{name: "nil source", size: 1, digest: strings.Repeat("a", 64)},
		{name: "negative size", source: strings.NewReader("x"), size: -1, digest: strings.Repeat("a", 64)},
		{name: "oversized", source: strings.NewReader("x"), size: maxObjectBytes + 1, digest: strings.Repeat("a", 64)},
		{name: "invalid digest", source: strings.NewReader("x"), size: 1, digest: "not-a-sha256"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := client.Upload(context.Background(), test.source, test.size, test.digest); err == nil {
				t.Fatal("Upload() error = nil")
			}
		})
	}
}
