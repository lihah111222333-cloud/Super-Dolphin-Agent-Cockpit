package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type identity struct {
	MainCommit              string   `json:"main_commit"`
	MainTree                string   `json:"main_tree"`
	Platform                string   `json:"platform"`
	PolicyDigest            string   `json:"policy_digest"`
	ToolchainDigest         string   `json:"toolchain_digest"`
	GateSourceDigest        string   `json:"gate_source_digest"`
	RuntimeDependencyDigest string   `json:"runtime_dependency_digest"`
	RuntimeBuildArgs        []string `json:"runtime_build_args"`
}

func digest(parts ...string) string {
	material := ""
	for index, part := range parts {
		if index > 0 {
			material += "\x00"
		}
		material += part
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(material)))
}

func main() {
	repository := os.Getenv("GENERATION_ONE_REPOSITORY")
	commit := os.Getenv("GENERATION_ONE_COMMIT")
	treeOID := os.Getenv("GENERATION_ONE_TREE")
	if repository == "" || commit == "" || treeOID == "" {
		panic("generation-one repository, commit, and tree are required")
	}
	ctx := context.Background()
	spec := gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindTree, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Tree: &gatecontract.TreeSource{SHA: treeOID, ParentCommitSHA: commit}, SourceTreeSHA: treeOID,
	}
	tree, err := remoteci.LoadReadOnlyGitTree(ctx, repository, spec)
	if err != nil {
		panic(err)
	}
	runtimeDigest, runtimeArgs, err := remoteci.ResolveRuntimeDependencyBuild(tree.Entries, "linux/amd64")
	if err != nil {
		panic(err)
	}
	registryDigest, err := gatecontract.GateRegistryDigest()
	if err != nil {
		panic(err)
	}
	gateSourceDigest, gateToolchainLockDigest, _, err := remoteci.LoadGateCLICompileClosure(ctx, repository, treeOID)
	if err != nil {
		panic(err)
	}
	goToolchain, err := remoteci.ResolveGoToolchain(tree.Entries)
	if err != nil {
		panic(err)
	}
	result := identity{
		MainCommit: commit, MainTree: treeOID, Platform: "linux/amd64",
		PolicyDigest:     digest("super-dolphin.remote-oci-baseline-policy.v1", registryDigest, runtimeDigest),
		ToolchainDigest:  digest("super-dolphin.remote-baseline-toolchain.v1", gateToolchainLockDigest, goToolchain),
		GateSourceDigest: gateSourceDigest, RuntimeDependencyDigest: runtimeDigest, RuntimeBuildArgs: runtimeArgs,
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
