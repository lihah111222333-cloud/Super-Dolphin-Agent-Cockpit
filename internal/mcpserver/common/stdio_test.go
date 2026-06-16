package common

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

// TestStdioTransportReadMessageWrapsMalformedContentLength verifies malformed
// framed headers preserve the strconv parser error for callers that classify
// transport failures.
func TestStdioTransportReadMessageWrapsMalformedContentLength(t *testing.T) {
	transport := NewStdioTransport(strings.NewReader("Content-Length: nope\r\n\r\n{}"), io.Discard)

	_, err := transport.ReadMessage()
	if err == nil {
		t.Fatal("ReadMessage succeeded with malformed Content-Length")
	}
	var numErr *strconv.NumError
	if !errors.As(err, &numErr) {
		t.Fatalf("error does not expose strconv.NumError: %v", err)
	}
}
