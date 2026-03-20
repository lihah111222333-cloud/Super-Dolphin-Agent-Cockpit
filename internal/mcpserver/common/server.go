package common

import (
	"context"
	"io"
)

type Server struct {
	name   string
	stdin  io.Reader
	stdout io.Writer
}

func NewServer(name string, stdin io.Reader, stdout io.Writer) *Server {
	return &Server{name: name, stdin: stdin, stdout: stdout}
}

func (s *Server) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
