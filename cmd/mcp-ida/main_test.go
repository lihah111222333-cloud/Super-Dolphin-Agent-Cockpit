package main

import (
	"os"
	"testing"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestProtectMCPStdoutRebindsLoggerOffProtocolStdout(t *testing.T) {
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
		mcpStdout.Store(nil)
		pkglogger.InitWithConsoleWriter(os.Stderr)
	})

	protocolRead, protocolWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("protocol pipe: %v", err)
	}
	defer protocolRead.Close()
	defer protocolWrite.Close()

	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	defer stderrRead.Close()
	defer stderrWrite.Close()

	os.Stdout = protocolWrite
	os.Stderr = stderrWrite
	pkglogger.InitWithConsoleWriter(os.Stdout)

	protectMCPStdout()
	pkglogger.Info("mcp-ida logger routing test")

	if mcpStdout.Load() != protocolWrite {
		t.Fatalf("mcpStdout = %v, want original protocol stdout", mcpStdout.Load())
	}
	if os.Stdout != os.Stderr {
		t.Fatalf("os.Stdout was not redirected to stderr")
	}

	if err := protocolWrite.Close(); err != nil {
		t.Fatalf("close protocol writer: %v", err)
	}
	buf := make([]byte, 1)
	n, _ := protocolRead.Read(buf)
	if n != 0 {
		t.Fatalf("logger wrote %q to protocol stdout", string(buf[:n]))
	}
}
