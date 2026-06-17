package workflowtemplates

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var windowsDrivePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

var defaultSharedFilePrefixes = []string{"reports/workflows/", "dag/"}

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

func isAbsolutePath(path string) bool {
	return filepath.IsAbs(path) || strings.HasPrefix(path, "/") || windowsDrivePath.MatchString(path)
}

func containsParentSegment(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func targetsTemplateAssets(path string) bool {
	return strings.HasPrefix(path, "assets/") || strings.Contains(path, "/assets/") || strings.Contains(path, "workflowtemplates/assets")
}

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

func hasAllowedPrefix(path string, prefixes []string) bool {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}
