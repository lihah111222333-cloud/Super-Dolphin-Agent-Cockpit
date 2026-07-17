package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	gateDockerfile = "build/gate/Dockerfile"
	gateInputs     = "build/gate/inputs.json"
	gateToolchain  = "build/gate/toolchain.lock"
	gateVendor     = "build/gate/vendor.tar.gz"
)

var buildTargets = []string{"./cmd/super-dolphin-gate", "./internal/devtools/gatehook"}

type packageModule struct {
	Main bool
}

type listedPackage struct {
	Dir        string
	ImportPath string
	Standard   bool
	Module     *packageModule
	GoFiles    []string
	SFiles     []string
	SysoFiles  []string
	EmbedFiles []string
}

type inputManifest struct {
	SchemaVersion string   `json:"schema_version"`
	Dockerfile    string   `json:"dockerfile"`
	Inputs        []string `json:"inputs"`
}

type toolchainLock struct {
	SchemaVersion      string   `json:"schema_version"`
	BuildKitVersion    string   `json:"buildkit_version"`
	DockerfileFrontend string   `json:"dockerfile_frontend"`
	SourceDateEpoch    string   `json:"source_date_epoch"`
	TargetPlatforms    []string `json:"target_platforms"`
	BaseImages         []struct {
		Name      string `json:"name"`
		Reference string `json:"reference"`
	} `json:"base_images"`
	DependencySources []string `json:"dependency_sources"`
	NetworkPolicy     string   `json:"network_policy"`
}

func main() {
	tree := flag.String("tree", "HEAD", "Git tree or commit used as the only source input")
	check := flag.Bool("check", false, "verify generated files in the Git tree without writing the worktree")
	flag.Parse()
	if err := generate(*tree, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(tree string, check bool) error {
	root, err := commandOutput("", nil, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root = strings.TrimSpace(root)
	treeSHA, err := commandOutput(root, nil, "git", "rev-parse", tree+"^{tree}")
	if err != nil {
		return err
	}
	treeSHA = strings.TrimSpace(treeSHA)
	tempRoot, err := os.MkdirTemp("", "super-dolphin-gate-closure-")
	if err != nil {
		return fmt.Errorf("create temporary root: %w", err)
	}
	defer os.RemoveAll(tempRoot)

	sourceRoot := filepath.Join(tempRoot, "source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		return fmt.Errorf("create source root: %w", err)
	}
	sourceRoot, err = filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return fmt.Errorf("resolve source root: %w", err)
	}
	if err := extractGitTree(root, treeSHA, sourceRoot); err != nil {
		return err
	}
	fullVendor := filepath.Join(tempRoot, "full-vendor")
	if _, err := commandOutput(sourceRoot, targetEnvironment(), "go", "mod", "vendor", "-o", fullVendor); err != nil {
		return fmt.Errorf("vendor dependencies from Git tree %s: %w", treeSHA, err)
	}
	packages, err := listPackages(sourceRoot)
	if err != nil {
		return err
	}
	localFiles, vendorFiles, err := classifyBuildFiles(sourceRoot, fullVendor, packages)
	if err != nil {
		return err
	}
	vendorData, err := buildVendorArchive(fullVendor, vendorFiles)
	if err != nil {
		return err
	}
	lock, err := readToolchainLock(filepath.Join(sourceRoot, gateToolchain))
	if err != nil {
		return err
	}
	dockerfile, err := renderDockerfile(lock, localFiles)
	if err != nil {
		return err
	}
	manifestData, err := renderManifest(localFiles)
	if err != nil {
		return err
	}
	outputs := map[string][]byte{
		gateDockerfile: dockerfile,
		gateInputs:     manifestData,
		gateVendor:     vendorData,
	}
	if check {
		for name, wanted := range outputs {
			tracked, err := os.ReadFile(filepath.Join(sourceRoot, filepath.FromSlash(name)))
			if err != nil {
				return fmt.Errorf("read generated file %s from Git tree %s: %w", name, treeSHA, err)
			}
			if !bytes.Equal(tracked, wanted) {
				return fmt.Errorf("generated file %s drifted from Git tree %s; run go run ./build/gate/cmd/generate-closure -tree <tree>", name, treeSHA)
			}
		}
		fmt.Printf("verified gate image closure in Git tree %s (%d local files, %d vendored files)\n", treeSHA, len(localFiles), len(vendorFiles))
		return nil
	}
	for name, data := range outputs {
		if err := writeAtomic(filepath.Join(root, filepath.FromSlash(name)), data); err != nil {
			return err
		}
	}
	fmt.Printf("generated gate image closure from Git tree %s (%d local files, %d vendored files)\n", treeSHA, len(localFiles), len(vendorFiles))
	return nil
}

func extractGitTree(repo string, tree string, destination string) error {
	command := exec.Command("git", "-C", repo, "archive", "--format=tar", tree)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open git archive stream: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start git archive: %w", err)
	}
	reader := tar.NewReader(stdout)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read git archive: %w", err)
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create archive directory %s: %w", name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create archive parent for %s: %w", name, err)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, header.FileInfo().Mode().Perm())
			if err != nil {
				return fmt.Errorf("create archived file %s: %w", name, err)
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("write archived file %s: %w", name, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close archived file %s: %w", name, closeErr)
			}
		default:
			return fmt.Errorf("Git tree contains forbidden non-regular entry %q (type %d)", name, header.Typeflag)
		}
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("git archive %s: %w: %s", tree, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func safeArchiveName(name string) (string, error) {
	cleaned := filepath.ToSlash(filepath.Clean(name))
	if name == "" || cleaned == "." || cleaned != strings.TrimSuffix(name, "/") || strings.HasPrefix(cleaned, "../") || filepath.IsAbs(name) {
		return "", fmt.Errorf("archive path %q is not canonical", name)
	}
	return cleaned, nil
}

func listPackages(root string) ([]listedPackage, error) {
	arguments := append([]string{"list", "-mod=mod", "-deps", "-json"}, buildTargets...)
	output, err := commandOutput(root, targetEnvironment(), "go", arguments...)
	if err != nil {
		return nil, fmt.Errorf("list gate build dependencies: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	var packages []listedPackage
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go list package: %w", err)
		}
		if !pkg.Standard && pkg.Module != nil {
			packages = append(packages, pkg)
		}
	}
	return packages, nil
}

func classifyBuildFiles(sourceRoot string, fullVendor string, packages []listedPackage) ([]string, []string, error) {
	localSet := make(map[string]struct{})
	vendorSet := map[string]struct{}{"modules.txt": {}}
	for _, pkg := range packages {
		files := append([]string{}, pkg.GoFiles...)
		files = append(files, pkg.SFiles...)
		files = append(files, pkg.SysoFiles...)
		files = append(files, pkg.EmbedFiles...)
		for _, file := range files {
			if pkg.Module.Main {
				absolute := filepath.Join(pkg.Dir, filepath.FromSlash(file))
				relative, err := filepath.Rel(sourceRoot, absolute)
				if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					return nil, nil, fmt.Errorf("main-module build file %q escapes Git tree", absolute)
				}
				localSet[filepath.ToSlash(relative)] = struct{}{}
				continue
			}
			vendorPath := filepath.ToSlash(filepath.Join(pkg.ImportPath, file))
			if err := requireRegularFile(filepath.Join(fullVendor, filepath.FromSlash(vendorPath))); err != nil {
				return nil, nil, err
			}
			vendorSet[vendorPath] = struct{}{}
		}
	}
	localFiles := sortedKeys(localSet)
	vendorFiles := sortedKeys(vendorSet)
	return localFiles, vendorFiles, nil
}

func buildVendorArchive(fullVendor string, files []string) ([]byte, error) {
	var compressed bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("create vendor gzip writer: %w", err)
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(fullVendor, filepath.FromSlash(name)))
		if err != nil {
			return nil, fmt.Errorf("read vendored file %s: %w", name, err)
		}
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("write vendor header %s: %w", name, err)
		}
		if _, err := tarWriter.Write(data); err != nil {
			return nil, fmt.Errorf("write vendor file %s: %w", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("close vendor tar: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("close vendor gzip: %w", err)
	}
	return compressed.Bytes(), nil
}

func readToolchainLock(path string) (toolchainLock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return toolchainLock{}, fmt.Errorf("read toolchain lock: %w", err)
	}
	var lock toolchainLock
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err != nil {
		return toolchainLock{}, fmt.Errorf("decode toolchain lock: %w", err)
	}
	return lock, nil
}

func renderDockerfile(lock toolchainLock, localFiles []string) ([]byte, error) {
	if len(lock.BaseImages) != 1 || lock.BaseImages[0].Name != "GO_IMAGE" || !strings.Contains(lock.BaseImages[0].Reference, "@sha256:") {
		return nil, errors.New("toolchain lock must contain exactly one immutable GO_IMAGE")
	}
	if !isCanonicalSourceDateEpoch(lock.SourceDateEpoch) {
		return nil, errors.New("toolchain lock source_date_epoch must be a canonical non-negative integer")
	}
	groups := make(map[string][]string)
	for _, name := range localFiles {
		groups[filepath.ToSlash(filepath.Dir(name))] = append(groups[filepath.ToSlash(filepath.Dir(name))], name)
	}
	directories := sortedKeys(groups)
	var output strings.Builder
	fmt.Fprintf(&output, "ARG GO_IMAGE=%s\nARG SOURCE_DATE_EPOCH=%s\nFROM ${GO_IMAGE} AS build\nARG SOURCE_DATE_EPOCH\n\n", lock.BaseImages[0].Reference, lock.SourceDateEpoch)
	output.WriteString("WORKDIR /src\nENV GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off\n")
	output.WriteString("COPY [\"go.mod\", \"go.sum\", \"./\"]\n")
	output.WriteString("ADD build/gate/vendor.tar.gz ./vendor/\n")
	for _, directory := range directories {
		files := slices.Clone(groups[directory])
		sort.Strings(files)
		values := append(files, "./"+directory+"/")
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, fmt.Errorf("encode Docker COPY for %s: %w", directory, err)
		}
		fmt.Fprintf(&output, "COPY %s\n", encoded)
	}
	output.WriteString("RUN --network=none CGO_ENABLED=0 go test -mod=vendor -run '^$' ./internal/devtools/gatehook\n")
	output.WriteString("RUN --network=none CGO_ENABLED=0 go build -mod=vendor -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate && touch -d \"@${SOURCE_DATE_EPOCH}\" /out/super-dolphin-gate\n\n")
	output.WriteString("FROM scratch\nCOPY --from=build /out/super-dolphin-gate /super-dolphin-gate\nENTRYPOINT [\"/super-dolphin-gate\"]\n")
	return []byte(output.String()), nil
}

func isCanonicalSourceDateEpoch(value string) bool {
	seconds, err := strconv.ParseInt(value, 10, 64)
	return err == nil && seconds >= 0 && strconv.FormatInt(seconds, 10) == value
}

func renderManifest(localFiles []string) ([]byte, error) {
	inputs := []string{gateDockerfile, gateInputs, gateToolchain, gateVendor, "go.mod", "go.sum"}
	inputs = append(inputs, localFiles...)
	sort.Strings(inputs)
	manifest := inputManifest{SchemaVersion: "1", Dockerfile: gateDockerfile, Inputs: inputs}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode input manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func targetEnvironment() []string {
	return []string{"GOWORK=off", "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOTOOLCHAIN=local"}
}

func commandOutput(directory string, environment []string, name string, arguments ...string) (string, error) {
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func requireRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect vendored file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("vendored build input %s is not a regular file", path)
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory for %s: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".gate-closure-")
	if err != nil {
		return fmt.Errorf("create temporary output for %s: %w", path, err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary output for %s: %w", path, err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("chmod temporary output for %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary output for %s: %w", path, err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace output %s: %w", path, err)
	}
	return nil
}
