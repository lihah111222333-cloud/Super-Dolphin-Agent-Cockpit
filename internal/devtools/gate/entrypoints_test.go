package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCIEntrypointRegistryCapabilities(t *testing.T) {
	t.Parallel()

	want := []CIEntrypoint{
		newCIEntrypoint(CIEntrypointGitPreCommit, []SourceKind{SourceKindTree}, []Profile{ProfileLocalFast}, true, CIEntrypointOwnerManagedGitPreCommit, CIEntrypointAdapterGitPreCommit),
		newCIEntrypoint(CIEntrypointGitPrePush, []SourceKind{SourceKindRange}, []Profile{ProfilePush}, true, CIEntrypointOwnerManagedGitPrePush, CIEntrypointAdapterGitPrePush),
		newCIEntrypoint(CIEntrypointManualCLI, []SourceKind{SourceKindCommit, SourceKindTree, SourceKindRange}, []Profile{ProfileLocalFast, ProfilePush, ProfileRemoteRequired, ProfilePromotion, ProfileRelease}, false, CIEntrypointOwnerManualCLI, CIEntrypointAdapterManualCLI),
		newCIEntrypoint(CIEntrypointRelease, []SourceKind{SourceKindCommit}, []Profile{ProfileRelease}, true, CIEntrypointOwnerRelease, CIEntrypointAdapterRelease),
	}
	got := CIEntrypointRegistry()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CIEntrypointRegistry() = %#v, want %#v", got, want)
	}
	if err := validateCIEntrypointRegistry(got); err != nil {
		t.Fatalf("validateCIEntrypointRegistry() error = %v", err)
	}
	manual := got[2]
	if manual.Authoritative || !slices.Contains(manual.AllowedProfiles, ProfileRemoteRequired) || !slices.Contains(manual.AllowedProfiles, ProfileRelease) {
		t.Fatalf("manual CLI authority contract = %#v", manual)
	}
}

func TestCIEntrypointStableIDsAndExternalAuthorityOwners(t *testing.T) {
	t.Parallel()

	registry := CIEntrypointRegistry()
	gotIDs := make([]string, 0, len(registry))
	for _, entrypoint := range registry {
		gotIDs = append(gotIDs, string(entrypoint.ID))
	}
	wantIDs := []string{"git-pre-commit", "git-pre-push", "manual-cli", "release"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("CI entrypoint IDs = %v, want %v", gotIDs, wantIDs)
	}
	wantOwners := map[CIEntrypointID]string{
		CIEntrypointGitPreCommit: "managed-launcher/git-pre-commit",
		CIEntrypointGitPrePush:   "managed-launcher/git-pre-push",
		CIEntrypointRelease:      "external-release-authority",
	}
	for _, entrypoint := range registry {
		if want, ok := wantOwners[entrypoint.ID]; ok && string(entrypoint.Owner) != want {
			t.Fatalf("CI entrypoint %q owner = %q, want %q", entrypoint.ID, entrypoint.Owner, want)
		}
	}
}

func TestCIEntrypointRegistryDigestAndDeepCopy(t *testing.T) {
	t.Parallel()

	digest, err := testCIEntrypointRegistryDigest()
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("testCIEntrypointRegistryDigest() = %q, %v", digest, err)
	}
	registry := CIEntrypointRegistry()
	registry[0].AllowedSources[0] = SourceKindCommit
	registry[0].AllowedProfiles[0] = ProfileRelease
	registry[0].Owner = CIEntrypointOwnerRelease
	registry[0].Adapter = CIEntrypointAdapterRelease
	fresh := CIEntrypointRegistry()[0]
	if fresh.AllowedSources[0] != SourceKindTree || fresh.AllowedProfiles[0] != ProfileLocalFast || fresh.Owner != CIEntrypointOwnerManagedGitPreCommit || fresh.Adapter != CIEntrypointAdapterGitPreCommit {
		t.Fatalf("CIEntrypointRegistry() leaked canonical state: %#v", fresh)
	}
	freshDigest, err := testCIEntrypointRegistryDigest()
	if err != nil || freshDigest != digest {
		t.Fatalf("digest after caller mutation = %q, %v, want %q", freshDigest, err, digest)
	}
}

func TestCIEntrypointValidateRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	base := CIEntrypointRegistry()
	tests := []struct {
		name   string
		mutate func(CIEntrypoint) CIEntrypoint
	}{
		{name: "unknown id", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.ID = CIEntrypointID("unknown")
			return entrypoint
		}},
		{name: "empty sources", mutate: func(entrypoint CIEntrypoint) CIEntrypoint { entrypoint.AllowedSources = nil; return entrypoint }},
		{name: "unknown source", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.AllowedSources = []SourceKind{"unknown"}
			return entrypoint
		}},
		{name: "duplicate source", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.AllowedSources = []SourceKind{SourceKindTree, SourceKindTree}
			return entrypoint
		}},
		{name: "unordered sources", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.AllowedSources = []SourceKind{SourceKindRange, SourceKindCommit}
			return entrypoint
		}},
		{name: "empty profiles", mutate: func(entrypoint CIEntrypoint) CIEntrypoint { entrypoint.AllowedProfiles = nil; return entrypoint }},
		{name: "unknown profile", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.AllowedProfiles = []Profile{"unknown"}
			return entrypoint
		}},
		{name: "duplicate profile", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.AllowedProfiles = []Profile{ProfileLocalFast, ProfileLocalFast}
			return entrypoint
		}},
		{name: "unordered profiles", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.AllowedProfiles = []Profile{ProfileRelease, ProfileLocalFast}
			return entrypoint
		}},
		{name: "unknown owner", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.Authoritative = false
			entrypoint.Owner = CIEntrypointOwner("unknown")
			return entrypoint
		}},
		{name: "unknown adapter", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.Adapter = CIEntrypointAdapter("unknown")
			return entrypoint
		}},
		{name: "authoritative without trusted owner", mutate: func(entrypoint CIEntrypoint) CIEntrypoint {
			entrypoint.Owner = CIEntrypointOwnerManualCLI
			return entrypoint
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.mutate(base[0]).Validate(); err == nil {
				t.Fatal("invalid CI entrypoint passed validation")
			}
		})
	}
}

func TestAuthoritativeCIEntrypointRejectsUntrustedOwners(t *testing.T) {
	t.Parallel()

	untrusted := []CIEntrypointOwner{
		CIEntrypointOwnerRepositoryGitHooks,
		CIEntrypointOwnerManualCLI,
	}
	for _, owner := range untrusted {
		t.Run(string(owner), func(t *testing.T) {
			entrypoint := CIEntrypointRegistry()[0]
			entrypoint.Owner = owner
			if err := entrypoint.Validate(); err == nil {
				t.Fatalf("authoritative entrypoint accepted untrusted owner %q", owner)
			}
		})
	}
}

func TestValidateCIEntrypointRegistryRejectsMalformedCatalogs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func() []CIEntrypoint
	}{
		{name: "empty", build: func() []CIEntrypoint { return nil }},
		{name: "missing", build: func() []CIEntrypoint {
			registry := CIEntrypointRegistry()
			return registry[:len(registry)-1]
		}},
		{name: "duplicate id", build: func() []CIEntrypoint { registry := CIEntrypointRegistry(); registry[1] = registry[0]; return registry }},
		{name: "unordered", build: func() []CIEntrypoint {
			registry := CIEntrypointRegistry()
			registry[0], registry[1] = registry[1], registry[0]
			return registry
		}},
		{name: "duplicate owner", build: func() []CIEntrypoint {
			registry := CIEntrypointRegistry()
			registry[1].Owner = registry[0].Owner
			return registry
		}},
		{name: "duplicate adapter", build: func() []CIEntrypoint {
			registry := CIEntrypointRegistry()
			registry[1].Adapter = registry[0].Adapter
			return registry
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateCIEntrypointRegistry(test.build()); err == nil {
				t.Fatal("malformed CI entrypoint registry passed validation")
			}
		})
	}
}

func TestCIEntrypointJSONContractIsStrictAndFieldComplete(t *testing.T) {
	t.Parallel()

	producer, err := JSONFieldNames(reflect.TypeFor[CIEntrypoint]())
	if err != nil {
		t.Fatalf("JSONFieldNames() error = %v", err)
	}
	coverage := []string{"adapter", "allowed_profiles", "allowed_sources", "authoritative", "id", "owner"}
	missing, stale := FieldCoverageDiff(producer, coverage)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("CIEntrypoint JSON field coverage missing=%v stale=%v", missing, stale)
	}
	for _, entrypoint := range CIEntrypointRegistry() {
		encoded, err := json.Marshal(entrypoint)
		if err != nil {
			t.Fatalf("Marshal(%q) error = %v", entrypoint.ID, err)
		}
		var decoded CIEntrypoint
		if err := DecodeStrictJSON(encoded, &decoded); err != nil {
			t.Fatalf("DecodeStrictJSON(%q) error = %v", entrypoint.ID, err)
		}
		if !reflect.DeepEqual(decoded, entrypoint) {
			t.Fatalf("decoded entrypoint = %#v, want %#v", decoded, entrypoint)
		}
		assertCIEntrypointMissingFieldsFail(t, encoded, producer)
	}
	unknown := []byte(`{"id":"manual-cli","allowed_sources":["commit"],"allowed_profiles":["local-fast"],"authoritative":false,"owner":"gate-cli/manual","adapter":"cmd/super-dolphin-gate/manual","unknown":true}`)
	var decoded CIEntrypoint
	if err := DecodeStrictJSON(unknown, &decoded); err == nil {
		t.Fatal("unknown CI entrypoint JSON field passed strict decoding")
	}
}

func TestResolveCIEntrypointBindsCanonicalSourceAndProfile(t *testing.T) {
	entrypoint, err := ResolveCIEntrypoint(
		CIEntrypointGitPreCommit,
		SourceKindTree,
		ProfileLocalFast,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !entrypoint.Authoritative || entrypoint.ID != CIEntrypointGitPreCommit {
		t.Fatalf("entrypoint = %#v", entrypoint)
	}
	if _, err := ResolveCIEntrypoint(
		CIEntrypointGitPreCommit,
		SourceKindCommit,
		ProfileLocalFast,
	); err == nil {
		t.Fatal("ResolveCIEntrypoint() accepted commit source for git pre-commit")
	}
	if _, err := ResolveCIEntrypoint(
		CIEntrypointGitPrePush,
		SourceKindRange,
		ProfileLocalFast,
	); err == nil {
		t.Fatal("ResolveCIEntrypoint() accepted local-fast profile for git pre-push")
	}
}

func assertCIEntrypointMissingFieldsFail(t *testing.T, encoded []byte, fields []string) {
	t.Helper()
	for _, field := range fields {
		var document map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &document); err != nil {
			t.Fatal(err)
		}
		delete(document, field)
		missing, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		var decoded CIEntrypoint
		if err := DecodeStrictJSON(missing, &decoded); err == nil {
			t.Fatalf("missing required field %q passed validation", field)
		}
	}
}

// testCIEntrypointRegistryDigest 保留入口清单摘要的测试语义，不扩大生产 API。
func testCIEntrypointRegistryDigest() (string, error) {
	registry := CIEntrypointRegistry()
	if err := validateCIEntrypointRegistry(registry); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(registry)
	if err != nil {
		return "", fmt.Errorf("marshal CI entrypoint registry: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// validateCIEntrypointRegistry 保留固定清单的 fail-fast 测试覆盖，不作为生产表面。
func validateCIEntrypointRegistry(registry []CIEntrypoint) error {
	canonical := canonicalCIEntrypoints()
	if len(registry) == 0 {
		return errors.New("CI entrypoint registry is empty")
	}
	if len(registry) != len(canonical) {
		return fmt.Errorf("CI entrypoint registry has %d entries, want %d", len(registry), len(canonical))
	}
	seenIDs := make(map[CIEntrypointID]struct{}, len(registry))
	seenOwners := make(map[CIEntrypointOwner]struct{}, len(registry))
	seenAdapters := make(map[CIEntrypointAdapter]struct{}, len(registry))
	for index, entrypoint := range registry {
		if _, duplicate := seenIDs[entrypoint.ID]; duplicate {
			return fmt.Errorf("CI entrypoint registry repeats id %q", entrypoint.ID)
		}
		if entrypoint.ID != canonical[index].ID {
			return fmt.Errorf("CI entrypoint registry is not canonically ordered at index %d", index)
		}
		if _, duplicate := seenOwners[entrypoint.Owner]; duplicate {
			return fmt.Errorf("CI entrypoint registry repeats owner %q", entrypoint.Owner)
		}
		if _, duplicate := seenAdapters[entrypoint.Adapter]; duplicate {
			return fmt.Errorf("CI entrypoint registry repeats adapter %q", entrypoint.Adapter)
		}
		if err := entrypoint.Validate(); err != nil {
			return fmt.Errorf("CI entrypoint registry entry %d: %w", index, err)
		}
		seenIDs[entrypoint.ID] = struct{}{}
		seenOwners[entrypoint.Owner] = struct{}{}
		seenAdapters[entrypoint.Adapter] = struct{}{}
	}
	return nil
}
