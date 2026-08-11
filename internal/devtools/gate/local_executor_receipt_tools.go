package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// TrustedGitBinary 是 gate 解析并封存的 Git 可执行文件证明。其路径只能由
// receipt 的固定可信目录解析器取得，调用方不能提供路径覆盖。
type TrustedGitBinary struct {
	path                     string
	digest                   string
	version                  string
	candidateObjectAuthority CandidateObjectAuthority
}

func (binary TrustedGitBinary) withCandidateObjectAuthority(authority CandidateObjectAuthority) (TrustedGitBinary, error) {
	if _, err := authority.Digest(); err != nil {
		return TrustedGitBinary{}, err
	}
	binary.candidateObjectAuthority = authority
	return binary, nil
}

// VerifiedPath 在每个 Git 执行前复核固定目录、绝对路径和内容摘要。
func (binary TrustedGitBinary) VerifiedPath() (string, error) {
	if binary.path == "" || !isPrefixedSHA256Digest(binary.digest) || strings.TrimSpace(binary.version) == "" {
		return "", errors.New("trusted Git binary proof is incomplete")
	}
	path, err := resolveReceiptToolPath(binary.path)
	if err != nil {
		return "", fmt.Errorf("reverify trusted Git binary path: %w", err)
	}
	if path != binary.path {
		return "", errors.New("trusted Git binary path drifted")
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return "", fmt.Errorf("digest trusted Git binary: %w", err)
	}
	if digest != binary.digest {
		return "", errors.New("trusted Git binary content drifted")
	}
	return path, nil
}

// trustedGitBinaryFromProofs 从 receipt tools 中提取唯一 Git proof 并立即复验。
func trustedGitBinaryFromProofs(tools []localExecutorToolProof) (TrustedGitBinary, error) {
	for _, tool := range tools {
		if tool.name == "git" {
			binary := TrustedGitBinary{path: tool.path, digest: tool.digest, version: tool.version}
			if _, err := binary.VerifiedPath(); err != nil {
				return TrustedGitBinary{}, err
			}
			return binary, nil
		}
	}
	return TrustedGitBinary{}, errors.New("local executor receipt Git tool proof is missing")
}

// ResolveTrustedGitBinary 为尚未持有 sealed receipt 的 exact-tree consumer 提供唯一 gate-owned Git resolver。
func ResolveTrustedGitBinary(ctx context.Context) (TrustedGitBinary, error) {
	path, err := localReceiptToolCandidate("git")
	if err != nil {
		return TrustedGitBinary{}, err
	}
	path, err = resolveReceiptToolPath(path)
	if err != nil {
		return TrustedGitBinary{}, err
	}
	proofs, err := localReceiptToolProofs(ctx, map[string]string{"git": path})
	if err != nil {
		return TrustedGitBinary{}, err
	}
	return trustedGitBinaryFromProofs(proofs)
}

// discoverLocalReceiptTools 解析固定可信目录中的执行工具，并生成稳定排序的证明。
func discoverLocalReceiptTools(ctx context.Context, programs map[GateID]ExecutorProgram) (string, []localExecutorToolProof, error) {
	paths, err := resolveLocalReceiptToolPaths(localReceiptToolNames(programs))
	if err != nil {
		return "", nil, err
	}
	if err := addLocalReceiptSandbox(paths); err != nil {
		return "", nil, err
	}
	proofs, err := localReceiptToolProofs(ctx, paths)
	if err != nil {
		return "", nil, err
	}
	return localReceiptToolPath(paths), proofs, nil
}

// localReceiptToolNames 从程序步骤和显式要求汇总需绑定的工具名称。
func localReceiptToolNames(programs map[GateID]ExecutorProgram) map[string]struct{} {
	commands := map[string]struct{}{"git": {}, "go": {}}
	for _, program := range programs {
		for _, step := range program.Steps {
			if len(step.Argv) > 0 && filepath.Base(step.Argv[0]) == step.Argv[0] {
				if step.Argv[0] == ExecutorSelfCommandName {
					continue
				}
				commands[step.Argv[0]] = struct{}{}
			}
		}
		for _, required := range program.RequiredExecutables {
			commands[required] = struct{}{}
		}
	}
	return commands
}

func resolveLocalReceiptToolPaths(commands map[string]struct{}) (map[string]string, error) {
	paths := make(map[string]string, len(commands)+1)
	for name := range commands {
		path, err := localReceiptToolCandidate(name)
		if err != nil {
			return nil, err
		}
		resolved, err := resolveReceiptToolPath(path)
		if err != nil {
			return nil, fmt.Errorf("local executor receipt tool %q: %w", name, err)
		}
		paths[name] = resolved
	}
	return paths, nil
}

func localReceiptToolCandidate(name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	if name == "go" {
		return localExecutorTrustedGoBinary()
	}
	path, err := findTrustedReceiptTool(name)
	if err != nil {
		return "", fmt.Errorf("local executor receipt tool %q is missing: %w", name, err)
	}
	return path, nil
}

func addLocalReceiptSandbox(paths map[string]string) error {
	sandbox, err := resolveReceiptToolPath("/usr/bin/sandbox-exec")
	if err != nil {
		return fmt.Errorf("local executor receipt sandbox binary: %w", err)
	}
	paths["sandbox-exec"] = sandbox
	return nil
}

func localReceiptToolPath(paths map[string]string) string {
	directories := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		directory := filepath.Dir(path)
		if _, ok := seen[directory]; ok {
			continue
		}
		seen[directory] = struct{}{}
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	return strings.Join(directories, string(filepath.ListSeparator))
}

// localReceiptToolProofs 对 receipt 已解析的固定可信工具生成稳定排序的内容证明。
func localReceiptToolProofs(ctx context.Context, paths map[string]string) ([]localExecutorToolProof, error) {
	proofs := make([]localExecutorToolProof, 0, len(paths))
	for name, path := range paths {
		digest, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		version, err := probeReceiptToolVersion(ctx, name, path)
		if err != nil {
			return nil, err
		}
		proof := localExecutorToolProof{name: name, path: path, digest: digest, version: version}
		if name == "go" {
			proof.goRoot, err = resolveLocalToolchainRoot(path)
			if err != nil {
				return nil, fmt.Errorf("resolve local executor Go tool root: %w", err)
			}
		}
		proofs = append(proofs, proof)
	}
	sort.Slice(proofs, func(left, right int) bool { return proofs[left].name < proofs[right].name })
	return proofs, nil
}

// trustedReceiptToolDirectories 仅返回与运行中 gate 二进制绑定的固定可信工具目录。
func trustedReceiptToolDirectories() ([]string, error) {
	goRoot, err := localExecutorTrustedGoRoot()
	if err != nil {
		return nil, fmt.Errorf("resolve gate-owned trusted Go root: %w", err)
	}
	candidates := []string{
		filepath.Join(goRoot, "bin"), "/usr/bin", "/bin", "/usr/sbin", "/sbin",
		"/usr/local/bin", "/usr/local/go/bin", "/opt/homebrew/bin", "/opt/local/bin",
	}
	directories := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		directories = append(directories, resolved)
	}
	sort.Strings(directories)
	return directories, nil
}

// findTrustedReceiptTool 在固定可信目录内查找裸名称的可执行文件。
func findTrustedReceiptTool(name string) (string, error) {
	if strings.TrimSpace(name) == "" || filepath.Base(name) != name {
		return "", errors.New("tool name must be a bare executable name")
	}
	directories, err := trustedReceiptToolDirectories()
	if err != nil {
		return "", err
	}
	for _, directory := range directories {
		candidate := filepath.Join(directory, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", errors.New("tool is absent from fixed receipt directories")
}

// resolveReceiptToolPath 规范化工具路径，并拒绝临时、可写或目录边界外的候选。
func resolveReceiptToolPath(path string) (string, error) {
	resolved, err := canonicalReceiptToolPath(path)
	if err != nil {
		return "", err
	}
	trusted, err := isTrustedReceiptToolPath(resolved)
	if err != nil {
		return "", err
	}
	if !trusted {
		return "", errors.New("tool is outside fixed receipt directories")
	}
	if pathContains(filepath.Clean(os.TempDir()), resolved) {
		return "", errors.New("tool path is under a temporary root")
	}
	if err := verifyReceiptToolDirectories(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// canonicalReceiptToolPath 解析并验证工具文件自身是可执行的常规文件。
func canonicalReceiptToolPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("tool path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("tool is not a regular executable")
	}
	return resolved, nil
}

func isTrustedReceiptToolPath(resolved string) (bool, error) {
	directories, err := trustedReceiptToolDirectories()
	if err != nil {
		return false, err
	}
	for _, root := range directories {
		if pathContains(root, resolved) {
			return true, nil
		}
	}
	return false, nil
}

// verifyReceiptToolDirectories 拒绝路径祖先中可被其他用户写入的目录。
func verifyReceiptToolDirectories(resolved string) error {
	for directory := filepath.Dir(resolved); directory != "." && directory != string(filepath.Separator); directory = filepath.Dir(directory) {
		stat, err := os.Stat(directory)
		if err != nil {
			return err
		}
		if stat.Mode().Perm()&0o002 != 0 {
			return fmt.Errorf("tool directory %q is writable by other", directory)
		}
		if filepath.Dir(directory) == directory {
			break
		}
	}
	return nil
}

func probeReceiptToolVersion(ctx context.Context, name, path string) (string, error) {
	if name == "sandbox-exec" {
		return "macOS-sandbox-exec", nil
	}
	args := []string{"--version"}
	if filepath.Base(path) == "go" {
		args = []string{"version"}
	}
	command := exec.CommandContext(ctx, path, args...)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("probe local executor tool %q version: %w", name, err)
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return "", fmt.Errorf("probe local executor tool %q returned empty version", name)
	}
	return version, nil
}
