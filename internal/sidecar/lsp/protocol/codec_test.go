package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestDecodeEnvelopeWrapsMalformedJSON verifies callers can match both the
// protocol sentinel and the underlying JSON decoder error.
func TestDecodeEnvelopeWrapsMalformedJSON(t *testing.T) {
	_, err := DecodeEnvelope([]byte(`{"jsonrpc":"2.0"`))
	if err == nil {
		t.Fatal("DecodeEnvelope succeeded for malformed JSON")
	}
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("error does not wrap ErrInvalidEnvelope: %v", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("error does not expose json.SyntaxError: %v", err)
	}
}
