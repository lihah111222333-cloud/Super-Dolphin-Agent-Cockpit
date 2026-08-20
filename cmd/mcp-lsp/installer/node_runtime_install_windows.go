//go:build windows

package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

// InstallWindowsNodeRuntimeExactPackages 在 Windows 锁定 Node cohort 内执行一次
// 精确 npm 安装；该动作可联网并写入可变 cache，调用方必须由 InstallAction 的
// timeout/lock/capability 生命周期触发，失败直接返回且不退回 PATH。
func (r *WindowsNodeRuntime) InstallWindowsNodeRuntimeExactPackages(ctx context.Context, packages []string) (paths WindowsNodeRuntimePaths, err error) {
	if r == nil {
		return WindowsNodeRuntimePaths{}, errors.New("Windows Node runtime is nil")
	}
	if ctx == nil {
		return WindowsNodeRuntimePaths{}, errors.New("Windows Node runtime context is nil")
	}
	if len(packages) == 0 {
		return WindowsNodeRuntimePaths{}, errors.New("exact Windows npm package list is empty")
	}
	paths, err = r.Ensure(ctx)
	if err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	installLock, err := acquireAssetOSLock(ctx, windowsNodeRuntimeInstallLockPath(paths.Prefix))
	if err != nil {
		return WindowsNodeRuntimePaths{}, fmt.Errorf("lock Windows Node npm cohort prefix %q: %w", paths.Prefix, err)
	}
	defer func() {
		if closeErr := installLock.Close(); err == nil && closeErr != nil {
			paths = WindowsNodeRuntimePaths{}
			err = fmt.Errorf("release Windows Node npm cohort prefix lock: %w", closeErr)
		}
	}()
	// Another sidecar may have completed the exact install while this process waited.
	// Revalidate under the OS lock before invoking npm so a valid cohort is never rewritten.
	if err := r.ValidateExactPackages(ctx, packages); err == nil {
		if err := publishWindowsNodeRuntimePath(paths.NodePath); err != nil {
			return WindowsNodeRuntimePaths{}, err
		}
		return paths, nil
	}
	if err := installWindowsNodeRuntimePackages(ctx, paths.NPMPath, paths.Prefix, packages); err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	if err = r.ValidateExactPackages(ctx, packages); err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	// 只有 InstallAction 到达这里时才发布已校验的绝对 Node 路径；Windows
	// runtime resolver 读取该事实或从 server cache 路径推导，绝不退回 PATH。
	if err := publishWindowsNodeRuntimePath(paths.NodePath); err != nil {
		return WindowsNodeRuntimePaths{}, err
	}
	return paths, nil
}

// installWindowsNodeRuntimePackages 执行锁定 Node cohort 的 npm 安装，并只返回
// 子进程退出码、参数/包数量和输出摘要；npm 的原始 stderr、路径及包版本不能进入错误。
func installWindowsNodeRuntimePackages(ctx context.Context, npmPath, prefix string, packages []string) error {
	// Windows 10/11 可以关闭 LongPathsEnabled；完整 SHA cache 路径仍是事实源，
	// 但 npm.cmd 与 Node 模块加载必须使用同一文件身份的 8.3 路径，否则深层
	// npm 文件会被 Win32 误报为不存在并以 exit=1、空输出结束。
	processNPMPath, err := windowsShortProcessPath(npmPath)
	if err != nil {
		return err
	}
	processPrefix, err := windowsShortProcessPath(prefix)
	if err != nil {
		return err
	}
	// 锁定语言服务包必须是可直接运行的发布产物；禁止 npm 执行任意依赖包的
	// install/postinstall 脚本，既避免旧 core-js 等脚本依赖宿主 PATH，也把
	// InstallAction 的执行边界限制在 npm 解包与链接阶段。
	args := []string{"install", "--prefix", processPrefix, "--save-exact", "--ignore-scripts"}
	args = append(args, packages...)
	command := hiddenexec.CommandContext(ctx, processNPMPath, args...)
	// npm.cmd 通过绝对 Node 路径启动 npm 自身，但依赖包 lifecycle script 仍只会
	// 以命令名调用 node。把同一份 8.3 runtime 目录显式置于该子进程 PATH 首位，
	// 避免 Windows 深路径或宿主 PATH 隔离导致 postinstall 在下载完成后失败。
	processPath := filepath.Dir(processNPMPath)
	if inheritedPath := strings.TrimSpace(os.Getenv("PATH")); inheritedPath != "" {
		processPath += string(os.PathListSeparator) + inheritedPath
	}
	command.Env = runtimeDependencyCommandEnvironment([]string{"PATH=" + processPath})
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	return newProcessFailureError(
		"windows-node-npm-install",
		"npm",
		joinProcessFailureCause(ctx.Err(), err),
		output,
		len(args),
		len(packages),
	)
}

func windowsNodeRuntimeInstallLockPath(prefix string) string {
	return filepath.Join(prefix, ".windows-node-install.lock")
}

func publishWindowsNodeRuntimePath(nodePath string) error {
	// Only InstallAction publishes this verified absolute path; read-only resolvers
	// never mutate process environment or fall back to PATH.
	if err := os.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", nodePath); err != nil {
		return fmt.Errorf("publish Windows locked Node executable path: %w", err)
	}
	return nil
}
