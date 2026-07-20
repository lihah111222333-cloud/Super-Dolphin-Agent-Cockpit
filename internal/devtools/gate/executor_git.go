package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	materializedSourceRef = "refs/source/materialized"
	baseSourceRef         = "refs/source/base"
)

// validateCopiedSnapshot 以固定 ref、脱离分支的 HEAD 和 clean 状态验证副本可信度。
func validateCopiedSnapshot(ctx context.Context, gitBinary string, sourceCopy string, environment []string) error {
	head, err := gitLine(ctx, gitBinary, sourceCopy, environment, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("resolve snapshot HEAD: %w", err)
	}
	materialized, err := gitLine(ctx, gitBinary, sourceCopy, environment, "rev-parse", "--verify", materializedSourceRef+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve materialized source ref: %w", err)
	}
	if head != materialized {
		return errors.New("snapshot HEAD does not match materialized source ref")
	}
	headData, err := os.ReadFile(filepath.Join(sourceCopy, ".git", "HEAD"))
	if err != nil || strings.TrimSpace(string(headData)) != head {
		return errors.New("snapshot HEAD must be detached at the materialized commit")
	}
	status, err := gitOutput(ctx, gitBinary, sourceCopy, environment, nil, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("verify copied snapshot status: %w", err)
	}
	if len(status) != 0 {
		return errors.New("copied snapshot is not clean")
	}
	return nil
}

// runFullTreeWhitespace 对可信 base 到 HEAD 的对象变更执行空白检查；缺失 base 时保守扫描整树。
func runFullTreeWhitespace(
	ctx context.Context,
	gitBinary string,
	sourceCopy string,
	environment []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	base, err := trustedWhitespaceBase(ctx, gitBinary, sourceCopy, environment)
	if err != nil {
		return err
	}
	step := resolvedStep{
		directory: sourceCopy,
		argv:      []string{"git", "diff", "--check", base, "HEAD", "--"},
		binary:    gitBinary,
	}
	if err := runResolvedStep(ctx, step, environment, stdout, stderr); err != nil {
		return fmt.Errorf("trusted-range whitespace check: %w", err)
	}
	return nil
}

// trustedWhitespaceBase 只接受可解析为 commit 的显式 base ref，并为确实缺失的 ref 返回空树。
func trustedWhitespaceBase(ctx context.Context, gitBinary string, sourceCopy string, environment []string) (string, error) {
	_, found, err := gitOptionalLine(ctx, gitBinary, sourceCopy, environment, "show-ref", "--hash", baseSourceRef)
	if err != nil {
		return "", fmt.Errorf("inspect trusted whitespace base ref: %w", err)
	}
	if found {
		base, err := gitLine(ctx, gitBinary, sourceCopy, environment, "rev-parse", "--verify", baseSourceRef+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("resolve trusted whitespace base commit: %w", err)
		}
		return base, nil
	}
	emptyTree, err := gitLineWithInput(ctx, gitBinary, sourceCopy, environment, bytes.NewReader(nil), "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", fmt.Errorf("resolve conservative whitespace base: %w", err)
	}
	return emptyTree, nil
}

// runChangedDiagnostics 只把可信 Git 范围内仍存在的受支持源码送入 LSP 门禁。
func runChangedDiagnostics(
	ctx context.Context,
	gitBinary string,
	sourceCopy string,
	environment []string,
	searchPath string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	selection, err := trustedChangedDiagnostics(ctx, gitBinary, sourceCopy, environment)
	if err != nil {
		return err
	}
	if len(selection.files) == 0 {
		fmt.Fprintf(stderr, "[gate-executor] lsp diagnostics skip: candidates=%d deleted=%d unsupported=%d\n", selection.candidates, selection.deleted, selection.unsupported)
		return nil
	}
	goBinary, err := resolveExecutable("go", searchPath)
	if err != nil {
		return err
	}
	argv := []string{"go", "run", "./scripts/lsp_diagnostics_gate"}
	for _, file := range selection.files {
		argv = append(argv, "--file", file)
	}
	step := resolvedStep{directory: sourceCopy, argv: argv, binary: goBinary}
	if err := runResolvedStep(ctx, step, environment, stdout, stderr); err != nil {
		return fmt.Errorf("changed LSP diagnostics: %w", err)
	}
	return nil
}

type changedDiagnosticsSelection struct {
	files       []string
	candidates  int
	deleted     int
	unsupported int
}

// trustedChangedDiagnostics 从快照内 base ref 推导诊断目标，缺失 base 时保守扫描整树。
func trustedChangedDiagnostics(
	ctx context.Context,
	gitBinary string,
	sourceCopy string,
	environment []string,
) (changedDiagnosticsSelection, error) {
	base, found, err := gitOptionalLine(ctx, gitBinary, sourceCopy, environment, "rev-parse", "--verify", "--quiet", baseSourceRef+"^{commit}")
	if err != nil {
		return changedDiagnosticsSelection{}, fmt.Errorf("resolve trusted source base: %w", err)
	}
	if !found {
		base, err = gitLineWithInput(ctx, gitBinary, sourceCopy, environment, bytes.NewReader(nil), "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			return changedDiagnosticsSelection{}, fmt.Errorf("resolve conservative diagnostics base: %w", err)
		}
	}
	output, err := gitOutput(ctx, gitBinary, sourceCopy, environment, nil, "diff", "--name-only", "-z", "--diff-filter=ACMRDT", base, "HEAD", "--")
	if err != nil {
		return changedDiagnosticsSelection{}, fmt.Errorf("derive changed diagnostics targets: %w", err)
	}
	selection, err := selectChangedDiagnostics(sourceCopy, output)
	if err != nil {
		return changedDiagnosticsSelection{}, err
	}
	return selection, nil
}

// selectChangedDiagnostics 过滤 Git 输出并区分可诊断文件、删除文件和不支持文件。
func selectChangedDiagnostics(root string, output []byte) (changedDiagnosticsSelection, error) {
	parts := bytes.Split(output, []byte{0})
	selection := changedDiagnosticsSelection{}
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		selection.candidates++
		relative := string(part)
		if err := addChangedDiagnostic(root, relative, &selection); err != nil {
			return changedDiagnosticsSelection{}, err
		}
	}
	return selection, nil
}

// addChangedDiagnostic 将单个规范 Git 路径归类为可诊断、删除或不支持。
func addChangedDiagnostic(root string, relative string, selection *changedDiagnosticsSelection) error {
	if !canonicalChangedPath(relative) {
		return errors.New("Git returned a non-canonical changed path")
	}
	if !lspDiagnosticsEligible(relative) {
		selection.unsupported++
		return nil
	}
	info, err := os.Lstat(filepath.Join(root, relative))
	if errors.Is(err, os.ErrNotExist) {
		selection.deleted++
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("changed diagnostics path %q is not a regular file", relative)
	}
	selection.files = append(selection.files, relative)
	return nil
}

func canonicalChangedPath(path string) bool {
	return !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." &&
		!strings.HasPrefix(path, ".."+string(filepath.Separator))
}

// lspDiagnosticsEligible 与 ai_maintenance 门禁保持一致，只接受底层 LSP 支持的源码边界。
func lspDiagnosticsEligible(path string) bool {
	prefixEligible := strings.HasPrefix(path, "frontend-app/") || strings.HasPrefix(path, "cmd/") ||
		strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "pkg/") ||
		(strings.HasPrefix(path, "scripts/") && strings.HasSuffix(path, ".go"))
	if !prefixEligible {
		return false
	}
	for _, suffix := range []string{".go", ".js", ".jsx", ".ts", ".tsx", ".css", ".sql"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// gitOptionalLine 读取可缺失的单行 Git 结果，并只把明确的未找到状态视为 absent。
func gitOptionalLine(
	ctx context.Context,
	binary string,
	directory string,
	environment []string,
	args ...string,
) (string, bool, error) {
	command := exec.CommandContext(ctx, binary, args...)
	configureCommandCancellation(command)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := runConfiguredCommand(command)
	if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 && stdout.Len() == 0 && stderr.Len() == 0 {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("git %q: %w: %s", args, err, strings.TrimSpace(stderr.String()))
	}
	line := strings.TrimSpace(stdout.String())
	if line == "" || strings.ContainsAny(line, "\r\n\x00") {
		return "", false, errors.New("git returned a non-canonical optional line")
	}
	return line, true, nil
}

func gitLine(ctx context.Context, binary string, directory string, environment []string, args ...string) (string, error) {
	return gitLineWithInput(ctx, binary, directory, environment, nil, args...)
}

func gitLineWithInput(
	ctx context.Context,
	binary string,
	directory string,
	environment []string,
	input io.Reader,
	args ...string,
) (string, error) {
	output, err := gitOutput(ctx, binary, directory, environment, input, args...)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(output))
	if line == "" || strings.ContainsAny(line, "\r\n\x00") {
		return "", errors.New("git returned a non-canonical line")
	}
	return line, nil
}

func gitOutput(
	ctx context.Context,
	binary string,
	directory string,
	environment []string,
	input io.Reader,
	args ...string,
) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	configureCommandCancellation(command)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdin = input
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := runConfiguredCommand(command); err != nil {
		return nil, fmt.Errorf("git %q: %w: %s", args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
