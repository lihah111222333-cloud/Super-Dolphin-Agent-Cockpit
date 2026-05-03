package shared

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	executablePath = os.Executable
	lookPath       = exec.LookPath
)

var managedBinaryNames = []string{"mcp-lsp", "mcp-orch"}

func ResolveBinaryDir(cwd string, cfg map[string]any) string {
	if dir := ConfigString(cfg, "binary_dir", "binaryDir"); dir != "" {
		return dir
	}
	candidates := make([]string, 0, 3)
	if exe, err := executablePath(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if dir := strings.TrimSpace(cwd); dir != "" {
		candidates = append(candidates, dir)
	}
	if dir := lookPathBinaryDir(); dir != "" {
		candidates = append(candidates, dir)
	}
	if dir := firstManagedBinaryDir(candidates...); dir != "" {
		return dir
	}
	for _, dir := range candidates {
		if dir = strings.TrimSpace(dir); dir != "" {
			return dir
		}
	}
	return ""
}

func lookPathBinaryDir() string {
	for _, name := range managedBinaryNames {
		if bin, err := lookPath(name); err == nil {
			return filepath.Dir(bin)
		}
	}
	return ""
}

func firstManagedBinaryDir(dirs ...string) string {
	for _, dir := range dirs {
		if hasManagedBinary(dir) {
			return dir
		}
	}
	return ""
}

func hasManagedBinary(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	for _, name := range managedBinaryNames {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func ConfigString(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := cfg[key].(string); ok {
			if value = SanitizeConfigString(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func SanitizeConfigString(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "[object object]", "undefined", "null":
		return ""
	default:
		return value
	}
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
