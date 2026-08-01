package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
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

type productionProvisionLauncherState uint8

type historicalProductionProvisionManifest productionProvisionManifest

type historicalStoredProductionCoordinatorConfig storedProductionCoordinatorConfig

func (manifest historicalProductionProvisionManifest) Validate() error {
	if manifest.SchemaVersion != productionProvisionSchemaVersion {
		return errors.New("historical production provision manifest schema version is invalid")
	}
	for _, value := range []string{manifest.InstallRoot, manifest.LauncherPath} {
		if err := validateProductionProvisionManifestValue(value); err != nil {
			return err
		}
	}
	return nil
}

func (config historicalStoredProductionCoordinatorConfig) Validate() error {
	runtimeConfig := productionCoordinatorConfig(config)
	return transformProductionCoordinatorConfigPaths(&runtimeConfig, func(path string) (string, error) {
		if path == "" {
			return "", nil
		}
		if filepath.IsAbs(path) && filepath.Clean(path) == path {
			return path, nil
		}
		native := filepath.FromSlash(path)
		if filepath.IsAbs(native) || filepath.Clean(native) != native || filepath.ToSlash(native) != path {
			return "", fmt.Errorf("historical production path %q is not canonical", path)
		}
		return path, nil
	})
}

const (
	productionProvisionLauncherAbsent productionProvisionLauncherState = iota
	productionProvisionLauncherCurrent
	productionProvisionLauncherReplaceable
)

type productionProvisionLauncherLine struct {
	index int
	value string
}

// productionProvisionExpectedFiles 从已验证输入唯一生成安装树内全部不可变文件。
func productionProvisionExpectedFiles(
	configRoot string,
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
	portableConfig, err := portableProductionCoordinatorConfig(configRoot, config)
	if err != nil {
		return nil, err
	}
	configData, err := json.MarshalIndent(portableConfig, "", "  ")
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
	expectedFiles, err := productionProvisionExpectedFiles(installRoot, config, inputs)
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
	if err := runtime.VerifyTrustedRepository(
		ctx, inputs.gitExecutable, inputs.root, trustedRepository,
	); err != nil {
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

func productionProvisionLauncherData(
	launcherPath string,
	configPath string,
	controllerPath string,
) ([]byte, error) {
	configRelative, err := filepath.Rel(filepath.Dir(launcherPath), configPath)
	if err != nil || filepath.IsAbs(configRelative) {
		return nil, errors.Join(errors.New("make production config launcher-relative"), err)
	}
	_ = controllerPath // The mutable CLI is always the fixed sibling, never a versioned controller path.
	return []byte("#!/bin/sh\nset -eu\n" +
		"launcher_dir=$(CDPATH= cd -P -- \"$(dirname -- \"$0\")\" && pwd)\n" +
		"SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG=\"$launcher_dir\"/" +
		shellQuoteProductionProvision(filepath.ToSlash(configRelative)) + "\n" +
		"export SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG\n" +
		"current=\"$launcher_dir\"/.super-dolphin-gate-current\n" +
		"test -f \"$current\"\n" +
		"\"$current\" _production-update \"$@\"\n" +
		"exec \"$current\" _production-launcher \"$@\"\n"), nil
}

func legacyProductionProvisionLauncherData(configRelative, controllerRelative string) []byte {
	return []byte("#!/bin/sh\nset -eu\n\n" +
		"launcher_dir=$(CDPATH= cd -P -- \"$(dirname -- \"$0\")\" && pwd)\n" +
		"SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG=\"$launcher_dir\"/" + shellQuoteProductionProvision(configRelative) + "\n" +
		"export SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG\n" +
		"exec \"$launcher_dir\"/" + shellQuoteProductionProvision(controllerRelative) + " _production-launcher \"$@\"\n")
}

// inspectProductionProvisionLauncher 只有固定 launcher 且 current 同时存在时才报告 ready。
func inspectProductionProvisionLauncher(path string, configPath string, controllerPath string) (bool, error) {
	state, err := inspectProductionProvisionLauncherState(path, configPath, controllerPath)
	if err != nil || state != productionProvisionLauncherCurrent {
		return false, err
	}
	current := filepath.Join(filepath.Dir(path), productionCurrentGateCLI)
	info, err := os.Lstat(current)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 ||
		!productionProvisionOwnedByCurrentUser(info) {
		return false, errors.Join(errors.New("production current CLI is not verified"), err)
	}
	return true, nil
}

// inspectProductionProvisionLauncherState 严格区分缺失、当前模板与可迁移的历史 launcher。
func inspectProductionProvisionLauncherState(
	launcherPath string,
	configPath string,
	controllerPath string,
) (productionProvisionLauncherState, error) {
	info, absent, err := inspectProductionProvisionLauncherInfo(launcherPath)
	if absent {
		return productionProvisionLauncherAbsent, nil
	}
	if err != nil {
		return productionProvisionLauncherAbsent, err
	}
	if err := validateProductionProvisionLauncherInfo(info); err != nil {
		return productionProvisionLauncherAbsent, err
	}
	data, err := os.ReadFile(launcherPath)
	if err != nil {
		return productionProvisionLauncherAbsent, fmt.Errorf("read production provision launcher: %w", err)
	}
	expected, err := productionProvisionLauncherData(launcherPath, configPath, controllerPath)
	if err != nil {
		return productionProvisionLauncherAbsent, err
	}
	if bytes.Equal(data, expected) {
		return productionProvisionLauncherCurrent, nil
	}
	replaceable, err := inspectReplaceableProductionLauncher(launcherPath, configPath, controllerPath, data)
	if err != nil {
		return productionProvisionLauncherAbsent, err
	}
	if replaceable {
		return productionProvisionLauncherReplaceable, nil
	}
	return productionProvisionLauncherAbsent, errors.New("production provision launcher already exists with unknown content")
}

// inspectProductionProvisionLauncherInfo 读取 launcher 元数据并把缺失与其他错误分开。
func inspectProductionProvisionLauncherInfo(path string) (os.FileInfo, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect production provision launcher: %w", err)
	}
	return info, false, nil
}

// validateProductionProvisionLauncherInfo 维持 launcher 的常规文件、owner-only 权限和所有者边界。
func validateProductionProvisionLauncherInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 || !productionProvisionOwnedByCurrentUser(info) {
		return errors.New("production provision launcher already exists and is not the verified launcher")
	}
	return nil
}

// inspectReplaceableProductionLauncher 仅验证允许迁移的稳定或历史 launcher 及其受控文件。
func inspectReplaceableProductionLauncher(launcherPath, expectedConfigPath, expectedControllerPath string, data []byte) (bool, error) {
	if configRelative, ok := parseStableProductionLauncher(data); ok {
		return true, verifyReplaceableProductionLauncherConfig(launcherPath, configRelative, expectedConfigPath)
	}
	configRelative, controllerRelative, ok := parseLegacyProductionLauncher(data)
	if ok {
		if err := verifyReplaceableProductionLauncherConfig(launcherPath, configRelative, expectedConfigPath); err != nil {
			return false, err
		}
		controllerPath, err := resolveProductionLauncherSiblingPath(launcherPath, controllerRelative)
		if err != nil {
			return false, err
		}
		info, err := os.Lstat(controllerPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 ||
			!productionProvisionOwnedByCurrentUser(info) || !isLegacyProductionControllerName(filepath.Base(controllerPath)) {
			return false, errors.Join(errors.New("legacy production launcher controller is not verified"), err)
		}
		return true, nil
	}
	historicalConfigPath, historicalControllerPath, ok := parseHistoricalProductionLauncher(data)
	if !ok {
		return false, nil
	}
	if err := verifyHistoricalProductionLauncherPaths(
		historicalConfigPath,
		historicalControllerPath,
		expectedConfigPath,
		expectedControllerPath,
	); err != nil {
		return false, err
	}
	return true, nil
}

// parseStableProductionLauncher 仅接受当前 launcher 的逐行精确模板。
func parseStableProductionLauncher(data []byte) (string, bool) {
	lines := strings.Split(string(data), "\n")
	if !matchProductionProvisionLauncherLines(lines, 10, []productionProvisionLauncherLine{
		{0, "#!/bin/sh"},
		{1, "set -eu"},
		{2, "launcher_dir=$(CDPATH= cd -P -- \"$(dirname -- \"$0\")\" && pwd)"},
		{4, "export SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG"},
		{5, "current=\"$launcher_dir\"/.super-dolphin-gate-current"},
		{6, "test -f \"$current\""},
		{7, "\"$current\" _production-update \"$@\""},
		{8, "exec \"$current\" _production-launcher \"$@\""},
		{9, ""},
	}) {
		return "", false
	}
	configRelative, ok := parseProductionLauncherQuotedRelative(
		lines[3],
		"SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG=\"$launcher_dir\"/",
		"",
	)
	return configRelative, ok
}

// parseLegacyProductionLauncher 仅接受允许迁移的历史 launcher 精确模板。
func parseLegacyProductionLauncher(data []byte) (string, string, bool) {
	lines := strings.Split(string(data), "\n")
	if !matchProductionProvisionLauncherLines(lines, 8, []productionProvisionLauncherLine{
		{0, "#!/bin/sh"},
		{1, "set -eu"},
		{2, ""},
		{3, "launcher_dir=$(CDPATH= cd -P -- \"$(dirname -- \"$0\")\" && pwd)"},
		{5, "export SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG"},
		{7, ""},
	}) {
		return "", "", false
	}
	configRelative, ok := parseProductionLauncherQuotedRelative(
		lines[4],
		"SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG=\"$launcher_dir\"/",
		"",
	)
	if !ok {
		return "", "", false
	}
	controllerRelative, ok := parseProductionLauncherQuotedRelative(
		lines[6],
		"exec \"$launcher_dir\"/",
		" _production-launcher \"$@\"",
	)
	if !ok || !bytes.Equal(data, legacyProductionProvisionLauncherData(configRelative, controllerRelative)) {
		return "", "", false
	}
	return configRelative, controllerRelative, true
}

// parseHistoricalProductionLauncher 只接受已部署的两行绝对路径 launcher 模板。
func parseHistoricalProductionLauncher(data []byte) (string, string, bool) {
	lines := strings.Split(string(data), "\n")
	if !matchProductionProvisionLauncherLines(lines, 3, []productionProvisionLauncherLine{
		{0, "#!/bin/sh"},
		{2, ""},
	}) {
		return "", "", false
	}
	configPath, remaining, ok := parseProductionProvisionSingleQuotedWord(
		lines[1],
		"SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG=",
	)
	if !ok {
		return "", "", false
	}
	controllerPath, remaining, ok := parseProductionProvisionSingleQuotedWord(remaining, " exec ")
	if !ok || remaining != " _production-launcher \"$@\"" {
		return "", "", false
	}
	return configPath, controllerPath, true
}

// parseProductionProvisionSingleQuotedWord accepts one literal POSIX single-quoted word.
func parseProductionProvisionSingleQuotedWord(input, prefix string) (string, string, bool) {
	if !strings.HasPrefix(input, prefix) {
		return "", "", false
	}
	encoded := strings.TrimPrefix(input, prefix)
	if len(encoded) < 2 || encoded[0] != '\'' {
		return "", "", false
	}
	end := strings.IndexByte(encoded[1:], '\'')
	if end < 0 {
		return "", "", false
	}
	end++
	value := encoded[1:end]
	if value == "" || strings.ContainsAny(value, "\x00\r\n$\\\x60") {
		return "", "", false
	}
	return value, encoded[end+1:], true
}

func verifyHistoricalProductionLauncherPaths(
	configPath, controllerPath, expectedConfigPath, expectedControllerPath string,
) error {
	if configPath != expectedConfigPath {
		return errors.New("historical production launcher config is not the provision config")
	}
	if controllerPath != expectedControllerPath {
		return errors.New("historical production launcher controller is not the provision bootstrap controller")
	}
	configCanonical, err := canonicalHistoricalProductionMigrationFile(
		"historical production launcher config", configPath,
	)
	if err != nil {
		return err
	}
	expectedConfigCanonical, err := canonicalHistoricalProductionMigrationFile(
		"expected historical production config", expectedConfigPath,
	)
	if err != nil || configCanonical != expectedConfigCanonical {
		return errors.Join(errors.New("historical production launcher config resolved identity changed"), err)
	}
	controllerCanonical, err := canonicalHistoricalProductionMigrationExecutable(
		"historical production launcher controller", controllerPath,
	)
	if err != nil {
		return err
	}
	expectedControllerCanonical, err := canonicalHistoricalProductionMigrationExecutable(
		"expected historical production controller", expectedControllerPath,
	)
	if err != nil || controllerCanonical != expectedControllerCanonical {
		return errors.Join(errors.New("historical production launcher controller resolved identity changed"), err)
	}
	return nil
}

// canonicalHistoricalProductionMigrationFile permits only a verified ancestral alias for v64 migration.
func canonicalHistoricalProductionMigrationFile(name, rawPath string) (string, error) {
	return canonicalHistoricalProductionMigrationPath(name, rawPath, canonicalProductionFile)
}

// canonicalHistoricalProductionMigrationExecutable preserves the normal executable validation on the target.
func canonicalHistoricalProductionMigrationExecutable(name, rawPath string) (string, error) {
	return canonicalHistoricalProductionMigrationPath(name, rawPath, canonicalProductionExecutable)
}

func canonicalHistoricalProductionMigrationPath(
	name, rawPath string,
	canonicalize func(string, string) (string, error),
) (string, error) {
	before, resolvedBefore, err := inspectHistoricalProductionMigrationAlias(rawPath)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	canonical, err := canonicalize(name, resolvedBefore)
	if err != nil {
		return "", err
	}
	after, resolvedAfter, err := inspectHistoricalProductionMigrationAlias(rawPath)
	if err != nil || resolvedAfter != resolvedBefore || !os.SameFile(before, after) {
		return "", errors.Join(errors.New("historical production launcher path changed while checking"), err)
	}
	if canonical != resolvedBefore {
		return "", errors.New("historical production launcher path resolved to an unexpected target")
	}
	return canonical, nil
}

func inspectHistoricalProductionMigrationAlias(rawPath string) (os.FileInfo, string, error) {
	if !filepath.IsAbs(rawPath) || filepath.Clean(rawPath) != rawPath {
		return nil, "", errors.New("historical production launcher path must be absolute and clean")
	}
	if err := verifyHistoricalProductionMigrationAliasChain(rawPath); err != nil {
		return nil, "", err
	}
	leaf, err := os.Lstat(rawPath)
	if err != nil || !leaf.Mode().IsRegular() || leaf.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.Join(errors.New("historical production launcher path must name a regular non-symlink file"), err)
	}
	resolved, err := filepath.EvalSymlinks(rawPath)
	if err != nil || !filepath.IsAbs(resolved) || filepath.Clean(resolved) != resolved {
		return nil, "", errors.Join(errors.New("historical production launcher path cannot resolve safely"), err)
	}
	return leaf, resolved, nil
}

func verifyHistoricalProductionMigrationAliasChain(rawPath string) error {
	volume := filepath.VolumeName(rawPath)
	root := volume + string(os.PathSeparator)
	current := root
	components := strings.Split(strings.TrimPrefix(rawPath, root), string(os.PathSeparator))
	userControlled := false
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			return errors.New("historical production launcher path contains an unsafe component")
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect historical production launcher path component: %w", err)
		}
		if index == len(components)-1 {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !productionProvisionOwnedByCurrentUser(info) {
				return errors.New("historical production launcher path has a leaf symlink or non-file")
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if !productionProvisionOwnedByCurrentUser(info) {
				return errors.New("historical production launcher alias is not owned by the current user")
			}
			userControlled = true
			continue
		}
		if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
			return errors.New("historical production launcher alias ancestor is not owner-controlled")
		}
		if productionProvisionOwnedByCurrentUser(info) {
			userControlled = true
		} else if userControlled {
			return errors.New("historical production launcher alias ancestor is not owned by the current user")
		}
	}
	return nil
}

// loadHistoricalProductionMigrationConfig creates a read-only canonical view of the retired v64 config.
func loadHistoricalProductionMigrationConfig(rawPath string) (productionCoordinatorConfig, error) {
	canonical, err := canonicalHistoricalProductionMigrationFile("historical production coordinator config", rawPath)
	if err != nil {
		return productionCoordinatorConfig{}, err
	}
	data, err := readProductionCoordinatorConfig(canonical)
	if err != nil {
		return productionCoordinatorConfig{}, err
	}
	var stored historicalStoredProductionCoordinatorConfig
	if err := gatecontract.DecodeStrictJSON(data, &stored); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("decode historical production coordinator config: %w", err)
	}
	config := productionCoordinatorConfig(stored)
	if err := resolveHistoricalProductionMigrationConfigPaths(filepath.Dir(rawPath), &config); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("resolve historical production coordinator config paths: %w", err)
	}
	return config, nil
}

func resolveHistoricalProductionMigrationConfigPaths(base string, config *productionCoordinatorConfig) error {
	return transformProductionCoordinatorConfigPaths(config, func(path string) (string, error) {
		if path == "" || filepath.IsAbs(path) {
			return path, nil
		}
		return filepath.Join(base, filepath.FromSlash(path)), nil
	})
}

// loadHistoricalProductionMigrationManifest decodes only the retired v64 input needed for read-only migration inspection.
func loadHistoricalProductionMigrationManifest(path string) (productionProvisionManifest, error) {
	canonical, err := canonicalProductionFile("historical production provision manifest", path)
	if err != nil {
		return productionProvisionManifest{}, err
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return productionProvisionManifest{}, err
	}
	var historical historicalProductionProvisionManifest
	if err := gatecontract.DecodeStrictJSON(data, &historical); err != nil {
		return productionProvisionManifest{}, fmt.Errorf("decode historical production provision manifest: %w", err)
	}
	return productionProvisionManifest(historical), nil
}

// matchProductionProvisionLauncherLines 按固定行数和索引逐项匹配 launcher 模板。
func matchProductionProvisionLauncherLines(lines []string, count int, required []productionProvisionLauncherLine) bool {
	if len(lines) != count {
		return false
	}
	for _, line := range required {
		if lines[line.index] != line.value {
			return false
		}
	}
	return true
}

// parseProductionLauncherQuotedRelative 解析 launcher 中规范的 shell 引号相对路径。
func parseProductionLauncherQuotedRelative(line, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return "", false
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
	value, ok := parseProductionProvisionShellQuote(encoded)
	if !ok || value == "" || strings.ContainsAny(value, "\x00\r\n") || path.IsAbs(value) ||
		path.Clean(value) != value || value == "." || value == ".." {
		return "", false
	}
	return value, true
}

func parseProductionProvisionShellQuote(encoded string) (string, bool) {
	if len(encoded) < 2 || encoded[0] != '\'' || encoded[len(encoded)-1] != '\'' {
		return "", false
	}
	decoded := strings.ReplaceAll(encoded[1:len(encoded)-1], "'\\''", "'")
	if shellQuoteProductionProvision(decoded) != encoded {
		return "", false
	}
	return decoded, true
}

func verifyReplaceableProductionLauncherConfig(launcherPath, configRelative, expectedConfigPath string) error {
	configPath, err := resolveProductionLauncherRelativePath(launcherPath, configRelative)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(expectedConfigPath) || filepath.Clean(expectedConfigPath) != expectedConfigPath || configPath != expectedConfigPath {
		return errors.New("replaceable production launcher config does not match the provision plan")
	}
	if _, err := loadProductionCoordinatorConfigFile(configPath); err != nil {
		return fmt.Errorf("verify replaceable production launcher config: %w", err)
	}
	return nil
}

// resolveProductionLauncherRelativePath 将已校验的相对路径解析为规范绝对路径。
func resolveProductionLauncherRelativePath(launcherPath, relative string) (string, error) {
	if decoded, ok := parseProductionProvisionShellQuote(shellQuoteProductionProvision(relative)); !ok ||
		decoded != relative || relative == "" || strings.ContainsAny(relative, "\x00\r\n") ||
		path.IsAbs(relative) || path.Clean(relative) != relative || relative == "." || relative == ".." {
		return "", errors.New("production launcher relative path is invalid")
	}
	directory := filepath.Dir(launcherPath)
	return filepath.Clean(filepath.Join(directory, filepath.FromSlash(relative))), nil
}

// resolveProductionLauncherSiblingPath 只允许历史 controller 指向 launcher 的同目录文件。
func resolveProductionLauncherSiblingPath(launcherPath, relative string) (string, error) {
	resolved, err := resolveProductionLauncherRelativePath(launcherPath, relative)
	if err != nil {
		return "", err
	}
	if filepath.Dir(resolved) != filepath.Dir(launcherPath) {
		return "", errors.New("legacy production launcher controller must be a sibling")
	}
	return resolved, nil
}

// isLegacyProductionControllerName 识别可迁移历史 controller 的严格版本化文件名。
func isLegacyProductionControllerName(name string) bool {
	version, ok := strings.CutPrefix(name, "gate-client-v")
	if !ok || version == "" || version[0] == '0' {
		return false
	}
	for _, character := range version {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// verifyProductionProvisionTrustedRepository 复核 bare remote、ref、commit 与 tree 均未漂移。
func verifyProductionProvisionTrustedRepository(
	ctx context.Context,
	gitExecutable string,
	root productionBootstrapRoot,
	repository string,
) error {
	bare, err := productionProvisionGitLine(
		ctx, gitExecutable, repository, "rev-parse", "--is-bare-repository",
	)
	if err != nil || bare != "true" {
		return errors.Join(errors.New("production provision trusted repository is not bare"), err)
	}
	remote, err := productionProvisionGitLine(
		ctx, gitExecutable, repository, "config", "--local", "--get", "remote.origin.url",
	)
	if err != nil || remote != root.RemoteURL {
		return errors.Join(errors.New("production provision trusted repository remote drifted"), err)
	}
	commit, err := productionProvisionGitLine(
		ctx, gitExecutable, repository, "rev-parse", "--verify", root.TrustedRef+"^{commit}",
	)
	if err != nil || commit != root.BaselineCommit {
		return errors.Join(errors.New("production provision trusted ref baseline commit drifted"), err)
	}
	tree, err := productionProvisionGitLine(
		ctx, gitExecutable, repository, "rev-parse", "--verify", root.BaselineCommit+"^{tree}",
	)
	if err != nil || tree != root.BaselineTree {
		return errors.Join(errors.New("production provision baseline tree drifted"), err)
	}
	return nil
}
