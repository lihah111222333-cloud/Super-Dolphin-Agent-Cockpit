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

func TestHostTestUsesIdentityScopedLocalGoCache(t *testing.T) {
	hostTest := readScript(t, "test_with_guard.sh")
	for _, required := range []string{
		`source "$ROOT_DIR/scripts/local_go_cache.sh"`,
		`local_go_cache_prepare "$ROOT_DIR" "$real_go"`,
		`export GOCACHE="$local_cache_root"`,
		`export GOTMPDIR="$local_temp_root"`,
		`local_go_cache_cleanup_temp "$local_temp_root"`,
	} {
		assertScriptContains(t, hostTest, required)
	}
	identityScript := readScript(t, "local_go_cache.sh")
	for _, required := range []string{"GOVERSION GOOS GOARCH GOAMD64 GOARM64 GOARM GOEXPERIMENT GOTOOLCHAIN", "tool_name in asm cgo compile link", "CC_VERSION", "CC_TARGET", "CC_SYSROOT", "APPLE_SDK_VERSION", "local-go-cache-state/v1", "|| return 1"} {
		assertScriptContains(t, identityScript, required)
	}
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
	source, err := os.ReadFile("local_go_cache.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "scripts", "local_go_cache.sh"), source, 0o700); err != nil {
		t.Fatal(err)
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
	runLocalGoCacheGit(t, repository, "add", "tracked", "go.mod", "main.go", "scripts/local_go_cache.sh")
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
