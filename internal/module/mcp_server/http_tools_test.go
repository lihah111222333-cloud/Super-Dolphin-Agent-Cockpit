package mcpserver

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestDecodeMCPHTTPRPCResultWrapsJSONSyntaxError verifies malformed envelopes preserve decoder detail.
func TestDecodeMCPHTTPRPCResultWrapsJSONSyntaxError(t *testing.T) {
	t.Parallel()

	_, err := decodeMCPHTTPRPCResult("tools/list", []byte("{"), true)
	if !errors.Is(err, errInvalidToolsResponse) {
		t.Fatalf("decodeMCPHTTPRPCResult() error = %v, want errInvalidToolsResponse", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("decodeMCPHTTPRPCResult() error = %v, want json.SyntaxError", err)
	}
}
