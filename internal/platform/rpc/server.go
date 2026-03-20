package rpc

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/server"
)

type Server struct {
	logger  *slog.Logger
	addr    string
	methods handler.Map
}

func NewServer(p Params) *Server {
	return &Server{
		logger:  p.Logger,
		addr:    p.Config.RPCAddr,
		methods: handler.Map{},
	}
}

func (s *Server) Register(maps ...handler.Map) {
	for _, current := range maps {
		for name, handlerFunc := range current {
			s.methods[name] = handlerFunc
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

	err = server.Loop(
		ctx,
		server.NetAccepter(listener, channel.Line),
		server.Static(s.methods),
		&server.LoopOptions{
			ServerOptions: prepareServerOptions(nil),
		},
	)
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) && !channel.IsErrClosing(err) {
		return err
	}
	return nil
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
