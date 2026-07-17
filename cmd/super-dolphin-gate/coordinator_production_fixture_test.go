package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

// newProductionTestFixture 组装 production 测试所需的仓库、密钥与权威配置。
func newProductionTestFixture(t *testing.T) productionTestFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	chmodPrivate(t, root)
	repository := prepareProductionRepository(t, root)
	authority := prepareProductionAuthority(t, root)
	bootstrapRootFile := filepath.Join(root, "bootstrap-root.json")
	bootstrapControllerFile := writeProductionTestBootstrapController(t, root)
	bootstrapControllerKeyFile := filepath.Join(root, "bootstrap-controller-key.json")
	config := productionCoordinatorConfig{
		AcceptedImageRoot:  makePrivateDirectory(t, root, "accepted"),
		CandidateStateRoot: makePrivateDirectory(t, root, "candidate-state"),
		BootstrapRootFile:  bootstrapRootFile, BootstrapControllerFile: bootstrapControllerFile,
		BootstrapControllerKeyFile: bootstrapControllerKeyFile,
		CandidateBuildRoot:         makePrivateDirectory(t, root, "build"),
		TrustedSourceRoot:          prepareProductionTrustedSourceRoot(t),
		SeccompProfile:             writeProductionSeccompProfile(t, root),
		Platform:                   "linux/arm64", RepoID: "example/repository",
		TrustedRef: "refs/heads/main", TrustedRepository: repository.trustedRepository,
		AcceptedImageSigners: []productionTrustedKey{{
			Signer: authority.signer, PublicKey: base64.StdEncoding.EncodeToString(authority.publicKey),
		}},
		ResultReceiptAuthority: productionResultReceiptAuthorityConfig{
			Signer:         authority.receiptSigner,
			PublicKey:      base64.StdEncoding.EncodeToString(authority.receiptPublicKey),
			PrivateKeyFile: authority.receiptKeyPath,
		},
		ActionGrantAuthority: productionActionGrantAuthorityConfig{
			Signer: authority.grantSigner, PublicKey: base64.StdEncoding.EncodeToString(authority.grantPublicKey),
			PrivateKeyFile: authority.grantKeyPath, TTLSeconds: 60,
		},
		PromotionSigner: productionPromotionKey{
			Signer: authority.signer, PrivateKeyFile: authority.promotionKeyPath,
		},
		CandidateTTLSeconds: 3600, PromotionPollMillis: 20,
	}
	bootstrapRoot, rootTrust, rootPrivateKey := productionBootstrapRootForFixture(
		t, config, repository.commit, repository.tree, authority.signer, authority.publicKey,
	)
	config.AcceptedImageSigners = append(config.AcceptedImageSigners, rootTrust)
	writePrivateJSON(t, bootstrapControllerKeyFile, productionBootstrapControllerPrivateKey{
		Signer: authority.signer, PrivateKey: base64.StdEncoding.EncodeToString(authority.privateKey),
	})
	writeProductionBootstrapRootFixture(t, bootstrapRootFile, bootstrapRoot, rootPrivateKey)
	fixture := productionTestFixture{
		config: config, signer: authority.signer, privateKey: authority.privateKey,
		bootstrapRootKey: rootPrivateKey,
		receiptKey:       authority.receiptPrivateKey, grantKey: authority.grantPrivateKey,
		commit: repository.commit, tree: repository.tree, sourceRepo: repository.sourceRepo,
	}
	baseTree, err := localci.LoadReadOnlyGitTree(
		context.Background(), repository.sourceRepo, productionSourceSpec(fixture),
	)
	if err != nil {
		t.Fatal(err)
	}
	baseInputs, err := localci.ResolveGateImageInputs(baseTree, productionDigest("1"), config.Platform)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapInputs, err := localci.ResolveGateImageInputs(baseTree, bootstrapRoot.PolicyDigest, config.Platform)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRoot.ImageInputDigest = bootstrapInputs.ImageInputDigest
	bootstrapRoot.ToolchainDigest = bootstrapInputs.ToolchainDigest
	bootstrapRoot.ImageSchemaVersion = bootstrapInputs.ImageSchemaVersion
	writeProductionBootstrapRootFixture(t, bootstrapRootFile, bootstrapRoot, rootPrivateKey)
	fixture.acceptedInputDigest = baseInputs.ImageInputDigest
	bootstrapAcceptedState(t, fixture)
	fixture.configPath = filepath.Join(root, "production.json")
	writePrivateJSON(t, fixture.configPath, config)
	return fixture
}

func writeProductionTestBootstrapController(t *testing.T, root string) string {
	t.Helper()
	bootstrapControllerFile := filepath.Join(root, "bootstrap-controller")
	if err := os.WriteFile(bootstrapControllerFile, []byte("#!/bin/sh\nexit 97\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bootstrapControllerFile
}
