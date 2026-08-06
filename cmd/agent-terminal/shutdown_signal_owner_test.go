package main

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentTerminalDesktopSignalOwnershipIsUnique(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"cmd/agent-terminal/main.go",
		"cmd/agent-terminal/recovery_ui.go",
		"internal/app/app.go",
		"internal/app/runner.go",
		"internal/ui/wails/module.go",
		"internal/ui/wails/lifecycle.go",
	}
	code := make(map[string]string, len(paths))
	for _, path := range paths {
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(path)), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		var source bytes.Buffer
		if printErr := printer.Fprint(&source, token.NewFileSet(), file); printErr != nil {
			t.Fatalf("print AST for %s: %v", path, printErr)
		}
		code[path] = source.String()
	}
	if got := strings.Count(code[paths[0]], "signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)"); got != 1 {
		t.Fatalf("desktop signal owner count = %d, want 1", got)
	}
	if got := strings.Count(code[paths[1]]+code[paths[4]], "DisableDefaultSignalHandler: true"); got != 2 {
		t.Fatalf("disabled Wails signal handler count = %d, want 2", got)
	}
	if got := [2]int{strings.Count(code[paths[2]], ".Done()"), strings.Count(code[paths[2]], "<-app.Done()")}; got != [2]int{1, 1} {
		t.Fatalf("Fx Done ownership is not unique to headless runApp")
	}
	if got := strings.Count(code[paths[4]]+code[paths[5]], ".Shutdown("); got != 0 {
		t.Fatalf("Wails lifecycle contains %d direct Shutdown calls", got)
	}
	if got := strings.Count(code[paths[3]], "EnableSignals: false"); got != 1 {
		t.Fatalf("desktop RunGroup signal-disable count = %d, want 1", got)
	}
}
