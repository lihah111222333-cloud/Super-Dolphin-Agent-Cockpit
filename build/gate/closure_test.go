package gateclosure_test

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	dockerfilePath = "build/gate/Dockerfile"
	manifestPath   = "build/gate/inputs.json"
	toolchainPath  = "build/gate/toolchain.lock"
	vendorPath     = "build/gate/vendor.tar.gz"
)

var gateTargets = []string{"./cmd/super-dolphin-gate", "./internal/devtools/gatehook"}

type manifest struct {
	SchemaVersion string   `json:"schema_version"`
	Dockerfile    string   `json:"dockerfile"`
	Inputs        []string `json:"inputs"`
}

type packageModule struct {
	Main bool
}

type listedPackage struct {
	Dir        string
	Standard   bool
	Module     *packageModule
	GoFiles    []string
	SFiles     []string
	SysoFiles  []string
	EmbedFiles []string
}

func TestGateImageManifestIsExactLinuxBuildClosure(t *testing.T) {
	root := repositoryRoot(t)
	tracked := readManifest(t, root)
	localFiles := listLocalBuildFiles(t, root)
	wanted := []string{dockerfilePath, manifestPath, toolchainPath, vendorPath, "go.mod", "go.sum"}
	wanted = append(wanted, localFiles...)
	sort.Strings(wanted)
	if !slices.Equal(tracked.Inputs, wanted) {
		t.Fatalf("tracked gate inputs drifted from Linux build closure\ntracked-only: %v\nmissing: %v", difference(tracked.Inputs, wanted), difference(wanted, tracked.Inputs))
	}
	if tracked.SchemaVersion != "1" || tracked.Dockerfile != dockerfilePath {
		t.Fatalf("unexpected tracked manifest identity: %+v", tracked)
	}

	dockerSources := parseDockerSources(t, filepath.Join(root, dockerfilePath))
	wantedDockerSources := append([]string{"go.mod", "go.sum", vendorPath}, localFiles...)
	sort.Strings(wantedDockerSources)
	if !slices.Equal(dockerSources, wantedDockerSources) {
		t.Fatalf("Dockerfile sources drifted from declared build sources\nDocker-only: %v\nmissing: %v", difference(dockerSources, wantedDockerSources), difference(wantedDockerSources, dockerSources))
	}
}

func TestGateVendorArchiveIsCanonicalAndBuildsOffline(t *testing.T) {
	root := repositoryRoot(t)
	tracked := readManifest(t, root)
	temporaryRoot := t.TempDir()
	for _, name := range tracked.Inputs {
		if strings.HasPrefix(name, "build/gate/") {
			continue
		}
		copyRegularFile(t, filepath.Join(root, filepath.FromSlash(name)), filepath.Join(temporaryRoot, filepath.FromSlash(name)))
	}
	extractCanonicalVendor(t, filepath.Join(root, vendorPath), filepath.Join(temporaryRoot, "vendor"))

	environment := append(os.Environ(), "GOWORK=off", "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	runCommand(t, temporaryRoot, environment, "go", "test", "-mod=vendor", "-run", "^$", "./internal/devtools/gatehook")
	runCommand(t, temporaryRoot, environment, "go", "build", "-mod=vendor", "-trimpath", "-buildvcs=false", "-o", filepath.Join(temporaryRoot, "super-dolphin-gate"), "./cmd/super-dolphin-gate")
	info, err := os.Stat(filepath.Join(temporaryRoot, "super-dolphin-gate"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 || info.Mode()&0o111 == 0 {
		t.Fatalf("offline gate binary is not executable: mode=%s size=%d", info.Mode(), info.Size())
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readManifest(t *testing.T, root string) manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value manifest
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func listLocalBuildFiles(t *testing.T, root string) []string {
	t.Helper()
	arguments := append([]string{"list", "-mod=mod", "-deps", "-json"}, gateTargets...)
	command := exec.Command("go", arguments...)
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOTOOLCHAIN=local")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list gate closure: %v", commandError(err))
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	files := make(map[string]struct{})
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if pkg.Standard || pkg.Module == nil || !pkg.Module.Main {
			continue
		}
		packageFiles := append([]string{}, pkg.GoFiles...)
		packageFiles = append(packageFiles, pkg.SFiles...)
		packageFiles = append(packageFiles, pkg.SysoFiles...)
		packageFiles = append(packageFiles, pkg.EmbedFiles...)
		for _, name := range packageFiles {
			absolute := filepath.Join(pkg.Dir, filepath.FromSlash(name))
			relative, err := filepath.Rel(root, absolute)
			if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				t.Fatalf("build file %q escapes repository", absolute)
			}
			files[filepath.ToSlash(relative)] = struct{}{}
		}
	}
	return sortedKeys(files)
}

func parseDockerSources(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var sources []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "COPY ["):
			var paths []string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "COPY ")), &paths); err != nil || len(paths) < 2 {
				t.Fatalf("invalid JSON COPY %q: %v", line, err)
			}
			sources = append(sources, paths[:len(paths)-1]...)
		case strings.HasPrefix(line, "COPY --from="):
			continue
		case strings.HasPrefix(line, "COPY "):
			t.Fatalf("non-JSON local COPY is forbidden: %s", line)
		case strings.HasPrefix(line, "ADD "):
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[1] == "." || strings.ContainsAny(fields[1], "*?[") {
				t.Fatalf("non-canonical ADD is forbidden: %s", line)
			}
			sources = append(sources, fields[1])
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(sources)
	return sources
}

func extractCanonicalVendor(t *testing.T, archivePath string, destination string) {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	previous := ""
	count := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg || header.Name == "" || filepath.IsAbs(header.Name) || filepath.ToSlash(filepath.Clean(header.Name)) != header.Name || strings.HasPrefix(header.Name, "../") {
			t.Fatalf("vendor archive contains non-canonical entry %q type=%d", header.Name, header.Typeflag)
		}
		if previous != "" && header.Name <= previous {
			t.Fatalf("vendor archive is not sorted: %q after %q", header.Name, previous)
		}
		if !header.ModTime.Equal(time.Unix(0, 0)) || header.Uid != 0 || header.Gid != 0 || header.Mode != 0o644 || header.Linkname != "" {
			t.Fatalf("vendor archive metadata is not canonical for %q", header.Name)
		}
		previous = header.Name
		count++
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, reader)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("extract %s: copy=%v close=%v", header.Name, copyErr, closeErr)
		}
	}
	if count == 0 {
		t.Fatal("vendor archive is empty")
	}
}

func copyRegularFile(t *testing.T, source string, destination string) {
	t.Helper()
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("build input %s is not a regular file", source)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func runCommand(t *testing.T, directory string, environment []string, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(arguments, " "), err, output)
	}
}

func commandError(err error) error {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitError.Stderr)))
	}
	return err
}

func difference(left []string, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	var result []string
	for _, value := range left {
		if _, exists := rightSet[value]; !exists {
			result = append(result, value)
		}
	}
	return result
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
