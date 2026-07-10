package summarysuggest

import (
	"errors"
	"testing"
)

func TestRetryableErrorMarker(t *testing.T) {
	t.Parallel()

	original := errors.New("parse skill summary suggestion: unexpected end of JSON input")
	marked := MarkRetryable(original)
	if !IsRetryable(marked) {
		t.Fatal("marked error must be retryable")
	}
	if !errors.Is(marked, original) {
		t.Fatal("marked error must preserve the original error")
	}
	if IsRetryable(errors.New("parse skill summary suggestion: unexpected end of JSON input")) {
		t.Fatal("matching error text alone must not be retryable")
	}
}
