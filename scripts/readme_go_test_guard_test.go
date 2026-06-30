package main

import (
	"strings"
	"testing"
)

func TestReadmeDoesNotAdvertiseBareFullGoTest(t *testing.T) {
	readme := readRepoFile(t, "../README.md")
	for _, line := range strings.Split(readme, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "go test ./...") {
			t.Fatalf("README advertises bare full Go test command: %q", trimmed)
		}
	}

	assertScriptContains(t, readme, "make test")
	assertScriptContains(t, readme, "make frontend-app-build && go test ./... -count=1")
}
