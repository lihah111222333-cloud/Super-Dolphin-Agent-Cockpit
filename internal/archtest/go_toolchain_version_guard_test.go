package archtest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

var goToolchainVersionPattern = regexp.MustCompile(`(?i)\bgo\s*1\.\d+\.\d+\b`)

type goToolchainConsumer struct {
	Path   string
	Reason string
}

// TestGoToolchainVersionParity 将根 go.mod 的版本绑定到全部正式工具链消费者。
func TestGoToolchainVersionParity(t *testing.T) {
	t.Parallel()

	repository := goToolchainRepositoryRoot(t)
	want := readCanonicalGoToolchainVersion(t, repository)
	consumers := registeredGoToolchainConsumers()
	discovered := discoverGoToolchainConsumers(t, repository)
	assertGoToolchainConsumerParity(t, consumers, discovered, want)

	assertOwnedGoModulesMatch(t, repository, want)
	assertDerivedGoToolchainConsumers(t, repository)
}

func assertGoToolchainConsumerParity(
	t *testing.T,
	consumers map[string]goToolchainConsumer,
	discovered map[string][]string,
	want string,
) {
	t.Helper()
	missing := missingGoToolchainConsumers(t, consumers, discovered)
	stale, unknown := compareGoToolchainConsumers(consumers, discovered, want)
	if len(missing) != 0 || len(stale) != 0 || len(unknown) != 0 {
		sort.Strings(missing)
		sort.Strings(stale)
		sort.Strings(unknown)
		t.Fatalf("Go toolchain parity mismatch: missing=%v stale=%v unknown=%v", missing, stale, unknown)
	}
}

func missingGoToolchainConsumers(
	t *testing.T,
	consumers map[string]goToolchainConsumer,
	discovered map[string][]string,
) []string {
	t.Helper()
	var missing []string
	for path, consumer := range consumers {
		if strings.TrimSpace(consumer.Reason) == "" {
			t.Fatalf("Go toolchain consumer %s has an empty reason", path)
		}
		if len(discovered[path]) == 0 {
			missing = append(missing, path)
		}
	}
	return missing
}

func compareGoToolchainConsumers(
	consumers map[string]goToolchainConsumer,
	discovered map[string][]string,
	want string,
) (stale, unknown []string) {
	for path, versions := range discovered {
		if _, ok := consumers[path]; !ok {
			unknown = append(unknown, path)
			continue
		}
		for _, version := range versions {
			if normalizeGoToolchainVersion(version) != want {
				stale = append(stale, fmt.Sprintf("%s=%s (want %s)", path, version, want))
			}
		}
	}
	return stale, unknown
}

func registeredGoToolchainConsumers() map[string]goToolchainConsumer {
	return map[string]goToolchainConsumer{
		"CONTRIBUTING.md":                              {Path: "CONTRIBUTING.md", Reason: "贡献者规范基线"},
		"README.de.md":                                 {Path: "README.de.md", Reason: "德语安装前置条件"},
		"README.es.md":                                 {Path: "README.es.md", Reason: "西语安装前置条件"},
		"README.ja.md":                                 {Path: "README.ja.md", Reason: "日语安装前置条件"},
		"README.ko.md":                                 {Path: "README.ko.md", Reason: "韩语安装前置条件"},
		"README.md":                                    {Path: "README.md", Reason: "规范安装前置条件"},
		"README.zh-CN.md":                              {Path: "README.zh-CN.md", Reason: "中文安装前置条件"},
		"build/gate/closure/closure.go":                {Path: "build/gate/closure/closure.go", Reason: "闭包生成器运行时工具链锁定"},
		"build/gate/runtime-deps.Dockerfile":           {Path: "build/gate/runtime-deps.Dockerfile", Reason: "运行时依赖镜像工具链探针"},
		"build/gate/runtime-proxy/go.mod":              {Path: "build/gate/runtime-proxy/go.mod", Reason: "truth image proxy 模块"},
		"build/gate/runtime-tools/go.mod":              {Path: "build/gate/runtime-tools/go.mod", Reason: "truth image tools 模块"},
		"build/gate/toolchain.lock":                    {Path: "build/gate/toolchain.lock", Reason: "远程门禁工具链锁定"},
		"docs/契约/jrpc2-convention.md":                  {Path: "docs/契约/jrpc2-convention.md", Reason: "当前 Go 契约版本"},
		"internal/devtools/gate/go_distribution.go":    {Path: "internal/devtools/gate/go_distribution.go", Reason: "远程 Go 分发锁定"},
		"internal/devtools/remoteci/runtime_inputs.go": {Path: "internal/devtools/remoteci/runtime_inputs.go", Reason: "远程运行时输入锁定"},
		"scripts/go_distribution_lock.sh":              {Path: "scripts/go_distribution_lock.sh", Reason: "Go 官方分发锁校验"},
		"scripts/verify_packaged_app_macos.sh":         {Path: "scripts/verify_packaged_app_macos.sh", Reason: "macOS 发布包验证模块"},
	}
}

func discoverGoToolchainConsumers(t *testing.T, repository string) map[string][]string {
	t.Helper()
	paths := registeredGoToolchainConsumerPaths()
	collectGoToolchainConsumerPaths(t, repository, paths)
	return readGoToolchainConsumerVersions(t, repository, paths)
}

func registeredGoToolchainConsumerPaths() map[string]struct{} {
	consumers := registeredGoToolchainConsumers()
	paths := make(map[string]struct{}, len(consumers))
	for path := range consumers {
		paths[path] = struct{}{}
	}
	return paths
}

func collectGoToolchainConsumerPaths(t *testing.T, repository string, paths map[string]struct{}) {
	t.Helper()
	for _, root := range []string{".github/workflows", "build/gate", "cmd/super-dolphin-gate", "internal/devtools/gate", "internal/devtools/remoteci", "scripts"} {
		err := filepath.WalkDir(filepath.Join(repository, root), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if strings.Contains(name, "_test.") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(repository, path)
			if err != nil {
				return err
			}
			paths[filepath.ToSlash(relative)] = struct{}{}
			return nil
		})
		if err != nil {
			t.Fatalf("scan Go toolchain consumer root %s: %v", root, err)
		}
	}
}

func readGoToolchainConsumerVersions(
	t *testing.T,
	repository string,
	paths map[string]struct{},
) map[string][]string {
	t.Helper()
	discovered := make(map[string][]string)
	for path := range paths {
		data, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read Go toolchain consumer %s: %v", path, err)
		}
		matches := goToolchainVersionPattern.FindAllString(string(data), -1)
		if len(matches) != 0 {
			discovered[path] = matches
		}
	}
	return discovered
}

func readCanonicalGoToolchainVersion(t *testing.T, repository string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		t.Fatalf("read canonical go.mod: %v", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		t.Fatalf("parse canonical go.mod: %v", err)
	}
	if parsed.Go == nil || strings.Count(parsed.Go.Version, ".") != 2 {
		t.Fatalf("canonical go.mod must contain an exact patch go directive, got %+v", parsed.Go)
	}
	if parsed.Toolchain != nil {
		t.Fatalf("canonical go.mod must use one SSOT without a second toolchain directive, got %q", parsed.Toolchain.Name)
	}
	return "go" + parsed.Go.Version
}

func normalizeGoToolchainVersion(version string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(version), " ", ""))
}

func assertOwnedGoModulesMatch(t *testing.T, repository, want string) {
	t.Helper()
	var stale []string
	err := filepath.WalkDir(repository, func(path string, entry fs.DirEntry, walkErr error) error {
		return collectOwnedGoModule(path, entry, walkErr, repository, want, &stale)
	})
	if err != nil {
		t.Fatalf("enumerate owned Go modules: %v", err)
	}
	if len(stale) != 0 {
		sort.Strings(stale)
		t.Fatalf("owned Go modules do not match canonical %s: %v", want, stale)
	}
}

func collectOwnedGoModule(
	path string,
	entry fs.DirEntry,
	walkErr error,
	repository string,
	want string,
	stale *[]string,
) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() {
		return skipOwnedGoModuleDirectory(entry.Name())
	}
	if entry.Name() != "go.mod" {
		return nil
	}
	return recordOwnedGoModule(path, repository, want, stale)
}

func skipOwnedGoModuleDirectory(name string) error {
	if name == ".git" || name == "third_party" {
		return filepath.SkipDir
	}
	return nil
}

func recordOwnedGoModule(path, repository, want string, stale *[]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	parsed, err := modfile.Parse(path, data, nil)
	if err != nil {
		return fmt.Errorf("parse owned Go module %s: %w", path, err)
	}
	if parsed.Go == nil {
		return fmt.Errorf("owned Go module %s is missing the go directive", path)
	}
	if parsed.Toolchain != nil {
		return fmt.Errorf("owned Go module %s has a second toolchain directive %q", path, parsed.Toolchain.Name)
	}
	relative, err := filepath.Rel(repository, path)
	if err != nil {
		return err
	}
	got := "go" + parsed.Go.Version
	if got != want {
		*stale = append(*stale, filepath.ToSlash(relative)+"="+got)
	}
	return nil
}

func assertDerivedGoToolchainConsumers(t *testing.T, repository string) {
	t.Helper()
	checks := map[string][]string{
		".github/workflows/release.yml":              {"actions/setup-go@", "go-version-file: go.mod"},
		"Makefile":                                   {"go env GOVERSION"},
		"build/gate/Dockerfile":                      {"GOTOOLCHAIN=local", `COPY ["go.mod","go.sum"`},
		"build/gate/runtime-deps.lock":               {`"go_mod_sha256"`},
		"build/gate/toolchain.lock":                  {`"name": "GO_IMAGE"`, `"reference": "mirror.gcr.io/library/golang@sha256:`},
		"internal/devtools/remoteci/go_toolchain.go": {"modfile.Parse", "parsed.Go.Version"},
		"scripts/package_windows.ps1":                {"go env GOVERSION"},
	}
	for path, required := range checks {
		data, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read derived Go toolchain consumer %s: %v", path, err)
		}
		for _, fragment := range required {
			if !strings.Contains(string(data), fragment) {
				t.Fatalf("derived Go toolchain consumer %s is missing %q", path, fragment)
			}
		}
	}
}

func goToolchainRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Go toolchain guard path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
