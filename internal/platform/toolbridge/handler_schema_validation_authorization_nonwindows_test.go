//go:build !windows

package toolbridge

import (
	"context"
	"errors"
	"testing"
)

func TestSchemaValidationAuthorizationIsNonWindowsNoOp(t *testing.T) {
	decision := (&Handler{}).handleSchemaValidationAuthorization(
		context.Background(), codexToolEntry{}, ToolCallRequest{CallID: "trusted"}, errors.New("permission"),
	)
	if decision.handled || decision.result != nil || decision.err != nil || decision.validationDone {
		t.Fatalf("non-Windows schema authorization decision = %#v, want strict no-op", decision)
	}
}
