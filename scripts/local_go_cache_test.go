package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type localGoCacheState struct {
	SchemaVersion    string `json:"schema_version"`
	IdentitySHA256   string `json:"identity_sha256"`
	CachePathHex     string `json:"cache_path_hex"`
	UpdatedAtUnixSec int64  `json:"updated_at_unix_sec"`
}

const (
	hostTestScopedCommandError     = `host-test 环境契约违规：必须且只能通过一条 env ... "$real_go" test 命令传入命令环境`
	hostTestRequiredEnvironmentErr = "host-test 环境契约违规：scoped env 必须包含 GOMAXPROCS、GOCACHE、GOTMPDIR 和 GOTOOLCHAIN"
	hostTestExportError            = "host-test 环境契约违规：run_host_test 禁止 export；命令环境必须通过 scoped env 传入"
)

func TestHostTestUsesIdentityScopedLocalGoCache(t *testing.T) {
	hostTest := readScript(t, "test_with_guard.sh")
	assertScriptContains(t, hostTest, `source "$ROOT_DIR/scripts/local_go_cache.sh"`)
	hostTestBody := functionBody(t, hostTest, "run_host_test")
	if diagnostic := validateHostTestScopedEnvironment(hostTestBody); diagnostic != "" {
		t.Fatal(diagnostic)
	}
	for _, required := range []string{
		`local_go_cache_prepare "$ROOT_DIR" "$real_go"`,
		`local_go_cache_cleanup_temp "$local_temp_root"`,
	} {
		assertScriptContains(t, hostTestBody, required)
	}
	t.Run("legacy cache exports fail with a fixed diagnostic", func(t *testing.T) {
		legacy := strings.Replace(hostTestBody, `env GOMAXPROCS="$gomaxprocs" GOCACHE="$local_cache_root" GOTMPDIR="$local_temp_root" GOTOOLCHAIN=local`, `export GOMAXPROCS="$gomaxprocs"
  export GOCACHE="$local_cache_root"
  export GOTMPDIR="$local_temp_root"
  export GOTOOLCHAIN=local`, 1)
		if got := validateHostTestScopedEnvironment(legacy); got != hostTestExportError {
			t.Fatalf("legacy host-test environment diagnostic = %q, want %q", got, hostTestExportError)
		}
	})
	t.Run("missing required environment fails with a fixed diagnostic", func(t *testing.T) {
		missing := strings.Replace(hostTestBody, ` GOCACHE="$local_cache_root"`, "", 1)
		if got := validateHostTestScopedEnvironment(missing); got != hostTestRequiredEnvironmentErr {
			t.Fatalf("missing host-test environment diagnostic = %q, want %q", got, hostTestRequiredEnvironmentErr)
		}
	})
	t.Run("missing scoped command fails with a fixed diagnostic", func(t *testing.T) {
		missing := strings.Replace(hostTestBody, "    env GOMAXPROCS=", "    GOMAXPROCS=", 1)
		if got := validateHostTestScopedEnvironment(missing); got != hostTestScopedCommandError {
			t.Fatalf("missing scoped command diagnostic = %q, want %q", got, hostTestScopedCommandError)
		}
	})
	t.Run("environment order and additional keys remain adaptable", func(t *testing.T) {
		adapted := strings.Replace(hostTestBody,
			`env GOMAXPROCS="$gomaxprocs" GOCACHE="$local_cache_root" GOTMPDIR="$local_temp_root" GOTOOLCHAIN=local`,
			`/usr/bin/env GOTMPDIR="$local_temp_root" SUPER_DOLPHIN_FUTURE_SCOPE=1 GOTOOLCHAIN=local GOCACHE="$local_cache_root" GOMAXPROCS="$gomaxprocs"`, 1)
		if got := validateHostTestScopedEnvironment(adapted); got != "" {
			t.Fatalf("adapted host-test environment was rejected: %s", got)
		}
	})
	identityScript := readScript(t, "local_go_cache.sh")
	for _, required := range []string{"GOVERSION GOOS GOARCH GOAMD64 GOARM64 GOARM GOEXPERIMENT GOTOOLCHAIN", "tool_name in asm cgo compile link", "CC_VERSION", "CC_TARGET", "CC_SYSROOT", "APPLE_SDK_VERSION", "local-go-cache-state/v1", "|| return 1"} {
		assertScriptContains(t, identityScript, required)
	}
}

// validateHostTestScopedEnvironment 固定宿主测试的命令环境只能经单次 env 调用传入，同时允许扩展新的环境键。
func validateHostTestScopedEnvironment(hostTestBody string) string {
	for line := range strings.SplitSeq(hostTestBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "export" || strings.HasPrefix(trimmed, "export ") {
			return hostTestExportError
		}
	}
	environment, commandCount := hostTestScopedGoCommandEnvironment(hostTestBody)
	if commandCount != 1 {
		return hostTestScopedCommandError
	}
	for _, required := range []string{"GOMAXPROCS", "GOCACHE", "GOTMPDIR", "GOTOOLCHAIN"} {
		if _, ok := environment[required]; !ok {
			return hostTestRequiredEnvironmentErr
		}
	}
	return ""
}

// hostTestScopedGoCommandEnvironment 提取 scoped Go test 命令的环境键，不依赖键顺序或额外扩展键。
func hostTestScopedGoCommandEnvironment(hostTestBody string) (map[string]struct{}, int) {
	environment := make(map[string]struct{})
	normalized := strings.ReplaceAll(hostTestBody, "\\\n", " ")
	commandCount := 0
	for line := range strings.SplitSeq(normalized, "\n") {
		fields := strings.Fields(line)
		commandIndex := scopedGoTestCommandIndex(fields)
		if commandIndex < 0 {
			continue
		}
		commandCount++
		for _, assignment := range fields[1:commandIndex] {
			name, _, ok := strings.Cut(assignment, "=")
			if ok && name != "" {
				environment[name] = struct{}{}
			}
		}
	}
	return environment, commandCount
}

// scopedGoTestCommandIndex 定位由 env 直接启动的真实 Go test，允许 env 使用绝对路径。
func scopedGoTestCommandIndex(fields []string) int {
	if len(fields) < 3 || filepath.Base(fields[0]) != "env" {
		return -1
	}
	for index := 1; index+1 < len(fields); index++ {
		if fields[index] == `"$real_go"` && fields[index+1] == "test" {
			return index
		}
	}
	return -1
}

func TestLocalGoCacheSharesObjectsButIsolatesTemporaryDirectories(t *testing.T) {
	repository := initializeLocalGoCacheRepository(t)
	first := addLocalGoCacheWorktree(t, repository, "first")
	second := addLocalGoCacheWorktree(t, repository, "second")
	realGo := localGoCacheTestGoBinary(t)
	firstValues := prepareLocalGoCache(t, first, realGo)
	secondValues := prepareLocalGoCache(t, second, realGo)
	if firstValues[0] != secondValues[0] || firstValues[2] != secondValues[2] {
		t.Fatalf("linked worktrees did not share cache identity:\nfirst=%v\nsecond=%v", firstValues, secondValues)
	}
	if firstValues[1] == secondValues[1] {
		t.Fatalf("linked worktrees shared temporary directory: %s", firstValues[1])
	}
	assertPrivateDirectory(t, firstValues[0])
	assertPrivateDirectory(t, firstValues[1])
	assertPrivateDirectory(t, secondValues[1])
	assertLocalGoCacheState(t, firstValues[0], firstValues[2])
}

func TestLocalGoCacheChangesWhenCompilerIdentityChanges(t *testing.T) {
	repository := initializeLocalGoCacheRepository(t)
	realGo := localGoCacheTestGoBinary(t)
	first := prepareLocalGoCache(t, repository, realGo)
	second := prepareLocalGoCacheWithEnv(t, repository, realGo, "GOFLAGS=-trimpath")
	if first[0] == second[0] || first[2] == second[2] {
		t.Fatalf("compiler policy change reused cache identity:\nfirst=%v\nsecond=%v", first, second)
	}
}

func TestLocalGoCacheCanonicalizesCompilerSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "compiler-real")
	link := filepath.Join(t.TempDir(), "compiler-link")
	if err := os.WriteFile(target, []byte("compiler\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("canonicalize compiler target: %v", err)
	}
	command := exec.Command("bash", "-c", "source ./scripts/local_go_cache.sh; local_go_cache_resolve_binary \"$1\"", "cache-test", link)
	command.Dir = initializeLocalGoCacheRepository(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve compiler symlink: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != canonicalTarget {
		t.Fatalf("resolved compiler = %q, want %q", output, canonicalTarget)
	}
}

func TestLocalGoCacheAvoidsRecompileAcrossWorktreesOnOneDevice(t *testing.T) {
	repository := initializeLocalGoCacheRepository(t)
	first := addLocalGoCacheWorktree(t, repository, "compile-first")
	second := addLocalGoCacheWorktree(t, repository, "compile-second")
	realGo := localGoCacheTestGoBinary(t)
	firstCache := prepareLocalGoCache(t, first, realGo)
	secondCache := prepareLocalGoCache(t, second, realGo)
	firstOutput := buildLocalGoCacheFixture(t, realGo, first, firstCache)
	if !strings.Contains(firstOutput, "/compile ") {
		t.Fatalf("cold worktree did not invoke compiler:\n%s", firstOutput)
	}
	secondOutput := buildLocalGoCacheFixture(t, realGo, second, secondCache)
	if strings.Contains(secondOutput, "/compile ") {
		t.Fatalf("second worktree recompiled despite shared local cache:\n%s", secondOutput)
	}
}

func buildLocalGoCacheFixture(t *testing.T, realGo, worktree string, cache []string) string {
	t.Helper()
	command := exec.Command(realGo, "build", "-x", "-trimpath", "-buildvcs=false", "-o", filepath.Join(worktree, "fixture"), ".")
	command.Dir = worktree
	command.Env = append(os.Environ(), "GOCACHE="+cache[0], "GOTMPDIR="+cache[1], "GOTOOLCHAIN=local")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build local cache fixture: %v\n%s", err, output)
	}
	return string(output)
}

// localGoCacheTestGoBinary 返回当前宿主 PATH 解析出的 Go 工具，使测试与真实入口使用同一编译器。
func localGoCacheTestGoBinary(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate Go tool: %v", err)
	}
	return binary
}

func prepareLocalGoCache(t *testing.T, repository, realGo string) []string {
	return prepareLocalGoCacheWithEnv(t, repository, realGo)
}

func prepareLocalGoCacheWithEnv(t *testing.T, repository, realGo string, extraEnv ...string) []string {
	t.Helper()
	command := exec.Command("bash", "-c", "source ./scripts/local_go_cache.sh; local_go_cache_prepare \"$1\" \"$2\"", "cache-test", repository, realGo)
	command.Dir = repository
	command.Env = append(os.Environ(), extraEnv...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare local Go cache: %v\n%s", err, output)
	}
	values := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(values) != 3 {
		t.Fatalf("local Go cache output = %q", output)
	}
	t.Cleanup(func() { _ = os.RemoveAll(values[1]) })
	return values
}

func initializeLocalGoCacheRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	runLocalGoCacheGit(t, "", "init", repository)
	if err := os.MkdirAll(filepath.Join(repository, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"local_go_cache.sh",
		filepath.Join("platform", "posix", "local_go_cache_path.sh"),
		filepath.Join("platform", "windows", "local_go_cache_path.sh"),
	} {
		source, err := os.ReadFile(relative)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(repository, "scripts", relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, source, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked"), []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"go.mod":  "module example.invalid/local-cache-fixture\n\ngo 1.26.5\n",
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(42) }\n",
	} {
		if err := os.WriteFile(filepath.Join(repository, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runLocalGoCacheGit(t, repository, "add", "tracked", "go.mod", "main.go", "scripts")
	runLocalGoCacheGit(t, repository, "-c", "user.name=Cache Test", "-c", "user.email=cache@example.invalid", "commit", "-m", "初始化")
	return repository
}

func addLocalGoCacheWorktree(t *testing.T, repository, name string) string {
	t.Helper()
	worktree := filepath.Join(filepath.Dir(repository), name)
	runLocalGoCacheGit(t, repository, "worktree", "add", "--detach", worktree, "HEAD")
	return worktree
}

func runLocalGoCacheGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func assertPrivateDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("directory %s mode = %o", path, info.Mode().Perm())
	}
}

func assertLocalGoCacheState(t *testing.T, cacheRoot, identity string) {
	t.Helper()
	statePath := filepath.Join(filepath.Dir(filepath.Dir(cacheRoot)), "state", identity+".json")
	payload, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var state localGoCacheState
	if err := decoder.Decode(&state); err != nil {
		t.Fatal(err)
	}
	decodedPath, err := hex.DecodeString(state.CachePathHex)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != "local-go-cache-state/v1" || state.IdentitySHA256 != identity || string(decodedPath) != cacheRoot || state.UpdatedAtUnixSec <= 0 {
		t.Fatalf("local cache state = %+v", state)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}
}
