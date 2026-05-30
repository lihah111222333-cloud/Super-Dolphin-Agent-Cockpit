package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestTimeoutUsesShorterToolLimitWhenParentDeadlineIsLonger(t *testing.T) {
	parentCtx, parentCancel := context.WithTimeout(context.Background(), time.Second)
	defer parentCancel()
	release := make(chan struct{})
	started := make(chan struct{})
	t.Cleanup(func() { close(release) })

	handler := Timeout(20 * time.Millisecond)(func(context.Context, json.RawMessage) (any, error) {
		close(started)
		<-release
		return map[string]any{"ok": true}, nil
	})

	done := make(chan error, 1)
	go func() {
		_, err := handler(parentCtx, nil)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not start")
	}
	select {
	case err := <-done:
		var coded *common.CodedToolError
		if !errors.As(err, &coded) {
			t.Fatalf("Timeout error = %T %v, want CodedToolError", err, err)
		}
		if coded.Code != "lsp_timeout" {
			t.Fatalf("Timeout code = %q, want lsp_timeout", coded.Code)
		}
	case <-time.After(120 * time.Millisecond):
		t.Fatal("Timeout did not return at shorter tool limit")
	}
}
