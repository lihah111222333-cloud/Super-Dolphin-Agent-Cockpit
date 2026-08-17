//go:build windows

package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWindowsAssetCacheRetriesTruncatedResponseBody(t *testing.T) {
	payload := []byte("kotlin-arm64-asset-complete")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			writer.Header().Set("Content-Length", stringContentLength(payload))
			_, _ = writer.Write(payload[:9])
			return
		}
		writer.Header().Set("Content-Length", stringContentLength(payload))
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(cache.root, "payload.bin")
	asset := windowsTestLockedAsset(server.URL+"/kotlin.zip", payload)
	if err := cache.downloadPayload(context.Background(), destination, asset); err != nil {
		t.Fatalf("downloadPayload() after truncated response: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("HTTP attempts=%d, want 2", got)
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("published payload=%q error=%v, want complete payload", got, err)
	}
}

func TestWindowsAssetCacheReportsExhaustedTruncatedResponse(t *testing.T) {
	payload := []byte("kotlin-arm64-asset-complete")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Length", stringContentLength(payload))
		_, _ = writer.Write(payload[:9])
	}))
	defer server.Close()

	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(cache.root, "payload.bin")
	err = cache.downloadPayload(context.Background(), destination, windowsTestLockedAsset(server.URL+"/kotlin.zip", payload))
	if err == nil || requests.Load() != lockedAssetDownloadAttempts {
		t.Fatalf("downloadPayload() error=%v attempts=%d, want final transfer error and %d attempts", err, requests.Load(), lockedAssetDownloadAttempts)
	}
	for _, marker := range []string{"attempt=3/3", "phase=copy_response_body", "received_bytes=9", "expected_bytes=27"} {
		if !strings.Contains(err.Error(), marker) {
			t.Fatalf("error=%v missing %q", err, marker)
		}
	}
	if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("truncated destination exists; stat error=%v", statErr)
	}
}

func TestWindowsAssetCacheDoesNotRetryLocalWriterOrCanceledRead(t *testing.T) {
	localWriter := &windowsTestFailingWriter{}
	_, err := copyLockedAssetPayload(localWriter, io.Discard, strings.NewReader("payload"), 1024)
	var localErr *windowsLockedAssetLocalWriteError
	if !errors.As(err, &localErr) || windowsLockedAssetRetryable(context.Background(), err) {
		t.Fatalf("local writer error=%v retryable=%t, want non-retryable local error", err, windowsLockedAssetRetryable(context.Background(), err))
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if windowsLockedAssetRetryable(canceled, &windowsLockedAssetTransferError{retryable: true, cause: io.ErrUnexpectedEOF}) {
		t.Fatal("canceled transfer must not be retried")
	}
}

type windowsTestFailingWriter struct{}

func (*windowsTestFailingWriter) Write([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func windowsTestLockedAsset(rawURL string, payload []byte) WindowsLockedAsset {
	digest := sha256.Sum256(payload)
	return WindowsLockedAsset{Architecture: WindowsHostArchARM64, Version: "test", URL: rawURL, SHA256: hex.EncodeToString(digest[:]), Format: WindowsLockedAssetFormatRaw, BinaryPath: "payload.bin"}
}

func stringContentLength(payload []byte) string { return fmtInt(len(payload)) }

func fmtInt(value int) string {
	return strconv.Itoa(value)
}
