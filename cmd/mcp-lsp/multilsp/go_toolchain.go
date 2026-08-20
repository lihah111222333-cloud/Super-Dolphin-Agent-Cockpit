package multilsp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// 自动工具链第一次运行可能需要下载，仍以有界超时阻断长期挂起。
const goVersionProbeTimeout = 30 * time.Second

type goToolchainCandidate struct {
	binDir   string
	path     string
	fromPATH bool
}

const goToolchainSelectionCacheLimit = 128

type goToolchainSelectionCache struct {
	sync.Mutex
	values map[[sha256.Size]byte]goToolchainSelectionCacheEntry
	order  [][sha256.Size]byte
}

type goToolchainSelectionCacheEntry struct {
	info       GoToolchainInfo
	executable string
	size       int64
	mtimeNS    int64
}

func withGoToolchain(info GoRootInfo, env []string, rootErr error, caches *goResolverCaches) (GoRootInfo, error) {
	if rootErr != nil {
		return info, rootErr
	}
	toolchain, err := resolveGoToolchain(
		info,
		goToolchainProbeDir(info),
		goToolchainProbeEnv(info, env), caches,
	)
	if err != nil {
		return GoRootInfo{}, err
	}
	info.GoToolchain = toolchain
	return info, nil
}

func resolveGoToolchain(info GoRootInfo, probeDir string, env []string, caches *goResolverCaches) (GoToolchainInfo, error) {
	required, source, err := requiredGoVersion(info)
	if err != nil {
		return GoToolchainInfo{}, err
	}
	if required == "" {
		return GoToolchainInfo{}, nil
	}
	toolchain, err := selectGoToolchainFromPATH(required, probeDir, env, caches)
	if err != nil {
		return GoToolchainInfo{}, fmt.Errorf("%s requires go %s but Go toolchain selection failed: %w", source, required, err)
	}
	return toolchain, nil
}

// requiredGoVersion 合并主模块与当前 go.work 的最低/首选工具链要求。
// workspace 指令高于模块时必须成为权威要求，go.work 文件目标也不能跳过探测。
func requiredGoVersion(info GoRootInfo) (string, string, error) {
	required := goVersion{}
	requiredText := ""
	source := ""
	for _, path := range []string{info.GoModPath, info.GoWorkPath} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		candidateText, err := requiredGoVersionFromFile(path)
		if err != nil {
			return "", "", err
		}
		if candidateText == "" {
			continue
		}
		candidate, err := parseGoVersion(candidateText)
		if err != nil {
			return "", "", err
		}
		if requiredText == "" || candidate.compare(required) > 0 {
			required = candidate
			requiredText = candidate.String()
			source = path
		}
	}
	return requiredText, source, nil
}

// requiredGoVersionFromFile 读取 go.mod 或 go.work 中声明的最低/首选 Go 版本。
// toolchain 指令高于 go 指令时也会参与比较，确保后续 PATH 选择满足当前 scope 要求。
func requiredGoVersionFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Go toolchain requirements %s: %w", path, err)
	}
	required := goVersion{}
	requiredText := ""
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		candidate, ok := goDirectiveVersionCandidate(fields)
		if !ok {
			continue
		}
		parsed, err := parseGoVersion(candidate)
		if err != nil {
			return "", fmt.Errorf("parse %s directive in %s: %w", fields[0], path, err)
		}
		if requiredText == "" || parsed.compare(required) > 0 {
			required = parsed
			requiredText = parsed.String()
		}
	}
	return requiredText, nil
}

func goDirectiveVersionCandidate(fields []string) (string, bool) {
	if len(fields) < 2 {
		return "", false
	}
	switch fields[0] {
	case "go":
		return fields[1], true
	case "toolchain":
		candidate := strings.TrimPrefix(fields[1], "go")
		return candidate, candidate != "default"
	default:
		return "", false
	}
}

// selectGoToolchainFromPATH 按版本要求扫描 PATH，并复用 adapter 私有的有效工具链缓存。
func selectGoToolchainFromPATH(requiredText, probeDir string, env []string, caches *goResolverCaches) (GoToolchainInfo, error) {
	startedAt := time.Now()
	required, err := parseGoVersion(requiredText)
	if err != nil {
		return GoToolchainInfo{}, err
	}
	pathValue := goToolchainPATHValue(env)
	cacheKey := goToolchainSelectionKey(requiredText, probeDir, env)
	if cached, ok := loadGoToolchainSelection(caches, cacheKey); ok {
		traceLSPTiming("go_toolchain cache=hit root=%q elapsed=%s", probeDir, time.Since(startedAt))
		return cached, nil
	}
	candidates := goToolchainCandidates(pathValue)
	candidates = append(candidates, managedGoToolchainCandidates(env)...)
	if len(candidates) == 0 {
		if strings.TrimSpace(pathValue) == "" {
			return GoToolchainInfo{}, fmt.Errorf("PATH is empty")
		}
		return GoToolchainInfo{}, fmt.Errorf("no go executable found in PATH")
	}
	selected, err := selectGoToolchainCandidate(required, pathValue, probeDir, env, candidates)
	if err == nil {
		storeGoToolchainSelection(caches, cacheKey, selected, selectedGoExecutable(candidates, selected.BinDir))
	}
	traceLSPTiming("go_toolchain cache=miss root=%q elapsed=%s err=%v", probeDir, time.Since(startedAt), err)
	return selected, err
}

func traceLSPTiming(format string, args ...any) {
	if os.Getenv("MCP_LSP_TRACE_TIMING") != "1" {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "mcp-lsp timing: "+format+"\n", args...)
}

// goToolchainSelectionKey 绑定工作区和完整环境，允许在扫描 PATH 前命中缓存。
func goToolchainSelectionKey(requiredText, probeDir string, env []string) [sha256.Size]byte {
	return sha256.Sum256([]byte(strings.Join([]string{requiredText, probeDir, strings.Join(env, "\x00")}, "\x00")))
}

// loadGoToolchainSelection 校验可执行文件身份未漂移后返回缓存的工具链选择。
func loadGoToolchainSelection(caches *goResolverCaches, key [sha256.Size]byte) (GoToolchainInfo, bool) {
	if caches == nil {
		return GoToolchainInfo{}, false
	}
	cache := &caches.toolchains
	cache.Lock()
	defer cache.Unlock()
	entry, ok := cache.values[key]
	if !ok {
		return GoToolchainInfo{}, false
	}
	info, err := os.Stat(entry.executable)
	if err != nil || info.Size() != entry.size || info.ModTime().UnixNano() != entry.mtimeNS {
		delete(cache.values, key)
		return GoToolchainInfo{}, false
	}
	return entry.info, true
}

// storeGoToolchainSelection 保存有界缓存，并在写入前绑定可执行文件大小与修改时间。
func storeGoToolchainSelection(caches *goResolverCaches, key [sha256.Size]byte, value GoToolchainInfo, executable string) {
	if caches == nil {
		return
	}
	info, err := os.Stat(executable)
	if err != nil {
		return
	}
	entry := goToolchainSelectionCacheEntry{info: value, executable: executable, size: info.Size(), mtimeNS: info.ModTime().UnixNano()}
	cache := &caches.toolchains
	cache.Lock()
	defer cache.Unlock()
	if cache.values == nil {
		cache.values = make(map[[sha256.Size]byte]goToolchainSelectionCacheEntry)
	}
	if _, exists := cache.values[key]; exists {
		cache.values[key] = entry
		return
	}
	if len(cache.order) >= goToolchainSelectionCacheLimit {
		oldest := cache.order[0]
		cache.order = cache.order[1:]
		delete(cache.values, oldest)
	}
	cache.order = append(cache.order, key)
	cache.values[key] = entry
}

func selectedGoExecutable(candidates []goToolchainCandidate, binDir string) string {
	for _, candidate := range candidates {
		if candidate.binDir == binDir {
			return candidate.path
		}
	}
	return ""
}

func selectGoToolchainCandidate(
	required goVersion,
	pathValue string,
	probeDir string,
	env []string,
	candidates []goToolchainCandidate,
) (GoToolchainInfo, error) {
	ctx, cancel := platformconfig.WithTimeout(context.Background(), goVersionProbeTimeout)
	defer cancel()
	probed := make([]string, 0, len(candidates))
	for index, candidate := range candidates {
		version, err := goExecutableVersion(ctx, candidate.path, probeDir, env)
		if err != nil {
			probed = append(probed, fmt.Sprintf("%s: %v", candidate.path, err))
			continue
		}
		probed = append(probed, fmt.Sprintf("%s=%s", candidate.path, version.String()))
		if version.compare(required) >= 0 {
			return selectedGoToolchain(required, version, pathValue, candidate.binDir, index > 0 || !candidate.fromPATH), nil
		}
	}
	return GoToolchainInfo{}, fmt.Errorf("checked PATH candidates: %s", strings.Join(probed, ", "))
}

func selectedGoToolchain(required, selected goVersion, pathValue, binDir string, overridePATH bool) GoToolchainInfo {
	toolchain := GoToolchainInfo{
		RequiredVersion: required.String(),
		SelectedVersion: selected.String(),
		BinDir:          binDir,
	}
	if overridePATH {
		toolchain.PathEnv = prependPATHDir(binDir, pathValue)
	}
	return toolchain
}

func goToolchainPATHValue(env []string) string {
	return goRootEnvValue(env, "PATH")
}

func goToolchainCandidates(pathValue string) []goToolchainCandidate {
	seenDirs := map[string]struct{}{}
	candidates := make([]goToolchainCandidate, 0)
	for _, dir := range filepath.SplitList(pathValue) {
		candidate, ok := goToolchainCandidateForDir(dir, seenDirs)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

// goToolchainCandidateForDir 在候选 bin 目录中查找可执行的 go 命令。
// seenDirs 记录规范化后的目录，避免 PATH 中的重复目录反复触发版本探测。
func goToolchainCandidateForDir(dir string, seenDirs map[string]struct{}) (goToolchainCandidate, bool) {
	if strings.TrimSpace(dir) == "" {
		return goToolchainCandidate{}, false
	}
	normalized, err := normalizeOptionalPath(dir, "")
	if err != nil {
		return goToolchainCandidate{}, false
	}
	if _, seen := seenDirs[normalized]; seen {
		return goToolchainCandidate{}, false
	}
	seenDirs[normalized] = struct{}{}
	for _, name := range goExecutableNames() {
		candidate := filepath.Join(normalized, name)
		if isExecutableFile(candidate) {
			return goToolchainCandidate{binDir: normalized, path: candidate, fromPATH: true}, true
		}
	}
	return goToolchainCandidate{}, false
}

// goExecutableVersion 在调用级共享 deadline 下运行候选 go 命令并解析其版本号。
// 坏二进制或卡住的 wrapper 会耗尽本次选择预算，而不会为每个 PATH 项重新计时。
func goExecutableVersion(ctx context.Context, path, probeDir string, env []string) (goVersion, error) {
	cmd := hiddenexec.CommandContext(ctx, path, "version")
	cmd.Dir = probeDir
	cmd.Env = env
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return goVersion{}, ctx.Err()
	}
	if err != nil {
		return goVersion{}, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "version" {
		return goVersion{}, fmt.Errorf("unexpected go version output %q", strings.TrimSpace(string(out)))
	}
	return parseGoVersion(strings.TrimPrefix(fields[2], "go"))
}

// goToolchainProbeDir 返回 go version 探测必须使用的模块或工作区目录。
// GOTOOLCHAIN=auto 只有在该目录下才能读取当前 go.mod/go.work 并选择所需工具链。
func goToolchainProbeDir(info GoRootInfo) string {
	if info.GoWorkPath != "" && info.WorkspaceRoot != "" {
		return info.WorkspaceRoot
	}
	if info.ModuleRoot != "" {
		return info.ModuleRoot
	}
	if info.GoModPath != "" {
		return filepath.Dir(info.GoModPath)
	}
	return info.WorkspaceRoot
}

// goToolchainProbeEnv 以已解析的请求环境为唯一基线，并把 GOWORK 绑定到同一探测上下文。
// req.Env=nil 已由 goRootRequestEnv 转成父环境；显式 env 不得重新继承宿主污染。
func goToolchainProbeEnv(info GoRootInfo, env []string) []string {
	probeEnv := append([]string(nil), env...)
	switch info.GOWORKMode {
	case goworkModeOff:
		probeEnv = append(probeEnv, "GOWORK=off")
	case goworkModeAuto, goworkModeExplicit:
		if info.GoWorkPath != "" {
			probeEnv = append(probeEnv, "GOWORK="+info.GoWorkPath)
		}
	}
	return probeEnv
}

func prependPATHDir(dir, pathValue string) string {
	parts := []string{dir}
	for _, existing := range filepath.SplitList(pathValue) {
		if strings.TrimSpace(existing) == "" {
			continue
		}
		normalized, err := normalizeOptionalPath(existing, "")
		if err == nil && normalized == dir {
			continue
		}
		parts = append(parts, existing)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func goExecutableNames() []string {
	if os.PathSeparator == '\\' {
		return []string{"go.exe"}
	}
	return []string{"go"}
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if os.PathSeparator == '\\' {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

type goVersion struct {
	major int
	minor int
	patch int
}

func parseGoVersion(text string) (goVersion, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(text), "go")
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return goVersion{}, fmt.Errorf("invalid Go version %q", text)
	}
	values := [3]int{}
	for i, part := range parts {
		value, err := parseGoVersionNumber(part, text)
		if err != nil {
			return goVersion{}, err
		}
		values[i] = value
	}
	return goVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func parseGoVersionNumber(part, original string) (int, error) {
	if part == "" {
		return 0, fmt.Errorf("invalid Go version %q", original)
	}
	value := 0
	for _, r := range part {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid Go version %q", original)
		}
		value = value*10 + int(r-'0')
	}
	return value, nil
}

func (v goVersion) compare(other goVersion) int {
	if v.major != other.major {
		return v.major - other.major
	}
	if v.minor != other.minor {
		return v.minor - other.minor
	}
	return v.patch - other.patch
}

// String 以 major.minor.patch 格式输出 Go 版本。
// patch 缺省时已在解析阶段补零，便于比较和错误信息复用同一表示。
func (v goVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}
