package gate

import (
	"errors"
	"os"
	"path/filepath"
)

func canonicalLocalExecutorSourceRoot(sourceRoot string) (string, error) {
	resolved, err := canonicalLocalSandboxPath(sourceRoot, "local executor source root")
	if err != nil {
		return "", err
	}
	if err := validateLocalExecutorSourceDirectory(resolved); err != nil {
		return "", err
	}
	if err := validateLocalExecutorSourceIsolation(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func validateLocalExecutorSourceDirectory(resolved string) error {
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return errors.New("local executor source root must be a real directory")
	}
	if _, err := os.Stat(filepath.Join(resolved, ".git")); err != nil {
		return errors.New("local executor source root must contain Git metadata")
	}
	return nil
}

func validateLocalExecutorSourceIsolation(resolved string) error {
	isolationFound := false
	for _, temporaryRootPath := range configuredLocalExecutorTempRoots() {
		temporaryRoot, err := canonicalLocalSandboxPathValue(temporaryRootPath, "local executor temporary root")
		if err != nil {
			return err
		}
		if temporaryRoot == resolved {
			return errors.New("local executor source root must be a strict temporary-tree descendant")
		}
		if pathContains(temporaryRoot, resolved) {
			isolationFound = true
		}
	}
	if !isolationFound {
		return errors.New("local executor source root must be an isolated temporary tree")
	}
	return nil
}
