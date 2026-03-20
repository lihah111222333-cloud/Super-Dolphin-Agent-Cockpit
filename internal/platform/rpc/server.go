package rpc

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
)

type Server struct {
	logger  *slog.Logger
	addr    string
	methods handler.Map

	mu     sync.RWMutex
	active map[*jrpc2.Server]struct{}
}

func NewServer(p Params) *Server {
	return &Server{
		logger:  p.Logger,
		addr:    p.Config.RPCAddr,
		methods: handler.Map{},
		active:  make(map[*jrpc2.Server]struct{}),
	}
}

func (s *Server) Register(maps ...handler.Map) {
	for _, current := range maps {
		for name, handlerFunc := range current {
			s.methods[name] = handlerFunc
		}
	}
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
	s.addActive(srv)
	defer s.removeActive(srv)

	go func() {
		<-connCtx.Done()
		srv.Stop()
	}()

	stat := srv.WaitStatus()
	if stat.Err != nil && !errors.Is(stat.Err, context.Canceled) && !errors.Is(stat.Err, net.ErrClosed) && !channel.IsErrClosing(stat.Err) {
		s.logger.Warn("rpc connection exited", "error", stat.Err)
	}
}

func (s *Server) addActive(srv *jrpc2.Server) {
	if srv == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[srv] = struct{}{}
}

func (s *Server) removeActive(srv *jrpc2.Server) {
	if srv == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, srv)
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

func prepareServerOptions(opts *jrpc2.ServerOptions) *jrpc2.ServerOptions {
	if opts == nil {
		return &jrpc2.ServerOptions{
			AllowPush: true,
			Logger:    jrpc2.StdLogger(nil),
		}
	}
	dup := *opts
	if dup.Logger == nil {
		dup.Logger = jrpc2.StdLogger(nil)
	}
	dup.AllowPush = true
	return &dup
}
