package kernel

import "strings"

// IsRemoteTurnInput reports whether value is an HTTP(S) input reference.
func IsRemoteTurnInput(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}
