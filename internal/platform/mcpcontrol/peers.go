package mcpcontrol

import (
	"context"
	"encoding/json"

	"github.com/creachadair/jrpc2"
)

func peerFromContext(ctx context.Context) (peer Peer, err error) {
	_, peer, err = resolveServerPeer(ctx)
	return peer, err
}

type jrpcPeer struct {
	server *jrpc2.Server
}

// Notify 发送通知消息。
func (p jrpcPeer) Notify(ctx context.Context, method string, params any) error {
	return p.server.Notify(ctx, method, params)
}

// Callback 发送回调消息。
func (p jrpcPeer) Callback(ctx context.Context, method string, params any, result any) error {
	resp, err := p.server.Callback(ctx, method, params)
	if err != nil {
		return err
	}
	if resp == nil || result == nil {
		return nil
	}
	if raw, ok := result.(*json.RawMessage); ok {
		return resp.UnmarshalResult(raw)
	}
	return resp.UnmarshalResult(result)
}

// Close 关闭平台mcpcontrol资源。
func (p jrpcPeer) Close() error {
	if p.server != nil {
		p.server.Stop()
	}
	return nil
}

func closePeer(peer Peer) {
	if peer != nil {
		_ = peer.Close()
	}
}
