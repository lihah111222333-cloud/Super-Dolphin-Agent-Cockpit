//go:build !windows

package installer

import (
	"errors"
	"testing"
)

func TestDetectHostPlatformFailsFastOnNonWindows(t *testing.T) {
	platform, err := DetectWindowsHostPlatform()
	if !errors.Is(err, ErrUnsupportedWindowsHostPlatform) {
		t.Fatalf("DetectWindowsHostPlatform() error = %v, want ErrUnsupportedWindowsHostPlatform", err)
	}
	if platform != (WindowsHostPlatform{}) {
		t.Fatalf("DetectWindowsHostPlatform() platform = %#v, want zero value", platform)
	}
}
