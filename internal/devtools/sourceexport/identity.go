package sourceexport

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateRepositoryIdentity 校验根 README、Go module 和许可证的项目身份。
func ValidateRepositoryIdentity(root string, policy Policy) error {
	if err := ValidatePolicy(policy); err != nil {
		return err
	}
	if !filepath.IsAbs(root) {
		return policyError("repository", errors.New("root must be absolute"))
	}
	identity := policy.projectIdentity()
	if err := validateGoModule(root, identity.GoModule); err != nil {
		return err
	}
	if err := validateRootLicense(root); err != nil {
		return err
	}
	return validateRootREADME(root, identity)
}

func validateGoModule(root string, expected string) error {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return &Error{Code: CodeModulePathMismatch, Path: "go.mod", Err: err}
	}
	line, _, _ := bytes.Cut(data, []byte("\n"))
	fields := strings.Fields(string(line))
	if len(fields) != 2 || fields[0] != "module" || fields[1] != expected {
		return &Error{Code: CodeModulePathMismatch, Path: "go.mod", Err: fmt.Errorf("module must equal %q", expected)}
	}
	return nil
}

func validateRootLicense(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return &Error{Code: CodeLicenseMismatch, Path: "LICENSE", Err: err}
	}
	content := string(data)
	if !strings.Contains(content, "Apache License") || !strings.Contains(content, "Version 2.0") {
		return &Error{Code: CodeLicenseMismatch, Path: "LICENSE", Err: errors.New("root license must be Apache License 2.0")}
	}
	return nil
}

func validateRootREADME(root string, identity ProjectIdentity) error {
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return policyError("README.md", err)
	}
	content := string(data)
	if !strings.Contains(content, identity.ProductName) {
		return policyError("README.md", fmt.Errorf("must contain product name %q", identity.ProductName))
	}
	repositoryURL := "https://github.com/" + strings.TrimPrefix(identity.Repository, "github.com/")
	if !strings.Contains(content, repositoryURL) {
		return policyError("README.md", fmt.Errorf("must contain repository URL %q", repositoryURL))
	}
	return nil
}
