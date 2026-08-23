package mcplspcatalogguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestSharedConsumerASTPredicatesAreExact(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", []byte(`package fixture
const exact = "catalog"
var other = exact
`), 0)
	if err != nil {
		t.Fatal(err)
	}
	exactIdent := file.Decls[0].(*ast.GenDecl).Specs[0].(*ast.ValueSpec).Names[0]
	otherIdent := file.Decls[1].(*ast.GenDecl).Specs[0].(*ast.ValueSpec).Names[0]
	if !isIdentifier(exactIdent, "exact") || !isIdentifier(otherIdent, "other") || isIdentifier(otherIdent, "exact") {
		t.Fatalf("identifier predicate accepted the wrong AST identity: exact=%v other=%v", exactIdent, otherIdent)
	}

	literal := file.Decls[0].(*ast.GenDecl).Specs[0].(*ast.ValueSpec).Values[0]
	if !isStringLiteral(literal, "catalog") || isStringLiteral(literal, "other") {
		t.Fatal("string literal predicate did not enforce exact decoded value")
	}
}
