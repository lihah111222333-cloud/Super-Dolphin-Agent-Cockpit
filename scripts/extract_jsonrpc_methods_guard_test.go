package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExtractJSONRPCMethodsScript_EmitsKnownMethods(t *testing.T) {
	cmd := exec.Command("go", "run", "scripts/extract_jsonrpc_methods.go")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run extract_jsonrpc_methods.go: %v\n%s", err, string(out))
	}
	output := string(out)
	for _, want := range []string{"thread/messages", "ui/sidebar/get", "turn/interrupt"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q\n%s", want, output)
		}
	}
}
