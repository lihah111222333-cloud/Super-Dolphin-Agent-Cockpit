package workflowtemplates

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// windowsDrivePath 识别 Windows 盘符绝对路径，补足 filepath.IsAbs 的跨平台差异。
var windowsDrivePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// defaultSharedFilePrefixes 是模板未声明路径前缀时的安全默认写入范围。
var defaultSharedFilePrefixes = []string{"reports/workflows/", "dag/"}

// validateTemplateOutputPaths 校验模板中的未渲染输出路径模板不会越界。
func validateTemplateOutputPaths(tpl Template) error {
	prefixes := sharedFilePrefixes(tpl.Validation)
	for _, node := range tpl.DAGTemplate.Nodes {
		if err := validateNodeOutputPaths(node, prefixes); err != nil {
			return err
		}
	}
	if err := validateTemplatePathTemplate(tpl.FinalOutput.PathTemplate, prefixes); err != nil {
		return fmt.Errorf("final_output.path_template: %w", err)
	}
	return nil
}

// validateNodeOutputPaths 校验单个节点声明的 sharedfile/artifact 输出路径模板。
func validateNodeOutputPaths(node NodeTemplate, prefixes []string) error {
	outputs, ok := objectMap(node.Config["outputs"])
	if !ok {
		return nil
	}
	if shared, ok := objectMap(outputs["to_sharedfile"]); ok {
		if err := validateTemplatePathTemplate(fmt.Sprint(shared["path"]), prefixes); err != nil {
			return fmt.Errorf("node %s outputs.to_sharedfile.path: %w", node.NodeKey, err)
		}
	}
	if artifact, ok := objectMap(outputs["to_artifact"]); ok {
		if err := validateTemplatePathTemplate(fmt.Sprint(artifact["path_template"]), prefixes); err != nil {
			return fmt.Errorf("node %s outputs.to_artifact.path_template: %w", node.NodeKey, err)
		}
	}
	return nil
}

// validateRenderedOutputPaths 校验渲染后的 DAG 输出路径已经落在允许前缀内。
func validateRenderedOutputPaths(tpl Template, nodes []NodeTemplate, finalOutput FinalOutput) error {
	prefixes := sharedFilePrefixes(tpl.Validation)
	for _, node := range nodes {
		if err := validateRenderedNodeOutputPaths(node, prefixes); err != nil {
			return err
		}
	}
	if err := validateOutputPathValue(finalOutput.PathTemplate, prefixes); err != nil {
		return fmt.Errorf("final_output.path_template: %w", err)
	}
	return nil
}

// validateRenderedNodeOutputPaths 校验单个渲染节点的输出路径值。
func validateRenderedNodeOutputPaths(node NodeTemplate, prefixes []string) error {
	outputs, ok := objectMap(node.Config["outputs"])
	if !ok {
		return nil
	}
	if shared, ok := objectMap(outputs["to_sharedfile"]); ok {
		if err := validateOutputPathValue(fmt.Sprint(shared["path"]), prefixes); err != nil {
			return fmt.Errorf("node %s outputs.to_sharedfile.path: %w", node.NodeKey, err)
		}
	}
	if artifact, ok := objectMap(outputs["to_artifact"]); ok {
		if err := validateOutputPathValue(fmt.Sprint(artifact["path_template"]), prefixes); err != nil {
			return fmt.Errorf("node %s outputs.to_artifact.path_template: %w", node.NodeKey, err)
		}
	}
	return nil
}

// validateTemplatePathTemplate 校验模板路径；允许 `{{output_path}}` 作为受控用户输入占位。
func validateTemplatePathTemplate(pathTemplate string, prefixes []string) error {
	path := strings.TrimSpace(pathTemplate)
	if path == "" {
		return errors.New("path is required")
	}
	if strings.HasPrefix(path, "{{output_path}}") {
		return validateNoPathEscape(path)
	}
	return validateOutputPathValue(path, prefixes)
}

// validateOutputPathValue 校验具体输出路径值，要求安全前缀和运行唯一占位同时存在。
func validateOutputPathValue(pathValue string, prefixes []string) error {
	path := filepath.ToSlash(strings.TrimSpace(pathValue))
	if path == "" {
		return errors.New("path is required")
	}
	if err := validateNoPathEscape(path); err != nil {
		return err
	}
	if !hasAllowedPrefix(path, prefixes) {
		return fmt.Errorf("path must start with one of: %s", strings.Join(prefixes, ", "))
	}
	if !strings.Contains(path, "{{run_id}}") && !strings.Contains(path, "{{run_key}}") {
		return errors.New("path must include {{run_id}} or {{run_key}}")
	}
	return nil
}

// validateNoPathEscape 拒绝绝对路径、父目录跳转和写入模板 assets。
func validateNoPathEscape(path string) error {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if normalized == "" {
		return errors.New("path is required")
	}
	if isAbsolutePath(normalized) {
		return errors.New("absolute output paths are not allowed")
	}
	if containsParentSegment(normalized) {
		return errors.New("output paths must not contain ..")
	}
	if targetsTemplateAssets(normalized) {
		return errors.New("output paths must not target template assets")
	}
	return nil
}

// isAbsolutePath 统一识别 Unix、当前平台和 Windows 盘符绝对路径。
func isAbsolutePath(path string) bool {
	return filepath.IsAbs(path) || strings.HasPrefix(path, "/") || windowsDrivePath.MatchString(path)
}

// containsParentSegment 检查路径段中是否包含父目录跳转。
func containsParentSegment(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// targetsTemplateAssets 防止运行输出覆盖内置模板资源目录。
func targetsTemplateAssets(path string) bool {
	return strings.HasPrefix(path, "assets/") || strings.Contains(path, "/assets/") || strings.Contains(path, "workflowtemplates/assets")
}

// sharedFilePrefixes 合并单前缀和多前缀配置，并在缺省时使用安全默认值。
func sharedFilePrefixes(rule ValidationRule) []string {
	prefixes := append([]string(nil), rule.SharedFilePrefixes...)
	if strings.TrimSpace(rule.SharedFilePrefix) != "" {
		prefixes = append(prefixes, strings.TrimSpace(rule.SharedFilePrefix))
	}
	if len(prefixes) == 0 {
		prefixes = append(prefixes, defaultSharedFilePrefixes...)
	}
	return normalizePrefixes(prefixes)
}

// normalizePrefixes 清理输出前缀并统一追加尾部斜杠。
func normalizePrefixes(prefixes []string) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		normalized := filepath.ToSlash(strings.TrimSpace(prefix))
		if normalized == "" {
			continue
		}
		if !strings.HasSuffix(normalized, "/") {
			normalized += "/"
		}
		out = append(out, normalized)
	}
	return out
}

// hasAllowedPrefix 判断路径是否落在任一允许输出前缀下。
func hasAllowedPrefix(path string, prefixes []string) bool {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}
