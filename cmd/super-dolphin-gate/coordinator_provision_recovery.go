package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type productionProvisionPlan struct {
	inputs        productionProvisionInputs
	installRoot   string
	parent        string
	config        productionCoordinatorConfig
	configPath    string
	rootExists    bool
	launcherReady bool
}

type productionProvisionExpectedFile struct {
	name string
	mode os.FileMode
	data []byte
}

// productionProvisionExpectedFiles 从已验证输入唯一生成安装树内全部不可变文件。
func productionProvisionExpectedFiles(
	config productionCoordinatorConfig,
	inputs productionProvisionInputs,
) ([]productionProvisionExpectedFile, error) {
	rootData, err := json.MarshalIndent(inputs.root, "", "  ")
	if err != nil {
		return nil, err
	}
	bootstrapKeyData, err := json.Marshal(inputs.bootstrapKey)
	if err != nil {
		return nil, err
	}
	receiptData, err := json.Marshal(productionResultReceiptPrivateKey{PrivateKey: inputs.receiptKey.PrivateKey})
	if err != nil {
		return nil, err
	}
	grantData, err := json.Marshal(productionActionGrantPrivateKey{PrivateKey: inputs.actionGrantKey.PrivateKey})
	if err != nil {
		return nil, err
	}
	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return []productionProvisionExpectedFile{
		{name: "bootstrap-root.json", mode: 0o600, data: append(rootData, '\n')},
		{name: "bootstrap-controller", mode: 0o500, data: inputs.controllerData},
		{name: "bootstrap-controller-key.json", mode: 0o600, data: append(bootstrapKeyData, '\n')},
		{name: "seccomp.json", mode: 0o600, data: inputs.seccompData},
		{name: "promotion-private.key", mode: 0o600, data: []byte(inputs.bootstrapKey.PrivateKey + "\n")},
		{name: "receipt-private.json", mode: 0o600, data: append(receiptData, '\n')},
		{name: "action-grant-private.json", mode: 0o600, data: append(grantData, '\n')},
		{name: "production.json", mode: 0o600, data: append(configData, '\n')},
	}, nil
}

func writeProductionProvisionExpectedFiles(root string, files []productionProvisionExpectedFile) error {
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(root, file.name), file.data, file.mode); err != nil {
			return fmt.Errorf("write production provision %s: %w", file.name, err)
		}
	}
	return nil
}

// verifyProductionProvisionResidue 只接受由同一外部输入生成且尚未承载状态的发布残留。
func verifyProductionProvisionResidue(
	ctx context.Context,
	installRoot string,
	manifest productionProvisionManifest,
	inputs productionProvisionInputs,
	runtime productionProvisionRuntime,
) error {
	if err := verifyProductionProvisionPrivateDirectory(installRoot, false); err != nil {
		return fmt.Errorf("production provision existing root is not repairable: %w", err)
	}
	config := productionProvisionConfig(installRoot, manifest, inputs)
	expectedFiles, err := productionProvisionExpectedFiles(config, inputs)
	if err != nil {
		return err
	}
	if err := verifyProductionProvisionRootEntries(installRoot, expectedFiles); err != nil {
		return err
	}
	if err := verifyProductionProvisionMutableDirectories(installRoot); err != nil {
		return err
	}
	trustedRepository := filepath.Join(installRoot, "trusted.git")
	if err := verifyProductionProvisionPrivateDirectory(trustedRepository, false); err != nil {
		return err
	}
	if err := runtime.VerifyTrustedRepository(ctx, inputs.root, trustedRepository); err != nil {
		return fmt.Errorf("verify repairable production trusted repository: %w", err)
	}
	if err := config.Validate(); err != nil {
		return fmt.Errorf("validate repairable production config: %w", err)
	}
	return nil
}

// verifyProductionProvisionRootEntries 要求根目录条目集合与不可变闭包完全一致。
func verifyProductionProvisionRootEntries(
	installRoot string,
	expectedFiles []productionProvisionExpectedFile,
) error {
	expectedEntries := map[string]struct{}{
		"accepted": {}, "candidate-state": {}, "candidate-build": {}, "trusted.git": {},
	}
	for _, file := range expectedFiles {
		expectedEntries[file.name] = struct{}{}
		if err := verifyProductionProvisionExpectedFile(installRoot, file); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(installRoot)
	if err != nil {
		return err
	}
	if len(entries) != len(expectedEntries) {
		return errors.New("production provision existing root has unknown or missing entries")
	}
	for _, entry := range entries {
		if _, ok := expectedEntries[entry.Name()]; !ok {
			return fmt.Errorf("production provision existing root contains unknown entry %q", entry.Name())
		}
	}
	return nil
}

func verifyProductionProvisionMutableDirectories(installRoot string) error {
	for _, name := range []string{"accepted", "candidate-state", "candidate-build"} {
		if err := verifyProductionProvisionPrivateDirectory(filepath.Join(installRoot, name), true); err != nil {
			return err
		}
	}
	return nil
}

// verifyProductionProvisionExpectedFile 要求残留文件内容、权限、类型与 owner 全部精确匹配。
func verifyProductionProvisionExpectedFile(root string, expected productionProvisionExpectedFile) error {
	path := filepath.Join(root, expected.name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != expected.mode ||
		!productionProvisionOwnedByCurrentUser(info) {
		return errors.Join(fmt.Errorf("production provision residue file %s is not exact", expected.name), err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, expected.data) {
		return errors.Join(fmt.Errorf("production provision residue file %s drifted", expected.name), err)
	}
	return nil
}

// verifyProductionProvisionPrivateDirectory 拒绝 symlink、非 owner、权限漂移及可变状态残留。
func verifyProductionProvisionPrivateDirectory(path string, requireEmpty bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.Join(fmt.Errorf("production provision directory %q is not exact owner-only state", path), err)
	}
	if err := verifyProductionProvisionDirectoryIdentity(path, info); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.Join(fmt.Errorf("production provision directory %q traverses symlinks", path), err)
	}
	if !requireEmpty {
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		return errors.Join(fmt.Errorf("production provision mutable directory %q is not empty", path), err)
	}
	return nil
}

func verifyProductionProvisionDirectoryIdentity(path string, info os.FileInfo) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("production provision directory %q is not a real directory", path)
	}
	if info.Mode().Perm() != 0o700 || !productionProvisionOwnedByCurrentUser(info) {
		return fmt.Errorf("production provision directory %q is not exact owner-only state", path)
	}
	return nil
}

func productionProvisionLauncherData(configPath string, controllerPath string) []byte {
	return []byte("#!/bin/sh\nSUPER_DOLPHIN_GATE_PRODUCTION_CONFIG=" + shellQuoteProductionProvision(configPath) +
		" exec " + shellQuoteProductionProvision(controllerPath) + " \"$@\"\n")
}

// inspectProductionProvisionLauncher 只把 absent 或逐字节匹配当前闭包的 launcher 视为可恢复状态。
func inspectProductionProvisionLauncher(path string, configPath string, controllerPath string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect production provision launcher: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 || !productionProvisionOwnedByCurrentUser(info) {
		return false, errors.New("production provision launcher already exists and is not the verified launcher")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, productionProvisionLauncherData(configPath, controllerPath)) {
		return false, errors.Join(errors.New("production provision launcher already exists with unknown content"), err)
	}
	return true, nil
}

// verifyProductionProvisionTrustedRepository 复核 bare remote、ref、commit 与 tree 均未漂移。
func verifyProductionProvisionTrustedRepository(
	ctx context.Context,
	root productionBootstrapRoot,
	repository string,
) error {
	bare, err := productionProvisionGitLine(ctx, repository, "rev-parse", "--is-bare-repository")
	if err != nil || bare != "true" {
		return errors.Join(errors.New("production provision trusted repository is not bare"), err)
	}
	remote, err := productionProvisionGitLine(ctx, repository, "config", "--get", "remote.origin.url")
	if err != nil || remote != root.RemoteURL {
		return errors.Join(errors.New("production provision trusted repository remote drifted"), err)
	}
	commit, err := productionProvisionGitLine(ctx, repository, "rev-parse", "--verify", root.TrustedRef+"^{commit}")
	if err != nil || commit != root.BaselineCommit {
		return errors.Join(errors.New("production provision trusted ref baseline commit drifted"), err)
	}
	tree, err := productionProvisionGitLine(ctx, repository, "rev-parse", "--verify", root.BaselineCommit+"^{tree}")
	if err != nil || tree != root.BaselineTree {
		return errors.Join(errors.New("production provision baseline tree drifted"), err)
	}
	return nil
}
