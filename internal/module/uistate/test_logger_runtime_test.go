package uistate

import (
	"io"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// testLoggerRuntime creates an isolated logger owner for each service fixture.
func testLoggerRuntime() *pkglogger.Runtime {
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(io.Discard)
	return runtime
}
