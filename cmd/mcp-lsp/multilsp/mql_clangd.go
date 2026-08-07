package multilsp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	clangdMQLScopeKey           = "clangdMQL"
	clangdMQLStrategyKey        = "clangdMQLStrategy"
	clangdMQLTargetHashKey      = "clangdMQLTargetSHA256"
	clangdMQLRootHashKey        = "clangdMQLRootSHA256"
	clangdMQLCandidateCountKey  = "clangdMQLCandidateCount"
	clangdMQLCandidateHashesKey = "clangdMQLCandidateSHA256"
	clangdMQLCompileTaskHashKey = "clangdMQLCompileTaskSHA256"

	clangdMQLStrategyCompileFlags      = "compile_flags"
	clangdMQLStrategyExactTask         = "exact_task"
	clangdMQLStrategySameStemTask      = "same_stem_task"
	clangdMQLStrategyNoCompileTask     = "no_compile_task"
	clangdMQLStrategyCompileFlagsError = "compile_flags_unusable"
	clangdMQLStrategyDatabaseError     = "compile_database_unusable"
	clangdMQLStrategyNoCandidate       = "no_candidate"
	clangdMQLStrategyAmbiguous         = "ambiguous_candidate"
)

type clangdLanguageAdapter struct {
	projectLanguageAdapter
}

// clangdAdapterFromConfig 为 clangd 注入项目根和 MQL 编译任务策略。
func clangdAdapterFromConfig(cfg contract.LSPConfig) clangdLanguageAdapter {
	return clangdLanguageAdapter{
		projectLanguageAdapter: projectAdapterFromConfig(clangdAdapterDefaults(), cfg, contract.LSPServiceClangd),
	}
}

// ResolveRoot 先解析 clangd 项目根，再为 MQL 文档锁定可验证的编译任务。
func (a clangdLanguageAdapter) ResolveRoot(ctx context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error) {
	resolved, err := a.projectLanguageAdapter.ResolveRoot(ctx, scope, target)
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	targetPath := firstNonEmpty(target, scope.TargetPath)
	if !isMQLPath(targetPath) {
		return resolved, nil
	}
	evidence, err := resolveMQLClangdTask(resolved.ProjectRoot, targetPath)
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	resolved.LanguageSpecific = mergeMQLLanguageSpecific(resolved.LanguageSpecific, evidence)
	return resolved, nil
}

// BootstrapPolicy 保留 clangd 的 C/C++ 首选扩展，避免语言级 bootstrap 任取首个 MQL 文件。
func (a clangdLanguageAdapter) BootstrapPolicy(scope ResolvedLanguageScope) BootstrapPolicy {
	policy := a.projectLanguageAdapter.BootstrapPolicy(scope)
	policy.FirstSourceExtensions = filterOutMQLExtensions(policy.FirstSourceExtensions)
	return policy
}

// CacheKeyParts 将 MQL 编译任务证据纳入 clangd scope key。
func (a clangdLanguageAdapter) CacheKeyParts(scope ResolvedLanguageScope) map[string]string {
	parts := a.projectLanguageAdapter.CacheKeyParts(scope)
	for key, value := range scope.LanguageSpecific {
		if strings.HasPrefix(key, "clangdMQL") {
			parts[key] = value
		}
	}
	return parts
}

func mergeMQLLanguageSpecific(base, evidence map[string]string) map[string]string {
	if len(base) == 0 && len(evidence) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(evidence))
	maps.Copy(merged, base)
	maps.Copy(merged, evidence)
	return merged
}

func filterOutMQLExtensions(extensions []string) []string {
	filtered := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		switch strings.ToLower(strings.TrimSpace(extension)) {
		case ".mq5", ".mqh":
			continue
		default:
			filtered = append(filtered, extension)
		}
	}
	return filtered
}

func isMQLPath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".mq5", ".mqh":
		return true
	default:
		return false
	}
}

type clangdCompileCommand struct {
	Directory string   `json:"directory"`
	File      string   `json:"file"`
	Command   string   `json:"command"`
	Arguments []string `json:"arguments"`
}

type mqlCompileCandidate struct {
	file string
}

// resolveMQLClangdTask 严格解析 MQL 文档可用的 clangd 编译任务，不提供默认参数。
func resolveMQLClangdTask(root, target string) (map[string]string, error) {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	targetHash := safeMQLHash(target)
	rootHash := safeMQLHash(root)
	compileFlagsPath := filepath.Join(root, "compile_flags.txt")
	compileDatabasePath := filepath.Join(root, "compile_commands.json")

	if info, statErr := os.Stat(compileFlagsPath); statErr == nil {
		if !info.Mode().IsRegular() {
			return nil, newMQLClangdTaskError(clangdMQLStrategyCompileFlagsError, targetHash, rootHash, 0, mqlSourceCandidates(root))
		}
		payload, readErr := os.ReadFile(compileFlagsPath)
		if readErr != nil || !hasMQLCompileFlags(payload) {
			candidates := mqlSourceCandidates(root)
			return nil, newMQLClangdTaskError(clangdMQLStrategyCompileFlagsError, targetHash, rootHash, len(candidates), candidates)
		}
		return mqlClangdEvidence(clangdMQLStrategyCompileFlags, target, root, 1, nil, compileFlagsPath), nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		candidates := mqlSourceCandidates(root)
		return nil, newMQLClangdTaskError(clangdMQLStrategyCompileFlagsError, targetHash, rootHash, len(candidates), candidates)
	}

	if info, statErr := os.Stat(compileDatabasePath); statErr == nil {
		if !info.Mode().IsRegular() {
			return nil, newMQLClangdTaskError(clangdMQLStrategyDatabaseError, targetHash, rootHash, 0, mqlSourceCandidates(root))
		}
		commands, readErr := readMQLCompileCommands(compileDatabasePath)
		if readErr != nil {
			candidates := mqlSourceCandidates(root)
			return nil, newMQLClangdTaskError(clangdMQLStrategyDatabaseError, targetHash, rootHash, len(candidates), candidates)
		}
		return resolveMQLCompileCommandEvidence(commands, root, target, compileDatabasePath)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		candidates := mqlSourceCandidates(root)
		return nil, newMQLClangdTaskError(clangdMQLStrategyDatabaseError, targetHash, rootHash, len(candidates), candidates)
	}

	candidates := mqlSourceCandidates(root)
	return nil, newMQLClangdTaskError(clangdMQLStrategyNoCompileTask, targetHash, rootHash, len(candidates), candidates)
}

// mqlSourceCandidates 收集根目录内的 MQL 候选，仅用于安全证据记录，不隐式选择文件。
func mqlSourceCandidates(root string) []mqlCompileCandidate {
	ignoredDirs := map[string]struct{}{
		".build-cache": {},
		".git":         {},
		".workspace":   {},
		"build":        {},
		"dist":         {},
		"node_modules": {},
		"vendor":       {},
	}
	candidates := make([]mqlCompileCandidate, 0)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root {
				if _, ignored := ignoredDirs[strings.ToLower(entry.Name())]; ignored {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type().IsRegular() && isMQLPath(path) {
			candidates = append(candidates, mqlCompileCandidate{file: filepath.Clean(path)})
		}
		return nil
	})
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].file < candidates[j].file
	})
	return candidates
}

func hasMQLCompileFlags(payload []byte) bool {
	for line := range strings.SplitSeq(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return true
	}
	return false
}

// readMQLCompileCommands 读取并校验 clangd compilation database 的可用任务条目。
func readMQLCompileCommands(path string) ([]clangdCompileCommand, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var commands []clangdCompileCommand
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	if err := decoder.Decode(&commands); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("compile database contains trailing JSON")
		}
		return nil, err
	}
	if len(commands) == 0 {
		return nil, errors.New("compile database is empty")
	}
	for _, command := range commands {
		if strings.TrimSpace(command.File) == "" ||
			(strings.TrimSpace(command.Command) == "" && len(command.Arguments) == 0) {
			return nil, errors.New("compile database contains an unusable command")
		}
	}
	return commands, nil
}

// resolveMQLCompileCommandEvidence 按精确路径或唯一同 stem 规则确定编译任务。
func resolveMQLCompileCommandEvidence(commands []clangdCompileCommand, root, target, databasePath string) (map[string]string, error) {
	targetPath := filepath.Clean(target)
	targetFile := normalizedMQLPath(targetPath)
	candidates := collectMQLCompileCandidates(commands, root)
	targetCandidates := exactMQLCompileCandidates(candidates, targetFile)
	if len(targetCandidates) == 1 {
		return mqlClangdEvidence(clangdMQLStrategyExactTask, targetPath, root, len(targetCandidates), targetCandidates, databasePath), nil
	}
	if len(targetCandidates) > 1 {
		return nil, newMQLClangdTaskError(clangdMQLStrategyAmbiguous, safeMQLHash(targetPath), safeMQLHash(root), len(targetCandidates), targetCandidates)
	}

	sameStem := sameStemMQLCompileCandidates(candidates, targetPath)
	if len(sameStem) == 1 {
		return mqlClangdEvidence(clangdMQLStrategySameStemTask, targetPath, root, len(sameStem), sameStem, databasePath), nil
	}
	if len(sameStem) > 1 {
		return nil, newMQLClangdTaskError(clangdMQLStrategyAmbiguous, safeMQLHash(targetPath), safeMQLHash(root), len(sameStem), sameStem)
	}
	return nil, newMQLClangdTaskError(clangdMQLStrategyNoCandidate, safeMQLHash(targetPath), safeMQLHash(root), len(candidates), candidates)
}

// collectMQLCompileCandidates 将 compilation database 条目规范化为候选任务。
func collectMQLCompileCandidates(commands []clangdCompileCommand, root string) []mqlCompileCandidate {
	candidates := make([]mqlCompileCandidate, 0, len(commands))
	for _, command := range commands {
		file := compileCommandPath(root, command)
		if file != "" {
			candidates = append(candidates, mqlCompileCandidate{file: file})
		}
	}
	return candidates
}

// exactMQLCompileCandidates 返回与目标绝对路径完全匹配的编译任务。
func exactMQLCompileCandidates(candidates []mqlCompileCandidate, targetFile string) []mqlCompileCandidate {
	exact := make([]mqlCompileCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if normalizedMQLPath(candidate.file) == targetFile {
			exact = append(exact, candidate)
		}
	}
	return exact
}

// sameStemMQLCompileCandidates 返回唯一同 stem 的可编译源候选。
func sameStemMQLCompileCandidates(candidates []mqlCompileCandidate, targetPath string) []mqlCompileCandidate {
	targetStem := strings.ToLower(strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath)))
	sameStem := make([]mqlCompileCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !isMQLCompileTaskSource(candidate.file) {
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(candidate.file), filepath.Ext(candidate.file)))
		if stem == targetStem {
			sameStem = append(sameStem, candidate)
		}
	}
	return sameStem
}
func compileCommandPath(root string, command clangdCompileCommand) string {
	file := strings.TrimSpace(command.File)
	if file == "" {
		return ""
	}
	if !filepath.IsAbs(file) {
		directory := strings.TrimSpace(command.Directory)
		if directory == "" {
			directory = root
		}
		file = filepath.Join(directory, file)
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		return ""
	}
	return filepath.Clean(absolute)
}

func normalizedMQLPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func isMQLCompileTaskSource(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mq5", ".c", ".cc", ".cpp", ".cxx", ".m", ".mm":
		return true
	default:
		return false
	}
}

func mqlClangdEvidence(strategy, target, root string, candidateCount int, candidates []mqlCompileCandidate, databasePath string) map[string]string {
	evidence := map[string]string{
		clangdMQLScopeKey:           "true",
		clangdMQLStrategyKey:        strategy,
		clangdMQLTargetHashKey:      safeMQLHash(target),
		clangdMQLRootHashKey:        safeMQLHash(root),
		clangdMQLCandidateCountKey:  strconv.Itoa(candidateCount),
		clangdMQLCompileTaskHashKey: safeMQLHash(databasePath),
	}
	hashes := safeMQLCandidateHashes(candidates)
	if len(hashes) > 0 {
		evidence[clangdMQLCandidateHashesKey] = strings.Join(hashes, ",")
	}
	return evidence
}

func safeMQLCandidateHashes(candidates []mqlCompileCandidate) []string {
	hashes := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.file) == "" {
			continue
		}
		hashes = append(hashes, safeMQLHash(candidate.file))
	}
	sort.Strings(hashes)
	return hashes
}

func safeMQLHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)
}

func newMQLClangdTaskError(strategy, targetHash, rootHash string, candidateCount int, candidates []mqlCompileCandidate) error {
	hashes := strings.Join(safeMQLCandidateHashes(candidates), ",")
	return fmt.Errorf("clangd MQL compile task unavailable: strategy=%s target_sha256=%s root_sha256=%s candidate_count=%d candidate_sha256=%s", strategy, targetHash, rootHash, candidateCount, hashes)
}
