package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMainOnlyWiresImportableRun 锁定 cmd 仅保留 os.Exit 与可导入组合根 wiring。
func TestMainOnlyWiresImportableRun(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"os.Exit(remotecicli.Run(os.Args[1:], os.Stdout, os.Stderr))",
		"internal/devtools/remotecicli",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("main.go omitted required wiring %q", required)
		}
	}
	for _, forbidden := range []string{
		"cmd/super-dolphin-gate",
		`"internal/devtools/remoteci"`,
		"syscall.Exec",
		"reexec",
		"dispatchCLI",
		"BindPortableRun",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("main.go contains composition logic or retired dependency %q", forbidden)
		}
	}
}
func TestRemoteCIBinaryDependencyClosureRejectsLocalAndLegacyAuthority(t *testing.T) {
	command := exec.Command("go", "list", "-deps", ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps .: %v\n%s", err, output)
	}
	dependencies := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, forbidden := range []string{
		"modernc.org/sqlite",
		"internal/platform/db/sqlite",
		"internal/devtools/gate",
		"internal/devtools/remoteci",
		"internal/devtools/alicloud",
		"internal/devtools/cicontract",
		"internal/devtools/shardresource",
		"internal/devtools/sourceexport",
	} {
		for _, dependency := range dependencies {
			if dependency == forbidden {
				t.Fatalf("binary dependency closure contains forbidden package %q", forbidden)
			}
		}
	}
}
