package gateclosure

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// generatorSource 嵌入全部闭包生成语义源码，使已编译 Gate 持有不可变生成器身份。
//
//go:embed closure.go runtime_deps.go provenance.go
var generatorSource embed.FS

var generatorSourceFiles = []string{
	"closure.go",
	"provenance.go",
	"runtime_deps.go",
}

// GeneratorProvenance 返回已编译 Gate 的不可变闭包生成器身份。
func GeneratorProvenance() (string, error) {
	files := make(map[string][]byte, len(generatorSourceFiles))
	for _, name := range generatorSourceFiles {
		data, err := generatorSource.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("read embedded closure generator source %s: %w", name, err)
		}
		files[name] = data
	}
	goVersion, xmodVersion, err := compiledGeneratorToolchainIdentity()
	if err != nil {
		return "", err
	}
	return generatorProvenance(files, goVersion, xmodVersion)
}

// GeneratorProvenanceForTree 从精确 Git tree 读取同一组输入，供 hook 拒绝旧语义 launcher。
func GeneratorProvenanceForTree(repository, tree string) (string, error) {
	treeSHA, err := resolveTreeSHA(repository, tree)
	if err != nil {
		return "", err
	}
	files := make(map[string][]byte, len(generatorSourceFiles))
	for _, name := range generatorSourceFiles {
		data, err := commandOutput(repository, nil, "git", "cat-file", "blob", treeSHA+":build/gate/closure/"+name)
		if err != nil {
			return "", fmt.Errorf("read closure generator source %s from candidate tree: %w", name, err)
		}
		files[name] = []byte(data)
	}
	goMod, err := commandOutput(repository, nil, "git", "cat-file", "blob", treeSHA+":go.mod")
	if err != nil {
		return "", fmt.Errorf("read candidate go.mod: %w", err)
	}
	goVersion, xmodVersion, err := moduleGeneratorToolchainIdentity([]byte(goMod))
	if err != nil {
		return "", err
	}
	return generatorProvenance(files, goVersion, xmodVersion)
}

func generatorProvenance(sourceFiles map[string][]byte, goVersion, xmodVersion string) (string, error) {
	if goVersion == "" || xmodVersion == "" {
		return "", fmt.Errorf("closure generator toolchain identity is incomplete")
	}
	names := append([]string(nil), generatorSourceFiles...)
	sort.Strings(names)
	hash := sha256.New()
	if _, err := fmt.Fprintf(hash, "go=%s\x00golang.org/x/mod=%s\x00", goVersion, xmodVersion); err != nil {
		return "", err
	}
	for _, name := range names {
		data, ok := sourceFiles[name]
		if !ok {
			return "", fmt.Errorf("closure generator source %s is missing", name)
		}
		if _, err := fmt.Fprintf(hash, "%s\x00%d\x00", name, len(data)); err != nil {
			return "", err
		}
		if _, err := hash.Write(data); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func compiledGeneratorToolchainIdentity() (string, string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.GoVersion != runtime.Version() {
		return "", "", fmt.Errorf("compiled closure generator Go identity is unavailable")
	}
	for _, dependency := range info.Deps {
		if dependency.Path == "golang.org/x/mod" && dependency.Version != "" {
			return strings.TrimPrefix(info.GoVersion, "go"), dependency.Version, nil
		}
	}
	return "", "", fmt.Errorf("compiled closure generator golang.org/x/mod identity is unavailable")
}

func moduleGeneratorToolchainIdentity(data []byte) (string, string, error) {
	moduleFile, err := modfile.Parse("go.mod", data, nil)
	if err != nil || moduleFile.Go == nil || moduleFile.Go.Version == "" {
		return "", "", fmt.Errorf("parse candidate go.mod: %w", err)
	}
	for _, requirement := range moduleFile.Require {
		if requirement.Mod.Path == "golang.org/x/mod" && requirement.Mod.Version != "" {
			return moduleFile.Go.Version, requirement.Mod.Version, nil
		}
	}
	return "", "", fmt.Errorf("candidate go.mod does not require golang.org/x/mod")
}
