package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExecutorCandidateTestBinaryIndexEnvironment points at the worker-installed, verified bundle index.
const ExecutorCandidateTestBinaryIndexEnvironment = "SUPER_DOLPHIN_CANDIDATE_TEST_BINARY_INDEX"

const candidateTestBinaryBundleIndexName = "candidate-test-binaries.json"

// CandidateTestBinaryBundle is one worker-local, already verified test binary binding.
type CandidateTestBinaryBundle struct {
	Package      string `json:"package"`
	Mode         string `json:"mode"`
	BinaryPath   string `json:"binary_path"`
	BinarySHA256 string `json:"binary_sha256"`
	BinarySize   int64  `json:"binary_size"`
}

type candidateTestBinaryBundleIndex struct {
	Binaries []CandidateTestBinaryBundle `json:"binaries"`
}

func (index candidateTestBinaryBundleIndex) Validate() error {
	if len(index.Binaries) == 0 || len(index.Binaries) > 64 {
		return errors.New("candidate test binary index count is invalid")
	}
	return nil
}

// InstallCandidateTestBinaryBundleIndex moves verified staged binaries to a read-only worker path and writes a strict index.
func InstallCandidateTestBinaryBundleIndex(root string, entries []CandidateTestBinaryBundle) (string, error) {
	if root == "" || len(entries) == 0 {
		return "", errors.New("candidate test binary bundle index is incomplete")
	}
	destination := filepath.Join(root, "test-binaries")
	if err := os.Mkdir(destination, 0o755); err != nil {
		return "", fmt.Errorf("create candidate test binary destination: %w", err)
	}
	installed := make([]CandidateTestBinaryBundle, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		if err := validateCandidateTestBinaryBundle(entry, filepath.Dir(entry.BinaryPath)); err != nil {
			return "", err
		}
		identity := entry.Package + "\x00" + entry.Mode
		if _, exists := seen[identity]; exists {
			return "", errors.New("candidate test binary bundle contains duplicate package and mode")
		}
		seen[identity] = struct{}{}
		finalPath := filepath.Join(destination, fmt.Sprintf("%02d.test-bin", index))
		if err := os.Rename(entry.BinaryPath, finalPath); err != nil {
			return "", fmt.Errorf("install candidate test binary %q: %w", identity, err)
		}
		entry.BinaryPath = finalPath
		if err := validateCandidateTestBinaryBundle(entry, destination); err != nil {
			return "", err
		}
		installed = append(installed, entry)
	}
	data, err := json.Marshal(candidateTestBinaryBundleIndex{Binaries: installed})
	if err != nil {
		return "", fmt.Errorf("encode candidate test binary index: %w", err)
	}
	indexPath := filepath.Join(destination, candidateTestBinaryBundleIndexName)
	if err := os.WriteFile(indexPath, data, 0o444); err != nil {
		return "", fmt.Errorf("write candidate test binary index: %w", err)
	}
	return indexPath, nil
}

func loadCandidateTestBinaryBundleIndex(path string) (candidateTestBinaryBundleIndex, error) {
	if path == "" {
		return candidateTestBinaryBundleIndex{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return candidateTestBinaryBundleIndex{}, errors.New("candidate test binary index is unreadable")
	}
	var index candidateTestBinaryBundleIndex
	if err := DecodeStrictJSON(data, &index); err != nil || len(index.Binaries) == 0 || len(index.Binaries) > 64 {
		return candidateTestBinaryBundleIndex{}, errors.New("candidate test binary index is invalid")
	}
	root := filepath.Dir(path)
	seen := make(map[string]struct{}, len(index.Binaries))
	for _, entry := range index.Binaries {
		if err := validateCandidateTestBinaryBundle(entry, root); err != nil {
			return candidateTestBinaryBundleIndex{}, err
		}
		identity := entry.Package + "\x00" + entry.Mode
		if _, exists := seen[identity]; exists {
			return candidateTestBinaryBundleIndex{}, errors.New("candidate test binary index contains duplicate package and mode")
		}
		seen[identity] = struct{}{}
	}
	return index, nil
}

func validateCandidateTestBinaryBundle(entry CandidateTestBinaryBundle, root string) error {
	if entry.Package == "" || entry.Mode != "test" || !filepath.IsAbs(entry.BinaryPath) ||
		!strings.HasPrefix(entry.BinarySHA256, "sha256:") || len(entry.BinarySHA256) != len("sha256:")+sha256.Size*2 || entry.BinarySize <= 0 ||
		filepath.Dir(entry.BinaryPath) != root || filepath.Ext(entry.BinaryPath) != ".test-bin" {
		return errors.New("candidate test binary bundle binding is invalid")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(entry.BinarySHA256, "sha256:")); err != nil {
		return errors.New("candidate test binary bundle digest is invalid")
	}
	info, err := os.Stat(entry.BinaryPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.BinarySize || info.Mode()&0o111 == 0 {
		return errors.New("candidate test binary bundle file is invalid")
	}
	data, err := os.ReadFile(entry.BinaryPath)
	if err != nil || "sha256:"+fmt.Sprintf("%x", sha256.Sum256(data)) != entry.BinarySHA256 {
		return errors.New("candidate test binary bundle digest does not match")
	}
	return nil
}

func (index candidateTestBinaryBundleIndex) lookup(pkg, mode string) (CandidateTestBinaryBundle, error) {
	for _, entry := range index.Binaries {
		if entry.Package == pkg && entry.Mode == mode {
			return entry, nil
		}
	}
	return CandidateTestBinaryBundle{}, fmt.Errorf("candidate test binary is missing for package %q mode %q", pkg, mode)
}

// candidateTestBinaryExecutorProgram replaces a dynamic Go target with its precompiled candidate binary.
func candidateTestBinaryExecutorProgram[T ~string](id T, index candidateTestBinaryBundleIndex) (ExecutorProgram, error) {
	parent, kind, target, targeted, err := parseTargetWorkloadID(string(id))
	if err != nil || !targeted || kind != workloadTargetGoTest || parent != GateIDBackendTestWithGuard {
		return ExecutorProgram{}, errors.New("candidate test binary workload is invalid")
	}
	testTarget, err := ParseGoTestTarget(target)
	if err != nil {
		return ExecutorProgram{}, err
	}
	bundle, err := index.lookup(testTarget.Package, "test")
	if err != nil {
		return ExecutorProgram{}, err
	}
	argv := []string{
		"go", "tool", "test2json", "-p", testTarget.Package, "-t", bundle.BinaryPath,
		"-test.v=test2json", "-test.timeout=0", "-test.run=^" + testTarget.Name + "$", "-test.count=1",
	}
	return ExecutorProgram{Strategy: ExecutorStrategyCommands, Steps: []ExecutorStep{{Argv: argv}}}, nil
}
