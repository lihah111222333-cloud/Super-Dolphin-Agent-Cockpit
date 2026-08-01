//go:build !linux

package gateprivate

func trustedExecutableOnReadOnlyMount(string) bool {
	return false
}
