package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerArgs 按 gopls 二进制内容、Go 构建环境和 daemon 参数派生唯一共享 cohort。
func runtimeServerArgs(command multilsp.ServerCommand, binary string, env []string) ([]string, error) {
	return runtimeServerArgsForOS(command, binary, env, runtime.GOOS)
}

// runtimeServerArgsForOS 在支持 auto daemon 的平台派生 cohort，在 Windows 移除不受支持的 remote 参数。
func runtimeServerArgsForOS(command multilsp.ServerCommand, binary string, env []string, goos string) ([]string, error) {
	args := slices.Clone(command.Args)
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return args, nil
	}
	if goos == "windows" {
		return slices.DeleteFunc(args, func(arg string) bool {
			return strings.HasPrefix(arg, "-remote=") || strings.HasPrefix(arg, "-remote.listen.timeout=")
		}), nil
	}
	cohortID, err := runtimeServerGoplsCohortID(command, binary, env)
	if err != nil {
		return nil, err
	}
	for index, arg := range args {
		if strings.HasPrefix(arg, "-remote=auto;") {
			args[index] = "-remote=auto;" + cohortID
		}
	}
	return args, nil
}

func runtimeServerGoplsCohortID(command multilsp.ServerCommand, binary string, env []string) (string, error) {
	_, binaryDigest, err := runtimeServerBinaryIdentity(binary, env)
	if err != nil {
		return "", err
	}
	cohortEnv := runtimeServerGoplsEnvironment(env)
	cohortEnv = append(cohortEnv, runtimeServerGoplsDaemonArgs(command.Args))
	cohortEnv = append(cohortEnv, "GOPLS_BINARY_SHA256="+binaryDigest)
	return "sdmcp2-" + runtimeServerEnvironmentFingerprint(cohortEnv), nil
}

func runtimeServerGoplsRemoteID(args []string) string {
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "-remote=auto;"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// runtimeServerGoplsEnvironment 合并父环境与适配器覆盖，只保留会影响 Go 构建或工具链选择的变量。
func runtimeServerGoplsEnvironment(overrides []string) []string {
	values := make(map[string]string)
	for _, entry := range append(os.Environ(), overrides...) {
		key, value, ok := strings.Cut(entry, "=")
		if ok && runtimeServerRelevantGoplsEnvironmentKey(key) {
			values[key] = value
		}
	}
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

// runtimeServerRelevantGoplsEnvironmentKey 识别会改变 gopls 构建语义或基础工具链的父环境变量。
func runtimeServerRelevantGoplsEnvironmentKey(key string) bool {
	if strings.HasPrefix(key, "GO_AGENT_") {
		return false
	}
	if strings.HasPrefix(key, "GO") || strings.HasPrefix(key, "CGO_") {
		return true
	}
	switch key {
	case "HOME", "PATH", "CC", "CXX", "FC", "AR", "PKG_CONFIG", "SDKROOT", "MACOSX_DEPLOYMENT_TARGET", "CPATH", "LIBRARY_PATH":
		return true
	default:
		return false
	}
}

// runtimeServerGoplsDaemonArgs 把 remote 地址之外的 daemon 参数纳入 cohort，防止不同 idle 策略互相抢占。
func runtimeServerGoplsDaemonArgs(args []string) string {
	settings := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-remote=auto;") {
			settings = append(settings, arg)
		}
	}
	return "GOPLS_DAEMON_ARGS=" + strings.Join(settings, "\x00")
}

// runtimeServerEnvironmentFingerprint 对最终环境覆盖做顺序无关、last-wins 的短指纹。
func runtimeServerEnvironmentFingerprint(env []string) string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var normalized strings.Builder
	for _, key := range keys {
		normalized.WriteString(key)
		normalized.WriteByte('=')
		normalized.WriteString(values[key])
		normalized.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(normalized.String()))
	return hex.EncodeToString(sum[:6])
}

// runtimeServerUsesSharedGoplsDaemon 识别会派生进程外共享 daemon 的 gopls auto-remote 命令。
func runtimeServerUsesSharedGoplsDaemon(command multilsp.ServerCommand) bool {
	base := filepath.Base(strings.TrimSpace(command.Executable))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base == "gopls" && slices.ContainsFunc(command.Args, func(arg string) bool {
		return strings.HasPrefix(arg, "-remote=auto;")
	})
}
