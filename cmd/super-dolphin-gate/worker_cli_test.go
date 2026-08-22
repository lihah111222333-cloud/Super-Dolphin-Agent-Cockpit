package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestWorkerCLIRejectsNonCanonicalInvocation(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"run", "--gate"},
		{"cli-identity"},
		{"go-cache-proxy", "--seed", "/seed", "--private", "/private"},
		{"validate-go-distribution"},
		{"race-package-patterns"},
	} {
		stderr := &bytes.Buffer{}
		if code := runWorkerCLI(args, &bytes.Buffer{}, stderr); code == 0 {
			t.Fatalf("worker unexpectedly accepted arguments %q", args)
		}
		if !strings.Contains(stderr.String(), "super-dolphin-gate:") {
			t.Fatalf("worker stderr = %q, want CLI error prefix", stderr.String())
		}
	}
}

func TestWorkerBuiltinCommandsReturnInfrastructureWhenErrorOutputFails(t *testing.T) {
	if code := runWorkerCLI([]string{"mcp-lsp-catalog-guard"}, &bytes.Buffer{}, failingWriter{err: errors.New("closed stderr")}); code != int(gate.ExitInfrastructure) {
		t.Fatalf("runWorkerCLI catalog guard code = %d, want infrastructure after stderr failure", code)
	}
}

func TestRunCLIDispatchesWorkerNamespace(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runCLI([]string{"worker", "run", "--gate"}, stdout, stderr)
	if code != int(gate.ExitProtocol) || !strings.Contains(stderr.String(), "unknown worker command") {
		t.Fatalf("code=%d stderr=%q, want worker protocol failure", code, stderr.String())
	}
}
