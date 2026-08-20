package multilsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestPHPRenameFallbackBuildsSemanticEditsFromDeclarationAndReferences(t *testing.T) {
	root := t.TempDir()
	declaration := filepath.Join(root, "User.php")
	consumer := filepath.Join(root, "consumer.php")
	other := filepath.Join(root, "other.php")
	files := map[string]string{
		declaration: "<?php\nclass User {}\n",
		consumer:    "<?php\nuse App\\User;\n$user = new User();\n",
		other:       "<?php\nclass UserFactory {}\n// User is documentation, not a reference\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	declarationURI := fileURIFromPath(declaration)
	consumerURI := fileURIFromPath(consumer)
	refs := []protocol.LocationResult{
		{Location: &protocol.Location{URI: declarationURI, Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 6}, End: protocol.Position{Line: 1, Character: 10}}}},
		{Location: &protocol.Location{URI: consumerURI, Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 8}, End: protocol.Position{Line: 1, Character: 12}}}},
		{Location: &protocol.Location{URI: consumerURI, Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 12}, End: protocol.Position{Line: 2, Character: 16}}}},
	}
	definition := refs[:1]

	edit, err := buildSemanticPHPRenameEdit(context.Background(), declarationURI, protocol.Position{Line: 1, Character: 7}, "UserLspProbe", refs, definition)
	if err != nil {
		t.Fatalf("buildSemanticPHPRenameEdit() error = %v", err)
	}
	if got := len(edit.Changes); got != 2 {
		t.Fatalf("edit changes = %d, want declaration plus consumer", got)
	}
	if _, ok := edit.Changes[fileURIFromPath(other)]; ok {
		t.Fatalf("same-name non-reference file was included in workspace edit")
	}
	if got := len(edit.Changes[declarationURI]); got != 1 {
		t.Fatalf("declaration edits = %d, want 1", got)
	}
	if got := len(edit.Changes[consumerURI]); got != 2 {
		t.Fatalf("consumer edits = %d, want import and call, got %d", got, got)
	}
}

func TestPHPRenameFallbackFailsWithoutDeclarationReference(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "User.php")
	if err := os.WriteFile(path, []byte("<?php\nclass User {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := fileURIFromPath(path)
	refs := []protocol.LocationResult{{Location: &protocol.Location{URI: uri, Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 5}}}}}
	if _, err := buildSemanticPHPRenameEdit(context.Background(), uri, protocol.Position{Line: 1, Character: 7}, "UserLspProbe", refs, nil); err == nil {
		t.Fatal("buildSemanticPHPRenameEdit() succeeded without a declaration")
	}
}
