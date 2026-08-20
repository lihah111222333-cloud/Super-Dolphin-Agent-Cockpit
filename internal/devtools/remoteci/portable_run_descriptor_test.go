package remoteci

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type portableRunGitResolverFixture struct {
	commitTrees  map[string]string
	manifest     []byte
	manifestMode string
	readRepo     string
	readTree     string
	readPath     string
}

func (fixture portableRunGitResolverFixture) ResolveCommitTree(_ context.Context, _ string, commit string) (string, error) {
	tree, ok := fixture.commitTrees[commit]
	if !ok {
		return "", errors.New("commit is not available")
	}
	return tree, nil
}

func (fixture *portableRunGitResolverFixture) ReadTreeBlob(_ context.Context, repositoryRoot, tree, repoRelativePath string) (GitTreeBlob, error) {
	fixture.readRepo = repositoryRoot
	fixture.readTree = tree
	fixture.readPath = repoRelativePath
	return GitTreeBlob{Mode: fixture.manifestMode, Data: append([]byte(nil), fixture.manifest...)}, nil
}

type portableRunFileIdentityFixture struct {
	identityByPath map[string]string
	digestByPath   map[string]string
}

func (fixture portableRunFileIdentityFixture) ReadFileIdentity(_ context.Context, path string) (string, error) {
	identity, ok := fixture.identityByPath[path]
	if !ok {
		return "", errors.New("file identity is not available")
	}
	return identity, nil
}

func (fixture portableRunFileIdentityFixture) ReadFileSHA256(_ context.Context, path string) (string, error) {
	digest, ok := fixture.digestByPath[path]
	if !ok {
		return "", errors.New("file SHA-256 is not available")
	}
	return digest, nil
}

func portableRunManifestFixture(t *testing.T) []byte {
	t.Helper()
	manifest := RepositoryWorkloadManifest{
		SchemaVersion:         PortableWorkloadManifestSchemaVersion,
		RepositoryID:          "repo-1",
		Modules:               []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/repo-1"}},
		ExcludedNestedModules: []string{},
		Workloads: []RepositoryWorkload{{
			ID: "go-test/example", Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".",
			Package: "./example", Name: "TestExample", Platform: RepositoryWorkloadPlatformLinuxAMD64,
			ExecutionPolicy: RepositoryWorkloadExecutionPolicyNormal,
		}},
	}
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func portableRunInputFixture(t *testing.T) (PortableRunInput, *portableRunGitResolverFixture, portableRunFileIdentityFixture) {
	t.Helper()
	engineRoot := filepath.Join(t.TempDir(), "engine")
	targetRoot := filepath.Join(t.TempDir(), "target")
	configPath := filepath.Join(t.TempDir(), "config.json")
	ledgerPath := filepath.Join(t.TempDir(), "ledger.sqlite")
	abiDigest, err := RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	manifest := portableRunManifestFixture(t)
	engineCommit := "1111111111111111111111111111111111111111"
	engineTree := "2222222222222222222222222222222222222222"
	targetCommit := "3333333333333333333333333333333333333333"
	targetTree := "4444444444444444444444444444444444444444"
	input := PortableRunInput{
		ExecutionProvider: PortableExecutionProvider,
		ContractID:        PortableRunContractID,
		Mode:              PortableRunModeValidateOnly,
		Engine: PortableRunEngine{
			RepositoryRoot:        engineRoot,
			CommitSHA:             engineCommit,
			TreeSHA:               engineTree,
			ProtocolABIDigest:     "sha256:9999999999999999999999999999999999999999999999999999999999999999",
			RuntimeArtifactSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ToolchainSHA256:       "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		Target: PortableRunTarget{
			RepositoryRoot: targetRoot,
			CommitSHA:      targetCommit,
			TreeSHA:        targetTree,
			BaseCommitSHA:  "5555555555555555555555555555555555555555",
			RepositoryID:   "repo-1",
			ObjectFormat:   "sha1",
			SourceBaseline: PortableSourceBaseline{
				ObjectFormat:      "sha1",
				BaselineTreeSHA:   "6666666666666666666666666666666666666666",
				BaselineCommitSHA: "7777777777777777777777777777777777777777",
				BundleContractID:  "git-bundle-thin/v2",
				ArtifactKind:      "source-baseline",
				ArtifactKey:       "baseline/repo-1/tree-6666",
				ArtifactSHA256:    "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
				ArtifactSizeBytes: 1024,
			},
		},
		WorkloadManifest: PortableWorkloadManifest{
			RepoRelativePath: "ci/workload-manifest.json",
			SHA256:           bytesDigest(manifest),
			ABIDigest:        abiDigest,
			SchemaVersion:    "portable-workload-manifest/v1",
			RepositoryID:     "repo-1",
		},
		Authority: PortableRunAuthority{
			ConfigPath:         configPath,
			ConfigSHA256:       "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			LedgerPath:         ledgerPath,
			LedgerFileIdentity: "dev:1:ino:2:size:3",
		},
	}
	resolver := &portableRunGitResolverFixture{manifest: manifest, manifestMode: "100644", commitTrees: map[string]string{
		engineCommit: engineTree,
		targetCommit: targetTree,
	}}
	reader := portableRunFileIdentityFixture{
		identityByPath: map[string]string{ledgerPath: input.Authority.LedgerFileIdentity},
		digestByPath:   map[string]string{configPath: input.Authority.ConfigSHA256},
	}
	return input, resolver, reader
}

func TestBindPortableRunBindsValidatedDescriptor(t *testing.T) {
	input, resolver, reader := portableRunInputFixture(t)
	bound, err := BindPortableRun(context.Background(), input, resolver, reader)
	if err != nil {
		t.Fatalf("BindPortableRun() error = %v", err)
	}
	if bound.BindingSHA256 == "" {
		t.Fatal("BindPortableRun() returned an empty binding hash")
	}
	if bound.Mode != PortableRunModeValidateOnly || bound.ContractID != PortableRunContractID {
		t.Fatalf("bound protocol identity = %#v", bound)
	}
	if bound.WorkloadManifest.ABIDigest != input.WorkloadManifest.ABIDigest {
		t.Fatalf("bound workload manifest ABI digest = %q, want %q", bound.WorkloadManifest.ABIDigest, input.WorkloadManifest.ABIDigest)
	}
	if resolver.readRepo != input.Target.RepositoryRoot || resolver.readTree != input.Target.TreeSHA || resolver.readPath != input.WorkloadManifest.RepoRelativePath {
		t.Fatalf("manifest read was not target-tree scoped: repo=%q tree=%q path=%q", resolver.readRepo, resolver.readTree, resolver.readPath)
	}
}

func TestBindPortableRunRejectsInvalidShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PortableRunInput)
	}{
		{name: "engine path is not clean", mutate: func(input *PortableRunInput) { input.Engine.RepositoryRoot += "/../engine" }},
		{name: "target path is relative", mutate: func(input *PortableRunInput) { input.Target.RepositoryRoot = "cwd" }},
		{name: "manifest escapes tree", mutate: func(input *PortableRunInput) { input.WorkloadManifest.RepoRelativePath = "../manifest.json" }},
		{name: "manifest uses host path", mutate: func(input *PortableRunInput) { input.WorkloadManifest.RepoRelativePath = "/tmp/manifest.json" }},
		{name: "engine digest is bare", mutate: func(input *PortableRunInput) {
			input.Engine.ToolchainSHA256 = strings.TrimPrefix(input.Engine.ToolchainSHA256, "sha256:")
		}},
		{name: "engine ABI digest is empty", mutate: func(input *PortableRunInput) { input.Engine.ProtocolABIDigest = "" }},
		{name: "target commit is HEAD", mutate: func(input *PortableRunInput) { input.Target.CommitSHA = "HEAD" }},
		{name: "target repository identity is empty", mutate: func(input *PortableRunInput) { input.Target.RepositoryID = "" }},
		{name: "baseline artifact key is empty", mutate: func(input *PortableRunInput) { input.Target.SourceBaseline.ArtifactKey = "" }},
		{name: "baseline artifact size is zero", mutate: func(input *PortableRunInput) { input.Target.SourceBaseline.ArtifactSizeBytes = 0 }},
		{name: "manifest ABI digest is caller-reported", mutate: func(input *PortableRunInput) { input.WorkloadManifest.ABIDigest = "sha256:" + strings.Repeat("f", 64) }},
		{name: "manifest ABI digest is missing", mutate: func(input *PortableRunInput) { input.WorkloadManifest.ABIDigest = "" }},
		{name: "provider is default", mutate: func(input *PortableRunInput) { input.ExecutionProvider = "default" }},
		{name: "mode is not validate only", mutate: func(input *PortableRunInput) { input.Mode = "execute" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, resolver, reader := portableRunInputFixture(t)
			test.mutate(&input)
			if _, err := BindPortableRun(context.Background(), input, resolver, reader); err == nil {
				t.Fatal("BindPortableRun() unexpectedly accepted invalid input")
			}
		})
	}
}

func TestBindPortableRunRejectsCrossBindingDrift(t *testing.T) {
	input, resolver, reader := portableRunInputFixture(t)
	resolver.commitTrees[input.Engine.CommitSHA] = "8888888888888888888888888888888888888888"
	if _, err := BindPortableRun(context.Background(), input, resolver, reader); err == nil {
		t.Fatal("BindPortableRun() accepted engine commit/tree drift")
	}

	input, resolver, reader = portableRunInputFixture(t)
	resolver.commitTrees[input.Target.CommitSHA] = "8888888888888888888888888888888888888888"
	if _, err := BindPortableRun(context.Background(), input, resolver, reader); err == nil {
		t.Fatal("BindPortableRun() accepted target commit/tree drift")
	}

	input, resolver, reader = portableRunInputFixture(t)
	input.WorkloadManifest.SHA256 = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if _, err := BindPortableRun(context.Background(), input, resolver, reader); err == nil {
		t.Fatal("BindPortableRun() accepted manifest digest drift")
	}

	input, resolver, reader = portableRunInputFixture(t)
	resolver.manifestMode = "120000"
	if _, err := BindPortableRun(context.Background(), input, resolver, reader); err == nil {
		t.Fatal("BindPortableRun() accepted a symlink manifest blob")
	}

	input, resolver, reader = portableRunInputFixture(t)
	reader.identityByPath[input.Authority.LedgerPath] = "dev:other"
	if _, err := BindPortableRun(context.Background(), input, resolver, reader); err == nil {
		t.Fatal("BindPortableRun() accepted ledger identity drift")
	}

	input, resolver, reader = portableRunInputFixture(t)
	reader.digestByPath[input.Authority.ConfigPath] = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := BindPortableRun(context.Background(), input, resolver, reader); err == nil {
		t.Fatal("BindPortableRun() accepted config digest drift")
	}
}

func TestBindPortableRunDoesNotFallbackToEngineTree(t *testing.T) {
	input, resolver, reader := portableRunInputFixture(t)
	delete(resolver.commitTrees, input.Target.CommitSHA)
	if _, err := BindPortableRun(context.Background(), input, resolver, reader); err == nil {
		t.Fatal("BindPortableRun() unexpectedly fell back to the engine tree")
	}
}

func TestBindPortableRunRejectsCanceledContext(t *testing.T) {
	input, resolver, reader := portableRunInputFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BindPortableRun(ctx, input, resolver, reader); err == nil {
		t.Fatal("BindPortableRun() unexpectedly accepted a canceled context")
	}
}

func TestDecodePortableRunDescriptorIsStrict(t *testing.T) {
	input, _, _ := portableRunInputFixture(t)
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePortableRunDescriptor(data); err != nil {
		t.Fatalf("DecodePortableRunDescriptor() error = %v", err)
	}
	for _, malformed := range [][]byte{
		append(append([]byte(nil), data[:len(data)-1]...), []byte(`,"unknown":true}`)...),
		append(append([]byte(nil), data[:len(data)-1]...), []byte(`,"contract_id":"portable-remote-ci-run/v1"}`)...),
		append(append([]byte(nil), data...), []byte(` {}`)...),
	} {
		if _, err := DecodePortableRunDescriptor(malformed); err == nil {
			t.Fatalf("DecodePortableRunDescriptor() accepted %q", malformed)
		}
	}
}

func TestPortableWorkloadManifestABIDigestFieldGuardRoundTrips(t *testing.T) {
	input, _, _ := portableRunInputFixture(t)
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	assertPortableWorkloadManifestDescriptorFields(t, root["workload_manifest"])
	decoded, err := DecodePortableRunDescriptor(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WorkloadManifest != input.WorkloadManifest {
		t.Fatalf("descriptor round trip changed workload manifest: got %#v want %#v", decoded.WorkloadManifest, input.WorkloadManifest)
	}
}

func assertPortableWorkloadManifestDescriptorFields(t *testing.T, data []byte) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeFor[PortableWorkloadManifest]()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name, options, hasOptions := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" || hasOptions || options != "" {
			t.Fatalf("%s.%s has an invalid JSON field tag", typ, field.Name)
		}
		if _, ok := object[name]; !ok {
			t.Fatalf("descriptor producer field %q has no JSON consumer coverage", name)
		}
	}
}
