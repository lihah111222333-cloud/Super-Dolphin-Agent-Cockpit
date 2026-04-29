package skilllibrary

import "testing"

func sigPtr(s string) *string { return &s }

func TestEvaluateTrust_BuiltinTrusted(t *testing.T) {
	if got := EvaluateTrust(SkillMeta{Origin: OriginBuiltin}); got != TrustLevelTrusted {
		t.Errorf("builtin: got %v, want trusted", got)
	}
}

func TestEvaluateTrust_MarketplaceAlwaysUntrusted(t *testing.T) {
	// 即使 signature 非空，本期仍按 untrusted 处理（签名校验未实现）
	cases := []SkillMeta{
		{Origin: OriginMarketplace, Signature: nil},
		{Origin: OriginMarketplace, Signature: sigPtr("ed25519:fakesig")},
	}
	for _, m := range cases {
		if got := EvaluateTrust(m); got != TrustLevelUntrusted {
			t.Errorf("marketplace meta=%+v: got %v, want untrusted", m, got)
		}
	}
}

func TestEvaluateTrust_LocalUntrusted(t *testing.T) {
	for _, o := range []Origin{OriginLocal, OriginDevOverride} {
		if got := EvaluateTrust(SkillMeta{Origin: o}); got != TrustLevelUntrusted {
			t.Errorf("origin=%s: got %v, want untrusted", o, got)
		}
	}
}

func TestEvaluateTrust_UnknownOrigin(t *testing.T) {
	if got := EvaluateTrust(SkillMeta{Origin: ""}); got != TrustLevelUnknown {
		t.Errorf("empty origin: got %v, want unknown", got)
	}
	if got := EvaluateTrust(SkillMeta{Origin: "garbage-origin"}); got != TrustLevelUnknown {
		t.Errorf("garbage origin: got %v, want unknown", got)
	}
}

func TestIsTrusted_OnlyBuiltin(t *testing.T) {
	if !IsTrusted(SkillMeta{Origin: OriginBuiltin}) {
		t.Error("builtin should be trusted")
	}
	for _, o := range []Origin{OriginMarketplace, OriginLocal, OriginDevOverride, ""} {
		if IsTrusted(SkillMeta{Origin: o}) {
			t.Errorf("origin=%q should NOT be trusted", o)
		}
	}
}

func TestTrustLevel_String(t *testing.T) {
	cases := map[TrustLevel]string{
		TrustLevelTrusted:   "trusted",
		TrustLevelUntrusted: "untrusted",
		TrustLevelUnknown:   "unknown",
	}
	for lvl, want := range cases {
		if got := lvl.String(); got != want {
			t.Errorf("level %d: got %q, want %q", lvl, got, want)
		}
	}
}
