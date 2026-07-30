package gate

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateRequesterFingerprintUsesCanonicalRandom256BitText(t *testing.T) {
	t.Parallel()

	fingerprint, err := GenerateRequesterFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if err := fingerprint.Validate(); err != nil {
		t.Fatalf("generated requester fingerprint is invalid: %v", err)
	}
	if len(fingerprint) != requesterFingerprintTextLength ||
		!strings.HasPrefix(fingerprint.String(), requesterFingerprintPrefix) {
		t.Fatalf("generated requester fingerprint = %q", fingerprint)
	}
	entropy, err := hex.DecodeString(fingerprint.String()[len(requesterFingerprintPrefix):])
	if err != nil {
		t.Fatalf("decode generated requester fingerprint: %v", err)
	}
	if len(entropy) != requesterFingerprintEntropySize {
		t.Fatalf("requester fingerprint entropy bytes = %d, want %d", len(entropy), requesterFingerprintEntropySize)
	}
}

func TestParseRequesterFingerprintAcceptsOnlyCanonicalText(t *testing.T) {
	t.Parallel()

	valid := requesterFingerprintPrefix + strings.Repeat("a1", requesterFingerprintEntropySize)
	parsed, err := ParseRequesterFingerprint(valid)
	if err != nil {
		t.Fatalf("parse canonical requester fingerprint: %v", err)
	}
	if parsed.String() != valid {
		t.Fatalf("parsed requester fingerprint = %q, want %q", parsed, valid)
	}

	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "empty"},
		{name: "leading whitespace", value: " " + valid},
		{name: "trailing whitespace", value: valid + "\n"},
		{name: "uppercase prefix", value: "SHA256:" + strings.Repeat("a1", requesterFingerprintEntropySize)},
		{name: "uppercase digest", value: requesterFingerprintPrefix + strings.Repeat("A1", requesterFingerprintEntropySize)},
		{name: "short digest", value: requesterFingerprintPrefix + strings.Repeat("a", 63)},
		{name: "long digest", value: requesterFingerprintPrefix + strings.Repeat("a", 65)},
		{name: "wrong prefix", value: "sha512:" + strings.Repeat("a1", requesterFingerprintEntropySize)},
		{name: "non hexadecimal", value: requesterFingerprintPrefix + strings.Repeat("g1", requesterFingerprintEntropySize)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseRequesterFingerprint(test.value); err == nil {
				t.Fatalf("ParseRequesterFingerprint(%q) accepted non-canonical text", test.value)
			}
			if err := RequesterFingerprint(test.value).Validate(); err == nil {
				t.Fatalf("RequesterFingerprint(%q).Validate() accepted non-canonical text", test.value)
			}
		})
	}
}
