package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// publishSourceArtifacts 原子移动两个只读文件，并在失败时清理局部发布。
func publishSourceArtifacts(outputRoot string, stageBundle string, stageManifest string, manifest SourceMaterializationManifest) (result SourceMaterialization, err error) {
	bundlePath := filepath.Join(outputRoot, sourceBundleName)
	manifestPath := filepath.Join(outputRoot, sourceManifestName)
	defer func() {
		if err != nil {
			err = errors.Join(err, removeSourceFile(bundlePath), removeSourceFile(manifestPath))
		}
	}()
	if err := os.Rename(stageBundle, bundlePath); err != nil {
		return SourceMaterialization{}, fmt.Errorf("publish source bundle: %w", err)
	}
	if err := os.Rename(stageManifest, manifestPath); err != nil {
		return SourceMaterialization{}, fmt.Errorf("publish source manifest: %w", err)
	}
	if err := removeSourceTemp(filepath.Dir(stageBundle)); err != nil {
		return SourceMaterialization{}, err
	}
	if err := validatePublishedArtifacts(outputRoot, bundlePath, manifestPath); err != nil {
		return SourceMaterialization{}, err
	}
	return SourceMaterialization{BundlePath: bundlePath, ManifestPath: manifestPath, Manifest: manifest}, nil
}

// validateCanonicalDirectory 拒绝非绝对、非 canonical、链接或公开输出目录。
func validateCanonicalDirectory(path string, private bool) error {
	if !validCanonicalPath(path) {
		return errors.New("path must be canonical, absolute, and free of control characters")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("canonicalize path: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat path: %w", err)
	}
	if resolved != path || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("path %q must be a real non-symlink directory (resolved=%q mode=%s)", path, resolved, info.Mode())
	}
	if private && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("private output root mode %04o exposes group or world access", info.Mode().Perm())
	}
	return nil
}

func validCanonicalPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && !containsControl(path)
}

// validatePublishedArtifacts 要求输出根只含两个 0400 regular artifacts。
func validatePublishedArtifacts(outputRoot string, paths ...string) error {
	entries, err := os.ReadDir(outputRoot)
	if err != nil {
		return fmt.Errorf("read published source artifacts: %w", err)
	}
	if len(entries) != len(paths) {
		return errors.New("source output root contains missing or trailing artifacts")
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("lstat source artifact: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != privateSourceFileMode {
			return fmt.Errorf("source artifact %s must be a 0400 regular non-symlink file", filepath.Base(path))
		}
	}
	return nil
}

func digestSourceFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open source bundle for digest: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.Join(copyErr, closeErr)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func validDigest(value string) bool {
	encoded, found := strings.CutPrefix(value, "sha256:")
	_, err := hex.DecodeString(encoded)
	return found && len(encoded) == sha256.Size*2 && encoded == strings.ToLower(encoded) && err == nil
}

// validOID 校验对象格式、长度、小写十六进制并拒绝全零 OID。
func validOID(value string, format gate.GitObjectFormat) bool {
	want := 0
	switch format {
	case gate.GitObjectFormatSHA1:
		want = 40
	case gate.GitObjectFormatSHA256:
		want = 64
	}
	_, err := hex.DecodeString(value)
	return want != 0 && len(value) == want && value == strings.ToLower(value) && strings.Trim(value, "0") != "" && err == nil
}

func strictGitLine(output []byte) (string, error) {
	if len(output) < 2 || output[len(output)-1] != '\n' || bytes.Count(output, []byte{'\n'}) != 1 {
		return "", errors.New("Git output must contain exactly one terminated line")
	}
	return string(output[:len(output)-1]), nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, func(character rune) bool { return character < 0x20 || character == 0x7f }) >= 0
}

func runGitOutput(ctx context.Context, repoRoot string, stdin io.Reader, args ...string) ([]byte, error) {
	var stdout bytes.Buffer
	err := runGit(ctx, repoRoot, stdin, &stdout, args...)
	return stdout.Bytes(), err
}

// runGit 以固定环境执行 Git plumbing，并保留 context 与 stderr 根因。
func runGit(ctx context.Context, repoRoot string, stdin io.Reader, stdout io.Writer, args ...string) error {
	return runGitWithEnvironment(ctx, repoRoot, stdin, stdout, sourceGitEnvironment(), args...)
}

// runGitWithEnvironment 使用受控环境运行 Git plumbing 并保留原始 stderr。
func runGitWithEnvironment(ctx context.Context, repoRoot string, stdin io.Reader, stdout io.Writer, environment []string, args ...string) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if interfaceValueIsNil(stdout) {
		return errors.New("Git plumbing stdout is required")
	}
	if stdin != nil && interfaceValueIsNil(stdin) {
		return errors.New("Git plumbing stdin must not be typed nil")
	}
	if len(args) == 0 {
		return errors.New("Git plumbing command is required")
	}
	commandArgs := append([]string{"--no-replace-objects", "-C", repoRoot}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Stdin = stdin
	command.Stdout = stdout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	command.Env = environment
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("git %s: %w", args[0], ctxErr)
		}
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// sourceGitEnvironment 仅继承进程定位变量，清除全部 Git repository-local 重定向环境。
func sourceGitEnvironment() []string {
	environment := make([]string, 0, 16)
	for _, key := range []string{"HOME", "PATH", "TMPDIR", "SystemRoot"} {
		if value, present := os.LookupEnv(key); present {
			environment = append(environment, key+"="+value)
		}
	}
	return append(environment,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_AUTHOR_NAME=Super Dolphin Source Materializer",
		"GIT_AUTHOR_EMAIL=source-materializer.invalid",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=Super Dolphin Source Materializer",
		"GIT_COMMITTER_EMAIL=source-materializer.invalid",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
		"LC_ALL=C",
	)
}

func closeSourceFile(file *os.File, cause error) error {
	return errors.Join(cause, file.Close())
}

// removeSourceTemp 校验并清理本模块创建的只读 source 临时目录。
func removeSourceTemp(path string) error {
	if path == "" {
		return nil
	}
	if err := validateSourceTempPath(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat source temporary directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("source temporary path must be a real directory")
	}
	if err := restoreSourceTempPermissions(path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove source temporary directory: %w", err)
	}
	return nil
}

func validateSourceTempPath(path string) error {
	if !validCanonicalPath(path) || filepath.Dir(path) == path || !strings.HasPrefix(filepath.Base(path), ".source-") {
		return errors.New("source temporary path must be a canonical named source temporary directory")
	}
	return nil
}

// restoreSourceTempPermissions 恢复本模块创建的只读临时树的 owner 写权限，
// 使清理不会依赖外部特权；任何链接或特殊文件都直接失败。
func restoreSourceTempPermissions(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source temporary tree must not contain symlink %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat source temporary entry %s: %w", path, err)
		}
		if entry.IsDir() {
			return os.Chmod(path, (info.Mode().Perm()&0o777)|0o700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source temporary tree contains unsupported entry %s", path)
		}
		return os.Chmod(path, (info.Mode().Perm()&0o777)|0o600)
	})
}

func removeSourceFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove partial source artifact: %w", err)
	}
	return nil
}
