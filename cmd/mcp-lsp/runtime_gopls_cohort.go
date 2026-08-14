package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

const runtimeServerGoplsGoEnvTimeout = 5 * time.Second

func runtimeServerGoplsDefaultableEnvironmentKeys() []string {
	return []string{"AR", "CC", "CXX", "FC", "GCCGO", "GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT", "PKG_CONFIG"}
}

// runtimeServerArgs 按 gopls 二进制内容、Go 构建环境和 daemon 参数派生唯一共享 cohort。
func runtimeServerArgs(command multilsp.ServerCommand, binary string, env []string) ([]string, error) {
	return runtimeServerArgsPlatform(command, binary, env)
}

// runtimeServerGoplsAutoDaemonArgs 为非 Windows auto daemon 派生稳定 cohort 参数。
func runtimeServerGoplsAutoDaemonArgs(command multilsp.ServerCommand, binary string, env []string, workspaceRoot ...string) ([]string, error) {
	args := slices.Clone(command.Args)
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

// runtimeServerGoplsCohortID 按可选 root authority 与稳定工具链身份生成共享 daemon ID。
func runtimeServerGoplsCohortID(command multilsp.ServerCommand, binary string, env []string, workspaceRoot ...string) (string, error) {
	if len(workspaceRoot) > 0 && strings.TrimSpace(workspaceRoot[0]) != "" {
		config, err := runtimeServerGoplsRootCohortConfig(command, binary, workspaceRoot[0], env)
		if err != nil {
			return "", err
		}
		return config.CohortID, nil
	}
	binaryRealpath, binaryDigest, err := runtimeServerBinaryIdentity(binary, env)
	if err != nil {
		return "", err
	}
	cohortEnv, err := runtimeServerGoplsSemanticEnvironment(env)
	if err != nil {
		return "", err
	}
	cohortEnv = append(cohortEnv, runtimeServerGoplsDaemonArgs(command.Args))
	cohortEnv = append(cohortEnv, "GOPLS_BINARY_REALPATH="+binaryRealpath)
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
	if err := validateRuntimeServerGoplsRootCohortPlatform(); err != nil {
		return multilsp.GoplsRootCohortConfig{}, err
	}
	proof, err := runtimeServerGoplsRepositoryInstanceProof(workspaceRoot)
	if err != nil {
		return multilsp.GoplsRootCohortConfig{}, err
	}
	binaryRealpath, binaryDigest, err := runtimeServerBinaryIdentity(binary, env)
	if err != nil {
		return multilsp.GoplsRootCohortConfig{}, err
	}
	effective, err := runtimeServerGoplsEffectiveConfigDigest(command, binaryRealpath, binaryDigest, env)
	if err != nil {
		return multilsp.GoplsRootCohortConfig{}, err
	}
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
	filesystemIdentity, err := runtimeServerStableFilesystemIdentity(canonical, info)
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

func runtimeServerGoplsEffectiveConfigDigest(command multilsp.ServerCommand, binaryRealpath, binaryDigest string, env []string) (string, error) {
	semanticEnv, err := runtimeServerGoplsSemanticEnvironment(env)
	if err != nil {
		return "", err
	}
	value := strings.Join([]string{
		"gopls-effective-config-v3",
		binaryRealpath,
		binaryDigest,
		runtimeServerEnvironmentFingerprint(semanticEnv),
		runtimeServerGoplsDaemonArgs(command.Args),
	}, "\x00")
	return runtimeServerDigestString(value), nil
}

// runtimeServerGoplsSemanticEnvironment 将会影响 Go 构建的环境收敛为稳定语义身份。
// 原始 PATH 只用于解析实际 Go 与显式辅助工具；digest 记录解析后的真实路径与内容摘要，
// 避免 Codex arg0 临时目录、重复目录或等价 PATH 顺序把同一工具链拆成不同 cohort。
func runtimeServerGoplsSemanticEnvironment(overrides []string) ([]string, error) {
	relevant := runtimeServerGoplsFilterInactiveGCCGO(runtimeServerGoplsEnvironment(overrides))
	goBinary, goDigest, err := runtimeServerBinaryIdentity("go", relevant)
	if err != nil {
		return nil, fmt.Errorf("resolve Go toolchain for gopls cohort: %w", err)
	}
	defaultable, err := runtimeServerGoplsDefaultEnvironment(goBinary, relevant)
	if err != nil {
		return nil, err
	}
	semantic, auxiliaryInput := runtimeServerGoplsFilteredEnvironment(relevant, defaultable)
	semantic = append(semantic,
		"GO_BINARY_REALPATH="+goBinary,
		"GO_BINARY_SHA256="+goDigest,
	)
	auxiliary, err := runtimeServerGoplsAuxiliaryToolEnvironment(auxiliaryInput)
	if err != nil {
		return nil, err
	}
	return append(semantic, auxiliary...), nil
}

// runtimeServerGoplsFilteredEnvironment 移除 Go 自身报告的默认值，并保留显式辅助工具输入。
func runtimeServerGoplsFilteredEnvironment(relevant []string, defaultable map[string]string) ([]string, []string) {
	semantic := make([]string, 0, len(relevant))
	auxiliaryInput := make([]string, 0, len(relevant))
	usesGCCGO := runtimeServerGoplsUsesGCCGO(relevant)
	for _, entry := range relevant {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == "PATH" {
			auxiliaryInput = append(auxiliaryInput, entry)
			continue
		}
		defaultValue, defaultableKey := defaultable[key]
		if defaultableKey && (value == "" || value == defaultValue) && !(key == "GCCGO" && usesGCCGO) {
			continue
		}
		semantic = append(semantic, entry)
		auxiliaryInput = append(auxiliaryInput, entry)
	}
	return semantic, auxiliaryInput
}

// runtimeServerGoplsAuxiliaryToolEnvironment 将显式 Go 辅助工具绑定到真实二进制身份。
func runtimeServerGoplsAuxiliaryToolEnvironment(relevant []string) ([]string, error) {
	semantic := make([]string, 0, 16)
	for _, key := range []string{"CC", "CXX", "FC", "AR", "GCCGO", "GOCACHEPROG", "PKG_CONFIG"} {
		command := runtimeServerGoplsEnvironmentValue(relevant, key)
		if command == "" {
			continue
		}
		executable, err := runtimeServerCommandExecutable(command)
		if err != nil {
			return nil, fmt.Errorf("parse %s for gopls cohort: %w", key, err)
		}
		toolRealpath, toolDigest, err := runtimeServerBinaryIdentity(executable, relevant)
		if err != nil {
			return nil, fmt.Errorf("resolve %s tool for gopls cohort: %w", key, err)
		}
		semantic = append(semantic,
			key+"_BINARY_REALPATH="+toolRealpath,
			key+"_BINARY_SHA256="+toolDigest,
		)
	}
	return semantic, nil
}

// runtimeServerGoplsEnvironmentValue 只读取已经过默认值过滤的环境，不回退到父进程。
func runtimeServerGoplsEnvironmentValue(environment []string, key string) string {
	value := ""
	for _, entry := range environment {
		entryKey, entryValue, ok := strings.Cut(entry, "=")
		if ok && entryKey == key {
			value = strings.TrimSpace(entryValue)
		}
	}
	return value
}

// runtimeServerGoplsFilterInactiveGCCGO 移除 gc 构建不会读取的默认 GCCGO 值。
func runtimeServerGoplsFilterInactiveGCCGO(relevant []string) []string {
	if runtimeServerGoplsUsesGCCGO(relevant) {
		return relevant
	}
	return slices.DeleteFunc(slices.Clone(relevant), func(entry string) bool {
		key, _, ok := strings.Cut(entry, "=")
		return ok && key == "GCCGO"
	})
}

// runtimeServerGoplsUsesGCCGO 仅在 Go 构建参数明确选择 gccgo 时启用 GCCGO。
// go env 会在 gc 工具链上也报告默认 GCCGO=gccgo；无条件解析该值会把一个未使用、
// 通常也未安装的命令错误地升级为 gopls cohort 的启动依赖。
func runtimeServerGoplsUsesGCCGO(relevant []string) bool {
	fields := strings.Fields(runtimeServerEnvValue(relevant, "GOFLAGS"))
	for index, field := range fields {
		if field == "-compiler=gccgo" {
			return true
		}
		if field == "-compiler" && index+1 < len(fields) && fields[index+1] == "gccgo" {
			return true
		}
	}
	return false
}

// runtimeServerGoplsDefaultEnvironment 查询移除显式覆盖后的 Go 默认环境，
// 使缺省值与宿主重复写出的同值收敛为同一语义身份；真正自定义值仍保留在 digest。
func runtimeServerGoplsDefaultEnvironment(goBinary string, relevant []string) (map[string]string, error) {
	defaultableKeys := runtimeServerGoplsDefaultableEnvironmentKeys()
	present := false
	for _, key := range defaultableKeys {
		if _, ok := runtimeServerEnvLookup(relevant, key); ok {
			present = true
			break
		}
	}
	if !present {
		return nil, nil
	}
	merged := appendRuntimeServerEnvironment(os.Environ(), relevant)
	probeEnv := slices.DeleteFunc(merged, func(entry string) bool {
		key, _, ok := strings.Cut(entry, "=")
		return ok && slices.Contains(defaultableKeys, key)
	})
	ctx, cancel := platformconfig.WithTimeout(context.Background(), runtimeServerGoplsGoEnvTimeout)
	defer cancel()
	args := append([]string{"env", "-json"}, defaultableKeys...)
	command := hiddenexec.CommandContext(ctx, goBinary, args...)
	command.Env = probeEnv
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("resolve default Go environment for gopls cohort: %w", err)
	}
	defaults := make(map[string]string, len(defaultableKeys))
	if err := json.Unmarshal(output, &defaults); err != nil {
		return nil, fmt.Errorf("decode default Go environment for gopls cohort: %w", err)
	}
	for _, key := range defaultableKeys {
		if _, ok := defaults[key]; !ok {
			return nil, fmt.Errorf("default Go environment for gopls cohort omitted %s", key)
		}
	}
	return defaults, nil
}

// runtimeServerCommandExecutable 读取 Go 工具变量中的首个命令词，并保留引号与转义路径语义。
func runtimeServerCommandExecutable(command string) (string, error) {
	parser := runtimeServerCommandWordParser{}
	for _, char := range command {
		if parser.consume(char) {
			return parser.executable.String(), nil
		}
	}
	return parser.finish(command)
}

type runtimeServerCommandWordParser struct {
	executable strings.Builder
	quote      rune
	escaped    bool
}

// consume 吸收一个命令字符；返回 true 表示首个命令词已经结束。
func (p *runtimeServerCommandWordParser) consume(char rune) bool {
	if p.escaped {
		p.executable.WriteRune(char)
		p.escaped = false
		return false
	}
	if p.quote != 0 {
		return p.consumeQuoted(char)
	}
	return p.consumeUnquoted(char)
}

// consumeQuoted 吸收引号内字符，单引号内不解释反斜杠。
func (p *runtimeServerCommandWordParser) consumeQuoted(char rune) bool {
	if char == '\\' && p.quote != '\'' {
		p.escaped = true
	} else if char == p.quote {
		p.quote = 0
	} else {
		p.executable.WriteRune(char)
	}
	return false
}

// consumeUnquoted 吸收未加引号字符，并在首个空白边界结束命令词。
func (p *runtimeServerCommandWordParser) consumeUnquoted(char rune) bool {
	if char == '\\' {
		p.escaped = true
		return false
	}
	if char == '\'' || char == '"' {
		p.quote = char
		return false
	}
	if char == ' ' || char == '\t' || char == '\r' || char == '\n' {
		return p.executable.Len() > 0
	}
	p.executable.WriteRune(char)
	return false
}

// finish 校验命令词终态，拒绝空命令、未闭合引号和未闭合转义。
func (p *runtimeServerCommandWordParser) finish(command string) (string, error) {
	if p.quote != 0 {
		return "", fmt.Errorf("unterminated quote in command %q", command)
	}
	if p.escaped {
		return "", fmt.Errorf("unterminated escape in command %q", command)
	}
	if p.executable.Len() == 0 {
		return "", fmt.Errorf("command executable is empty")
	}
	return p.executable.String(), nil
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
	switch key {
	case "GO111MODULE", "GO386", "GOAMD64", "GOARCH", "GOARM", "GOARM64", "GOAUTH", "GOCACHE", "GOCACHEPROG", "GODEBUG", "GOENV",
		"GOEXPERIMENT", "GOFIPS140", "GOFLAGS", "GOINSECURE", "GOMIPS", "GOMIPS64", "GOMODCACHE", "GONOPROXY", "GONOSUMDB",
		"GOOS", "GOPATH", "GOPPC64", "GOPRIVATE", "GOPROXY", "GORISCV64", "GOROOT", "GOSUMDB", "GOTOOLCHAIN",
		"GOVCS", "GOWASM", "GOWORK",
		"CGO_ENABLED", "CGO_CFLAGS", "CGO_CFLAGS_ALLOW", "CGO_CFLAGS_DISALLOW", "CGO_CPPFLAGS", "CGO_CPPFLAGS_ALLOW",
		"CGO_CPPFLAGS_DISALLOW", "CGO_CXXFLAGS", "CGO_CXXFLAGS_ALLOW", "CGO_CXXFLAGS_DISALLOW", "CGO_FFLAGS",
		"CGO_FFLAGS_ALLOW", "CGO_FFLAGS_DISALLOW", "CGO_LDFLAGS", "CGO_LDFLAGS_ALLOW", "CGO_LDFLAGS_DISALLOW",
		"HOME", "PATH", "CC", "CXX", "FC", "AR", "GCCGO", "PKG_CONFIG", "SDKROOT", "MACOSX_DEPLOYMENT_TARGET", "CPATH", "LIBRARY_PATH":
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

// runtimeServerUsesSharedGoplsDaemon 按平台识别会接入进程外共享 daemon 的 gopls 命令。
func runtimeServerUsesSharedGoplsDaemon(command multilsp.ServerCommand) bool {
	base := filepath.Base(strings.TrimSpace(command.Executable))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return runtimeServerUsesSharedGoplsDaemonPlatform(base, command.Args)
}
