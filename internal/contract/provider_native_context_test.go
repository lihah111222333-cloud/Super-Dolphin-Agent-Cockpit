package contract

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func validProviderNativeContext(t *testing.T) ProviderNativeContext {
	t.Helper()
	cwd := canonicalProviderNativeTestDir(t)
	return ProviderNativeContext{
		SchemaVersion:   ProviderNativeContextSchemaVersion,
		Provider:        ProviderNativeProviderClaude,
		ProviderVersion: "2.1.237",
		CWD:             cwd,
		ProviderHome:    filepath.Join(cwd, "provider-home"),
		Profile:         "default",
		Capabilities: ProviderNativeCapabilities{
			Memory: ProviderNativeFeatureCapability{
				Owner:    ProviderNativeOwnerProvider,
				Status:   ProviderNativeStatusSupported,
				Version:  "2.1.237",
				Evidence: "probe:memory:v1",
			},
			Skills: ProviderNativeFeatureCapability{
				Owner:    ProviderNativeOwnerProvider,
				Status:   ProviderNativeStatusSupported,
				Version:  "2.1.237",
				Evidence: "probe:skills:v1",
			},
		},
	}
}

func canonicalProviderNativeTestDir(t *testing.T) string {
	t.Helper()
	// macOS exposes /tmp through the /private/tmp symlink. Keep test fixtures
	// in the same canonical namespace as production CWD validation without
	// making the expected value depend on the implementation under test.
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks(temp dir) error = %v", err)
	}
	return filepath.Clean(path)
}

func TestProviderNativeFeaturesReflectJSONFields(t *testing.T) {
	features := ProviderNativeFeatures()
	want := []ProviderNativeFeature{
		ProviderNativeFeatureMemory,
		ProviderNativeFeatureSkills,
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if !reflect.DeepEqual(features, want) {
		t.Fatalf("ProviderNativeFeatures() = %#v, want %#v", features, want)
	}
	caps := validProviderNativeContext(t).Capabilities
	for _, feature := range features {
		if _, err := caps.Capability(feature); err != nil {
			t.Fatalf("Capability(%q) error = %v", feature, err)
		}
	}
}

func TestCanonicalProviderNativeProviderWhitelist(t *testing.T) {
	for _, raw := range []string{"claude", " CLAUDE ", "codex"} {
		got, err := CanonicalProviderNativeProvider(raw)
		if err != nil {
			t.Fatalf("CanonicalProviderNativeProvider(%q) error = %v", raw, err)
		}
		if !IsProviderNativeProvider(got) {
			t.Fatalf("IsProviderNativeProvider(%q) = false", got)
		}
	}
	if _, err := CanonicalProviderNativeProvider("arbitrary"); !errors.Is(err, ErrProviderNativeContextInvalid) {
		t.Fatalf("unknown provider error = %v, want context invalid", err)
	}
}

// TestProviderNativeContextValidateIdentity 验证 context 的 provider、cwd 与元数据边界。
func TestProviderNativeContextValidateIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProviderNativeContext)
	}{
		{name: "zero schema version", mutate: func(ctx *ProviderNativeContext) { ctx.SchemaVersion = 0 }},
		{name: "missing provider", mutate: func(ctx *ProviderNativeContext) { ctx.Provider = "" }},
		{name: "unknown provider", mutate: func(ctx *ProviderNativeContext) { ctx.Provider = "arbitrary" }},
		{name: "missing cwd", mutate: func(ctx *ProviderNativeContext) { ctx.CWD = "" }},
		{
			name: "provider home must be absolute and clean",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.ProviderHome = "relative/home"
			},
		},
		{
			name: "profile must be a stable code",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Profile = "profile with spaces"
			},
		},
		{
			name: "provider version must not carry a path",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.ProviderVersion = "/private/provider/version"
			},
		},
		{
			name: "whitespace provider version is not omission",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.ProviderVersion = " "
			},
		},
		{
			name: "whitespace profile is not omission",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Profile = "\t"
			},
		},
		{
			name: "whitespace provider home is not omission",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.ProviderHome = "\n"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertProviderNativeContextInvalid(t, test.mutate)
		})
	}
}

// TestProviderNativeContextValidateCapabilityStates 验证 capability owner/status 的 fail-fast 组合。
func TestProviderNativeContextValidateCapabilityStates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProviderNativeContext)
	}{
		{
			name: "unsupported memory without reason",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Status = ProviderNativeStatusUnsupported
			},
		},
		{
			name: "supported capability requires evidence",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Evidence = ""
			},
		},
		{
			name: "supported capability requires provider owner",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Owner = ProviderNativeOwnerLegacyV3
			},
		},
		{
			name: "legacy owner is invalid for unknown capability",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Owner = ProviderNativeOwnerLegacyV3
				ctx.Capabilities.Memory.Status = ProviderNativeStatusUnknown
				ctx.Capabilities.Memory.Reason = "capability_probe_required"
			},
		},
		{
			name: "legacy owner is invalid for unsupported capability",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Owner = ProviderNativeOwnerLegacyV3
				ctx.Capabilities.Memory.Status = ProviderNativeStatusUnsupported
				ctx.Capabilities.Memory.Reason = "provider_missing_feature"
			},
		},
		{
			name: "disabled owner with supported status",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Skills.Owner = ProviderNativeOwnerDisabled
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertProviderNativeContextInvalid(t, test.mutate)
		})
	}
}

// TestProviderNativeContextValidateCapabilityMetadata 验证 capability metadata 的 token 边界。
func TestProviderNativeContextValidateCapabilityMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProviderNativeContext)
	}{
		{
			name: "reason must be a stable code",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Status = ProviderNativeStatusUnknown
				ctx.Capabilities.Memory.Reason = "/private/path/secret"
			},
		},
		{
			name: "unknown evidence must be a stable token",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Status = ProviderNativeStatusUnknown
				ctx.Capabilities.Memory.Reason = "probe_unavailable"
				ctx.Capabilities.Memory.Evidence = "/private/provider token"
			},
		},
		{
			name: "unsupported version must be a stable token",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Status = ProviderNativeStatusUnsupported
				ctx.Capabilities.Memory.Reason = "provider_missing_feature"
				ctx.Capabilities.Memory.Version = "provider version with spaces"
			},
		},
		{
			name: "disabled evidence must reject control characters",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Owner = ProviderNativeOwnerDisabled
				ctx.Capabilities.Memory.Status = ProviderNativeStatusDisabled
				ctx.Capabilities.Memory.Reason = "disabled_by_policy"
				ctx.Capabilities.Memory.Evidence = "probe\nsecret"
			},
		},
		{
			name: "supported reason whitespace is not an omission",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Reason = " "
			},
		},
		{
			name: "reason token must be status-specific",
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Reason = "arbitrary_probe_result"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertProviderNativeContextInvalid(t, test.mutate)
		})
	}
}

func TestProviderNativeContextAcceptsFixedReasonCodes(t *testing.T) {
	cases := []struct {
		name   string
		status ProviderNativeStatus
		reason string
	}{
		{name: "supported", status: ProviderNativeStatusSupported, reason: "capability_supported"},
		{name: "unknown", status: ProviderNativeStatusUnknown, reason: "capability_probe_required"},
		{name: "unsupported", status: ProviderNativeStatusUnsupported, reason: "provider_missing_feature"},
		{name: "disabled", status: ProviderNativeStatusDisabled, reason: "disabled_by_policy"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := validProviderNativeContext(t)
			capability := &ctx.Capabilities.Memory
			capability.Status = test.status
			capability.Reason = test.reason
			if test.status == ProviderNativeStatusDisabled {
				capability.Owner = ProviderNativeOwnerDisabled
			} else {
				capability.Owner = ProviderNativeOwnerProvider
			}
			if test.status != ProviderNativeStatusSupported {
				capability.Evidence = ""
			}
			if err := ctx.Validate(); err != nil {
				t.Fatalf("Validate() error = %v, want fixed reason code accepted", err)
			}
		})
	}
}

func assertProviderNativeContextInvalid(t *testing.T, mutate func(*ProviderNativeContext)) {
	t.Helper()
	ctx := validProviderNativeContext(t)
	mutate(&ctx)
	if err := ctx.Validate(); !errors.Is(err, ErrProviderNativeContextInvalid) {
		t.Fatalf("Validate() error = %v, want errors.Is(_, %v)", err, ErrProviderNativeContextInvalid)
	}
}

func TestProviderNativeContextRequireProviderFeatureSupported(t *testing.T) {
	ctx := validProviderNativeContext(t)
	if err := ctx.RequireProviderFeature(ProviderNativeFeatureMemory); err != nil {
		t.Fatalf("RequireProviderFeature(memory) error = %v", err)
	}
}

func TestProviderNativeContextValidateRejectsMissingCapabilities(t *testing.T) {
	ctx := validProviderNativeContext(t)
	ctx.Capabilities = ProviderNativeCapabilities{}
	if err := ctx.Validate(); !errors.Is(err, ErrProviderNativeContextInvalid) {
		t.Fatalf("Validate(empty capabilities) error = %v, want context invalid", err)
	}
}

func TestProviderNativeContextRequireProviderFeatureRejects(t *testing.T) {
	tests := []struct {
		name    string
		feature ProviderNativeFeature
		mutate  func(*ProviderNativeContext)
		wantErr error
	}{
		{
			name:    "confirmed unsupported",
			feature: ProviderNativeFeatureMemory,
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Status = ProviderNativeStatusUnsupported
				ctx.Capabilities.Memory.Reason = "provider_missing_feature"
			},
			wantErr: ErrProviderNativeCapabilityUnsupported,
		},
		{
			name:    "unknown capability",
			feature: ProviderNativeFeatureSkills,
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Skills.Status = ProviderNativeStatusUnknown
				ctx.Capabilities.Skills.Reason = "probe_unavailable"
			},
			wantErr: ErrProviderNativeCapabilityUnknown,
		},
		{
			name:    "legacy owner never falls back implicitly",
			feature: ProviderNativeFeatureSkills,
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Skills.Owner = ProviderNativeOwnerLegacyV3
			},
			wantErr: ErrProviderNativeContextInvalid,
		},
		{
			name:    "disabled capability",
			feature: ProviderNativeFeatureMemory,
			mutate: func(ctx *ProviderNativeContext) {
				ctx.Capabilities.Memory.Owner = ProviderNativeOwnerDisabled
				ctx.Capabilities.Memory.Status = ProviderNativeStatusDisabled
				ctx.Capabilities.Memory.Reason = "disabled_by_policy"
			},
			wantErr: ErrProviderNativeCapabilityUnsupported,
		},
		{
			name:    "unknown feature",
			feature: ProviderNativeFeature("transcript"),
			wantErr: ErrProviderNativeFeatureInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := validProviderNativeContext(t)
			if test.mutate != nil {
				test.mutate(&ctx)
			}
			assertProviderNativeFeatureError(t, ctx, test.feature, test.wantErr)
		})
	}
}

func TestProviderNativeCapabilitiesRejectsNonCanonicalFeatureNames(t *testing.T) {
	caps := validProviderNativeContext(t).Capabilities
	for _, raw := range []ProviderNativeFeature{
		" MEMORY ",
		"MEMORY",
		"skills\t",
	} {
		if _, err := caps.Capability(raw); !errors.Is(err, ErrProviderNativeFeatureInvalid) {
			t.Fatalf("Capability(%q) error = %v, want strict feature invalid", raw, err)
		}
	}
	ctx := validProviderNativeContext(t)
	if err := ctx.RequireProviderFeature(" SKILLS "); !errors.Is(err, ErrProviderNativeFeatureInvalid) {
		t.Fatalf("RequireProviderFeature(non-canonical) error = %v, want strict feature invalid", err)
	}
}

func assertProviderNativeFeatureError(t *testing.T, ctx ProviderNativeContext, feature ProviderNativeFeature, wantErr error) {
	t.Helper()
	err := ctx.RequireProviderFeature(feature)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RequireProviderFeature(%q) error = %v, want errors.Is(_, %v)", feature, err, wantErr)
	}
	if wantErr == ErrProviderNativeContextInvalid || errors.Is(err, ErrProviderNativeFeatureInvalid) {
		return
	}
	var capabilityErr ProviderNativeCapabilityError
	if !errors.As(err, &capabilityErr) {
		t.Fatalf("RequireProviderFeature(%q) error = %v, want ProviderNativeCapabilityError", feature, err)
	}
	if capabilityErr.Feature != feature {
		t.Fatalf("capability error feature = %q, want %q", capabilityErr.Feature, feature)
	}
}

func TestProviderNativeContextJSONRoundTrip(t *testing.T) {
	want := validProviderNativeContext(t)
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got ProviderNativeContext
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-tripped context = %#v, want %#v", got, want)
	}
}

func TestProviderNativeContextStrictJSON(t *testing.T) {
	ctx := validProviderNativeContext(t)
	raw, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	base := string(raw)
	cases := []struct {
		name string
		json string
	}{
		{name: "unknown field", json: strings.TrimSuffix(base, "}") + `,"unexpected":true}`},
		{name: "duplicate field", json: strings.TrimSuffix(base, "}") + `,"provider":"codex"}`},
		{name: "case variant duplicate", json: strings.TrimSuffix(base, "}") + `,"Provider":"codex"}`},
		{name: "case variant field", json: strings.Replace(base, `"provider":"claude"`, `"Provider":"claude"`, 1)},
		{name: "nested case variant field", json: strings.Replace(base, `"owner":"provider"`, `"Owner":"provider"`, 1)},
		{name: "trailing value", json: base + ` {}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var got ProviderNativeContext
			err := json.Unmarshal([]byte(test.json), &got)
			if err == nil {
				t.Fatalf("json.Unmarshal() unexpectedly succeeded")
			}
			if test.name != "trailing value" && !errors.Is(err, ErrProviderNativeContextInvalid) {
				t.Fatalf("json.Unmarshal() error = %v, want errors.Is(_, %v)", err, ErrProviderNativeContextInvalid)
			}
		})
	}
}

func TestProviderNativeContextJSONFailureDoesNotMutate(t *testing.T) {
	want := validProviderNativeContext(t)
	got := want
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	raw = append(raw[:len(raw)-1], []byte(`,"unexpected":true}`)...)
	if err := json.Unmarshal(raw, &got); err == nil {
		t.Fatalf("json.Unmarshal() unexpectedly succeeded")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failed unmarshal mutated destination = %#v, want %#v", got, want)
	}
}

func TestProviderNativeContextCanonicalCWD(t *testing.T) {
	root := canonicalProviderNativeTestDir(t)
	canonical, err := CanonicalizeProviderNativeCWD(root)
	if err != nil {
		t.Fatalf("CanonicalizeProviderNativeCWD() error = %v", err)
	}
	if canonical != root {
		t.Fatalf("canonical cwd = %q, want %q", canonical, root)
	}
	assertCanonicalProviderNativeCWDNormalizesWhitespace(t, root)
	assertCanonicalProviderNativeCWDRejectsRelative(t)
	assertProviderNativeContextRejectsNonCanonicalCWD(t, root)
	assertCanonicalProviderNativeCWDResolvesSymlink(t, root)
}

func assertCanonicalProviderNativeCWDNormalizesWhitespace(t *testing.T, root string) {
	t.Helper()
	for _, raw := range []string{" " + root, root + " "} {
		canonical, err := CanonicalizeProviderNativeCWD(raw)
		if err != nil {
			t.Fatalf("CanonicalizeProviderNativeCWD(%q) error = %v, want normalized path", raw, err)
		}
		if canonical != root {
			t.Fatalf("CanonicalizeProviderNativeCWD(%q) = %q, want %q", raw, canonical, root)
		}
	}
}

func assertCanonicalProviderNativeCWDRejectsRelative(t *testing.T) {
	t.Helper()
	if _, err := CanonicalizeProviderNativeCWD("relative"); err == nil {
		t.Fatalf("CanonicalizeProviderNativeCWD(relative) unexpectedly succeeded")
	}
}

func assertProviderNativeContextRejectsNonCanonicalCWD(t *testing.T, root string) {
	t.Helper()
	ctx := validProviderNativeContext(t)
	ctx.CWD = " " + root
	if err := ctx.Validate(); err == nil {
		t.Fatalf("Validate(whitespace cwd) unexpectedly succeeded")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ctx = validProviderNativeContext(t)
	ctx.CWD = link
	if err := ctx.Validate(); err == nil {
		t.Fatalf("Validate(symlink cwd) unexpectedly succeeded")
	}
}

func assertCanonicalProviderNativeCWDResolvesSymlink(t *testing.T, root string) {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resolved, err := CanonicalizeProviderNativeCWD(link)
	if err != nil {
		t.Fatalf("CanonicalizeProviderNativeCWD(symlink) error = %v", err)
	}
	if resolved != root {
		t.Fatalf("canonical symlink cwd = %q, want %q", resolved, root)
	}
}

func TestProviderNativeContextRejectsProviderHomeSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "provider-home-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ctx := validProviderNativeContext(t)
	ctx.ProviderHome = link
	if err := ctx.Validate(); err == nil {
		t.Fatalf("Validate(provider home symlink) unexpectedly succeeded")
	}
}

func TestCanonicalizeProviderNativeContextNormalizesProviderAndCWD(t *testing.T) {
	ctx := validProviderNativeContext(t)
	ctx.Provider = " CLAUDE "
	ctx.CWD = filepath.Join(ctx.CWD, ".")
	wantCWD, err := filepath.EvalSymlinks(ctx.CWD)
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks(cwd) error = %v", err)
	}
	wantCWD = filepath.Clean(wantCWD)
	canonical, err := CanonicalizeProviderNativeContext(ctx)
	if err != nil {
		t.Fatalf("CanonicalizeProviderNativeContext() error = %v", err)
	}
	if canonical.Provider != ProviderNativeProviderClaude {
		t.Fatalf("canonical provider = %q, want %q", canonical.Provider, ProviderNativeProviderClaude)
	}
	if canonical.CWD != wantCWD {
		t.Fatalf("canonical cwd = %q, want %q", canonical.CWD, wantCWD)
	}
}

func TestProviderNativeCapabilityErrorRedactsReason(t *testing.T) {
	err := newProviderNativeCapabilityError(
		ErrProviderNativeCapabilityUnsupported,
		ProviderNativeProviderClaude,
		ProviderNativeFeatureMemory,
		ProviderNativeFeatureCapability{
			Owner:  ProviderNativeOwnerProvider,
			Status: ProviderNativeStatusUnsupported,
			Reason: "/private/project/provider-token=secret",
		},
	)
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/private/project") {
		t.Fatalf("capability error leaked raw reason: %v", err)
	}
	if !strings.Contains(err.Error(), "reason_code=capability_unsupported") {
		t.Fatalf("capability error missing fixed reason code: %v", err)
	}
}
