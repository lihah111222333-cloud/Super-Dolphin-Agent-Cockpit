package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestThreadListPageHandlerRejectsTimestampOnlyCursor(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"thread/listPage", "thread/loaded/listPage"} {
		server := newThreadTestServer(&stubThreadService{})
		_, err := server.Dispatch(
			context.Background(),
			method,
			json.RawMessage(`{"limit":2,"cursor_created_at":123}`),
		)
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.Code(contract.CodeInvalidParams) {
			t.Fatalf("Dispatch(%s) error = %v, want invalid params", method, err)
		}
		if !strings.Contains(rpcErr.Message, "cursor_thread_id is required") {
			t.Fatalf("Dispatch(%s) message = %q, want cursor pair error", method, rpcErr.Message)
		}
	}
}
