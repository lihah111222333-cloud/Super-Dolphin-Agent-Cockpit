package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fxInvokeFixtureCase struct {
	name        string
	relPath     string
	source      string
	wantReasons []string
}

var fxInvokeGuardFixtureCases = []fxInvokeFixtureCase{
	{
		name:    "allows_lightweight_registration",
		relPath: "internal/module/example/module.go",
		source: `package example

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Invoke(register)
}

func register() {
	_ = "register"
}
`,
	},
	{
		name:    "allows_root_bridge_exception_by_symbol",
		relPath: "cmd/mcp-lsp/fx.go",
		source: `package main

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Invoke(bindRuntime)
}

func bindRuntime() {
	go func() {}()
}
`,
	},
	{
		name:    "rejects_goroutine",
		relPath: "internal/module/example/module.go",
		source: `package example

import "go.uber.org/fx"

func Module() fx.Option {
	return fx.Invoke(register)
}

func register() {
	go func() {}()
}
`,
		wantReasons: []string{"starts goroutine"},
	},
	{
		name:    "rejects_exec_command",
		relPath: "internal/module/example/module.go",
		source: `package example

import (
	"os/exec"

	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Invoke(register)
}

func register() {
	_ = exec.Command("sh", "-c", "echo bad")
}
`,
		wantReasons: []string{"calls exec command"},
	},
	{
		name:    "rejects_setter_mutation",
		relPath: "internal/module/example/module.go",
		source: `package example

import "go.uber.org/fx"

type service struct{}

func (s *service) SetDispatcher() {}

func Module() fx.Option {
	return fx.Invoke(register)
}

func register(s *service) {
	s.SetDispatcher()
}
`,
		wantReasons: []string{"mutates constructed object through setter"},
	},
	{
		name:    "rejects_sleep_retry",
		relPath: "internal/module/example/module.go",
		source: `package example

import (
	"time"

	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Invoke(register)
}

func register() {
	time.Sleep(time.Second)
}
`,
		wantReasons: []string{"sleeps or retries"},
	},
}

func TestFXInvokeGuardHasNoMatcherSkeletonSkips(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRootForGuardTests(t), "internal", "archtest", "fx_invoke_guard_test.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fx invoke guard: %v", err)
	}
	source := string(data)
	if strings.Contains(source, "matcher skeleton only") || strings.Contains(source, "fx_invoke_target_must_not_") && strings.Contains(source, "t.Skip") {
		t.Fatal("fx.Invoke guard still exposes matcher skeleton skips instead of real fixtures")
	}
}

func TestFXInvokeGuardMatcherFixtures(t *testing.T) {
	t.Parallel()

	for _, tc := range fxInvokeGuardFixtureCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			violations, err := fxInvokeGuardViolationsInSource(tc.relPath, []byte(tc.source))
			if err != nil {
				t.Fatalf("scan fixture: %v", err)
			}
			got := strings.Join(fxInvokeGuardViolationStrings(violations), "\n")
			if len(tc.wantReasons) == 0 {
				if got != "" {
					t.Fatalf("unexpected violations:\n%s", got)
				}
				return
			}
			for _, reason := range tc.wantReasons {
				if !strings.Contains(got, reason) {
					t.Fatalf("fixture violations = %q, want reason %q", got, reason)
				}
			}
		})
	}
}
