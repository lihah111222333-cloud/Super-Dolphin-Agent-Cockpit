package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRepositoryNightlyProtocolContract(t *testing.T) {
	root, err := findRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNightlyProtocols(root); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolExpectationsFactoryPreservesContractAndVersionRegex(t *testing.T) {
	want := []protocolExpectation{
		{
			Path: rootProtocolPath,
			ID:   "repository-nightly-gate-health",
			FrontmatterRefs: map[string]string{
				"ledger_handoff_protocol":    ledgerProtocolPath,
				"authorized_repair_protocol": repairProtocolPath,
			},
			MarkdownLinks: []string{"(门禁问题台账接管协议.md)", "(授权问题修复与验证协议.md)"},
		},
		{
			Path: ledgerProtocolPath,
			ID:   "gate-issue-ledger-handoff",
			FrontmatterRefs: map[string]string{
				"root_protocol":   rootProtocolPath,
				"repair_protocol": repairProtocolPath,
			},
			MarkdownLinks: []string{"(全仓夜间门禁健康巡检协议.md)", "(授权问题修复与验证协议.md)"},
		},
		{
			Path: repairProtocolPath,
			ID:   "authorized-issue-repair-and-verification",
			FrontmatterRefs: map[string]string{
				"root_protocol":   rootProtocolPath,
				"ledger_protocol": ledgerProtocolPath,
			},
			MarkdownLinks: []string{"(全仓夜间门禁健康巡检协议.md)", "(门禁问题台账接管协议.md)"},
		},
	}
	got := protocolExpectations()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("protocolExpectations() = %#v, want %#v", got, want)
	}
	got[0].ID = "mutated"
	got[0].FrontmatterRefs["ledger_handoff_protocol"] = "mutated"
	got[0].MarkdownLinks[0] = "(mutated.md)"
	if next := protocolExpectations(); !reflect.DeepEqual(next, want) {
		t.Fatalf("protocolExpectations() retained mutation: %#v, want %#v", next, want)
	}

	for _, version := range []string{"1.0.0", "1.42.7"} {
		if !protocolVersionPattern.MatchString(version) {
			t.Fatalf("protocolVersionPattern rejected compatible version %q", version)
		}
	}
	for _, version := range []string{"0.9.9", "1.0", "1.0.0-rc1", "2.0.0", "v1.0.0"} {
		if protocolVersionPattern.MatchString(version) {
			t.Fatalf("protocolVersionPattern accepted incompatible version %q", version)
		}
	}
}

func TestNightlyProtocolValidatorRejectsContractDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(root string)
		want   string
	}{
		{
			name: "canonical path",
			mutate: func(root string) {
				replaceFixture(t, root, rootProtocolPath, "canonical_file: "+rootProtocolPath, "canonical_file: docs/automation/wrong.md")
			},
			want: "source.canonical_file",
		},
		{
			name: "relative backlink",
			mutate: func(root string) {
				replaceFixture(t, root, ledgerProtocolPath, "(授权问题修复与验证协议.md)", "(missing.md)")
			},
			want: "missing stable relative Markdown link",
		},
		{
			name: "external prompt locator",
			mutate: func(root string) {
				replaceFixture(t, root, rootProtocolPath, "external_prompt_locator: automation-5", "external_prompt_locator: mutable-workspace")
			},
			want: "immutable bootstrap",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeValidProtocolFixture(t)
			test.mutate(root)
			err := validateNightlyProtocols(root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func writeValidProtocolFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", "module fixture\n\ngo 1.26\n")
	writeFixtureFile(t, root, rootProtocolPath, protocolFixture(
		"repository-nightly-gate-health",
		rootProtocolPath,
		"  ledger_handoff_protocol: "+ledgerProtocolPath+"\n"+
			"  authorized_repair_protocol: "+repairProtocolPath+"\n"+
			"  immutable_bootstrap_contract:\n"+
			"    id: "+bootstrapContractID+"\n"+
			"    version: "+bootstrapVersion+"\n"+
			"    external_prompt_locator: "+externalPromptLocator+"\n",
		"[ledger](门禁问题台账接管协议.md) [repair](授权问题修复与验证协议.md)",
	))
	writeFixtureFile(t, root, ledgerProtocolPath, protocolFixture(
		"gate-issue-ledger-handoff",
		ledgerProtocolPath,
		"  root_protocol: "+rootProtocolPath+"\n  repair_protocol: "+repairProtocolPath+"\n",
		"[root](全仓夜间门禁健康巡检协议.md) [repair](授权问题修复与验证协议.md)",
	))
	writeFixtureFile(t, root, repairProtocolPath, protocolFixture(
		"authorized-issue-repair-and-verification",
		repairProtocolPath,
		"  root_protocol: "+rootProtocolPath+"\n  ledger_protocol: "+ledgerProtocolPath+"\n",
		"[root](全仓夜间门禁健康巡检协议.md) [ledger](门禁问题台账接管协议.md)",
	))
	return root
}

func protocolFixture(id, canonical, resources, body string) string {
	return "---\nprotocol:\n  id: " + id + "\n  version: 1.0.0\n  schema_version: 1\n  status: active\nresources:\n" +
		resources + "source:\n  canonical_file: " + canonical + "\n---\n" + body + "\n"
}

func replaceFixture(t *testing.T, root, relative, old, replacement string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), old, replacement, 1)
	if updated == string(data) {
		t.Fatalf("fixture %s does not contain %q", relative, old)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFixtureFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
