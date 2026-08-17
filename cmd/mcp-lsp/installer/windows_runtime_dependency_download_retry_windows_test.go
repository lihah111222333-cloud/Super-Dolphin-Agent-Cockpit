//go:build windows

package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type windowsRuntimeDependencyRetryRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip windowsRuntimeDependencyRetryRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type windowsRuntimeDependencyUnexpectedEOFBody struct {
	payload []byte
	read    bool
}

func (body *windowsRuntimeDependencyUnexpectedEOFBody) Read(target []byte) (int, error) {
	if body.read {
		return 0, io.ErrUnexpectedEOF
	}
	body.read = true
	count := copy(target, body.payload)
	return count, io.ErrUnexpectedEOF
}

func (body *windowsRuntimeDependencyUnexpectedEOFBody) Close() error { return nil }

// TestWindowsRuntimeDependencyDownloadRetriesUnexpectedEOF 证明 Windows 固定资产 body
// 被截断时会清理本次临时文件并从头重试，最终仍须通过同一个锁定摘要才能发布。
func TestWindowsRuntimeDependencyDownloadRetriesUnexpectedEOF(t *testing.T) {
	payload := []byte("complete-windows-arm64-runtime-asset")
	attempts := 0
	client := &http.Client{Transport: windowsRuntimeDependencyRetryRoundTrip(func(request *http.Request) (*http.Response, error) {
		attempts++
		if request.Method != http.MethodGet || request.UserAgent() != windowsAssetHTTPUserAgent {
			t.Fatalf("fixed asset request method/user-agent = %q/%q", request.Method, request.UserAgent())
		}
		body := io.ReadCloser(io.NopCloser(bytes.NewReader(payload)))
		if attempts == 1 {
			body = &windowsRuntimeDependencyUnexpectedEOFBody{payload: payload[:9]}
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Body: body,
			ContentLength: int64(len(payload)), Header: make(http.Header), Request: request,
		}, nil
	})}
	directory := t.TempDir()
	destination := filepath.Join(directory, "asset.zip")
	if err := defaultWindowsRuntimeDependencyAssetFetcher(client)(context.Background(), windowsRuntimeDependencyRetryAsset(payload), destination); err != nil {
		t.Fatalf("fetch fixed asset after truncated first response: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("fixed asset attempts=%d, want exactly 2", attempts)
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("published fixed asset = %q, %v; want complete payload", got, err)
	}
	windowsRuntimeDependencyRequireNoDownloadTemporary(t, directory)
}

// TestWindowsRuntimeDependencyDownloadReportsExhaustedTransferFacts 锁定三次截断后的
// 根因日志，确保错误包含尝试次数与实际/期望字节数，并且不发布半截资产。
func TestWindowsRuntimeDependencyDownloadReportsExhaustedTransferFacts(t *testing.T) {
	payload := []byte("complete-runtime-asset")
	attempts := 0
	client := &http.Client{Transport: windowsRuntimeDependencyRetryRoundTrip(func(request *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK",
			Body:          &windowsRuntimeDependencyUnexpectedEOFBody{payload: payload[:7]},
			ContentLength: int64(len(payload)), Header: make(http.Header), Request: request,
		}, nil
	})}
	directory := t.TempDir()
	destination := filepath.Join(directory, "asset.zip")
	err := defaultWindowsRuntimeDependencyAssetFetcher(client)(context.Background(), windowsRuntimeDependencyRetryAsset(payload), destination)
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("exhausted fixed asset error=%v, want wrapped unexpected EOF", err)
	}
	if attempts != runtimeDependencyAssetDownloadAttempts {
		t.Fatalf("fixed asset attempts=%d, want %d", attempts, runtimeDependencyAssetDownloadAttempts)
	}
	for _, marker := range []string{"attempts=3/3", "operation=copy_response_body", "received_bytes=7", "expected_bytes=22", "retryable=true"} {
		if !strings.Contains(err.Error(), marker) {
			t.Fatalf("exhausted fixed asset error lacks %q: %v", marker, err)
		}
	}
	if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("truncated fixed asset destination exists; stat error=%v", statErr)
	}
	windowsRuntimeDependencyRequireNoDownloadTemporary(t, directory)
}

// TestWindowsRuntimeDependencyDownloadDoesNotRetryPermanentStatus 证明固定 4xx
// 不会被暂态重试政策掩盖，权限/参数类服务端拒绝仍立即 fail-fast。
func TestWindowsRuntimeDependencyDownloadDoesNotRetryPermanentStatus(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: windowsRuntimeDependencyRetryRoundTrip(func(request *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("missing")),
			ContentLength: 7, Header: make(http.Header), Request: request,
		}, nil
	})}
	err := defaultWindowsRuntimeDependencyAssetFetcher(client)(context.Background(), windowsRuntimeDependencyRetryAsset([]byte("expected")), filepath.Join(t.TempDir(), "asset.zip"))
	if err == nil || attempts != 1 || !strings.Contains(err.Error(), "status=404") || !strings.Contains(err.Error(), "attempts=1/3") {
		t.Fatalf("permanent HTTP status error=%v attempts=%d", err, attempts)
	}
}

// TestWindowsRuntimeDependencyCopyRetryClassifiesConnectionReset 锁定 WinSock 连接重置的
// 重试语义，同时证明调用方取消与普通本地写入错误不会被误判为可重试传输故障。
func TestWindowsRuntimeDependencyCopyRetryClassifiesConnectionReset(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: syscall.Errno(10054)}
	if !windowsRuntimeDependencyAssetCopyRetryable(context.Background(), reset) {
		t.Fatal("WSAECONNRESET response-body read must be retryable")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if windowsRuntimeDependencyAssetCopyRetryable(canceled, reset) {
		t.Fatal("canceled download context must stop retrying")
	}
	if windowsRuntimeDependencyAssetCopyRetryable(context.Background(), errors.New("local disk write failed")) {
		t.Fatal("ordinary local write error must remain non-retryable")
	}
}

func windowsRuntimeDependencyRetryAsset(payload []byte) WindowsRuntimeDependencyAsset {
	digest := sha256.Sum256(payload)
	return WindowsRuntimeDependencyAsset{
		Component: "dotnet-sdk", Version: "test-arm64", URL: "https://example.invalid/windows-arm64.zip",
		ChecksumAlgorithm: WindowsRuntimeDependencyChecksumSHA256, Checksum: hex.EncodeToString(digest[:]),
	}
}

func windowsRuntimeDependencyRequireNoDownloadTemporary(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read fixed asset directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".download-") {
			t.Fatalf("fixed asset temporary file survived retry: %s", entry.Name())
		}
	}
}
