package localci

import (
	"context"
	"strings"
	"testing"
)

type registryPushRunnerFake struct {
	request     BuildKitBuildRequest
	destination RegistryPushRequest
	result      BuildKitResult
}

func (fake *registryPushRunnerFake) Build(context.Context, BuildKitBuildRequest) (BuildKitResult, error) {
	return BuildKitResult{}, nil
}

func (fake *registryPushRunnerFake) BuildAndPush(_ context.Context, request BuildKitBuildRequest, destination RegistryPushRequest) (BuildKitResult, error) {
	fake.request, fake.destination = request, destination
	return fake.result, nil
}

func TestImageBuilderEnsureCandidatePushedUsesRegistryRunner(t *testing.T) {
	runner := &registryPushRunnerFake{result: BuildKitResult{PlatformManifestDigest: digest("1"), ConfigDigest: digest("2")}}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	request := candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e"))
	result, err := builder.EnsureCandidatePushed(context.Background(), request, RegistryPushRequest{
		Repository: "registry.example.com/super-dolphin/baseline",
		Credential: RegistryCredential{Server: "registry.example.com", Username: "request-user", Password: "request-password"},
	})
	if err != nil {
		t.Fatalf("EnsureCandidatePushed() error = %v", err)
	}
	if !result.Built || result.ImageDigest != runner.result.PlatformManifestDigest || runner.request.SourceTreeSHA != request.SourceTreeSHA || runner.destination.Repository != "registry.example.com/super-dolphin/baseline" {
		t.Fatalf("registry candidate result/request = %#v / %#v", result, runner)
	}
}

func TestImageBuilderEnsureCandidatePushedRejectsNonPushingRunner(t *testing.T) {
	builder, err := NewImageBuilder(&recordingBuildKitRunner{digest: digest("1")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = builder.EnsureCandidatePushed(context.Background(), candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e")), RegistryPushRequest{})
	if err == nil || !strings.Contains(err.Error(), "does not support registry push") {
		t.Fatalf("EnsureCandidatePushed() error = %v, want missing push capability", err)
	}
}

func TestDockerBuildxPushCommandUsesRegistryOutputNotLocalDockerOutput(t *testing.T) {
	request := validBuildxRequest(t)
	runner := &DockerBuildxRunner{}
	arguments := runner.commandArgs(request, "/private/metadata.json", "docker.io/local:ignored", false, "controlled", &RegistryPushRequest{
		Repository: "registry.example.com/super-dolphin/baseline",
		Credential: RegistryCredential{Server: "registry.example.com", Username: "request-user", Password: "request-password"},
	})
	joined := strings.Join(arguments, "\n")
	if !strings.Contains(joined, "--output=type=image,push=true,oci-mediatypes=true") || strings.Contains(joined, "--output=type=docker") || !strings.Contains(joined, "--tag=registry.example.com/super-dolphin/baseline:baseline-") {
		t.Fatalf("registry push command = %v", arguments)
	}
}

func TestValidateRegistryPushRequestRejectsAmbientOrMismatchedAuth(t *testing.T) {
	err := validateRegistryPushRequest(RegistryPushRequest{Repository: "registry.example.com/super-dolphin/baseline", Credential: RegistryCredential{Server: "other.example.com", Username: "user", Password: "password"}})
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("validateRegistryPushRequest() error = %v, want request-scoped credential rejection", err)
	}
}

func TestCandidateInputDigestBindsPriorImmutableRuntimeReference(t *testing.T) {
	first := candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e"))
	first.AcceptedImageReference = "registry.example.com/super-dolphin/baseline@" + digest("a")
	second := first
	second.AcceptedImageReference = "registry.example.com/super-dolphin/baseline@" + digest("b")
	preparedFirst, err := prepareCandidate(first)
	if err != nil {
		t.Fatal(err)
	}
	preparedSecond, err := prepareCandidate(second)
	if err != nil {
		t.Fatal(err)
	}
	if preparedFirst.result.InputDigest == preparedSecond.result.InputDigest {
		t.Fatal("prior immutable runtime reference did not bind candidate input digest")
	}
}

func TestBuildxAcceptedRuntimeSkipsLocalRuntimeDepsContext(t *testing.T) {
	request := validBuildxRequest(t)
	for index := range request.BuildArguments {
		if request.BuildArguments[index].Name == "RUNTIME_DEPS_IMAGE" {
			request.BuildArguments[index].Value = "registry.example.com/super-dolphin/baseline@" + digest("a")
		}
	}
	if usesPreparedRuntimeDeps(request) {
		t.Fatal("accepted immutable runtime image selected local runtime-deps build")
	}
	arguments := (&DockerBuildxRunner{}).commandArgs(request, "/private/metadata.json", "docker.io/local:ignored", false, "controlled")
	if strings.Contains(strings.Join(arguments, "\n"), "context:runtime-deps") {
		t.Fatalf("accepted runtime command still requires local runtime-deps context: %v", arguments)
	}
}
