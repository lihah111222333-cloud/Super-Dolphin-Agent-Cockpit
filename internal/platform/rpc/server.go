package rpc

import (
	"context"
	"encoding/json"
	"errors"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"maps"
	"net"
	"sync"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
)

type Server struct {
	logger  *pkglogger.Logger
	addr    string
	methods handler.Map

	mu         sync.RWMutex
	active     map[*jrpc2.Server]string
	onConnects []func(*jrpc2.Server)
}

func NewServer(p Params) *Server {
	return &Server{
		logger:  p.Logger,
		addr:    p.Config.RPCAddr,
		methods: handler.Map{},
		active:  make(map[*jrpc2.Server]string),
	}
}

func (s *Server) Register(handlerMaps ...handler.Map) {
	for _, current := range handlerMaps {
		maps.Copy(s.methods, current)
	}
}

// Dispatch executes a registered handler locally without using the network.
// It is used by the Wails binding layer to bridge CallAPI requests.
func (s *Server) Dispatch(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	local := jrpcserver.NewLocal(s.methods, &jrpcserver.LocalOptions{
		Server: prepareServerOptions(nil),
	})
	defer local.Close()

	var callParams any
	if len(params) != 0 {
		callParams = append(json.RawMessage(nil), params...)
	}

	var result json.RawMessage
	if err := local.Client.CallResult(ctx, method, callParams, &result); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), result...), nil
}

func (s *Server) NotifyAll(ctx context.Context, bridge *PushBridge, method string, params any) {
	if bridge == nil {
		return
	}
	for _, current := range s.snapshotActive() {
		if err := bridge.NotifyClient(ctx, current, method, params); err != nil {
			s.logger.Warn("rpc push notify failed", "method", method, "error", err)
		}
	}
}

func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	s.logger.Info("rpc server listening", "addr", listener.Addr().String())
	err = s.acceptLoop(ctx, jrpcserver.NetAccepter(listener, channel.Line))
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) && !channel.IsErrClosing(err) {
		return err
	}
	return nil
}

func (s *Server) acceptLoop(ctx context.Context, accepter jrpcserver.Accepter) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	opts := prepareServerOptions(nil)
	for {
		ch, err := accepter.Accept(ctx)
		if err != nil {
			return err
		}
		wg.Add(1)
		go s.serveConn(ctx, ch, opts, &wg)
	}
}

func (s *Server) serveConn(ctx context.Context, ch channel.Channel, opts *jrpc2.ServerOptions, wg *sync.WaitGroup) {
	defer wg.Done()

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	srv := jrpc2.NewServer(s.methods, opts).Start(ch)
	s.addActive(srv, dto.PeerKindTool)
	defer s.removeActive(srv)
	s.notifyConnected(srv)

	platformshared.SafeGo(s.logger, func() {
		<-connCtx.Done()
		srv.Stop()
	})

	stat := srv.WaitStatus()
	if stat.Err != nil && !errors.Is(stat.Err, context.Canceled) && !errors.Is(stat.Err, net.ErrClosed) && !channel.IsErrClosing(stat.Err) {
		s.logger.Warn("rpc connection exited", "error", stat.Err)
	}
}

func (s *Server) addActive(srv *jrpc2.Server, peerKind string) {
	if srv == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[srv] = peerKind
}

func (s *Server) removeActive(srv *jrpc2.Server) {
	if srv == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, srv)
}

func (s *Server) OnConnect(fn func(*jrpc2.Server)) {
	if s == nil || fn == nil {
		return
	}
	for _, current := range s.addOnConnect(fn) {
		fn(current)
	}
}

func (s *Server) OnConnectUI(fn func(*jrpc2.Server)) {
	if s == nil || fn == nil {
		return
	}
	s.OnConnect(func(current *jrpc2.Server) {
		if s.PeerKind(current) == dto.PeerKindUI {
			fn(current)
		}
	})
}

func (s *Server) notifyConnected(srv *jrpc2.Server) {
	for _, hook := range s.snapshotOnConnects() {
		hook(srv)
	}
}

func (s *Server) PeerKind(srv *jrpc2.Server) string {
	if s == nil || srv == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active[srv]
}

func (s *Server) snapshotActive() []*jrpc2.Server {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*jrpc2.Server, 0, len(s.active))
	for current := range s.active {
		out = append(out, current)
	}
	return out
}

func (s *Server) snapshotOnConnects() []func(*jrpc2.Server) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]func(*jrpc2.Server){}, s.onConnects...)
}

func (s *Server) addOnConnect(fn func(*jrpc2.Server)) []*jrpc2.Server {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.onConnects = append(s.onConnects, fn)
	out := make([]*jrpc2.Server, 0, len(s.active))
	for current := range s.active {
		out = append(out, current)
	}
	return out
}

func prepareServerOptions(opts *jrpc2.ServerOptions) *jrpc2.ServerOptions {
	if opts == nil {
		return &jrpc2.ServerOptions{
			AllowPush: true,
		}
	}
	dup := *opts
	dup.AllowPush = true
	return &dup
}
