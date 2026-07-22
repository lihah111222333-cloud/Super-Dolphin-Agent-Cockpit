package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestReleaseGrantOptionsCanonicalizeAssetsAndRejectDuplicateNames(t *testing.T) {
	remaining, options, err := extractReleaseGrantOptions([]string{
		"--wait", "--release-repository", "super-dolphin/releases", "--release-tag", "v1.2.3",
		"--release-grant-output", filepath.Join(t.TempDir(), "grant.json"),
		"--release-asset", "z.exe|sha256:" + strings.Repeat("b", 64) + "|2",
		"--release-asset", "a.json|sha256:" + strings.Repeat("a", 64) + "|1",
	}, true)
	if err != nil || options == nil || len(remaining) != 1 || remaining[0] != "--wait" {
		t.Fatalf("extract release grant options remaining=%v options=%#v error=%v", remaining, options, err)
	}
	if options.Assets[0].Name != "a.json" || options.Assets[1].Name != "z.exe" {
		t.Fatalf("release assets were not canonicalized: %#v", options.Assets)
	}
	_, _, err = extractReleaseGrantOptions([]string{
		"--release-repository", "super-dolphin/releases", "--release-tag", "v1.2.3",
		"--release-grant-output", filepath.Join(t.TempDir(), "grant.json"),
		"--release-asset", "same.bin|sha256:" + strings.Repeat("a", 64) + "|1",
		"--release-asset", "same.bin|sha256:" + strings.Repeat("b", 64) + "|2",
	}, true)
	if err == nil || !strings.Contains(err.Error(), "duplicate asset") {
		t.Fatalf("duplicate release asset error = %v", err)
	}
}

func TestReleaseActionGrantExpectationRejectsParameterDrift(t *testing.T) {
	request := gatecontract.GrantRequest{
		Audience: gatecontract.ActionAudienceRelease, RepoID: "repo", InvocationID: "invocation", SourceTreeSHA: strings.Repeat("3", 40),
		Generation: 1, ReleaseRepository: "super-dolphin/releases", ReleaseTag: "v1.2.3", ReleaseCommitSHA: strings.Repeat("2", 40),
		ReleaseAssets:   []gatecontract.ReleaseAsset{{Name: "app.dmg", SHA256: "sha256:" + strings.Repeat("a", 64), Size: 1}},
		ActionAttemptID: actionGrantAttemptID("a"),
	}
	expected := actionGrantExpectation{
		Audience: request.Audience, RepoID: request.RepoID, InvocationID: request.InvocationID, SourceTreeSHA: request.SourceTreeSHA,
		Generation: request.Generation, ReleaseRepository: request.ReleaseRepository, ReleaseTag: request.ReleaseTag,
		ReleaseCommitSHA: request.ReleaseCommitSHA, ReleaseAssets: append([]gatecontract.ReleaseAsset(nil), request.ReleaseAssets...),
		ActionAttemptID: request.ActionAttemptID,
	}
	if !actionGrantMatchesExpectation(request, expected) {
		t.Fatal("exact release action binding was rejected")
	}
	for _, mutate := range []func(*actionGrantExpectation){
		func(value *actionGrantExpectation) { value.ReleaseTag = "v1.2.4" },
		func(value *actionGrantExpectation) { value.SourceTreeSHA = strings.Repeat("4", 40) },
		func(value *actionGrantExpectation) {
			value.ReleaseAssets[0].SHA256 = "sha256:" + strings.Repeat("b", 64)
		},
		func(value *actionGrantExpectation) { value.ReleaseAssets[0].Size++ },
	} {
		candidate := expected
		candidate.ReleaseAssets = append([]gatecontract.ReleaseAsset(nil), expected.ReleaseAssets...)
		mutate(&candidate)
		if actionGrantMatchesExpectation(request, candidate) {
			t.Fatal("release action binding accepted drift")
		}
	}
}

type replayingReleaseGrantRuntime struct {
	consumes int
}

func (*replayingReleaseGrantRuntime) Verify(context.Context, gatecontract.ActionGrant, actionGrantExpectation) error {
	return nil
}

func (runtime *replayingReleaseGrantRuntime) Consume(_ context.Context, grant gatecontract.ActionGrant, expected actionGrantExpectation) (gatecontract.ActionGrant, error) {
	runtime.consumes++
	if runtime.consumes != 1 {
		return gatecontract.ActionGrant{}, errors.New("grant is consumed")
	}
	if expected.ReleaseTag != grant.Request.ReleaseTag || !actionGrantMatchesExpectation(grant.Request, expected) {
		return gatecontract.ActionGrant{}, errors.New("release grant binding drift")
	}
	return grant, nil
}

func (*replayingReleaseGrantRuntime) Revoke(context.Context, string) (gatecontract.ActionGrant, error) {
	return gatecontract.ActionGrant{}, nil
}

func (*replayingReleaseGrantRuntime) Expire(context.Context, string) (gatecontract.ActionGrant, error) {
	return gatecontract.ActionGrant{}, nil
}

func (*replayingReleaseGrantRuntime) Close() error { return nil }

func TestGrantConsumeReleaseRejectsReplay(t *testing.T) {
	fixture := newActionGrantTestFixture(t)
	grant, err := fixture.service.Issue(context.Background(), actionGrantIntent{
		Receipt: fixture.receipt, InvocationOwner: fixture.submit.Invocation.Owner, Audience: gatecontract.ActionAudienceRelease,
		ActionPolicy: releaseActionPolicy, ReleaseRepository: "super-dolphin/releases", ReleaseTag: "v1.2.3",
		ReleaseCommitSHA: strings.Repeat("2", 40), ReleaseAssets: []gatecontract.ReleaseAsset{{
			Name: "app.dmg", SHA256: "sha256:" + strings.Repeat("a", 64), Size: 1,
		}},
		ActionAttemptID: actionGrantAttemptID("a"), RequestNonce: "sha256:" + strings.Repeat("f", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "grant.json")
	data, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &replayingReleaseGrantRuntime{}
	connector := func(context.Context) (actionGrantRuntime, error) { return runtime, nil }
	args := []string{
		"--input", path, "--release-repository", "super-dolphin/releases", "--release-tag", "v1.2.3",
		"--commit", strings.Repeat("2", 40), "--tree", strings.Repeat("3", 40),
		"--release-asset", "app.dmg|sha256:" + strings.Repeat("a", 64) + "|1",
	}
	if err := runGrantConsumeRelease(args, &bytes.Buffer{}, connector); err != nil {
		t.Fatalf("first consume error = %v", err)
	}
	if err := runGrantConsumeRelease(args, &bytes.Buffer{}, connector); err == nil || !strings.Contains(err.Error(), "consumed") {
		t.Fatalf("replayed consume error = %v", err)
	}
}
