package workflowtemplates

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func checkTemplateVersion(tpl Template, version any) error {
	text := workflowTemplateVersionString(version)
	if text == "" {
		return nil
	}
	if text != strconv.Itoa(tpl.Version) {
		return fmt.Errorf("workflowtemplates: template %q version %s not found", tpl.ID, text)
	}
	return nil
}

func requireFields(tpl Template, values map[string]string) error {
	missing := make([]string, 0)
	for _, field := range tpl.UISchema {
		if field.Required && strings.TrimSpace(values[field.Key]) == "" {
			missing = append(missing, field.Key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("workflowtemplates: missing required fields: %s", strings.Join(missing, ", "))
	}
	if outputPath := values["output_path"]; outputPath != "" {
		if err := validateOutputPathValue(outputPath, sharedFilePrefixes(tpl.Validation)); err != nil {
			return fmt.Errorf("workflowtemplates: output_path %w", err)
		}
	}
	return nil
}

func normalizedValues(values map[string]any) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = normalizedValue(value)
	}
	return out
}

func normalizedValue(value any) string {
	switch current := value.(type) {
	case string:
		return strings.TrimSpace(current)
	case nil:
		return ""
	default:
		raw, err := json.Marshal(current)
		if err != nil {
			return fmt.Sprint(current)
		}
		return strings.TrimSpace(string(raw))
	}
}

func renderValues(req RenderRequest) map[string]any {
	out := make(map[string]any, len(req.Values)+len(req.UserInputs))
	for key, value := range req.Values {
		out[key] = value
	}
	for key, value := range req.UserInputs {
		out[key] = value
	}
	return out
}

func renderMap(input map[string]any, values map[string]string) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = renderValue(value, values)
	}
	return out
}

func renderSlice(input []any, values map[string]string) []any {
	out := make([]any, len(input))
	for index, value := range input {
		out[index] = renderValue(value, values)
	}
	return out
}

func renderValue(value any, values map[string]string) any {
	switch current := value.(type) {
	case string:
		return renderString(current, values)
	case map[string]any:
		return renderMap(current, values)
	case []any:
		return renderSlice(current, values)
	default:
		return current
	}
}

func renderString(input string, values map[string]string) string {
	out := input
	for key, value := range values {
		out = strings.ReplaceAll(out, "{{"+key+"}}", value)
	}
	return out
}

func templateLocale(req RenderRequest) string {
	if locale := strings.TrimSpace(req.TemplateLocale); locale != "" {
		return locale
	}
	if locale := strings.TrimSpace(fmt.Sprint(req.RuntimeContext["locale"])); locale != "" && locale != "<nil>" {
		return locale
	}
	return "zh-CN"
}

func workflowTemplateVersionString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}
