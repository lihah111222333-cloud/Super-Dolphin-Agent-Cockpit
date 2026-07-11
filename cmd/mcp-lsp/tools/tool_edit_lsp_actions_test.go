package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestCodeActionRejectsWorkspaceEditOutsideRoots(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	targetOriginal := "package main\n\nfunc main() {\n\toldName\n}\n"
	if err := os.WriteFile(target, []byte(targetOriginal), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideRoot := t.TempDir()
	outside := filepath.Join(outsideRoot, "outside.go")
	outsideOriginal := "package outside\n\nfunc main() {\n\toldName\n}\n"
	if err := os.WriteFile(outside, []byte(outsideOriginal), 0o644); err != nil {
		t.Fatal(err)
	}

	outsideURI := fileURI(outside)
	manager := &lspReproManager{
		codeActions: []protocol.CodeActionResult{{
			CodeAction: &protocol.CodeAction{
				Title: "Apply unsafe edit",
				Kind:  "quickfix",
				Edit: &protocol.WorkspaceEdit{
					Changes: map[string][]protocol.TextEdit{
						fileURI(target): {{
							Range: protocol.Range{
								Start: protocol.Position{Line: 3, Character: 1},
								End:   protocol.Position{Line: 3, Character: 8},
							},
							NewText: "newName",
						}},
						outsideURI: {{
							Range: protocol.Range{
								Start: protocol.Position{Line: 3, Character: 1},
								End:   protocol.Position{Line: 3, Character: 8},
							},
							NewText: "newName",
						}},
					},
				},
			},
		}},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input := marshalReproParams(t, EditRequest{
		Action: "code_action",
		Pos:    "main.go:4:2",
		Only:   []string{"quickfix"},
	})

	_, err := handler(testToolContext(root), input)
	if err == nil {
		t.Fatal("code_action error = nil, want root validation rejection")
	}
	if !strings.Contains(err.Error(), "outside workspace roots") || !strings.Contains(err.Error(), outsideURI) {
		t.Fatalf("code_action error = %v, want outside workspace roots error containing %s", err, outsideURI)
	}
	gotTarget, _ := os.ReadFile(target)
	if string(gotTarget) != targetOriginal {
		t.Fatalf("target content = %q, want unchanged %q", gotTarget, targetOriginal)
	}
	gotOutside, _ := os.ReadFile(outside)
	if string(gotOutside) != outsideOriginal {
		t.Fatalf("outside content = %q, want unchanged %q", gotOutside, outsideOriginal)
	}
}
