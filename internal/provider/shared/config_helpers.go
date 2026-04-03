package shared

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ResolveBinaryDir(cwd string, cfg map[string]any) string {
	if dir := ConfigString(cfg, "binary_dir", "binaryDir"); dir != "" {
		return dir
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	if bin, err := exec.LookPath("mcp-lsp"); err == nil {
		return filepath.Dir(bin)
	}
	return strings.TrimSpace(cwd)
}

func ConfigString(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := cfg[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func StringMap(raw any) map[string]string {
	input, _ := raw.(map[string]any)
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); key == "" {
			continue
		}
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		out[key] = text
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ConfigStringSlice(cfg map[string]any, keys ...string) []string {
	for _, key := range keys {
		values, ok := cfg[key]
		if !ok {
			continue
		}
		if out := NormalizeConfigStringSlice(values); len(out) > 0 {
			return out
		}
	}
	return nil
}

func NormalizeConfigStringSlice(values any) []string {
	switch typed := values.(type) {
	case []string:
		return TrimStrings(typed)
	case []any:
		return TrimConfigStringValues(typed)
	case string:
		return SplitConfigStringSlice(typed)
	default:
		return nil
	}
}

func TrimConfigStringValues(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func SplitConfigStringSlice(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return TrimStrings(strings.Split(value, ","))
}

func TrimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
