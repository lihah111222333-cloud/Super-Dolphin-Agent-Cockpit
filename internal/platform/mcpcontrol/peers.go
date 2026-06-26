package mcpcontrol

import (
	"context"
	"encoding/json"

	"github.com/creachadair/jrpc2"
)

// peerFromContext 解析当前 jrpc2 handler 的反向 peer，用于注册请求绑定连接。
func peerFromContext(ctx context.Context) (peer Peer, err error) {
	_, peer, err = resolveServerPeer(ctx)
	return peer, err
}

// jrpcPeer 用 jrpc2.Server 封装 Peer 接口，所有回调都回到当前已注册连接。
type jrpcPeer struct {
	server *jrpc2.Server
}

// Notify 通过 jrpc2 notification 向 peer 发送无返回值消息。
func (p jrpcPeer) Notify(ctx context.Context, method string, params any) error {
	return p.server.Notify(ctx, method, params)
}

// Callback 通过 jrpc2 request 调用 peer，并按调用方提供的 result 类型解码响应。
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

// Close 停止底层 jrpc2 server，重复或 nil server 调用保持无副作用。
func (p jrpcPeer) Close() error {
	if p.server != nil {
		p.server.Stop()
	}
	return nil
}

// closePeer 吞掉关闭错误，控制面断链路径只负责尽力释放底层连接。
func closePeer(peer Peer) {
	if peer != nil {
		_ = peer.Close()
	}
}
