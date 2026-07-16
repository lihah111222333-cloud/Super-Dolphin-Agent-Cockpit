package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	productionCoordinatorConfigEnv      = "SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG"
	productionCoordinatorConfigMaxBytes = 1 << 20
)

type productionCoordinatorConfig struct {
	AcceptedImageRoot      string                                 `json:"accepted_image_root"`
	CandidateBuildRoot     string                                 `json:"candidate_build_root"`
	TrustedSourceRoot      string                                 `json:"trusted_source_root"`
	SeccompProfile         string                                 `json:"seccomp_profile"`
	Platform               string                                 `json:"platform"`
	RepoID                 string                                 `json:"repo_id"`
	TrustedRef             string                                 `json:"trusted_ref"`
	TrustedRepository      string                                 `json:"trusted_repository"`
	AcceptedImageSigners   []productionTrustedKey                 `json:"accepted_image_signers"`
	ResultReceiptAuthority productionResultReceiptAuthorityConfig `json:"result_receipt_authority"`
}

type productionTrustedKey struct {
	Signer    gatecontract.SignerIdentity `json:"signer"`
	PublicKey string                      `json:"public_key"`
}

type productionResultReceiptAuthorityConfig struct {
	Signer         gatecontract.SignerIdentity `json:"signer"`
	PublicKey      string                      `json:"public_key"`
	PrivateKeyFile string                      `json:"private_key_file"`
}

type productionResultReceiptPrivateKey struct {
	PrivateKey string `json:"private_key"`
}

// Validate 校验 owner 私钥配置只携带规范的非空编码。
func (key productionResultReceiptPrivateKey) Validate() error {
	if strings.TrimSpace(key.PrivateKey) == "" || strings.TrimSpace(key.PrivateKey) != key.PrivateKey {
		return errors.New("production result receipt private key is required and canonical")
	}
	return nil
}

func loadProductionCoordinatorConfig() (productionCoordinatorConfig, error) {
	path, ok := os.LookupEnv(productionCoordinatorConfigEnv)
	if !ok || path == "" {
		return productionCoordinatorConfig{}, fmt.Errorf(
			"%w: %s is required",
			errCoordinatorDependency,
			productionCoordinatorConfigEnv,
		)
	}
	return loadProductionCoordinatorConfigFile(path)
}

// loadProductionCoordinatorConfigFile 从私有规范文件严格解码生产配置。
func loadProductionCoordinatorConfigFile(path string) (productionCoordinatorConfig, error) {
	canonical, err := canonicalProductionFile("production coordinator config", path)
	if err != nil {
		return productionCoordinatorConfig{}, err
	}
	data, err := readProductionCoordinatorConfig(canonical)
	if err != nil {
		return productionCoordinatorConfig{}, err
	}
	var config productionCoordinatorConfig
	if err := gatecontract.DecodeStrictJSON(data, &config); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("decode production coordinator config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("validate production coordinator config: %w", err)
	}
	return config, nil
}

// readProductionCoordinatorConfig 防止路径与已打开文件在读取期间发生身份漂移。
func readProductionCoordinatorConfig(canonical string) ([]byte, error) {
	file, err := os.Open(canonical)
	if err != nil {
		return nil, fmt.Errorf("open production coordinator config: %w", err)
	}
	opened, statErr := file.Stat()
	pathInfo, lstatErr := os.Lstat(canonical)
	if statErr != nil || lstatErr != nil || !os.SameFile(opened, pathInfo) ||
		!opened.Mode().IsRegular() || opened.Mode().Perm() != 0o600 {
		return nil, errors.Join(
			errors.New("production coordinator config changed while opening"), statErr, lstatErr, file.Close(),
		)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, productionCoordinatorConfigMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(data) > productionCoordinatorConfigMaxBytes {
		return nil, errors.New("production coordinator config exceeds size limit")
	}
	return data, nil
}

// Validate 校验生产配置的显式信任根、执行根与签名者集合。
func (config productionCoordinatorConfig) Validate() error {
	if err := config.validateIdentity(); err != nil {
		return err
	}
	if err := config.validatePaths(); err != nil {
		return err
	}
	return validateProductionRootSeparation(config)
}

// validateIdentity 校验 repository、ref、platform 与 signer 集合均显式且规范。
func (config productionCoordinatorConfig) validateIdentity() error {
	if err := config.validateRepositoryIdentity(); err != nil {
		return err
	}
	return config.validateReceiptAuthorityIdentity()
}

// validateRepositoryIdentity 校验仓库、ref、平台与 accepted signer 集合。
func (config productionCoordinatorConfig) validateRepositoryIdentity() error {
	if strings.TrimSpace(config.RepoID) == "" || strings.TrimSpace(config.RepoID) != config.RepoID {
		return errors.New("production coordinator repo_id is required and canonical")
	}
	if !strings.HasPrefix(config.TrustedRef, "refs/") || strings.TrimSpace(config.TrustedRef) != config.TrustedRef {
		return errors.New("production coordinator trusted_ref must be a canonical full ref")
	}
	if strings.Count(config.Platform, "/") < 1 || strings.TrimSpace(config.Platform) != config.Platform {
		return errors.New("production coordinator platform must be explicit os/architecture[/variant]")
	}
	if len(config.AcceptedImageSigners) == 0 {
		return errors.New("production coordinator accepted_image_signers are required")
	}
	return nil
}

// validateReceiptAuthorityIdentity 校验 receipt signer 及其密钥位置均已显式配置。
func (config productionCoordinatorConfig) validateReceiptAuthorityIdentity() error {
	if err := config.ResultReceiptAuthority.Signer.Validate(); err != nil {
		return fmt.Errorf("production result receipt signer: %w", err)
	}
	if strings.TrimSpace(config.ResultReceiptAuthority.PublicKey) == "" ||
		strings.TrimSpace(config.ResultReceiptAuthority.PrivateKeyFile) == "" {
		return errors.New("production result receipt public key and private key file are required")
	}
	return nil
}

func (config productionCoordinatorConfig) validatePaths() error {
	for _, path := range []string{
		config.AcceptedImageRoot, config.CandidateBuildRoot, config.TrustedSourceRoot, config.TrustedRepository,
	} {
		if _, err := canonicalProductionDirectory(path); err != nil {
			return err
		}
	}
	if _, err := canonicalProductionFile("seccomp profile", config.SeccompProfile); err != nil {
		return err
	}
	if _, err := canonicalProductionFile("result receipt private key", config.ResultReceiptAuthority.PrivateKeyFile); err != nil {
		return err
	}
	return nil
}

// validateProductionRootSeparation 阻断信任状态、候选构建、源码快照和 bare mirror 互相嵌套。
func validateProductionRootSeparation(config productionCoordinatorConfig) error {
	roots := []string{
		config.AcceptedImageRoot, config.CandidateBuildRoot, config.TrustedSourceRoot, config.TrustedRepository,
	}
	for left := range roots {
		for right := left + 1; right < len(roots); right++ {
			if productionPathsOverlap(roots[left], roots[right]) {
				return fmt.Errorf("production coordinator roots must not overlap: %q and %q", roots[left], roots[right])
			}
		}
	}
	for _, root := range roots {
		if productionPathsOverlap(root, config.ResultReceiptAuthority.PrivateKeyFile) {
			return fmt.Errorf("result receipt private key must be isolated from production root %q", root)
		}
	}
	return nil
}

// canonicalProductionDirectory 要求目录规范、私有、无符号链接且不在候选 worktree 内。
func canonicalProductionDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("production coordinator directory must be canonical and absolute: %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve production coordinator directory %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.Join(fmt.Errorf("production coordinator directory %q must be private", path), err)
	}
	if resolved != path {
		return "", fmt.Errorf("production coordinator directory must not traverse symlinks: %q", path)
	}
	if err := rejectProductionWorktreePath(path); err != nil {
		return "", err
	}
	return resolved, nil
}

// canonicalProductionFile 要求文件与其父目录均为仓库外私有路径。
func canonicalProductionFile(name string, path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("%s must be canonical and absolute", name)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("%s must be a 0600 regular file", name)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errors.Join(fmt.Errorf("%s must not traverse symlinks", name), err)
	}
	if _, err := canonicalProductionDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	return resolved, nil
}

// rejectProductionWorktreePath 沿父链拒绝任何带 .git 标记的非 bare worktree。
func rejectProductionWorktreePath(path string) error {
	for directory := path; ; directory = filepath.Dir(directory) {
		if _, err := os.Lstat(filepath.Join(directory, ".git")); err == nil {
			return fmt.Errorf("production trust path must be outside a Git worktree: %q", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect production trust path ancestry: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}
	}
}

func productionPathsOverlap(left string, right string) bool {
	return productionPathContains(left, right) || productionPathContains(right, left)
}

func productionPathContains(root string, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
