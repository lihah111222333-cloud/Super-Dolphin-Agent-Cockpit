package sqlcconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ValidateOutputOwners 校验每个配置单元的输出是否严格位于其 owner 安全父目录内。
func ValidateOutputOwners(units []Unit) error {
	for _, unit := range units {
		parent, err := OutputOwnerParent(unit.ConfigPath)
		if err != nil {
			return err
		}
		output := path.Clean(filepath.ToSlash(unit.Output))
		if output == parent || !strings.HasPrefix(output, parent+"/") {
			return fmt.Errorf("SQLC output %s must be a strict descendant of owner parent %s", unit.Output, parent)
		}
	}
	return nil
}

// rejectStaleConfiguredOutputs 拒绝 owner 父目录内已不属于当前配置的旧 SQLC 产物。
func rejectStaleConfiguredOutputs(repoRoot string, units []Unit) error {
	for _, unit := range units {
		parent, err := OutputOwnerParent(unit.ConfigPath)
		if err != nil {
			return err
		}
		if err := rejectStaleOutputTree(repoRoot, parent, unit.Output); err != nil {
			return err
		}
	}
	return nil
}

func rejectStaleOutputTree(repoRoot, ownerParent, configuredOutput string) error {
	root := filepath.Join(repoRoot, filepath.FromSlash(ownerParent))
	inspection := staleOutputInspection{repoRoot: repoRoot, configuredOutput: configuredOutput}
	err := filepath.WalkDir(root, inspection.inspect)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect SQLC owner parent %s: %w", ownerParent, err)
	}
	return nil
}

type staleOutputInspection struct {
	repoRoot, configuredOutput string
}

func (inspection staleOutputInspection) inspect(current string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	relative, err := filepath.Rel(inspection.repoRoot, current)
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	if relative == inspection.configuredOutput {
		if !entry.IsDir() {
			return fmt.Errorf("configured SQLC output %s is not a directory", inspection.configuredOutput)
		}
		return filepath.SkipDir
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("SQLC owner parent contains symbolic link: %s", relative)
	}
	if entry.IsDir() || filepath.Ext(relative) != ".go" {
		return nil
	}
	return inspectStaleOutputFile(current, relative, inspection.configuredOutput)
}

func inspectStaleOutputFile(current, relative, configuredOutput string) error {
	data, err := os.ReadFile(current)
	if err != nil {
		return err
	}
	if bytes.HasPrefix(data, []byte(GeneratedGoMarker)) || bytes.HasPrefix(data, []byte(ManualExtensionMarker)) || bytes.HasPrefix(data, []byte(ManualTestExtensionMarker)) {
		return fmt.Errorf("stale SQLC output %s remains outside configured output %s", relative, configuredOutput)
	}
	return nil
}

// OutputOwnerParent 返回配置 owner 的保守安全父目录，供边界守卫复用同一事实源。
func OutputOwnerParent(configPath string) (string, error) {
	switch configPath {
	case RootConfigPath:
		return RootOutputOwnerParent, nil
	case OrchestratorConfigPath:
		return OrchestratorOutputOwnerParent, nil
	default:
		return "", fmt.Errorf("SQLC config %s has no owner parent", configPath)
	}
}

// OutputForConfig 从已验证 unit 集合返回指定 canonical 配置的唯一 output。
func OutputForConfig(units []Unit, configPath string) (string, error) {
	output := ""
	for _, unit := range units {
		if unit.ConfigPath != configPath {
			continue
		}
		if output != "" {
			return "", fmt.Errorf("SQLC config %s has multiple output units", configPath)
		}
		output = unit.Output
	}
	if output == "" {
		return "", fmt.Errorf("SQLC config %s has no output unit", configPath)
	}
	return output, nil
}
