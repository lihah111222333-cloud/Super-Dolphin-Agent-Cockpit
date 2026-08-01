package frontendcodesizetrusted

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const closureManifestPath = "frontend-app/scripts/lib/frontend-code-size-dependency-closure.json"
const closureGeneratorPath = "frontend-app/scripts/lib/generate-frontend-code-size-dependency-closure.mjs"

type closureManifest struct {
	SchemaVersion     int           `json:"schemaVersion"`
	Generator         string        `json:"generator"`
	GeneratorSHA256   string        `json:"generatorSha256"`
	PackageLockSHA256 string        `json:"packageLockSha256"`
	RootPackage       string        `json:"rootPackage"`
	Packages          []string      `json:"packages"`
	Files             []closureFile `json:"files"`
	ClosureSHA256     string        `json:"closureSha256"`
}

type closureFile struct {
	Path   string      `json:"path"`
	Mode   fs.FileMode `json:"mode"`
	SHA256 string      `json:"sha256"`
}

// Receipt is the immutable execution closure identity printed by the CLI.
type Receipt struct {
	CandidateTree        string `json:"candidateTree"`
	AcceptedTree         string `json:"acceptedTree"`
	NodeSHA256           string `json:"nodeSha256"`
	NodeVersion          string `json:"nodeVersion"`
	NodePlatform         string `json:"nodePlatform"`
	PackageLockSHA256    string `json:"packageLockSha256"`
	ParserClosureSHA256  string `json:"parserClosureSha256"`
	PrivateClosureSHA256 string `json:"privateClosureSha256"`
	EmbeddedAssetSHA256  string `json:"embeddedAssetSha256"`
	IdentitySHA256       string `json:"identitySha256"`
}

func sha256Bytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func parseClosureManifest(data, lock, generator []byte) (closureManifest, error) {
	var manifest closureManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return closureManifest{}, fmt.Errorf("parse accepted parser closure manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return closureManifest{}, fmt.Errorf("parse accepted parser closure manifest: %w", err)
	}
	if manifest.SchemaVersion != 2 || manifest.Generator != closureGeneratorPath || manifest.GeneratorSHA256 != sha256Bytes(generator) || manifest.RootPackage != "@babel/parser" || manifest.PackageLockSHA256 != sha256Bytes(lock) || !exactSHA256(manifest.ClosureSHA256) || len(manifest.Packages) == 0 || len(manifest.Files) == 0 {
		return closureManifest{}, fmt.Errorf("accepted parser closure manifest is invalid")
	}
	packagePaths := make(map[string]struct{}, len(manifest.Packages))
	for _, packagePath := range manifest.Packages {
		if !validClosurePath(packagePath) {
			return closureManifest{}, fmt.Errorf("accepted parser closure package is invalid: %q", packagePath)
		}
		if _, duplicate := packagePaths[packagePath]; duplicate {
			return closureManifest{}, fmt.Errorf("accepted parser closure package is duplicated: %q", packagePath)
		}
		packagePaths[packagePath] = struct{}{}
	}
	if _, ok := packagePaths["node_modules/"+manifest.RootPackage]; !ok {
		return closureManifest{}, fmt.Errorf("accepted parser closure root package is missing")
	}
	canonical, err := canonicalClosureManifest(manifest)
	if err != nil || sha256Bytes(canonical) != manifest.ClosureSHA256 {
		return closureManifest{}, fmt.Errorf("accepted parser closure manifest digest mismatch")
	}
	filePaths := make(map[string]struct{}, len(manifest.Files))
	for _, entry := range manifest.Files {
		if !validClosurePath(entry.Path) || !entry.Mode.IsRegular() || entry.Mode.Perm()&0o022 != 0 || !exactSHA256(entry.SHA256) {
			return closureManifest{}, fmt.Errorf("accepted parser closure file is invalid: %q", entry.Path)
		}
		if _, duplicate := filePaths[entry.Path]; duplicate {
			return closureManifest{}, fmt.Errorf("accepted parser closure file is duplicated: %q", entry.Path)
		}
		filePaths[entry.Path] = struct{}{}
		contained := false
		for packagePath := range packagePaths {
			if strings.HasPrefix(entry.Path, packagePath+"/") {
				contained = true
				break
			}
		}
		if !contained {
			return closureManifest{}, fmt.Errorf("accepted parser closure file is outside declared packages: %q", entry.Path)
		}
	}
	return manifest, nil
}

func canonicalClosureManifest(manifest closureManifest) ([]byte, error) {
	return json.Marshal(struct {
		SchemaVersion     int           `json:"schemaVersion"`
		Generator         string        `json:"generator"`
		GeneratorSHA256   string        `json:"generatorSha256"`
		PackageLockSHA256 string        `json:"packageLockSha256"`
		RootPackage       string        `json:"rootPackage"`
		Packages          []string      `json:"packages"`
		Files             []closureFile `json:"files"`
	}{
		SchemaVersion: manifest.SchemaVersion, Generator: manifest.Generator,
		GeneratorSHA256: manifest.GeneratorSHA256, PackageLockSHA256: manifest.PackageLockSHA256,
		RootPackage: manifest.RootPackage, Packages: manifest.Packages, Files: manifest.Files,
	})
}

func validClosurePath(value string) bool {
	return strings.HasPrefix(value, "node_modules/") &&
		!strings.ContainsAny(value, "\\\x00\r\n") && path.Clean(value) == value
}

func exactSHA256(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func acceptedClosure(ctx context.Context, root, accepted string) (closureManifest, []byte, error) {
	lock, err := gitBytes(ctx, root, "show", accepted+":frontend-app/package-lock.json")
	if err != nil {
		return closureManifest{}, nil, classified(ErrorInfrastructure, "read accepted package lock", err)
	}
	data, err := gitBytes(ctx, root, "show", accepted+":"+closureManifestPath)
	if err != nil {
		return closureManifest{}, nil, classified(ErrorInfrastructure, "read accepted parser closure manifest", err)
	}
	generator, err := gitBytes(ctx, root, "show", accepted+":"+closureGeneratorPath)
	if err != nil {
		return closureManifest{}, nil, classified(ErrorInfrastructure, "read accepted parser closure generator", err)
	}
	manifest, err := parseClosureManifest(data, lock, generator)
	if err != nil {
		return closureManifest{}, nil, classified(ErrorInfrastructure, "validate accepted parser closure manifest", err)
	}
	return manifest, lock, nil
}

func candidateClosure(appRoot string) (closureManifest, error) {
	lock, err := os.ReadFile(filepath.Join(appRoot, "package-lock.json"))
	if err != nil {
		return closureManifest{}, err
	}
	data, err := os.ReadFile(filepath.Join(appRoot, "scripts", "lib", "frontend-code-size-dependency-closure.json"))
	if err != nil {
		return closureManifest{}, err
	}
	generator, err := os.ReadFile(filepath.Join(appRoot, "scripts", "lib", "generate-frontend-code-size-dependency-closure.mjs"))
	if err != nil {
		return closureManifest{}, err
	}
	return parseClosureManifest(data, lock, generator)
}

func verifyParserClosure(seed string, manifest closureManifest) error {
	seedInfo, err := os.Lstat(seed)
	if err != nil || !seedInfo.IsDir() || seedInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("shared node_modules seed must be a physical directory")
	}
	expected := make(map[string]closureFile, len(manifest.Files))
	for _, entry := range manifest.Files {
		expected[entry.Path] = entry
	}
	for _, entry := range manifest.Files {
		target := filepath.Join(seed, strings.TrimPrefix(entry.Path, "node_modules/"))
		info, err := os.Lstat(target)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != entry.Mode.Perm() {
			return fmt.Errorf("parser closure file drift: %s", entry.Path)
		}
		data, err := os.ReadFile(target)
		if err != nil || sha256Bytes(data) != entry.SHA256 {
			return fmt.Errorf("parser closure content drift: %s", entry.Path)
		}
	}
	for _, packagePath := range manifest.Packages {
		root := filepath.Join(seed, strings.TrimPrefix(packagePath, "node_modules/"))
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(seed, path)
			if err != nil {
				return err
			}
			key := "node_modules/" + filepath.ToSlash(rel)
			if _, ok := expected[key]; !ok {
				return fmt.Errorf("unexpected parser closure path: %s", key)
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("parser closure file type drift: %s", key)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func materializeParserClosure(runtimeRoot, seed string, manifest closureManifest) (string, error) {
	privateRoot := filepath.Join(runtimeRoot, "node_modules")
	if err := os.Mkdir(privateRoot, 0o755); err != nil {
		return "", err
	}
	for _, entry := range manifest.Files {
		source := filepath.Join(seed, strings.TrimPrefix(entry.Path, "node_modules/"))
		target := filepath.Join(runtimeRoot, entry.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, entry.Mode.Perm()); err != nil {
			return "", err
		}
	}
	if err := verifyParserClosure(privateRoot, manifest); err != nil {
		return "", err
	}
	return manifest.ClosureSHA256, nil
}

func embeddedAssetsSHA256(assets fs.FS) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(assets, "assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%s\x00", path, sha256Bytes(data))
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

func receiptFor(candidate, accepted, node, nodeVersion, nodePlatform string, lock []byte, manifest closureManifest, privateClosure string, assets fs.FS) (Receipt, error) {
	nodeBytes, err := os.ReadFile(node)
	if err != nil {
		return Receipt{}, err
	}
	assetDigest, err := embeddedAssetsSHA256(assets)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{CandidateTree: candidate, AcceptedTree: accepted, NodeSHA256: sha256Bytes(nodeBytes), NodeVersion: nodeVersion, NodePlatform: nodePlatform, PackageLockSHA256: sha256Bytes(lock), ParserClosureSHA256: manifest.ClosureSHA256, PrivateClosureSHA256: privateClosure, EmbeddedAssetSHA256: assetDigest}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return Receipt{}, err
	}
	receipt.IdentitySHA256 = sha256Bytes(canonical)
	return receipt, nil
}
