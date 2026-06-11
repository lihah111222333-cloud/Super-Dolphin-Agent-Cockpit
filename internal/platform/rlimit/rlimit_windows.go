//go:build windows

package rlimit

// Init is a no-op on Windows.
func Init() {}
