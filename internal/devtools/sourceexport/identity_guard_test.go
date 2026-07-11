package sourceexport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRepositoryIdentityAcceptsThirdPartyUnlicenseMetadata(t *testing.T) {
	root := newIdentityFixture(t)
	packageLock := `{"packages":{"node_modules/example":{"license":"Unlicense"}}}`
	writeIdentityFile(t, root, "frontend-app/package-lock.json", packageLock)

	if err := ValidateRepositoryIdentity(root, validPolicy()); err != nil {
		t.Fatalf("ValidateRepositoryIdentity() error = %v", err)
	}
}

func TestValidateRepositoryIdentityRejectsProjectMismatch(t *testing.T) {
	tests := []struct {
		name string
		path string
		data string
		code Code
	}{
		{name: "module", path: "go.mod", data: "module github.com/" + "anthropic-ai/super-agent-v3\n", code: CodeModulePathMismatch},
		{name: "license", path: "LICENSE", data: "This is free and unencumbered software released into the public domain.\n", code: CodeLicenseMismatch},
		{name: "product", path: "README.md", data: "# Super Agent" + " v3\n", code: CodePolicyInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newIdentityFixture(t)
			writeIdentityFile(t, root, tt.path, tt.data)
			assertErrorCode(t, ValidateRepositoryIdentity(root, validPolicy()), tt.code)
		})
	}
}

func TestCurrentRepositoryIdentity(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateRepositoryIdentity(root, validPolicy())
	if err != nil {
		t.Fatalf("ValidateRepositoryIdentity() error = %v", err)
	}
}

func newIdentityFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeIdentityFile(t, root, "go.mod", "module github.com/lihah111222333-cloud/super-dolphin-agent\n")
	writeIdentityFile(t, root, "README.md", "# Super Dolphin Agent\n\nhttps://github.com/lihah111222333-cloud/super-dolphin-agent\n")
	writeIdentityFile(t, root, "LICENSE", "Apache License\nVersion 2.0, January 2004\n")
	return root
}

func writeIdentityFile(t *testing.T, root string, name string, data string) {
	t.Helper()
	filePath := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
