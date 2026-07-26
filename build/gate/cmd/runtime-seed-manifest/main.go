package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fail(err.Error())
	}
}

// run 构造或校验 runtime manifest；Go seed 由 BuildRuntimeSeedManifest 验证 go-proxy。
func run(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: super-dolphin-runtime-seed <write|verify> <snapshot-root> <runtime-root>")
	}
	manifest, err := gate.BuildRuntimeSeedManifest(args[1], args[2])
	if err != nil {
		return err
	}
	if _, err := gate.RuntimeSeedTreeDigest(filepath.Join(args[2], "frontend", "node_modules")); err != nil {
		return err
	}
	return executeRuntimeSeedAction(args[0], args[1], args[2], manifest)
}

// executeRuntimeSeedAction 分派 manifest 写入或复验动作。
func executeRuntimeSeedAction(action, snapshotRoot, runtimeRoot string, manifest gate.RuntimeSeedManifest) error {
	switch action {
	case "write":
		return writeRuntimeSeedManifest(runtimeRoot, manifest)
	case "verify":
		return verifyRuntimeSeedManifest(snapshotRoot, runtimeRoot, manifest)
	default:
		return errors.New("runtime seed action must be write or verify")
	}
}

// writeRuntimeSeedManifest 以排他创建方式写入不可变 manifest。
func writeRuntimeSeedManifest(runtimeRoot string, manifest gate.RuntimeSeedManifest) error {
	path := filepath.Join(runtimeRoot, "manifest.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	encodeErr := gate.EncodeRuntimeSeedManifest(file, manifest)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}

// verifyRuntimeSeedManifest 复验已写入 manifest 与当前 seed 树完全一致。
func verifyRuntimeSeedManifest(snapshotRoot, runtimeRoot string, manifest gate.RuntimeSeedManifest) error {
	tracked, err := gate.LoadRuntimeSeedManifest(filepath.Join(runtimeRoot, "manifest.json"))
	if err != nil {
		return err
	}
	if tracked != manifest {
		return errors.New("runtime seed manifest does not match the immutable image seeds")
	}
	return tracked.Validate(snapshotRoot, runtimeRoot)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
