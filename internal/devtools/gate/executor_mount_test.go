package gate

import (
	"strings"
	"testing"
)

func TestValidateMountInfoRequiresExactReadOnlyMount(t *testing.T) {
	readOnly := "41 30 0:40 / /workspace/source ro,nosuid,nodev - ext4 /dev/root ro\n"
	if err := validateMountInfo(strings.NewReader(readOnly), "/workspace/source"); err != nil {
		t.Fatalf("validateMountInfo read-only: %v", err)
	}
	readWrite := "41 30 0:40 / /workspace/source rw,nosuid,nodev - ext4 /dev/root rw\n"
	if err := validateMountInfo(strings.NewReader(readWrite), "/workspace/source"); err == nil {
		t.Fatal("validateMountInfo unexpectedly accepted a read-write source mount")
	}
	if err := validateMountInfo(strings.NewReader(readOnly), "/workspace/source/nested"); err == nil {
		t.Fatal("validateMountInfo unexpectedly accepted a non-mount child path")
	}
}

func TestValidateMountInfoDecodesEscapedMountPath(t *testing.T) {
	mountInfo := "41 30 0:40 / /workspace/source\\040snapshot ro - ext4 /dev/root ro\n"
	if err := validateMountInfo(strings.NewReader(mountInfo), "/workspace/source snapshot"); err != nil {
		t.Fatalf("validateMountInfo escaped path: %v", err)
	}
}
