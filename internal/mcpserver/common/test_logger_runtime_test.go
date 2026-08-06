package common

import (
	"io"
	"testing"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// newTestLoggerRuntime creates the explicit log runtime owner required by a server fixture.
func newTestLoggerRuntime() *pkglogger.Runtime {
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(io.Discard)
	return runtime
}

// newTestServer always supplies the server fixture's explicit logger runtime.
func newTestServer(name, version string, transport *StdioTransport, tools ToolProvider, opts ...ServerOption) *Server {
	opts = append(opts, WithLoggerRuntime(newTestLoggerRuntime()))
	return NewServer(name, version, transport, tools, opts...)
}

// newTestHTTPServer always supplies the server fixture's explicit logger runtime.
func newTestHTTPServer(name, version string, tools ToolProvider, opts ...HTTPServerOption) *HTTPServer {
	opts = append(opts, WithHTTPLoggerRuntime(newTestLoggerRuntime()))
	return NewHTTPServer(name, version, tools, opts...)
}

func TestNewServerPanicsWithoutLoggerRuntime(t *testing.T) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer() did not reject a missing logger runtime")
		}
	}()
	NewServer("test", "dev", NewStdioTransport(nil, io.Discard), testToolProvider{})
}

func TestNewHTTPServerPanicsWithoutLoggerRuntime(t *testing.T) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("NewHTTPServer() did not reject a missing logger runtime")
		}
	}()
	NewHTTPServer("test", "dev", testToolProvider{})
}
