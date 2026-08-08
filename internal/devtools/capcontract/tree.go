package capcontract

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/projectmaptrusted"
)

const capabilityManifestPath = "docs/doc/codemap/capability-contract/capability_manifest.json"

// managedOutputs 返回 capability-contract refresh 唯一允许写回工作区的生成物。
func managedOutputs() []string {
	return []string{capabilityManifestPath}
}

type managedOutputSnapshot struct {
	data   []byte
	mode   fs.FileMode
	exists bool
}

// CheckTree 在隔离目录中校验精确 Git tree 的 capability manifest。
// 扫描器来自受信 gate 二进制；候选 tree 只作为 Go AST/type-check 输入，
// 不会执行候选 scripts/capcontract 生成器。
func CheckTree(repository, tree string) (resultErr error) {
	exact, err := projectmaptrusted.MaterializeExactTree(repository, tree, "super-dolphin-capcontract-check-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, exact.Cleanup()) }()

	manifestPath := filepath.Join(exact.SourceRoot, filepath.FromSlash(capabilityManifestPath))
	generatedAt, ok := existingGeneratedAt(manifestPath)
	if !ok {
		return fmt.Errorf("exact-tree capability manifest generated_at is required: %s", capabilityManifestPath)
	}
	_, generated, err := buildGeneratedManifest(exact.SourceRoot, generatedAt)
	if err != nil {
		return fmt.Errorf("check capability manifest: %w", err)
	}
	actual, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", capabilityManifestPath, err)
	}
	if !bytes.Equal(actual, generated) {
		return fmt.Errorf("%s differs from generated output; run the canonical capability-contract refresh", capabilityManifestPath)
	}
	return nil
}

// RefreshTree 从精确 Git tree 生成 capability manifest，并安全写回当前工作区。
// 受管输出在生成期间发生变化时直接失败，避免覆盖其他并发工作或污染候选树。
func RefreshTree(repository, tree string) (resultErr error) {
	exact, err := projectmaptrusted.MaterializeExactTree(repository, tree, "super-dolphin-capcontract-refresh-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, exact.Cleanup()) }()

	snapshots, err := snapshotManagedOutputs(exact.RepositoryRoot)
	if err != nil {
		return err
	}
	generatedAt, ok := existingGeneratedAt(filepath.Join(exact.SourceRoot, filepath.FromSlash(capabilityManifestPath)))
	if !ok {
		generatedAt, ok = existingGeneratedAt(filepath.Join(exact.RepositoryRoot, filepath.FromSlash(capabilityManifestPath)))
	}
	if !ok {
		return fmt.Errorf("deterministic capability refresh requires an existing generated_at: %s", capabilityManifestPath)
	}
	_, generated, err := buildGeneratedManifest(exact.SourceRoot, generatedAt)
	if err != nil {
		return fmt.Errorf("refresh capability manifest: %w", err)
	}
	if err := requireManagedOutputsUnchanged(exact.RepositoryRoot, snapshots); err != nil {
		return err
	}
	return replaceManagedOutput(exact.RepositoryRoot, generated, snapshots[capabilityManifestPath].mode)
}

// buildGeneratedManifest 使用与 scripts/capcontract 相同的受信扫描器和路径规则。
// generatedAt 必须由精确 tree 或受管工作区已有 manifest 提供，不读取墙钟。
func buildGeneratedManifest(repoRoot, generatedAt string) (*Manifest, []byte, error) {
	rules, err := LoadPathRules(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := Scan(ScanOptions{
		RepoRoot:    repoRoot,
		Roots:       rules.DefaultRoots,
		GeneratedAt: generatedAt,
	})
	if err != nil {
		return nil, nil, err
	}
	data, err := MarshalManifest(manifest)
	if err != nil {
		return nil, nil, err
	}
	return manifest, data, nil
}

func existingGeneratedAt(path string) (string, bool) {
	manifest, err := LoadManifest(path)
	if err != nil || manifest.GeneratedAt == "" {
		return "", false
	}
	return manifest.GeneratedAt, true
}

// snapshotManagedOutputs 记录当前工作区受管清单的内容、权限和存在状态。
func snapshotManagedOutputs(repository string) (map[string]managedOutputSnapshot, error) {
	outputs := managedOutputs()
	snapshots := make(map[string]managedOutputSnapshot, len(outputs))
	for _, output := range outputs {
		path := filepath.Join(repository, filepath.FromSlash(output))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			snapshots[output] = managedOutputSnapshot{}
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.Join(fmt.Errorf("snapshot capability output %s", output), err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read capability output %s: %w", output, err)
		}
		snapshots[output] = managedOutputSnapshot{data: data, mode: info.Mode().Perm(), exists: true}
	}
	return snapshots, nil
}

// requireManagedOutputsUnchanged 拒绝生成期间受管清单被并发改写。
func requireManagedOutputsUnchanged(repository string, snapshots map[string]managedOutputSnapshot) error {
	current, err := snapshotManagedOutputs(repository)
	if err != nil {
		return err
	}
	for _, output := range managedOutputs() {
		before, after := snapshots[output], current[output]
		if before.exists != after.exists || before.mode != after.mode || !bytes.Equal(before.data, after.data) {
			return fmt.Errorf("capability contract managed output changed during refresh: %s", output)
		}
	}
	return nil
}

// replaceManagedOutput 原子替换受管清单并保留已有文件权限。
func replaceManagedOutput(repository string, data []byte, mode fs.FileMode) error {
	target := filepath.Join(repository, filepath.FromSlash(capabilityManifestPath))
	if mode == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create capability output directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".capability-manifest-*")
	if err != nil {
		return fmt.Errorf("stage capability manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("chmod capability manifest: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write capability manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync capability manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close capability manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("replace capability manifest: %w", err)
	}
	return nil
}
