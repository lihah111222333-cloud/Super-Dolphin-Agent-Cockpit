package archtest

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var remoteCIACRArtifactPattern = regexp.MustCompile(`\bACR\b|\bacr(?:Auth|Client|Repository|Registry|Role|Token)\b`)

// TestRemoteCISuccessorExecutorArtifactsAreAbsent makes the accepted-only
// boundary explicit: repository code may consume an accepted snapshot and
// import a generation-one receipt, but it may not construct its successor.
func TestRemoteCISuccessorExecutorArtifactsAreAbsent(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range remoteCIProductionFiles(t, root) {
		relative := relativeRemoteCIContractPath(t, root, file)
		original := readRemoteCIContractGuardFile(t, file)
		source := strings.ToLower(original)
		for _, forbidden := range []string{
			"buildkit", "output_repository", "outputrepository",
			"createimagecache", "findimagecachebyname",
			"remotebaselinerefresh", "candidate reservation",
			"cas promotion", "promoteremotebaseline",
			"docker desktop", "docker daemon", "docker buildx",
			"datacache", "fullworkspacetar", "fullcontextbootstrap",
			"legacydelta", "directcachedelta", "docker_host", "zstd",
			"aliyuncr", "container registry",
		} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s retains forbidden accepted-only successor artifact %q", relative, forbidden)
			}
		}
		if remoteCIACRArtifactPattern.MatchString(original) {
			t.Errorf("%s retains forbidden accepted-only ACR artifact", relative)
		}
	}
	for _, config := range []string{
		"cmd/super-dolphin-gate/remote_run_config.go",
		"cmd/super-dolphin-gate/remote_run_options.go",
	} {
		source := strings.ToLower(readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(config))))
		for _, forbidden := range []string{"oci_refresh", "output_repository", "refresh image", "refreshimage"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s retains successor executor configuration %q", config, forbidden)
			}
		}
	}
}

// TestRemoteCIACRArtifactPatternCounterexamples 锁定 ACR 专用入口与普通标识符的边界。
func TestRemoteCIACRArtifactPatternCounterexamples(t *testing.T) {
	for _, source := range []string{"ACR", "acrClient", "acrRepository", "acrAuth"} {
		if !remoteCIACRArtifactPattern.MatchString(source) {
			t.Fatalf("ACR artifact pattern accepted %q", source)
		}
	}
	for _, source := range []string{"metadataCredentials", "duplicated across shards", "immutable image identity"} {
		if remoteCIACRArtifactPattern.MatchString(source) {
			t.Fatalf("ACR artifact pattern rejected unrelated source %q", source)
		}
	}
}
