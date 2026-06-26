package contract

import (
	"context"
	"encoding/json"
)

// RPCDispatcher 是前端层派发 JRPC 调用的窄接口。
// UI 通过该契约调用 RPC，不直接导入具体 *rpc.Server 实现。
type RPCDispatcher interface {
	Dispatch(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}
