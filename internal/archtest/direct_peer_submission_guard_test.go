package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDirectPeerSubmissionDoesNotForgeProviderReceipt keeps the legacy
// error-only turn port from deriving a provider receipt from message_id.
func TestDirectPeerSubmissionDoesNotForgeProviderReceipt(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd/mcp-orch/tools/direct_peer_tools.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"MarkSubmitted",
		"MarkSubmitted(ctx, message.MessageID, message.MessageID)",
		"SubmissionID: message.MessageID",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("direct-peer handler reintroduced forged provider receipt pattern %q", forbidden)
		}
	}
}

// TestDirectPeerStoreReceiptWritesRequireOwnerCapability prevents a public
// message_id/status-only mutator from becoming a receipt authority.
func TestDirectPeerStoreReceiptWritesRequireOwnerCapability(t *testing.T) {
	root := filepath.Join(repoRoot(t), "cmd/mcp-orch/store/directpeer")
	storePath := filepath.Join(root, "store.go")
	ownerPath := filepath.Join(root, "owner_store.go")
	storeSource, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	ownerSource, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	storeAST := parseDirectPeerFile(t, storePath)
	ownerAST := parseDirectPeerFile(t, ownerPath)
	if !hasEmbeddedType(storeAST, "Store", "ownerStore") {
		t.Fatal("Store must embed ownerStore for promoted owner APIs")
	}
	for _, method := range []string{"OwnerCapability", "MarkSubmittedByOwner", "MarkDeliveryFailedByOwner"} {
		if !hasMethod(ownerAST, "ownerStore", method) {
			t.Fatalf("ownerStore missing promoted owner method %q", method)
		}
		if hasMethod(storeAST, "Store", method) {
			t.Fatalf("Store must not directly define owner method %q", method)
		}
	}
	text := string(storeSource) + "\n" + string(ownerSource)
	for _, required := range []string{
		"type OwnerCapability struct",
		"func (o *ownerStore) OwnerCapability() (OwnerCapability, error)",
		"func (o *ownerStore) MarkSubmittedByOwner(ctx context.Context, capability OwnerCapability",
		"func (o *ownerStore) MarkDeliveryFailedByOwner(ctx context.Context, capability OwnerCapability",
		"return Message{}, ErrOwnerCapability",
		"subtle.ConstantTimeCompare(capability.token[:], o.parent.ownerToken[:])",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("direct-peer store owner guard missing %q", required)
		}
	}
}

func parseDirectPeerFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func hasEmbeddedType(file *ast.File, structName, embeddedName string) bool {
	for _, decl := range file.Decls {
		typeSpec, ok := findDirectPeerTypeSpec(decl, structName)
		if !ok {
			continue
		}
		structType, ok := typeSpec.Type.(*ast.StructType)
		if ok && structEmbedsName(structType, embeddedName) {
			return true
		}
	}
	return false
}

func findDirectPeerTypeSpec(decl ast.Decl, name string) (*ast.TypeSpec, bool) {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.TYPE {
		return nil, false
	}
	for _, spec := range gen.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if ok && typeSpec.Name.Name == name {
			return typeSpec, true
		}
	}
	return nil, false
}

func structEmbedsName(structType *ast.StructType, embeddedName string) bool {
	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 && embeddedTypeName(field.Type) == embeddedName {
			return true
		}
	}
	return false
}

func embeddedTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, _ := expr.(*ast.Ident)
	if ident == nil {
		return ""
	}
	return ident.Name
}

func hasMethod(file *ast.File, receiver, method string) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != method || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		if embeddedTypeName(fn.Recv.List[0].Type) == receiver {
			return true
		}
	}
	return false
}
