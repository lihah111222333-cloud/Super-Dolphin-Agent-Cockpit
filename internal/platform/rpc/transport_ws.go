package rpc

import (
	"io"
	"net/http"
	"sync"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	"github.com/gorilla/websocket"
)

var defaultWSUpgrader = websocket.Upgrader{}

var _ channel.Channel = (*wsChannel)(nil)

// WSHandler bridges a websocket connection into a jrpc2 channel.
func WSHandler(server *Server, opts *jrpc2.ServerOptions) http.Handler {
	mux := handler.Map{}
	if server != nil && server.methods != nil {
		mux = server.methods
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := defaultWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ch := newWSChannel(conn)
		var rpcLog jrpc2.RPCLogger
		tracker := (*rpcRequestTracker)(nil)
		if server != nil {
			tracker = newRPCRequestTracker(server.logger)
			rpcLog = tracker
		}
		srv := jrpc2.NewServer(mux, prepareServerOptions(rpcLog, opts)).Start(ch)
		if server != nil {
			server.addActive(srv, dto.PeerKindUI)
			defer server.removeActive(srv)
			server.notifyConnected(srv)
		}
		defer srv.Stop()
		defer ch.Close()
		if err := srv.Wait(); err != nil && !isExpectedCloseErr(err) {
			if tracker != nil {
				tracker.logConnectionExit(err)
			}
			deadline := time.Now().Add(time.Second)
			msg := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, http.StatusText(http.StatusInternalServerError))
			_ = conn.WriteControl(websocket.CloseMessage, msg, deadline)
		}
	})
}

type wsChannel struct {
	conn      *websocket.Conn
	sendMu    sync.Mutex
	closeOnce sync.Once
}

func newWSChannel(conn *websocket.Conn) *wsChannel {
	return &wsChannel{conn: conn}
}

func (c *wsChannel) Send(msg []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		return sendWSError(err)
	}
	return nil
}

func (c *wsChannel) Recv() ([]byte, error) {
	for {
		msgType, msg, err := c.conn.ReadMessage()
		if err != nil {
			return nil, recvWSError(err)
		}
		if msgType == websocket.TextMessage || msgType == websocket.BinaryMessage {
			return msg, nil
		}
	}
}

func (c *wsChannel) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.conn.Close()
	})
	if err != nil {
		return sendWSError(err)
	}
	return nil
}

func recvWSError(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || isExpectedCloseErr(err) {
		return io.EOF
	}
	return err
}

func sendWSError(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || isExpectedCloseErr(err) {
		return channel.ErrClosed
	}
	return err
}
