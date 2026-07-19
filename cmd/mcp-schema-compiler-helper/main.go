package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
)

const maxHelperStderrBytes = 16 * 1024

// main 根据显式模式生成或校验 package manifest，否则进入 one-shot helper 协议。
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--write-package-manifest" {
		writePackageManifest(os.Args[2:])
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--verify-package" {
		if err := verifyPackage(os.Args[2]); err != nil {
			slog.Error("verify schema helper package", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		_, _ = fmt.Fprint(os.Stderr, "unsupported schema helper arguments")
		os.Exit(2)
	}
	if err := schema.ServeOneShot(os.Stdin, os.Stdout); err != nil {
		message := fmt.Sprintf("%v", err)
		if len(message) > maxHelperStderrBytes {
			message = "schema helper failed with oversized diagnostic"
		}
		_, _ = fmt.Fprint(os.Stderr, message)
		os.Exit(1)
	}
}

func verifyPackage(manifestArg string) error {
	identity, err := schema.CurrentBuildIdentity()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	manifest, err := filepath.Abs(manifestArg)
	if err != nil {
		return err
	}
	return schema.VerifyHelperPackage(executable, manifest, identity)
}

func writePackageManifest(args []string) {
	flags := flag.NewFlagSet("write-package-manifest", flag.ContinueOnError)
	helper := flags.String("helper", "", "helper binary path")
	output := flags.String("output", "", "manifest output path")
	commit := flags.String("app-commit", "", "application commit")
	goVersion := flags.String("go-version", "", "Go toolchain version")
	goos := flags.String("goos", "", "target GOOS")
	goarch := flags.String("goarch", "", "target GOARCH")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}
	if err := schema.WriteHelperManifest(*helper, *output, schema.HelperIdentity{
		AppCommit: *commit, GoVersion: *goVersion, GOOS: *goos, GOARCH: *goarch,
	}); err != nil {
		slog.Error("write schema helper manifest", "error", err)
		os.Exit(1)
	}
}
