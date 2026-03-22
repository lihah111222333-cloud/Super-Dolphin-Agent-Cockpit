package mcpcontrol

import (
	"context"
	"encoding/json"

	"github.com/creachadair/jrpc2"
)

func peerFromContext(ctx context.Context) (peer Peer, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errPeerUnavailable("mcp control request must run inside a jrpc2 handler")
			peer = nil
		}
	}()
	server := jrpc2.ServerFromContext(ctx)
	if server == nil {
		return nil, errPeerUnavailable("mcp control peer is not available")
	}
	return jrpcPeer{server: server}, nil
}

type jrpcPeer struct {
	server *jrpc2.Server
}

func (p jrpcPeer) Notify(ctx context.Context, method string, params any) error {
	return p.server.Notify(ctx, method, params)
}

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
