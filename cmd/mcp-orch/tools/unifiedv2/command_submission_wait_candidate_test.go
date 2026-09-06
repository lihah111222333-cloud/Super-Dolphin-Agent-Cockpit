package unifiedv2

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCommandSubmissionWaitCandidateFailsClosed(t *testing.T) {
	adapter, err := NewCommandSubmissionWaitAdapter()
	if adapter != nil || !errors.Is(err, ErrCommandSubmissionWaitDisabled) {
		t.Fatalf("NewCommandSubmissionWaitAdapter() = %#v, %v, want disabled candidate", adapter, err)
	}
	var nilAdapter *CommandSubmissionWaitAdapter
	if _, err := nilAdapter.Handle(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, ErrCommandSubmissionWaitDisabled) {
		t.Fatalf("nil candidate Handle() error = %v, want disabled", err)
	}
}
