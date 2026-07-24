package sourceexport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPolicyRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	data := `{"schema_version":1,"canonical_product_name":"Super Dolphin Agent","canonical_repository":"github.com/lihah111222333-cloud/super-dolphin-agent","canonical_module_path":"github.com/lihah111222333-cloud/super-dolphin-agent","license_spdx":"Apache-2.0","required_root_files":["README.md"],"allow_rules":[{"pattern":"README.md","kind":"file"}],"deny_rules":[{"pattern":"docs/plans/**","kind":"glob"}],"forbidden_identities":["/Users/"],"required_readmes":["README.md"],"required_readme_sections":["sd:why"],"forbidden_file_names":[".env"],"generated_files":["OPEN_SOURCE_EXPORT.json"],"unexpected":true}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPolicy(path)
	assertErrorCode(t, err, CodePolicyInvalid)
}

func TestValidatePolicyRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Policy)
		code Code
	}{
		{name: "unsupported version", edit: func(p *Policy) { p.SchemaVersion = 0 }, code: CodePolicyInvalid},
		{name: "wrong license", edit: func(p *Policy) { p.LicenseSPDX = "Unlicense" }, code: CodeLicenseMismatch},
		{name: "wrong module", edit: func(p *Policy) { p.CanonicalModulePath = "github.com/" + "anthropic-ai/super-agent-v3" }, code: CodeModulePathMismatch},
		{name: "absolute allow", edit: func(p *Policy) { p.AllowRules[0].Pattern = "/README.md" }, code: CodePolicyInvalid},
		{name: "parent traversal", edit: func(p *Policy) { p.DenyRules[0].Pattern = "../private/**" }, code: CodePolicyInvalid},
		{name: "backslash", edit: func(p *Policy) { p.RequiredRootFiles[0] = `docs\README.md` }, code: CodePolicyInvalid},
		{name: "duplicate allow", edit: func(p *Policy) { p.AllowRules = append(p.AllowRules, p.AllowRules[0]) }, code: CodePolicyInvalid},
		{name: "empty generated file", edit: func(p *Policy) { p.GeneratedFiles[0] = "" }, code: CodePolicyInvalid},
		{name: "empty required readmes", edit: func(p *Policy) { p.RequiredReadmes = nil }, code: CodePolicyInvalid},
		{name: "empty readme sections", edit: func(p *Policy) { p.RequiredREADMESections = nil }, code: CodePolicyInvalid},
		{name: "empty forbidden file names", edit: func(p *Policy) { p.ForbiddenFileNames = nil }, code: CodePolicyInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := validPolicy()
			tt.edit(&policy)
			assertErrorCode(t, ValidatePolicy(policy), tt.code)
		})
	}
}

func TestValidatePolicyAcceptsCanonicalPolicy(t *testing.T) {
	if err := ValidatePolicy(validPolicy()); err != nil {
		t.Fatalf("ValidatePolicy() error = %v", err)
	}
}

func TestErrorCodesStable(t *testing.T) {
	codes := []Code{
		CodePolicyInvalid, CodeUnclassifiedPath, CodeForbiddenPath,
		CodeForbiddenIdentity, CodeLicenseMismatch, CodeModulePathMismatch,
		CodeSymlinkRejected, CodeCaseCollision, CodeOutputNotEmpty,
		CodeSourceDirty, CodeSecretScanFailed, CodeExportReceiptMismatch,
	}
	want := []string{
		"POLICY_INVALID", "UNCLASSIFIED_PATH", "FORBIDDEN_PATH",
		"FORBIDDEN_IDENTITY", "LICENSE_MISMATCH", "MODULE_PATH_MISMATCH",
		"SYMLINK_REJECTED", "CASE_COLLISION", "OUTPUT_NOT_EMPTY",
		"SOURCE_DIRTY", "SECRET_SCAN_FAILED", "EXPORT_RECEIPT_MISMATCH",
	}
	for index := range codes {
		if string(codes[index]) != want[index] {
			t.Fatalf("code[%d] = %q, want %q", index, codes[index], want[index])
		}
	}
}

func TestRepositoryPolicyLoads(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := LoadPolicy(filepath.Join(root, "release", "open-source-policy.json"))
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if len(policy.RequiredReadmes) != 6 {
		t.Fatalf("required readmes = %d, want 6", len(policy.RequiredReadmes))
	}
	if len(policy.RequiredREADMESections) != 6 {
		t.Fatalf("required README sections = %d, want 6", len(policy.RequiredREADMESections))
	}
}

func TestRepositoryPolicyClassifiesLaunchersAndGitHooks(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := LoadPolicy(filepath.Join(root, "release", "open-source-policy.json"))
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}

	tests := []struct {
		path string
		code Code
	}{
		{path: "run-new-ui-desktop.sh"},
		{path: "run-new-ui-desktop.ps1"},
		{path: ".githooks/README.md"},
		{path: ".githooks/commit-msg"},
		{path: ".githooks/pre-commit"},
		{path: ".githooks/pre-push"},
		{path: ".githooks/unapproved-local-hook", code: CodeUnclassifiedPath},
		{path: "run-unapproved-local.sh", code: CodeUnclassifiedPath},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			decision, err := classifyPath(policy, tt.path)
			if tt.code != "" {
				assertErrorCode(t, err, tt.code)
				return
			}
			if err != nil {
				t.Fatalf("classifyPath() error = %v", err)
			}
			if decision != pathAllowed {
				t.Fatalf("classifyPath() decision = %d, want %d", decision, pathAllowed)
			}
		})
	}
}

func TestRepositoryPolicySchemaIsClosed(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "release", "open-source-export.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		AdditionalProperties bool     `json:"additionalProperties"`
		Required             []string `json:"required"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties || len(schema.Required) != 13 {
		t.Fatalf("schema must reject extra fields and require all 13 policy fields")
	}
}

func validPolicy() Policy {
	return Policy{
		SchemaVersion:          1,
		CanonicalProductName:   "Super Dolphin Agent",
		CanonicalRepository:    "github.com/lihah111222333-cloud/super-dolphin-agent",
		CanonicalModulePath:    "github.com/lihah111222333-cloud/super-dolphin-agent",
		LicenseSPDX:            "Apache-2.0",
		RequiredRootFiles:      []string{"README.md"},
		AllowRules:             []PathRule{{Pattern: "README.md", Kind: "file"}},
		DenyRules:              []PathRule{{Pattern: "docs/plans/**", Kind: "glob"}},
		ForbiddenIdentities:    []string{"/Users/"},
		RequiredReadmes:        []string{"README.md"},
		RequiredREADMESections: []string{"sd:why"},
		ForbiddenFileNames:     []string{".env"},
		GeneratedFiles:         []string{"OPEN_SOURCE_EXPORT.json"},
	}
}

func assertErrorCode(t *testing.T, err error, want Code) {
	t.Helper()
	var coded *Error
	if !errors.As(err, &coded) {
		t.Fatalf("error = %v, want coded error %s", err, want)
	}
	if coded.Code != want {
		t.Fatalf("error code = %s, want %s", coded.Code, want)
	}
}
