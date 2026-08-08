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

func TestValidateMountInfoRejectsDuplicateWritableMount(t *testing.T) {
	mountInfo := strings.Join([]string{
		"41 30 0:40 / /workspace/source ro,nosuid,nodev - ext4 /dev/root ro",
		"42 41 0:41 / /workspace/source rw,nosuid,nodev - tmpfs tmpfs rw",
	}, "\n") + "\n"
	if err := validateMountInfo(strings.NewReader(mountInfo), "/workspace/source"); err == nil {
		t.Fatal("validateMountInfo accepted duplicate writable source mount")
	}
}

func TestValidateContainingReadOnlyMountInfoAcceptsImageLayerChild(t *testing.T) {
	mountInfo := "41 30 0:40 / / ro,nosuid,nodev - overlay overlay ro\n"
	if err := validateContainingReadOnlyMountInfo(strings.NewReader(mountInfo), "/opt/super-dolphin/cache/go-build"); err != nil {
		t.Fatalf("validateContainingReadOnlyMountInfo image child: %v", err)
	}
}

func TestValidateContainingReadOnlyMountInfoRejectsNestedWritableMount(t *testing.T) {
	mountInfo := strings.Join([]string{
		"41 30 0:40 / / ro,nosuid,nodev - overlay overlay ro",
		"42 41 0:41 / /opt/super-dolphin/cache rw,nosuid,nodev - tmpfs tmpfs rw",
	}, "\n") + "\n"
	if err := validateContainingReadOnlyMountInfo(strings.NewReader(mountInfo), "/opt/super-dolphin/cache/go-build"); err == nil {
		t.Fatal("validateContainingReadOnlyMountInfo accepted nested writable mount")
	}
}

func TestValidateContainingReadOnlyMountInfoRejectsDescendantMount(t *testing.T) {
	mountInfo := strings.Join([]string{
		"41 30 0:40 / / ro,nosuid,nodev - overlay overlay ro",
		"42 41 0:41 / /opt/super-dolphin/cache/go-build/sub ro,nosuid,nodev - tmpfs tmpfs ro",
	}, "\n") + "\n"
	if err := validateContainingReadOnlyMountInfo(strings.NewReader(mountInfo), "/opt/super-dolphin/cache/go-build"); err == nil {
		t.Fatal("validateContainingReadOnlyMountInfo accepted a descendant mount inside the image seed")
	}
}

func TestValidateContainingReadOnlyMountInfoRejectsExactMount(t *testing.T) {
	mountInfo := strings.Join([]string{
		"41 30 0:40 / / ro,nosuid,nodev - overlay overlay ro",
		"42 41 0:41 / /opt/super-dolphin/cache/go-build ro,nosuid,nodev - tmpfs tmpfs ro",
	}, "\n") + "\n"
	if err := validateContainingReadOnlyMountInfo(strings.NewReader(mountInfo), "/opt/super-dolphin/cache/go-build"); err == nil {
		t.Fatal("validateContainingReadOnlyMountInfo accepted a replacement mount at the image seed")
	}
}

func TestValidateContainingReadOnlyMountInfoRejectsNonRootAncestorMount(t *testing.T) {
	mountInfo := strings.Join([]string{
		"41 30 0:40 / / ro,nosuid,nodev - overlay overlay ro",
		"42 41 0:41 / /opt/super-dolphin ro,nosuid,nodev - ext4 /dev/root ro",
	}, "\n") + "\n"
	if err := validateContainingReadOnlyMountInfo(strings.NewReader(mountInfo), "/opt/super-dolphin/cache/go-build"); err == nil {
		t.Fatal("validateContainingReadOnlyMountInfo accepted a non-root ancestor mount")
	}
}

func TestValidateContainingReadOnlyMountInfoRejectsSiblingPrefix(t *testing.T) {
	mountInfo := "41 30 0:40 / /opt/super-dolphin-cache ro,nosuid,nodev - ext4 /dev/root ro\n"
	if err := validateContainingReadOnlyMountInfo(strings.NewReader(mountInfo), "/opt/super-dolphin/cache/go-build"); err == nil {
		t.Fatal("validateContainingReadOnlyMountInfo accepted sibling path prefix")
	}
}

func TestValidateContainingReadOnlyMountInfoRejectsInvalidInput(t *testing.T) {
	for name, input := range map[string]struct {
		path      string
		mountInfo string
	}{
		"relative path":      {"opt/super-dolphin/cache/go-build", "41 30 0:40 / / ro - overlay overlay ro\n"},
		"non-canonical path": {"/opt/super-dolphin/../super-dolphin/cache/go-build", "41 30 0:40 / / ro - overlay overlay ro\n"},
		"missing mount":      {"/opt/super-dolphin/cache/go-build", "41 30 0:40 / /other ro - ext4 /dev/root ro\n"},
		"malformed record":   {"/opt/super-dolphin/cache/go-build", "malformed\n41 30 0:40 / / ro - overlay overlay ro\n"},
		"writable root":      {"/opt/super-dolphin/cache/go-build", "41 30 0:40 / / rw - overlay overlay rw\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateContainingReadOnlyMountInfo(strings.NewReader(input.mountInfo), input.path); err == nil {
				t.Fatal("validateContainingReadOnlyMountInfo accepted invalid input")
			}
		})
	}
}

func TestValidateExecutorOCIProjectGoBuildCacheSeedRejectsNonCanonicalRoot(t *testing.T) {
	if err := validateExecutorOCIProjectGoBuildCacheSeed(t.TempDir()); err == nil {
		t.Fatal("validateExecutorOCIProjectGoBuildCacheSeed accepted a non-canonical seed root")
	}
}
