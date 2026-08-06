package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
func runtimeServerArgsForOS(command multilsp.ServerCommand, binary string, env []string, goos string, workspaceRoot ...string) ([]string, error) {
	args := slices.Clone(command.Args)
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return args, nil
	}
	if goos == "windows" {
		return slices.DeleteFunc(args, func(arg string) bool {
			return strings.HasPrefix(arg, "-remote=") || strings.HasPrefix(arg, "-remote.listen.timeout=")
		}), nil
	}
	cohortID, err := runtimeServerGoplsCohortID(command, binary, env, workspaceRoot...)
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

func runtimeServerGoplsCohortID(command multilsp.ServerCommand, binary string, env []string, workspaceRoot ...string) (string, error) {
	if len(workspaceRoot) > 0 && strings.TrimSpace(workspaceRoot[0]) != "" {
		config, err := runtimeServerGoplsRootCohortConfig(command, binary, workspaceRoot[0], env)
		if err != nil {
			return "", err
		}
		return config.CohortID, nil
	}
	_, binaryDigest, err := runtimeServerBinaryIdentity(binary, env)
	if err != nil {
		return "", err
	}
	cohortEnv := runtimeServerGoplsEnvironment(env)
	cohortEnv = append(cohortEnv, runtimeServerGoplsDaemonArgs(command.Args))
	cohortEnv = append(cohortEnv, "GOPLS_BINARY_SHA256="+binaryDigest)
	return "sdmcp2-" + runtimeServerEnvironmentFingerprint(cohortEnv), nil
}

// runtimeServerGoplsRootCohortConfig 将 main 解析出的 canonical Git/root identity
// 转换为 multilsp 的 immutable admission 配置。此配置不含可变 lease epoch。
func runtimeServerGoplsRootCohortConfig(
	command multilsp.ServerCommand,
	binary, workspaceRoot string,
	env []string,
) (multilsp.GoplsRootCohortConfig, error) {
	if runtime.GOOS == "windows" {
		return multilsp.GoplsRootCohortConfig{}, fmt.Errorf("gopls root cohort admission is unsupported on windows")
	}
	proof, err := runtimeServerGoplsRepositoryInstanceProof(workspaceRoot)
	if err != nil {
		return multilsp.GoplsRootCohortConfig{}, err
	}
	_, binaryDigest, err := runtimeServerBinaryIdentity(binary, env)
	if err != nil {
		return multilsp.GoplsRootCohortConfig{}, err
	}
	effective := runtimeServerGoplsEffectiveConfigDigest(command, binaryDigest, env)
	config := multilsp.GoplsRootCohortConfig{
		RepositoryInstanceProof: proof,
		EffectiveConfigDigest:   effective,
	}
	config.CohortID = runtimeServerGoplsCohortIDFromConfig(config)
	return config, nil
}

// runtimeServerGoplsRepositoryInstanceProof 读取 canonical root 及其 typed identity proof。
// linked worktree 使用 Git common-dir 的真实目录作为 proof owner，因此不按 worktree 路径分裂 cohort。
func runtimeServerGoplsRepositoryInstanceProof(workspaceRoot string) (multilsp.GoplsRepositoryInstanceProof, error) {
	identity, err := runtimeServerRepositoryIdentity(workspaceRoot)
	if err != nil {
		return multilsp.GoplsRepositoryInstanceProof{}, err
	}
	canonical, err := runtimeServerCanonicalRootPath(identity)
	if err != nil {
		return multilsp.GoplsRepositoryInstanceProof{}, err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return multilsp.GoplsRepositoryInstanceProof{}, fmt.Errorf("stat canonical root for gopls cohort proof: %w", err)
	}
	if !info.IsDir() {
		return multilsp.GoplsRepositoryInstanceProof{}, fmt.Errorf("canonical root for gopls cohort proof is not a directory: %s", canonical)
	}
	filesystemIdentity, err := runtimeServerStableFilesystemIdentity(info)
	if err != nil {
		return multilsp.GoplsRepositoryInstanceProof{}, err
	}
	markerDigest := runtimeServerDigestString("git-marker-v1\x00" + identity)
	rootDigest := runtimeServerDigestString("canonical-root-v1\x00" + identity)
	nonce := runtimeServerDigestString(strings.Join([]string{
		"instance-nonce-v1", rootDigest, filesystemIdentity, markerDigest,
	}, "\x00"))
	return multilsp.GoplsRepositoryInstanceProof{
		CanonicalRootDigest: rootDigest,
		FilesystemIdentity:  filesystemIdentity,
		GitMarkerDigest:     markerDigest,
		InstanceNonce:       nonce[:32],
	}, nil
}

func runtimeServerCanonicalRootPath(identity string) (string, error) {
	for _, prefix := range []string{"git:", "root:"} {
		if value, ok := strings.CutPrefix(identity, prefix); ok && strings.TrimSpace(value) != "" {
			return filepath.Clean(value), nil
		}
	}
	return "", fmt.Errorf("canonical root identity has unsupported form: %q", identity)
}

func runtimeServerGoplsEffectiveConfigDigest(command multilsp.ServerCommand, binaryDigest string, env []string) string {
	value := strings.Join([]string{
		"gopls-effective-config-v1",
		binaryDigest,
		runtimeServerEnvironmentFingerprint(runtimeServerGoplsEnvironment(env)),
		runtimeServerGoplsDaemonArgs(command.Args),
	}, "\x00")
	return runtimeServerDigestString(value)
}

func runtimeServerGoplsCohortIDFromConfig(config multilsp.GoplsRootCohortConfig) string {
	value := strings.Join([]string{
		"gopls-root-cohort-v1",
		config.RepositoryInstanceProof.CanonicalRootDigest,
		config.RepositoryInstanceProof.FilesystemIdentity,
		config.RepositoryInstanceProof.GitMarkerDigest,
		config.RepositoryInstanceProof.InstanceNonce,
		config.EffectiveConfigDigest,
	}, "\x00")
	return "sdmcp2-" + runtimeServerDigestString(value)[:12]
}

func runtimeServerDigestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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
