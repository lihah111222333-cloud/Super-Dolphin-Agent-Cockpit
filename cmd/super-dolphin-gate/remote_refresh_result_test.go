package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestRemoteBaselineRefreshResultEncodesRefreshOnlyTerminalOutcomes(t *testing.T) {
	for _, outcome := range []remoteBaselineRefreshResultOutcome{
		remoteBaselineRefreshResultOutcomeUnchanged,
		remoteBaselineRefreshResultOutcomeCleanupCompleted,
		remoteBaselineRefreshResultOutcomePromoted,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			var stdout bytes.Buffer
			if err := encodeRemoteBaselineRefreshResult(&stdout, newRemoteBaselineRefreshResult(outcome, remoteci.BaselineState{})); err != nil {
				t.Fatalf("encode refresh result: %v", err)
			}
			var encoded map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &encoded); err != nil {
				t.Fatalf("decode refresh result: %v", err)
			}
			if got := encoded["authority"]; got != remoteBaselineRefreshResultAuthority {
				t.Fatalf("authority = %q, want %q", got, remoteBaselineRefreshResultAuthority)
			}
			if got := encoded["outcome"]; got != string(outcome) {
				t.Fatalf("outcome = %q, want %q", got, outcome)
			}
			if got := encoded["phase"]; got != string(outcome) {
				t.Fatalf("phase = %q, want %q", got, outcome)
			}
			for _, forbidden := range []string{"reused", "passed", "test_pass"} {
				if _, found := encoded[forbidden]; found {
					t.Fatalf("refresh result retained forbidden field %q: %s", forbidden, stdout.String())
				}
			}
		})
	}
}

func TestRemoteBaselineRefreshResultRejectsNormalTestVocabulary(t *testing.T) {
	for _, result := range []remoteBaselineRefreshResult{
		{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, Authority: remoteBaselineRefreshResultAuthority, Outcome: "reused", Phase: "reused"},
		{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, Authority: remoteBaselineRefreshResultAuthority, Outcome: "passed", Phase: "passed"},
		{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, Authority: remoteBaselineRefreshResultAuthority, Outcome: "test_pass", Phase: "test_pass"},
		{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, Authority: "normal_test", Outcome: remoteBaselineRefreshResultOutcomePromoted, Phase: remoteBaselineRefreshResultOutcomePromoted},
		{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, Authority: remoteBaselineRefreshResultAuthority, Outcome: remoteBaselineRefreshResultOutcomePromoted, Phase: remoteBaselineRefreshResultOutcomeUnchanged},
	} {
		var stdout bytes.Buffer
		err := encodeRemoteBaselineRefreshResult(&stdout, result)
		if err == nil {
			t.Fatalf("encode refresh result %+v unexpectedly succeeded", result)
		}
		if strings.TrimSpace(stdout.String()) != "" {
			t.Fatalf("invalid refresh result wrote output: %q", stdout.String())
		}
	}
}
