package codemapindex

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/archtestmap"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/projectmaptrusted"
)

// ManagedOutputs 是 codemap refresh 唯一允许写回工作区的生成物。
var ManagedOutputs = []string{
	"README.md",
	"docs/doc/codemap/13-archtest-boundaries.md",
	"docs/doc/codemap/README.md",
	"docs/doc/codemap/ai-index.json",
	"docs/doc/codemap/anchor-identities.json",
}

type managedOutputSnapshot struct {
	data   []byte
	mode   fs.FileMode
	exists bool
}

// CheckTree 在隔离目录中校验精确 Git tree 的全部代码地图生成物。
func CheckTree(repository, tree string) (resultErr error) {
	exact, err := projectmaptrusted.MaterializeExactTree(repository, tree, "super-dolphin-codemap-check-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, exact.Cleanup()) }()
	if err := archtestmap.Generate(exact.SourceRoot, true); err != nil {
		return fmt.Errorf("check archtest map: %w", err)
	}
	if err := Generate(exact.SourceRoot, true); err != nil {
		return fmt.Errorf("check codemap index: %w", err)
	}
	return nil
}

// RefreshTree 从精确 Git tree 刷新固定生成物，并拒绝覆盖并发工作区变化。
func RefreshTree(repository, tree string) (resultErr error) {
	exact, err := projectmaptrusted.MaterializeExactTree(repository, tree, "super-dolphin-codemap-refresh-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, exact.Cleanup()) }()
	snapshots, err := snapshotManagedOutputs(exact.RepositoryRoot)
	if err != nil {
		return err
	}
	if err := archtestmap.Generate(exact.SourceRoot, false); err != nil {
		return fmt.Errorf("refresh archtest map: %w", err)
	}
	if err := Generate(exact.SourceRoot, false); err != nil {
		return fmt.Errorf("refresh codemap index: %w", err)
	}
	if err := requireManagedOutputsUnchanged(exact.RepositoryRoot, snapshots); err != nil {
		return err
	}
	for _, output := range ManagedOutputs {
		if err := replaceManagedOutput(exact.SourceRoot, exact.RepositoryRoot, output); err != nil {
			return err
		}
	}
	return nil
}

func snapshotManagedOutputs(repository string) (map[string]managedOutputSnapshot, error) {
	snapshots := make(map[string]managedOutputSnapshot, len(ManagedOutputs))
	for _, output := range ManagedOutputs {
		path := filepath.Join(repository, filepath.FromSlash(output))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			snapshots[output] = managedOutputSnapshot{}
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.Join(fmt.Errorf("snapshot codemap output %s", output), err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read codemap output %s: %w", output, err)
		}
		snapshots[output] = managedOutputSnapshot{data: data, mode: info.Mode().Perm(), exists: true}
	}
	return snapshots, nil
}

func requireManagedOutputsUnchanged(repository string, snapshots map[string]managedOutputSnapshot) error {
	current, err := snapshotManagedOutputs(repository)
	if err != nil {
		return err
	}
	for _, output := range ManagedOutputs {
		before, after := snapshots[output], current[output]
		if before.exists != after.exists || before.mode != after.mode || !bytes.Equal(before.data, after.data) {
			return fmt.Errorf("codemap managed output changed during refresh: %s", output)
		}
	}
	return nil
}

func replaceManagedOutput(sourceRoot, repository, output string) error {
	sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(output))
	info, err := os.Lstat(sourcePath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.Join(fmt.Errorf("generated codemap output is not a regular file: %s", output), err)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read generated codemap output %s: %w", output, err)
	}
	targetPath := filepath.Join(repository, filepath.FromSlash(output))
	temporary, err := os.CreateTemp(filepath.Dir(targetPath), ".codemap-output-*")
	if err != nil {
		return fmt.Errorf("stage codemap output %s: %w", output, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("chmod codemap output %s: %w", output, err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write codemap output %s: %w", output, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync codemap output %s: %w", output, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close codemap output %s: %w", output, err)
	}
	if err := os.Rename(temporaryPath, targetPath); err != nil {
		return fmt.Errorf("replace codemap output %s: %w", output, err)
	}
	return nil
}
