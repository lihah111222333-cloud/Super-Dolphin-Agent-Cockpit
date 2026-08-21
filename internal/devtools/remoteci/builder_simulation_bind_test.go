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

type builderSimulationBinderResolverFixture struct {
	formatByRoot map[string]string
	treeByCommit map[string]string
	blob         GitTreeBlob
	formatRoots  []string
	commitRoots  []string
	blobRoots    []string
}

func (fixture *builderSimulationBinderResolverFixture) ResolveObjectFormat(_ context.Context, repositoryRoot string) (string, error) {
	fixture.formatRoots = append(fixture.formatRoots, repositoryRoot)
	format, ok := fixture.formatByRoot[repositoryRoot]
	if !ok {
		return "", errors.New("object format is unavailable")
	}
	return format, nil
}

func (fixture *builderSimulationBinderResolverFixture) ResolveCommitTree(_ context.Context, repositoryRoot, commitSHA string) (string, error) {
	fixture.commitRoots = append(fixture.commitRoots, repositoryRoot)
	tree, ok := fixture.treeByCommit[commitSHA]
	if !ok {
		return "", errors.New("commit tree is unavailable")
	}
	return tree, nil
}

func (fixture *builderSimulationBinderResolverFixture) ReadTreeBlob(_ context.Context, repositoryRoot, treeSHA, repoRelativePath string) (GitTreeBlob, error) {
	fixture.blobRoots = append(fixture.blobRoots, repositoryRoot+"\x00"+treeSHA+"\x00"+repoRelativePath)
	return GitTreeBlob{Mode: fixture.blob.Mode, Data: append([]byte(nil), fixture.blob.Data...)}, nil
}

func builderSimulationBinderFixture(t *testing.T) (BuilderSimulationRequest, *builderSimulationBinderResolverFixture) {
	t.Helper()
	request := validBuilderSimulationRequest(t)
	request.Engine.RepositoryRoot = filepath.Join(t.TempDir(), "engine")
	request.Target.RepositoryRoot = filepath.Join(t.TempDir(), "target")
	manifest := RepositoryWorkloadManifest{
		SchemaVersion:         PortableWorkloadManifestSchemaVersion,
		RepositoryID:          request.Target.RepositoryID,
		Modules:               []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/target"}},
		ExcludedNestedModules: []string{},
		Profiles:              []RepositoryWorkloadProfile{{Profile: "local-fast", RequiredWorkloadIDs: []string{"go-test/binder"}}},
		Workloads: []RepositoryWorkload{{
			ID: "go-test/binder", Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./binder",
			Name: "TestBinder", Platform: RepositoryWorkloadPlatformLinuxAMD64, Runtime: RepositoryWorkloadRuntimeUbuntuContainer,
			BootstrapEstimateMS: 1_000, CPUMilli: 1_000, MemoryBytes: 1 << 30,
			ExecutionPolicy: RepositoryWorkloadExecutionPolicyNormal,
		}},
	}
	manifestBytes, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	request.Target.WorkloadManifest.SHA256 = bytesDigest(manifestBytes)
	request.Target.WorkloadManifest.ABIDigest, err = RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &builderSimulationBinderResolverFixture{
		formatByRoot: map[string]string{request.Engine.RepositoryRoot: "sha1", request.Target.RepositoryRoot: "sha1"},
		treeByCommit: map[string]string{request.Engine.CommitSHA: request.Engine.TreeSHA, request.Target.CommitSHA: request.Target.TreeSHA},
		blob:         GitTreeBlob{Mode: "100644", Data: manifestBytes},
	}
	return request, resolver
}

func TestBindBuilderSimulationRequestBindsExactObjectsAndManifest(t *testing.T) {
	request, resolver := builderSimulationBinderFixture(t)
	bound, err := BindBuilderSimulationRequest(context.Background(), request, resolver)
	if err != nil {
		t.Fatalf("BindBuilderSimulationRequest() error = %v", err)
	}
	assertBuilderSimulationBoundDigests(t, bound, request)
	assertBuilderSimulationResolverCalls(t, resolver, request)
	encoded, err := json.Marshal(bound)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{request.Engine.RepositoryRoot, request.Target.RepositoryRoot, request.ECI.CredentialProfile} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("bound JSON leaked internal request material %q", secret)
		}
	}
}

func assertBuilderSimulationBoundDigests(t *testing.T, bound BoundBuilderSimulationRequest, request BuilderSimulationRequest) {
	t.Helper()
	if !reflect.DeepEqual(bound.Request, request) {
		t.Fatalf("bound request = %#v, want %#v", bound.Request, request)
	}
	for name, digest := range map[string]string{
		"request": requestDigestForTest(bound.Request), "engine": bound.EngineBindingDigest, "target": bound.TargetBindingDigest,
	} {
		if !validPortableDigest(digest) {
			t.Fatalf("%s digest = %q is not canonical", name, digest)
		}
	}
}

func assertBuilderSimulationResolverCalls(t *testing.T, resolver *builderSimulationBinderResolverFixture, request BuilderSimulationRequest) {
	t.Helper()
	if len(resolver.formatRoots) != 2 || resolver.formatRoots[0] != request.Engine.RepositoryRoot || resolver.formatRoots[1] != request.Target.RepositoryRoot {
		t.Fatalf("object format resolution roots = %#v", resolver.formatRoots)
	}
	if len(resolver.commitRoots) != 2 || resolver.commitRoots[0] != request.Engine.RepositoryRoot || resolver.commitRoots[1] != request.Target.RepositoryRoot {
		t.Fatalf("commit resolution roots = %#v", resolver.commitRoots)
	}
	wantBlobCall := request.Target.RepositoryRoot + "\x00" + request.Target.TreeSHA + "\x00" + request.Target.WorkloadManifest.RepoRelativePath
	if !reflect.DeepEqual(resolver.blobRoots, []string{wantBlobCall}) {
		t.Fatalf("manifest read calls = %#v, want %#v", resolver.blobRoots, []string{wantBlobCall})
	}
}

func requestDigestForTest(request BuilderSimulationRequest) string {
	encoded, err := request.CanonicalEncode()
	if err != nil {
		return ""
	}
	return repositoryFramedDigest(BuilderSimulationRequestDigestDomain, encoded)
}

func TestBindBuilderSimulationRequestRejectsWrongFormatTreeBlobSHARepositorySchemaABIAndContext(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(BuilderSimulationRequest, *builderSimulationBinderResolverFixture)
	}{
		{name: "engine object format", mutate: func(request BuilderSimulationRequest, resolver *builderSimulationBinderResolverFixture) {
			resolver.formatByRoot[request.Engine.RepositoryRoot] = "sha256"
		}},
		{name: "target object format", mutate: func(request BuilderSimulationRequest, resolver *builderSimulationBinderResolverFixture) {
			resolver.formatByRoot[request.Target.RepositoryRoot] = "sha256"
		}},
		{name: "engine commit tree", mutate: func(request BuilderSimulationRequest, resolver *builderSimulationBinderResolverFixture) {
			resolver.treeByCommit[request.Engine.CommitSHA] = strings.Repeat("8", 40)
		}},
		{name: "target commit tree", mutate: func(request BuilderSimulationRequest, resolver *builderSimulationBinderResolverFixture) {
			resolver.treeByCommit[request.Target.CommitSHA] = strings.Repeat("8", 40)
		}},
		{name: "blob mode", mutate: func(_ BuilderSimulationRequest, resolver *builderSimulationBinderResolverFixture) {
			resolver.blob.Mode = "120000"
		}},
		{name: "blob SHA", mutate: func(_ BuilderSimulationRequest, resolver *builderSimulationBinderResolverFixture) {
			resolver.blob.Data = []byte("not-the-bound-manifest")
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request, resolver := builderSimulationBinderFixture(t)
			test.mutate(request, resolver)
			if _, err := BindBuilderSimulationRequest(context.Background(), request, resolver); err == nil {
				t.Fatal("binder unexpectedly accepted invalid Git identity")
			}
		})
	}

	request, resolver := builderSimulationBinderFixture(t)
	manifest := RepositoryWorkloadManifest{
		SchemaVersion: PortableWorkloadManifestSchemaVersion, RepositoryID: "different-repository",
		Modules: []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/different"}}, ExcludedNestedModules: []string{},
		Profiles:  []RepositoryWorkloadProfile{{Profile: "local-fast", RequiredWorkloadIDs: []string{"go-test/different"}}},
		Workloads: []RepositoryWorkload{{ID: "go-test/different", Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./different", Name: "TestDifferent", Platform: RepositoryWorkloadPlatformLinuxAMD64, Runtime: RepositoryWorkloadRuntimeUbuntuContainer, BootstrapEstimateMS: 1_000, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: RepositoryWorkloadExecutionPolicyNormal}},
	}
	manifestBytes, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	request.Target.WorkloadManifest.SHA256 = bytesDigest(manifestBytes)
	resolver.blob.Data = manifestBytes
	if _, err := BindBuilderSimulationRequest(context.Background(), request, resolver); err == nil {
		t.Fatal("binder accepted a manifest with a different repository identity")
	}

	request, resolver = builderSimulationBinderFixture(t)
	request.Target.WorkloadManifest.ABIDigest = builderSimulationDigest("f")
	if _, err := BindBuilderSimulationRequest(context.Background(), request, resolver); err == nil {
		t.Fatal("binder accepted a caller-reported manifest ABI")
	}

	request, resolver = builderSimulationBinderFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BindBuilderSimulationRequest(ctx, request, resolver); err == nil {
		t.Fatal("binder accepted a canceled context")
	}
	var nilContext context.Context
	if _, err := BindBuilderSimulationRequest(nilContext, request, resolver); err == nil {
		t.Fatal("binder accepted a nil context")
	}
}

func TestBindBuilderSimulationRequestRejectsNilAndTypedNilResolver(t *testing.T) {
	request, _ := builderSimulationBinderFixture(t)
	if _, err := BindBuilderSimulationRequest(context.Background(), request, nil); err == nil {
		t.Fatal("binder accepted a nil resolver")
	}
	var typedNil *builderSimulationBinderResolverFixture
	if _, err := BindBuilderSimulationRequest(context.Background(), request, typedNil); err == nil {
		t.Fatal("binder accepted a typed-nil resolver")
	}

	request.SchemaVersion = "wrong-schema"
	if _, err := BindBuilderSimulationRequest(context.Background(), request, nil); err == nil {
		t.Fatal("binder did not validate request before resolver nil")
	}
}

func TestBindBuilderSimulationRequestDigestsExcludeHostRootsAndSeparateTargets(t *testing.T) {
	request, resolver := builderSimulationBinderFixture(t)
	bound, err := BindBuilderSimulationRequest(context.Background(), request, resolver)
	if err != nil {
		t.Fatal(err)
	}

	rootBound := bindBuilderSimulationChangedRoots(t, request, resolver.blob.Data)
	if bound.EngineBindingDigest != rootBound.EngineBindingDigest || bound.TargetBindingDigest != rootBound.TargetBindingDigest {
		t.Fatal("host repository roots changed a binding digest")
	}
	if bound.RequestDigest == rootBound.RequestDigest {
		t.Fatal("canonical full request digest ignored host roots")
	}

	targetBound := bindBuilderSimulationChangedTarget(t, request, resolver.blob.Data)
	if bound.EngineBindingDigest != targetBound.EngineBindingDigest {
		t.Fatal("target-only changes rebuilt the engine binding")
	}
	if bound.TargetBindingDigest == targetBound.TargetBindingDigest {
		t.Fatal("different target identity produced the same target binding")
	}
}

func bindBuilderSimulationChangedRoots(t *testing.T, request BuilderSimulationRequest, manifest []byte) BoundBuilderSimulationRequest {
	t.Helper()
	request.Engine.RepositoryRoot = filepath.Join(t.TempDir(), "other-engine")
	request.Target.RepositoryRoot = filepath.Join(t.TempDir(), "other-target")
	resolver := &builderSimulationBinderResolverFixture{
		formatByRoot: map[string]string{request.Engine.RepositoryRoot: "sha1", request.Target.RepositoryRoot: "sha1"},
		treeByCommit: map[string]string{request.Engine.CommitSHA: request.Engine.TreeSHA, request.Target.CommitSHA: request.Target.TreeSHA},
		blob:         GitTreeBlob{Mode: "100644", Data: manifest},
	}
	bound, err := BindBuilderSimulationRequest(context.Background(), request, resolver)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func bindBuilderSimulationChangedTarget(t *testing.T, request BuilderSimulationRequest, originalManifest []byte) BoundBuilderSimulationRequest {
	t.Helper()
	targetChanged := request
	targetChanged.Target.RepositoryID = "wjboot-v2"
	targetChanged.Target.CommitSHA = strings.Repeat("1", 40)
	targetChanged.Target.TreeSHA = strings.Repeat("2", 40)
	targetChanged.Target.WorkloadManifest.RepositoryID = targetChanged.Target.RepositoryID
	resolver := &builderSimulationBinderResolverFixture{
		formatByRoot: map[string]string{targetChanged.Engine.RepositoryRoot: "sha1", targetChanged.Target.RepositoryRoot: "sha1"},
		treeByCommit: map[string]string{targetChanged.Engine.CommitSHA: targetChanged.Engine.TreeSHA, targetChanged.Target.CommitSHA: targetChanged.Target.TreeSHA},
		blob:         GitTreeBlob{Mode: "100644", Data: originalManifest},
	}
	// Rebuild the target manifest with the changed repository identity so its strict wire identity remains valid.
	manifest := RepositoryWorkloadManifest{
		SchemaVersion: PortableWorkloadManifestSchemaVersion, RepositoryID: targetChanged.Target.RepositoryID,
		Modules: []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/wjboot-v2"}}, ExcludedNestedModules: []string{},
		Profiles:  []RepositoryWorkloadProfile{{Profile: "local-fast", RequiredWorkloadIDs: []string{"go-test/binder"}}},
		Workloads: []RepositoryWorkload{{ID: "go-test/binder", Kind: RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./binder", Name: "TestBinder", Platform: RepositoryWorkloadPlatformLinuxAMD64, Runtime: RepositoryWorkloadRuntimeUbuntuContainer, BootstrapEstimateMS: 1_000, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: RepositoryWorkloadExecutionPolicyNormal}},
	}
	manifestBytes, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	targetChanged.Target.WorkloadManifest.SHA256 = bytesDigest(manifestBytes)
	targetChanged.Target.WorkloadManifest.ABIDigest, err = RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	resolver.blob.Data = manifestBytes
	bound, err := BindBuilderSimulationRequest(context.Background(), targetChanged, resolver)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

var _ BuilderSimulationGitResolver = (*builderSimulationBinderResolverFixture)(nil)
