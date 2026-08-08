package remoteci

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func assertRemoteRequestObjectsAreContentAddressed(t *testing.T, contents map[string][]byte, plannedSet gate.ContainerShardSet) {
	t.Helper()
	fullCount, bootstrapCount := 0, 0
	for key, data := range contents {
		digest, kind := remoteRequestObjectIdentity(t, key, data)
		if kind == "" {
			continue
		}
		sum := sha256.Sum256(data)
		if digest != hex.EncodeToString(sum[:]) {
			t.Fatalf("request object key %q is not content addressed by %x", key, sum)
		}
		if kind == "bootstrap" {
			bootstrapCount++
		} else {
			fullCount++
		}
	}
	want := len(plannedSet.Shards)
	if fullCount != want || bootstrapCount != want {
		t.Fatalf("request object counts full=%d bootstrap=%d want=%d", fullCount, bootstrapCount, want)
	}
}

func remoteRequestObjectIdentity(t *testing.T, key string, data []byte) (string, string) {
	t.Helper()
	base := key[strings.LastIndex(key, "/")+1:]
	if strings.HasSuffix(key, ".bootstrap.request.json") {
		if _, err := DecodeBootstrapShardRequest(data); err != nil {
			t.Fatalf("DecodeBootstrapShardRequest(%q) = %v", key, err)
		}
		return strings.TrimSuffix(base, ".bootstrap.request.json"), "bootstrap"
	}
	if strings.HasSuffix(key, ".request.json") {
		if _, err := DecodeShardRequest(data); err != nil {
			t.Fatalf("DecodeShardRequest(%q) = %v", key, err)
		}
		return strings.TrimSuffix(base, ".request.json"), "full"
	}
	return "", ""
}

func assertRemoteRequestEnvironmentIdentities(t *testing.T, contents map[string][]byte, creates []eci.CreateRequest) {
	t.Helper()
	for _, create := range creates {
		assertRemoteRequestEnvironmentIdentity(t, contents, create)
	}
}

func assertRemoteRequestEnvironmentIdentity(t *testing.T, contents map[string][]byte, create eci.CreateRequest) {
	t.Helper()
	environment := create.InitContainer.Environment
	bootstrapKey := environment["SUPER_DOLPHIN_REMOTE_REQUEST_KEY"]
	fullKey := environment[FullRequestKeyEnvironment]
	bootstrapData, bootstrapOK := contents[bootstrapKey]
	fullData, fullOK := contents[fullKey]
	if !bootstrapOK || !fullOK {
		t.Fatalf("create request references missing bootstrap/full objects: bootstrap=%q full=%q", bootstrapKey, fullKey)
	}
	assertRemoteRequestRawDigests(t, environment, bootstrapData, fullData)
	bootstrap, err := DecodeBootstrapShardRequest(bootstrapData)
	if err != nil {
		t.Fatalf("DecodeBootstrapShardRequest(%q) = %v", bootstrapKey, err)
	}
	full, err := DecodeShardRequest(fullData)
	if err != nil {
		t.Fatalf("DecodeShardRequest(%q) = %v", fullKey, err)
	}
	if err := ValidateBootstrapIdentity(bootstrap, full); err != nil {
		t.Fatalf("bootstrap/full identity for %q: %v", fullKey, err)
	}
	if environment[remoteCandidateGateSourceEnv] != full.CandidateGateSourceSHA256 ||
		environment[remoteCandidateGateToolEnv] != full.CandidateGateToolchainSHA256 {
		t.Fatalf(
			"candidate Gate identity env source=%q toolchain=%q request source=%q toolchain=%q",
			environment[remoteCandidateGateSourceEnv],
			environment[remoteCandidateGateToolEnv],
			full.CandidateGateSourceSHA256,
			full.CandidateGateToolchainSHA256,
		)
	}
	if environment[FullManifestDigestEnvironment] != full.ShardExecutionManifestDigest {
		t.Fatalf("full manifest digest env=%q request=%q", environment[FullManifestDigestEnvironment], full.ShardExecutionManifestDigest)
	}
}

func assertRemoteRequestRawDigests(t *testing.T, environment map[string]string, bootstrapData, fullData []byte) {
	t.Helper()
	bootstrapDigest := sha256.Sum256(bootstrapData)
	fullDigest := sha256.Sum256(fullData)
	if environment["SUPER_DOLPHIN_REMOTE_REQUEST_SHA256"] != hex.EncodeToString(bootstrapDigest[:]) ||
		environment[FullRequestSHA256Environment] != hex.EncodeToString(fullDigest[:]) {
		t.Fatalf("create request raw object digests drifted: env=%+v", environment)
	}
}
