//go:build windows

package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	windowsRubyLSPRuntimeRootName = "rubyinstaller-4.0.5-1-arm"
	windowsRubyLSPGemRootName     = "gems"
	windowsRubyLSPVersion         = "0.26.10"
	windowsRubyLSPProtocolVersion = "3.17.0.0"
)

// windowsRubyLSPRuntimeRoot 返回固定 RubyInstaller ARM64 的 cohort 相对根目录。
// 该路径只用于产品私有环境和启动参数，禁止从 PATH 或用户 Ruby 配置推导。
func windowsRubyLSPRuntimeRoot(root string) string {
	return filepath.Join(root, windowsRubyLSPRuntimeRootName)
}

// windowsRubyLSPPrivateEnvironment 构造 Ruby LSP 的完全私有环境，避免 GEM_HOME、
// GEM_PATH、Bundler 或用户目录把 rbs/prism 解析到系统安装。调用方会把这些覆盖项
// 合并到子进程环境；PATH 只保留 cohort Ruby bin 与 Windows System32。
func windowsRubyLSPPrivateEnvironment(root string) []string {
	root = filepath.Clean(root)
	runtimeRoot := windowsRubyLSPRuntimeRoot(root)
	gemRoot := filepath.Join(root, windowsRubyLSPGemRootName)
	defaultGemRoot := filepath.Join(runtimeRoot, "lib", "ruby", "gems", "4.0.0")
	privateRoot := filepath.Join(root, ".ruby-lsp-private")
	bundleRoot := filepath.Join(privateRoot, ".bundle")
	pathValue := filepath.Join(runtimeRoot, "bin")
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		pathValue += string(os.PathListSeparator) + filepath.Join(systemRoot, "System32")
	}
	return []string{
		"GEM_HOME=" + gemRoot,
		"GEM_PATH=" + gemRoot + string(os.PathListSeparator) + defaultGemRoot,
		"BUNDLE_GEMFILE=" + filepath.Join(privateRoot, "Gemfile"),
		// 空值由 Windows command environment 合并器解释为删除该变量；不能使用
		// RubyGems 约定的 "-"，因为它会从工作目录向上发现并执行 Gemfile/gem.deps.rb。
		"RUBYGEMS_GEMDEPS=",
		"BUNDLE_USER_CONFIG=" + filepath.Join(bundleRoot, "config"),
		"BUNDLE_APP_CONFIG=" + bundleRoot,
		"BUNDLE_PATH=" + gemRoot,
		"GEMRC=" + filepath.Join(privateRoot, "gemrc"),
		"RUBYOPT=",
		"RUBYLIB=",
		"HOME=" + filepath.Join(privateRoot, "home"),
		"USERPROFILE=" + filepath.Join(privateRoot, "home"),
		"APPDATA=" + filepath.Join(privateRoot, "appdata"),
		"LOCALAPPDATA=" + filepath.Join(privateRoot, "localappdata"),
		"PATH=" + pathValue,
	}
}

// windowsRubyLSPProcessRoot 将已通过 cohort 身份校验的根转换为同一文件身份的
// 8.3 路径。完整 root 仍是 ready 收据和完整性校验事实；短 root 只进入 Ruby
// 子进程，避免 RubyGems 的深层 require_relative 路径越过 Windows MAX_PATH。
func windowsRubyLSPProcessRoot(root string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || !filepath.IsAbs(root) {
		return "", fmt.Errorf("Ruby LSP cohort root must be absolute")
	}
	shortRoot, err := windowsShortProcessPath(root)
	if err != nil {
		return "", fmt.Errorf("resolve short Ruby LSP cohort root: %w", err)
	}
	return shortRoot, nil
}

// WindowsRubyLSPProcessEnvironment 返回只用于 Ruby 子进程的短路径环境；不把短
// 路径写入安装收据或缓存身份。路径转换失败必须阻断，不能回退到长路径。
func WindowsRubyLSPProcessEnvironment(root string) ([]string, error) {
	shortRoot, err := windowsRubyLSPProcessRoot(root)
	if err != nil {
		return nil, err
	}
	return windowsRubyLSPPrivateEnvironment(shortRoot), nil
}

// WindowsRubyLSPEnvironment 返回产品启动 Ruby LSP 所需的私有环境覆盖项。
// 该 Windows API 不读取系统 Ruby、Bundler 或用户 gem 配置。
func WindowsRubyLSPEnvironment(root string) []string {
	return windowsRubyLSPPrivateEnvironment(root)
}

// windowsRubyLSPEnvironmentValue 按 Windows 环境变量不区分大小写读取覆盖值。
func windowsRubyLSPEnvironmentValue(env []string, key string) string {
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

// windowsRubyLSPLaunchArguments 返回 Ruby 解释器启动 Ruby LSP 0.26.10 的固定参数。
// 各路径均由同一 cohort 根派生，避免通过 ruby-lsp wrapper、Bundler 或 PATH 逃逸。
func windowsRubyLSPLaunchArguments(root string) ([]string, error) {
	root = filepath.Clean(root)
	if root == "." || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("Ruby LSP cohort root must be absolute")
	}
	rubyLSPRoot := filepath.Join(root, windowsRubyLSPGemRootName, "gems", "ruby-lsp-"+windowsRubyLSPVersion)
	protocolRoot := filepath.Join(root, windowsRubyLSPGemRootName, "gems", "language_server-protocol-"+windowsRubyLSPProtocolVersion)
	return []string{
		"-I", filepath.Join(rubyLSPRoot, "lib"),
		"-I", filepath.Join(protocolRoot, "lib"),
		filepath.Join(rubyLSPRoot, "exe", "ruby-lsp"),
	}, nil
}

// WindowsRubyLSPLaunchArguments 返回产品启动 Ruby LSP 0.26.10 的固定参数。
// 调用方必须先通过 WindowsRuntimeDependency resolver 证明 root 属于 ready cohort。
func WindowsRubyLSPLaunchArguments(root string) ([]string, error) {
	return windowsRubyLSPLaunchArguments(root)
}

// WindowsRubyLSPProcessLaunchArguments 返回 Ruby 子进程使用的短路径启动参数；
// 解析器仍以完整 cohort 路径为根，短路径只跨越进程边界。
func WindowsRubyLSPProcessLaunchArguments(root string) ([]string, error) {
	shortRoot, err := windowsRubyLSPProcessRoot(root)
	if err != nil {
		return nil, err
	}
	return windowsRubyLSPLaunchArguments(shortRoot)
}

// windowsRubyLSPProcessGemPayloadPath 只缩短 gem 文件的父目录，保留固定的小写
// .gem 文件名。RubyGems 会把 8.3 的大写扩展名当作普通 gem 名称，因而不能直接
// 把整个 payload 转成 8.3 文件名；父目录短化仍保持同一文件身份并避开 MAX_PATH。
func windowsRubyLSPProcessGemPayloadPath(payload string) (string, error) {
	shortParent, err := windowsShortProcessPath(filepath.Dir(payload))
	if err != nil {
		return "", err
	}
	return filepath.Join(shortParent, filepath.Base(payload)), nil
}

// windowsRubyLSPExpectedPaths 返回安装完成后必须存在的 Ruby LSP 入口和协议库路径。
func windowsRubyLSPExpectedPaths(root string) []string {
	rubyLSPRoot := filepath.Join(root, windowsRubyLSPGemRootName, "gems", "ruby-lsp-"+windowsRubyLSPVersion)
	protocolRoot := filepath.Join(root, windowsRubyLSPGemRootName, "gems", "language_server-protocol-"+windowsRubyLSPProtocolVersion)
	return []string{
		filepath.Join(rubyLSPRoot, "exe", "ruby-lsp"),
		filepath.Join(rubyLSPRoot, "lib"),
		filepath.Join(protocolRoot, "lib", "language_server", "protocol.rb"),
	}
}

// installRubyLSPWindowsRuntimeDependency 只使用同一 stage 内的 RubyInstaller 和
// 已校验 gem payload。--local、--ignore-dependencies 与私有 GEM_PATH 共同阻断
// Bundler、用户 gem 配置和联网依赖解析；运行时默认 rbs/prism 必须来自 RubyInstaller。
func installRubyLSPWindowsRuntimeDependency(
	ctx context.Context,
	entry WindowsRuntimeDependencyCatalogEntry,
	architecture, stage string,
	payloads map[string]string,
	runner WindowsRuntimeDependencyCommandRunner,
) error {
	if architecture != WindowsHostArchARM64 {
		return &WindowsRuntimeDependencyUnsupportedError{Product: entry.Product, Architecture: architecture, Reason: "Ruby LSP is locked to the native ARM64 closure"}
	}
	if runner == nil {
		runner = defaultWindowsRuntimeDependencyCommandRunner
	}
	runtimePath := filepath.Join(stage, filepath.FromSlash(runtimeDependencyRuntimeExecutablePath(entry, architecture)))
	if _, err := requireRegularWindowsRuntimeDependencyPath(runtimePath); err != nil {
		return fmt.Errorf("resolve RubyInstaller ARM64 runtime: %w", err)
	}
	processRoot, err := windowsRubyLSPProcessRoot(stage)
	if err != nil {
		return err
	}
	processRuntimePath, err := windowsShortProcessPath(runtimePath)
	if err != nil {
		return fmt.Errorf("resolve short RubyInstaller ARM64 runtime: %w", err)
	}
	for _, component := range []string{"ruby-lsp", "language-server-protocol"} {
		payload, ok := payloads[component]
		if !ok {
			return fmt.Errorf("Ruby LSP fixed gem payload %q is missing", component)
		}
		if err := validateWindowsInstallerPathWithinRoot(stage, payload, false); err != nil {
			return fmt.Errorf("Ruby LSP gem payload %q escaped cohort: %w", component, err)
		}
		if _, err := requireRegularWindowsRuntimeDependencyPath(payload); err != nil {
			return fmt.Errorf("Ruby LSP gem payload %q is invalid: %w", component, err)
		}
	}
	for _, directory := range []string{
		filepath.Join(stage, windowsRubyLSPGemRootName),
		filepath.Join(stage, ".ruby-lsp-private", "home"),
		filepath.Join(stage, ".ruby-lsp-private", "appdata"),
		filepath.Join(stage, ".ruby-lsp-private", "localappdata"),
	} {
		if err := ensureDirectoryNoSymlink(directory); err != nil {
			return fmt.Errorf("create private Ruby LSP directory: %w", err)
		}
	}
	gemScript := filepath.Join(filepath.Dir(runtimePath), "gem")
	if _, err := requireRegularWindowsRuntimeDependencyPath(gemScript); err != nil {
		return fmt.Errorf("resolve RubyGems script from RubyInstaller: %w", err)
	}
	processGemScript, err := windowsShortProcessPath(gemScript)
	if err != nil {
		return fmt.Errorf("resolve short RubyGems script: %w", err)
	}
	env := windowsRubyLSPPrivateEnvironment(processRoot)
	for _, component := range []string{"language-server-protocol", "ruby-lsp"} {
		payload := payloads[component]
		processPayload, err := windowsRubyLSPProcessGemPayloadPath(payload)
		if err != nil {
			return fmt.Errorf("resolve short Ruby LSP gem payload %q: %w", component, err)
		}
		args := append([]string{processGemScript}, entry.Install.Args...)
		args = append(args, processPayload)
		if err := runner(ctx, processRuntimePath, processRoot, args, env); err != nil {
			return fmt.Errorf("install fixed Ruby LSP gem %q offline: %w", component, err)
		}
	}
	if err := requireWindowsRubyLSPInstalledClosure(stage); err != nil {
		return err
	}
	return nil
}

func requireWindowsRubyLSPInstalledClosure(stage string) error {
	for _, required := range windowsRubyLSPExpectedPaths(stage) {
		if strings.HasSuffix(required, string(filepath.Separator)+"lib") {
			info, err := os.Lstat(required)
			if err != nil || isUnsafeAssetFile(info) || !info.IsDir() {
				return fmt.Errorf("Ruby LSP required library directory is unavailable: %s", securefs.RedactPath(required))
			}
			continue
		}
		if _, err := requireRegularWindowsRuntimeDependencyPath(required); err != nil {
			return fmt.Errorf("Ruby LSP required path is unavailable: %s: %w", securefs.RedactPath(required), err)
		}
	}
	if err := requireWindowsRubyDefaultGem(stage, "rbs"); err != nil {
		return err
	}
	if err := requireWindowsRubyDefaultGem(stage, "prism"); err != nil {
		return err
	}
	return nil
}

func requireWindowsRubyDefaultGem(stage, prefix string) error {
	gemRoot := filepath.Join(windowsRubyLSPRuntimeRoot(stage), "lib", "ruby", "gems", "4.0.0", "gems")
	entries, err := os.ReadDir(gemRoot)
	if err != nil {
		return fmt.Errorf("inspect RubyInstaller default %s gem root: %w", prefix, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(strings.ToLower(entry.Name()), strings.ToLower(prefix)+"-") && entry.IsDir() {
			info, err := os.Lstat(filepath.Join(gemRoot, entry.Name()))
			if err == nil && !isUnsafeAssetFile(info) {
				return nil
			}
		}
	}
	return fmt.Errorf("RubyInstaller default gem %q is missing from the private GEM_PATH", prefix)
}
