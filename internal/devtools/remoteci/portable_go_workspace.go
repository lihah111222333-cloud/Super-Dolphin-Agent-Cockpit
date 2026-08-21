package remoteci

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	// PortableGoWorkspaceDescriptorPath 是组合树内唯一 workspace 模型文件。
	PortableGoWorkspaceDescriptorPath = ".super-dolphin/portable-go-workspace.json"
	// PortableGoWorkspaceSchema 是组合树 workspace 描述符的固定版本。
	PortableGoWorkspaceSchema   = "portable-go-workspace/v1"
	portableGoWorkspaceMaxBytes = 16 << 10
)

// PortableGoWorkspaceDescriptor 绑定 engine/target tree 与允许运行的 module 目录。
type PortableGoWorkspaceDescriptor struct {
	SchemaVersion string   `json:"schema_version"`
	EngineTree    string   `json:"engine_tree"`
	TargetTree    string   `json:"target_tree"`
	TargetPath    string   `json:"target_path"`
	Modules       []string `json:"modules"`
}

// DecodePortableGoWorkspaceDescriptor 严格解码并校验组合树 workspace 描述符。
func DecodePortableGoWorkspaceDescriptor(data []byte) (PortableGoWorkspaceDescriptor, error) {
	var descriptor PortableGoWorkspaceDescriptor
	if len(data) == 0 || len(data) > portableGoWorkspaceMaxBytes {
		return descriptor, errors.New("portable Go workspace descriptor size is invalid")
	}
	if err := gate.DecodeCanonicalStrictJSON(data, &descriptor); err != nil {
		return descriptor, fmt.Errorf("decode portable Go workspace descriptor: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return PortableGoWorkspaceDescriptor{}, err
	}
	return descriptor, nil
}

// Validate 校验 workspace 只含 engine 根和 target 子树中的显式 module。
func (descriptor PortableGoWorkspaceDescriptor) Validate() error {
	if err := validatePortableGoWorkspaceIdentity(descriptor); err != nil {
		return err
	}
	targetPath, err := validatePortableGoWorkspaceTarget(descriptor)
	if err != nil {
		return err
	}
	return validatePortableGoWorkspaceModules(descriptor.Modules, targetPath)
}

func validatePortableGoWorkspaceIdentity(descriptor PortableGoWorkspaceDescriptor) error {
	if descriptor.SchemaVersion != PortableGoWorkspaceSchema || !validRemoteGitObjectID(descriptor.EngineTree) || !validRemoteGitObjectID(descriptor.TargetTree) {
		return errors.New("portable Go workspace identity is invalid")
	}
	return nil
}

func validatePortableGoWorkspaceTarget(descriptor PortableGoWorkspaceDescriptor) (string, error) {
	targetPath, err := canonicalRemoteGoModuleDirectory(descriptor.TargetPath)
	if err != nil || targetPath == "." || len(descriptor.Modules) < 2 || descriptor.Modules[0] != "." {
		return "", errors.New("portable Go workspace target binding is invalid")
	}
	return targetPath, nil
}

// validatePortableGoWorkspaceModules 校验 module 唯一且全部位于显式 target 子树。
func validatePortableGoWorkspaceModules(modules []string, targetPath string) error {
	seen := make(map[string]struct{}, len(modules))
	for _, value := range modules {
		directory, err := canonicalRemoteGoModuleDirectory(value)
		if err != nil || directory != value || (directory != "." && !strings.HasPrefix(directory, targetPath+"/")) {
			return errors.New("portable Go workspace module directory is invalid")
		}
		if _, duplicate := seen[directory]; duplicate {
			return errors.New("portable Go workspace module directory is duplicated")
		}
		seen[directory] = struct{}{}
	}
	return nil
}

// GoWork 生成固定工具链版本、无 replace 的 canonical workspace 文件。
func (descriptor PortableGoWorkspaceDescriptor) GoWork() []byte {
	var output bytes.Buffer
	fmt.Fprintf(&output, "go %s\n\nuse (\n", strings.TrimPrefix(cicontract.GoToolchainVersion, "go"))
	for _, directory := range descriptor.Modules {
		if directory == "." {
			output.WriteString("\t.\n")
		} else {
			fmt.Fprintf(&output, "\t./%s\n", directory)
		}
	}
	output.WriteString(")\n")
	return output.Bytes()
}

// WorkspaceDirectories 返回不允许调用方修改的 module 目录副本。
func (descriptor PortableGoWorkspaceDescriptor) WorkspaceDirectories() []string {
	return slices.Clone(descriptor.Modules)
}

// InstallPortableGoWorkspace 从组合树 descriptor 原子生成 worker 消费的 go.work。
func InstallPortableGoWorkspace(sourceRoot string) (returnErr error) {
	if !filepath.IsAbs(sourceRoot) || filepath.Clean(sourceRoot) != sourceRoot {
		return errors.New("portable Go workspace source root must be clean and absolute")
	}
	descriptor, err := readPortableGoWorkspaceDescriptor(sourceRoot)
	if err != nil {
		return err
	}
	return writePortableGoWorkspace(sourceRoot, descriptor.GoWork())
}

func readPortableGoWorkspaceDescriptor(sourceRoot string) (PortableGoWorkspaceDescriptor, error) {
	descriptorPath := filepath.Join(sourceRoot, filepath.FromSlash(PortableGoWorkspaceDescriptorPath))
	info, err := os.Lstat(descriptorPath)
	if err != nil || !info.Mode().IsRegular() {
		return PortableGoWorkspaceDescriptor{}, errors.New("portable Go workspace descriptor must be a regular file")
	}
	data, err := os.ReadFile(descriptorPath)
	if err != nil {
		return PortableGoWorkspaceDescriptor{}, fmt.Errorf("read portable Go workspace descriptor: %w", err)
	}
	return DecodePortableGoWorkspaceDescriptor(data)
}

// writePortableGoWorkspace 以 fsync、rename 和目录同步安装不可部分可见的 go.work。
func writePortableGoWorkspace(sourceRoot string, data []byte) (returnErr error) {
	workspacePath := filepath.Join(sourceRoot, "go.work")
	if _, err := os.Lstat(workspacePath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("portable Go workspace refuses an existing go.work")
	}
	temporary, err := os.CreateTemp(sourceRoot, ".portable-go-work.*")
	if err != nil {
		return fmt.Errorf("create portable Go workspace: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporaryPath != "" {
			returnErr = errors.Join(returnErr, os.Remove(temporaryPath))
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write portable Go workspace: %w", err)
	}
	if err := temporary.Chmod(0o444); err != nil {
		temporary.Close()
		return fmt.Errorf("set portable Go workspace permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync portable Go workspace: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close portable Go workspace: %w", err)
	}
	if err := os.Rename(temporaryPath, workspacePath); err != nil {
		return fmt.Errorf("install portable Go workspace: %w", err)
	}
	temporaryPath = ""
	return syncPortableWorkspaceRoot(sourceRoot)
}

func syncPortableWorkspaceRoot(sourceRoot string) error {
	directory, err := os.Open(sourceRoot)
	if err != nil {
		return fmt.Errorf("open portable Go workspace root: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync portable Go workspace root: %w", err)
	}
	return directory.Close()
}
