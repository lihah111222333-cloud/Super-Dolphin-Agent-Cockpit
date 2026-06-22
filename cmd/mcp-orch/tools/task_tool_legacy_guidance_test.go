package tools

import (
	"os"
	"strings"
	"testing"
)

func TestTaskToolsSourceAvoidsLegacyStartNodeName(t *testing.T) {
	raw, err := os.ReadFile("task_tools.go")
	if err != nil {
		t.Fatalf("ReadFile(task_tools.go) error = %v", err)
	}
	if strings.Contains(string(raw), "task_start_node") {
		t.Fatalf("task_tools.go must not mention legacy start-node tool name")
	}
}
