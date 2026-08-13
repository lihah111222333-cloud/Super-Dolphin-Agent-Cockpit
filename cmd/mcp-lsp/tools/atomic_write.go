package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileWriter 抽象原子替换需要的文件系统操作，避免编辑路径直接截断目标文件。
type fileWriter interface {
	CreateTemp(dir string, pattern string) (*os.File, error)
	Open(name string) (*os.File, error)
	Remove(name string) error
	Rename(oldPath string, newPath string) error
}

// osFileWriter 使用标准库文件系统操作实现 fileWriter。
type osFileWriter struct{}

// CreateTemp 在指定目录创建临时文件。
func (osFileWriter) CreateTemp(dir string, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}

// Open 打开指定路径。
func (osFileWriter) Open(name string) (*os.File, error) {
	return os.Open(name)
}

// Remove 删除指定路径。
func (osFileWriter) Remove(name string) error {
	return os.Remove(name)
}

var defaultFileWriter fileWriter = osFileWriter{}

// atomicReplaceFile 通过同目录临时文件和 rename 原子替换目标文件。
func atomicReplaceFile(path string, content []byte, mode os.FileMode, writer fileWriter) error {
	if writer == nil {
		return errors.New("file writer is required")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := writer.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = writer.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp %s: %w", tmpPath, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmpPath, err)
	}
	if err := writer.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp %s to %s: %w", tmpPath, path, err)
	}
	removeTemp = false
	if err := syncParentDirectory(dir, writer); err != nil {
		return err
	}
	return nil
}

// validateReservedToolWrapperFields 校验 wrapper 保留字段，并删除已被 wrapper 消费的 work_dir。
func validateReservedToolWrapperFields(fields map[string]json.RawMessage) (bool, error) {
	changed := false
	if raw, ok := fields["work_dir"]; ok {
		var workDir string
		if err := json.Unmarshal(raw, &workDir); err != nil {
			return false, fmt.Errorf("work_dir must be a non-empty string: %w", err)
		}
		if strings.TrimSpace(workDir) == "" {
			return false, errors.New("work_dir is required")
		}
		delete(fields, "work_dir")
		changed = true
	}
	for _, field := range []string{"cwd", "agent_id"} {
		if _, ok := fields[field]; ok {
			return false, fmt.Errorf("argument field %q is reserved wrapper metadata; pass worktree cwd as top-level _cwd and agent identity as top-level _agentId", field)
		}
	}
	return changed, nil
}
