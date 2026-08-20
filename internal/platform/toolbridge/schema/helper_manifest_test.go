package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func writeHelperPackageFixture(t *testing.T, dir, content string, identity HelperIdentity) (string, string) {
	t.Helper()
	helper := filepath.Join(dir, HelperFileName(identity.GOOS))
	image := []byte(content)
	if identity.GOOS == "windows" {
		image = append([]byte{'M', 'Z'}, image...)
	}
	if err := os.WriteFile(helper, image, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := helper + HelperManifestSuffix
	if err := WriteHelperManifest(helper, manifest, identity); err != nil {
		t.Fatal(err)
	}
	return helper, manifest
}

func requireSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink requires privilege on windows: %v", err)
		}
		t.Fatal(err)
	}
}

func TestHelperPackageRejectsMissingMixedAndTamperedArtifacts(t *testing.T) {
	identity := HelperIdentity{AppCommit: "commit-a", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{"missing manifest", func(t *testing.T, _, manifest string) { t.Helper(); _ = os.Remove(manifest) }},
		{"symlink manifest", func(t *testing.T, _, manifest string) {
			t.Helper()
			realManifest := manifest + ".real"
			if err := os.Rename(manifest, realManifest); err != nil {
				t.Fatal(err)
			}
			requireSymlink(t, realManifest, manifest)
		}},
		{"tampered helper", func(t *testing.T, helper, _ string) { t.Helper(); _ = os.WriteFile(helper, []byte("tampered"), 0o700) }},
		{"mixed identity", func(t *testing.T, helper, manifest string) {
			t.Helper()
			other := identity
			other.AppCommit = "commit-b"
			if err := WriteHelperManifest(helper, manifest, other); err != nil {
				t.Fatal(err)
			}
		}},
		{"protocol mismatch", func(t *testing.T, _, manifest string) {
			mutateHelperManifestField(t, manifest, "protocol", "wrong")
		}},
		{"Go version mismatch", func(t *testing.T, _, manifest string) { mutateHelperManifestField(t, manifest, "go_version", "go0.0") }},
		{"GOOS mismatch", func(t *testing.T, _, manifest string) { mutateHelperManifestField(t, manifest, "goos", "wrong") }},
		{"GOARCH mismatch", func(t *testing.T, _, manifest string) { mutateHelperManifestField(t, manifest, "goarch", "wrong") }},
		{"executable policy mismatch", func(t *testing.T, _, manifest string) {
			mutateHelperManifestField(t, manifest, "executable_policy", "wrong")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			helper, manifest := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", identity)
			test.mutate(t, helper, manifest)
			if err := VerifyHelperPackage(helper, manifest, identity); err == nil {
				t.Fatal("VerifyHelperPackage() error = nil")
			}
		})
	}
}

func TestWriteHelperManifestAtomicallyReplacesExistingDocument(t *testing.T) {
	identity := HelperIdentity{AppCommit: "commit-a", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	helper, manifest := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", identity)
	updated := identity
	updated.AppCommit = "commit-b"
	if err := WriteHelperManifest(helper, manifest, updated); err != nil {
		t.Fatalf("rewrite helper manifest: %v", err)
	}
	if err := VerifyHelperPackage(helper, manifest, updated); err != nil {
		t.Fatalf("verify rewritten helper manifest: %v", err)
	}
	if _, err := os.Lstat(manifest + filesystemSnapshotPublishSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest publishing file = %v, want not exist", err)
	}
}

func TestWriteHelperManifestRejectsPartialPublish(t *testing.T) {
	identity := HelperIdentity{AppCommit: "commit-a", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	helper, manifest := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", identity)
	if err := os.WriteFile(manifest+filesystemSnapshotPublishSuffix, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteHelperManifest(helper, manifest, identity); err == nil {
		t.Fatal("WriteHelperManifest() error = nil with partial publish")
	}
}

func mutateHelperManifestField(t *testing.T, manifest, field string, value any) {
	t.Helper()
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document[field] = value
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

type helperManifestGenerationCoverage struct {
	expected func(helperManifestGuardFixture) string
}

type helperManifestValidationCoverage struct{}

type helperIdentityFieldCoverage struct {
	manifestField string
}

type helperManifestGuardFixture struct {
	helper   string
	image    []byte
	identity HelperIdentity
}

func helperManifestGenerationRegistry() map[string]helperManifestGenerationCoverage {
	return map[string]helperManifestGenerationCoverage{
		"protocol": {expected: func(helperManifestGuardFixture) string { return ProtocolID }},
		"helper":   {expected: func(f helperManifestGuardFixture) string { return filepath.Base(f.helper) }},
		"sha256": {expected: func(f helperManifestGuardFixture) string {
			sum := sha256.Sum256(f.image)
			return hex.EncodeToString(sum[:])
		}},
		"executable_policy": {expected: func(f helperManifestGuardFixture) string { return executablePolicy(f.identity.GOOS) }},
		"app_commit":        {expected: func(f helperManifestGuardFixture) string { return f.identity.AppCommit }},
		"go_version":        {expected: func(f helperManifestGuardFixture) string { return f.identity.GoVersion }},
		"goos":              {expected: func(f helperManifestGuardFixture) string { return f.identity.GOOS }},
		"goarch":            {expected: func(f helperManifestGuardFixture) string { return f.identity.GOARCH }},
	}
}

func helperManifestValidationRegistry() map[string]helperManifestValidationCoverage {
	return map[string]helperManifestValidationCoverage{
		"protocol": {}, "helper": {}, "sha256": {}, "executable_policy": {},
		"app_commit": {}, "go_version": {}, "goos": {}, "goarch": {},
	}
}

func helperIdentityCoverageRegistry() map[string]helperIdentityFieldCoverage {
	return map[string]helperIdentityFieldCoverage{
		"AppCommit": {manifestField: "app_commit"},
		"GoVersion": {manifestField: "go_version"},
		"GOOS":      {manifestField: "goos"},
		"GOARCH":    {manifestField: "goarch"},
	}
}

func TestHelperManifestFieldGuard(t *testing.T) {
	manifestFields := mustProducerFields(t, reflect.TypeFor[HelperManifest](), "HelperManifest", "enumeration", true)
	generationCoverage := helperManifestGenerationRegistry()
	if err := compareFieldCoverage("HelperManifest", "generation", manifestFields, coverageKeys(generationCoverage)); err != nil {
		t.Fatal(err)
	}
	validationCoverage := helperManifestValidationRegistry()
	if err := compareFieldCoverage("HelperManifest", "validation", manifestFields, coverageKeys(validationCoverage)); err != nil {
		t.Fatal(err)
	}

	identityFields := mustProducerFields(t, reflect.TypeFor[HelperIdentity](), "HelperIdentity", "enumeration", false)
	identityCoverage := helperIdentityCoverageRegistry()
	if err := compareFieldCoverage("HelperIdentity", "mapping", identityFields, coverageKeys(identityCoverage)); err != nil {
		t.Fatal(err)
	}
	if err := validateIdentityMappingTargets(identityCoverage, manifestFields); err != nil {
		t.Fatal(err)
	}

	verifyManifestGenerationCoverage(t, generationCoverage)
	verifyManifestValidationCoverage(t, validationCoverage)
	verifyIdentityMappingCoverage(t, identityCoverage)
}

func TestHelperManifestFieldGuardRejectsSyntheticFutureFieldMutation(t *testing.T) {
	type helperManifestFutureMutation struct {
		HelperManifest
		FutureField string `json:"future_field"`
	}
	type helperIdentityFutureMutation struct {
		HelperIdentity
		FutureField string
	}

	manifestFields := mustProducerFields(t, reflect.TypeFor[helperManifestFutureMutation](), "HelperManifest", "enumeration", true)
	for stage, coverage := range map[string]map[string]struct{}{
		"generation": coverageKeys(helperManifestGenerationRegistry()),
		"validation": coverageKeys(helperManifestValidationRegistry()),
	} {
		err := compareFieldCoverage("HelperManifest", stage, manifestFields, coverage)
		assertFieldGuardError(t, err, "producer=HelperManifest", "stage="+stage, "field=future_field")
	}

	identityFields := mustProducerFields(t, reflect.TypeFor[helperIdentityFutureMutation](), "HelperIdentity", "enumeration", false)
	err := compareFieldCoverage("HelperIdentity", "mapping", identityFields, coverageKeys(helperIdentityCoverageRegistry()))
	assertFieldGuardError(t, err, "producer=HelperIdentity", "stage=mapping", "field=FutureField")
}

func TestHelperManifestFieldGuardRejectsStaleCoverage(t *testing.T) {
	fields := mustProducerFields(t, reflect.TypeFor[HelperManifest](), "HelperManifest", "enumeration", true)
	coverage := coverageKeys(helperManifestValidationRegistry())
	coverage["stale_field"] = struct{}{}
	err := compareFieldCoverage("HelperManifest", "validation", fields, coverage)
	assertFieldGuardError(t, err, "producer=HelperManifest", "stage=validation", "field=stale_field", "stale coverage")
}

func verifyManifestGenerationCoverage(t *testing.T, coverage map[string]helperManifestGenerationCoverage) {
	t.Helper()
	identity := helperManifestGuardIdentity()
	helper, manifest := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", identity)
	image, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	document := readManifestDocument(t, manifest)
	fixture := helperManifestGuardFixture{helper: helper, image: image, identity: identity}
	for field, fieldCoverage := range coverage {
		got, ok := document[field].(string)
		if !ok || got != fieldCoverage.expected(fixture) {
			t.Fatalf("field guard producer=HelperManifest stage=generation field=%s got=%v", field, document[field])
		}
	}
}

func verifyManifestValidationCoverage(t *testing.T, coverage map[string]helperManifestValidationCoverage) {
	t.Helper()
	identity := helperManifestGuardIdentity()
	for field := range coverage {
		t.Run("validation_"+field, func(t *testing.T) {
			helper, manifest := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", identity)
			mutateHelperManifestField(t, manifest, field, "field-guard-invalid")
			if err := VerifyHelperPackage(helper, manifest, identity); err == nil {
				t.Fatalf("field guard producer=HelperManifest stage=validation field=%s was not rejected", field)
			}
		})
	}
}

func verifyIdentityMappingCoverage(t *testing.T, coverage map[string]helperIdentityFieldCoverage) {
	t.Helper()
	base := helperManifestGuardIdentity()
	for identityField, fieldCoverage := range coverage {
		t.Run("mapping_"+identityField, func(t *testing.T) {
			if runtime.GOOS == "windows" && identityField == "GOOS" {
				t.Skip("GOOS mutation exercises non-windows executable policy on windows")
			}
			identity := base
			marker := "field-guard-" + strings.ToLower(identityField)
			reflect.ValueOf(&identity).Elem().FieldByName(identityField).SetString(marker)
			_, manifest := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", identity)
			if got := readManifestDocument(t, manifest)[fieldCoverage.manifestField]; got != marker {
				t.Fatalf("field guard producer=HelperIdentity stage=mapping field=%s target=%s got=%v", identityField, fieldCoverage.manifestField, got)
			}
		})

		t.Run("required_"+identityField, func(t *testing.T) {
			helper, _ := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", base)
			identity := base
			reflect.ValueOf(&identity).Elem().FieldByName(identityField).SetString("")
			if err := WriteHelperManifest(helper, filepath.Join(t.TempDir(), "manifest.json"), identity); err == nil {
				t.Fatalf("field guard producer=HelperIdentity stage=validation field=%s was not rejected", identityField)
			}
		})

		t.Run("expected_"+identityField, func(t *testing.T) {
			helper, manifest := writeHelperPackageFixture(t, t.TempDir(), "#!/bin/sh\nexit 0\n", base)
			expected := base
			reflect.ValueOf(&expected).Elem().FieldByName(identityField).SetString("field-guard-mismatch")
			if err := VerifyHelperPackage(helper, manifest, expected); err == nil {
				t.Fatalf("field guard producer=HelperIdentity stage=validation field=%s was not rejected", identityField)
			}
		})
	}
}

func helperManifestGuardIdentity() HelperIdentity {
	return HelperIdentity{AppCommit: "field-guard-commit", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func readManifestDocument(t *testing.T, manifest string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func mustProducerFields(t *testing.T, producerType reflect.Type, producer, stage string, useJSONTags bool) map[string]struct{} {
	t.Helper()
	fields, err := producerFields(producerType, producer, stage, useJSONTags)
	if err != nil {
		t.Fatal(err)
	}
	return fields
}

func producerFields(producerType reflect.Type, producer, stage string, useJSONTags bool) (map[string]struct{}, error) {
	fields := make(map[string]struct{})
	for i := 0; i < producerType.NumField(); i++ {
		field := producerType.Field(i)
		if field.Anonymous {
			embedded, err := producerFields(field.Type, producer, stage, useJSONTags)
			if err != nil {
				return nil, err
			}
			for name := range embedded {
				fields[name] = struct{}{}
			}
			continue
		}
		if !field.IsExported() {
			continue
		}
		name := field.Name
		if useJSONTags {
			name = strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				return nil, fmt.Errorf("field guard producer=%s stage=%s field=%s missing JSON identity", producer, stage, field.Name)
			}
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("field guard producer=%s stage=%s field=%s duplicate identity", producer, stage, name)
		}
		fields[name] = struct{}{}
	}
	return fields, nil
}

func compareFieldCoverage(producer, stage string, fields, coverage map[string]struct{}) error {
	for _, field := range sortedFieldDifference(fields, coverage) {
		return fmt.Errorf("field guard producer=%s stage=%s field=%s missing coverage", producer, stage, field)
	}
	for _, field := range sortedFieldDifference(coverage, fields) {
		return fmt.Errorf("field guard producer=%s stage=%s field=%s stale coverage", producer, stage, field)
	}
	return nil
}

func validateIdentityMappingTargets(coverage map[string]helperIdentityFieldCoverage, manifestFields map[string]struct{}) error {
	for identityField, fieldCoverage := range coverage {
		if _, ok := manifestFields[fieldCoverage.manifestField]; !ok {
			return fmt.Errorf("field guard producer=HelperIdentity stage=mapping field=%s stale target=%s", identityField, fieldCoverage.manifestField)
		}
	}
	return nil
}

func coverageKeys[T any](coverage map[string]T) map[string]struct{} {
	keys := make(map[string]struct{}, len(coverage))
	for field := range coverage {
		keys[field] = struct{}{}
	}
	return keys
}

func sortedFieldDifference(left, right map[string]struct{}) []string {
	var difference []string
	for field := range left {
		if _, ok := right[field]; !ok {
			difference = append(difference, field)
		}
	}
	sort.Strings(difference)
	return difference
}

func assertFieldGuardError(t *testing.T, err error, evidence ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("field guard mutation unexpectedly passed")
	}
	for _, part := range evidence {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("field guard error %q missing evidence %q", err, part)
		}
	}
}
