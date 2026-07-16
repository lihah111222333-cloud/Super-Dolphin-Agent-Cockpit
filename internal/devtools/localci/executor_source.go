package localci

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// sourceBundleImporter is implemented by the sourceexport owner. localci must not parse Git trees or bundles.
// 依赖 sourceexport owner：需公开 commit 与 dangling synthetic-commit bundle 的 ImportAndVerify。
type sourceBundleImporter interface {
	ImportAndVerify(ctx context.Context, bundlePath string, expectedObject string, expectedTree string) (verifiedRepository string, err error)
}

// importSource 只编排 sourceexport owner 的 bundle 导入与对象复验。
func importSource(ctx context.Context, importer sourceBundleImporter, bundlePath string, expectedObject string, expectedTree string) (string, error) {
	if importer == nil {
		return "", errors.New("source bundle importer is required")
	}
	if !filepath.IsAbs(bundlePath) {
		return "", errors.New("source bundle path must be absolute")
	}
	if expectedObject == "" || expectedTree == "" {
		return "", errors.New("expected Git object and tree are required")
	}
	repository, err := importer.ImportAndVerify(ctx, bundlePath, expectedObject, expectedTree)
	if err != nil {
		return "", fmt.Errorf("import and verify source bundle: %w", err)
	}
	if !filepath.IsAbs(repository) {
		return "", errors.New("source importer returned a non-absolute verified repository")
	}
	return repository, nil
}
