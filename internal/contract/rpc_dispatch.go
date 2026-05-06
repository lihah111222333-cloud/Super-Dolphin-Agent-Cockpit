package contract

import (
	"context"
	"encoding/json"
)

// RPCDispatcher is the narrow interface consumed by frontends (e.g.
// ui/wails) that need to dispatch JRPC calls without importing the
// concrete *rpc.Server. Satisfied by *rpc.Server.
type RPCDispatcher interface {
	Dispatch(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}
