package main

import (
	"context"
	"errors"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

type testStdioServer struct{ err error }

func (s testStdioServer) Run(context.Context) error { return s.err }

type testStdioCloser struct{ err error }

func (c testStdioCloser) Close() error { return c.err }

func TestStdioRunnerJoinsRunAndCloseErrors(t *testing.T) {
	runErr := errors.New("run failed")
	closeErr := errors.New("close failed")
	for _, tc := range []struct {
		name     string
		runErr   error
		closeErr error
	}{
		{name: "both nil"},
		{name: "run only", runErr: runErr},
		{name: "close only", closeErr: closeErr},
		{name: "both", runErr: runErr, closeErr: closeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (stdioRunner{server: testStdioServer{err: tc.runErr}, manager: testStdioCloser{err: tc.closeErr}}).Run(context.Background())
			if errors.Is(err, runErr) != (tc.runErr != nil) || errors.Is(err, closeErr) != (tc.closeErr != nil) {
				t.Fatalf("Run() error = %v, run=%v close=%v", err, tc.runErr, tc.closeErr)
			}
		})
	}
}

func mustSetupInstaller(t *testing.T) *lspinstaller.Provider {
	t.Helper()
	provider, err := setupInstallerWithError()
	if err != nil {
		t.Fatalf("setup installer: %v", err)
	}
	return provider
}
