package gate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

// runTrustedSQLCPostprocess 仅用 gate runtime 的固定 gofmt 处理预检冻结的 output 集合。
func runTrustedSQLCPostprocess(
	ctx context.Context,
	gofmtBinary string,
	sourceCopy string,
	units []sqlcconfig.Unit,
	environment []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	for _, unit := range units {
		root := filepath.Join(sourceCopy, filepath.FromSlash(unit.Output))
		files, err := trustedSQLCGoFiles(root)
		if err != nil {
			return fmt.Errorf("collect trusted SQLC output %s: %w", unit.Output, err)
		}
		for _, file := range files {
			relative, err := filepath.Rel(sourceCopy, file)
			if err != nil {
				return fmt.Errorf("resolve trusted SQLC output %s: %w", file, err)
			}
			if sqlcconfig.IsManualExtension(unit, filepath.ToSlash(relative)) {
				continue
			}
			step := resolvedStep{directory: sourceCopy, binary: gofmtBinary, argv: []string{"gofmt", "-w", "-r", "interface{} -> any", "--", file}}
			if err := runResolvedStep(ctx, step, environment, stdout, stderr); err != nil {
				return fmt.Errorf("trusted SQLC postprocess %s: %w", file, err)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read trusted SQLC output %s: %w", file, err)
			}
			if bytes.Contains(data, []byte("interface{}")) {
				return fmt.Errorf("trusted SQLC postprocess left interface{} in %s", file)
			}
		}
	}
	return nil
}

func trustedSQLCGoFiles(root string) ([]string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("output is not a regular directory: %s", root)
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link is forbidden: %s", path)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular SQLC output is forbidden: %s", path)
		}
		if filepath.Ext(path) == ".go" {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
