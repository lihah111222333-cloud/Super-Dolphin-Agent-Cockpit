package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIConfigFileVolumeProjectionGuard 锁定控制文件投影边界。
func TestRemoteCIConfigFileVolumeProjectionGuard(t *testing.T) {
	root := findRepoRoot(t)
	coordinator := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_request.go"))
	client := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/alicloud/eci/client.go"))
	projection := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/alicloud/eci/config_file_volume.go"))
	assertConfigFileCoordinatorContract(t, coordinator)
	assertConfigFileClientContract(t, client)
	assertConfigFileProjectionContract(t, projection)
	assertConfigFileMainMountBoundary(t, client)
}

func assertConfigFileCoordinatorContract(t *testing.T, coordinator string) {
	t.Helper()
	for _, required := range []string{
		"remoteShardBootstrapVolumeName",
		"remoteShardBootstrapPath",
		"ConfigFileVolumes: []eci.ConfigFileVolume",
		"ConfigFileVolumeSafeMode",
		"ConfigFileToPath: []eci.ConfigFileToPath",
		"chmod -R a+rX /workspace/source",
		`{Name: "source-data", MountPath: gate.ExecutorSourcePath, ReadOnly: true}`,
	} {
		if !strings.Contains(coordinator, required) {
			t.Fatalf("normal shard ConfigFileVolume projection is missing %q", required)
		}
	}
	for _, forbidden := range []string{`Args: []string{"-c"`, `Args: []string{remoteShardBootstrapTemplateSH}`} {
		if strings.Contains(coordinator, forbidden) {
			t.Fatalf("normal shard retains inline bootstrap command %q", forbidden)
		}
	}
}

func assertConfigFileClientContract(t *testing.T, client string) {
	t.Helper()
	for _, required := range []string{
		"appendEmptyDirVolumes(volumeArgs, 1, emptyDirs)",
		"appendConfigFileVolumes(volumeArgs, len(emptyDirs)+1, request.ConfigFileVolumes)",
		"appendConfigFileVolumes",
		"mountPathsOverlap",
	} {
		if !strings.Contains(client, required) {
			t.Fatalf("ECI ConfigFileVolume CLI mapping is missing %q", required)
		}
	}
}

func assertConfigFileProjectionContract(t *testing.T, projection string) {
	t.Helper()
	for _, required := range []string{
		"ConfigFileVolumeType",
		"ConfigFileVolume.ConfigFileToPath",
		"ConfigFileVolumeSafeMode",
		"ConfigFileVolumeMaxVolumesPerGroup",
		"ConfigFileVolumeMaxFilesPerGroup",
		"len(emptyDirNames)+len(volumes) > ConfigFileVolumeMaxVolumesPerGroup",
		"totalFiles > ConfigFileVolumeMaxFilesPerGroup",
		"base64.StdEncoding.EncodeToString",
		"containsSensitiveRuntimeValue",
		"configFileProjectionRedactionValues",
		`"x-amz-"`,
		`"username"`,
		"strings.IndexFunc(filePath, unicode.IsControl)",
	} {
		if !strings.Contains(projection, required) {
			t.Fatalf("ConfigFileVolume projection contract is missing %q", required)
		}
	}
}

func assertConfigFileMainMountBoundary(t *testing.T, client string) {
	t.Helper()
	mainMountsStart := strings.Index(client, "func createMainMountNames")
	if mainMountsStart < 0 {
		t.Fatal("createMainMountNames implementation is missing")
	}
	mainMountsEnd := strings.Index(client[mainMountsStart:], "\n}")
	if mainMountsEnd < 0 {
		t.Fatal("createMainMountNames implementation is incomplete")
	}
	mainMounts := client[mainMountsStart : mainMountsStart+mainMountsEnd]
	if strings.Contains(mainMounts, "createConfigFileVolumeNames") {
		t.Fatal("main container may not mount ConfigFileVolume projections")
	}
}
