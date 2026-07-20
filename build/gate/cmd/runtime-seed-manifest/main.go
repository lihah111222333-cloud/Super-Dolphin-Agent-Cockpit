package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func main() {
	if len(os.Args) != 4 {
		fail("usage: super-dolphin-runtime-seed <write|verify> <snapshot-root> <runtime-root>")
	}
	manifest, err := gate.BuildRuntimeSeedManifest(os.Args[2], os.Args[3])
	if err != nil {
		fail(err.Error())
	}
	for _, seed := range []string{"vendor", filepath.Join("frontend", "node_modules")} {
		if _, err := gate.RuntimeSeedTreeDigest(filepath.Join(os.Args[3], seed)); err != nil {
			fail(err.Error())
		}
	}
	switch os.Args[1] {
	case "write":
		path := filepath.Join(os.Args[3], "manifest.json")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			fail(err.Error())
		}
		encodeErr := gate.EncodeRuntimeSeedManifest(file, manifest)
		closeErr := file.Close()
		if err := errors.Join(encodeErr, closeErr); err != nil {
			fail(err.Error())
		}
	case "verify":
		tracked, err := gate.LoadRuntimeSeedManifest(filepath.Join(os.Args[3], "manifest.json"))
		if err != nil {
			fail(err.Error())
		}
		if tracked != manifest {
			fail("runtime seed manifest does not match the immutable image seeds")
		}
		if err := tracked.Validate(os.Args[2], os.Args[3]); err != nil {
			fail(err.Error())
		}
	default:
		fail("runtime seed action must be write or verify")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
