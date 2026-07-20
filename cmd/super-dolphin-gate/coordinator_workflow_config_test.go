package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareWorkflowProductionConfigSeparatesAuthorityAndSharedRuntime(t *testing.T) {
	fixture := newProductionTestFixture(t)
	authorityRoot, templatePath := workflowConfigTemplate(t, fixture.config)
	runtimeRoot := workflowPrivateDirectory(t)
	outputPath := filepath.Join(runtimeRoot, workflowGeneratedConfigFile)
	if err := prepareWorkflowProductionConfig(templatePath, authorityRoot, runtimeRoot, outputPath); err != nil {
		t.Fatal(err)
	}
	config, err := loadProductionCoordinatorConfigFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(config.TrustedSourceRoot, "cache"))
	t.Setenv("HOME", filepath.Join(config.TrustedSourceRoot, "home"))
	if err := validateProductionRuntimeRoot(config.TrustedSourceRoot); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{
		config.AcceptedImageRoot, config.CandidateStateRoot, config.CandidateBuildRoot,
		config.TrustedSourceRoot,
	} {
		if !productionPathContains(runtimeRoot, root) {
			t.Fatalf("workflow runtime root does not contain %q", root)
		}
	}
	for _, path := range []string{
		config.BootstrapRootFile, config.BootstrapControllerFile, config.BootstrapControllerKeyFile,
		config.SeccompProfile, config.PromotionSigner.PrivateKeyFile,
		config.ResultReceiptAuthority.PrivateKeyFile, config.ActionGrantAuthority.PrivateKeyFile,
	} {
		if !productionPathContains(authorityRoot, path) {
			t.Fatalf("workflow authority root does not contain %q", path)
		}
	}
	if !productionPathContains(runtimeRoot, config.TrustedRepository) {
		t.Fatalf("workflow trusted repository is not in shared runtime root: %q", config.TrustedRepository)
	}
}

func TestPrepareWorkflowProductionConfigRejectsTemplatePathEscape(t *testing.T) {
	fixture := newProductionTestFixture(t)
	authorityRoot, templatePath := workflowConfigTemplate(t, fixture.config)
	runtimeRoot := workflowPrivateDirectory(t)
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	var template productionCoordinatorConfig
	if err := json.Unmarshal(data, &template); err != nil {
		t.Fatal(err)
	}
	template.PromotionSigner.PrivateKeyFile = "@authority/../promotion-private.key"
	writeWorkflowConfigTemplate(t, templatePath, template)
	err = prepareWorkflowProductionConfig(templatePath, authorityRoot, runtimeRoot, filepath.Join(runtimeRoot, workflowGeneratedConfigFile))
	if err == nil || !strings.Contains(err.Error(), "promotion private key") {
		t.Fatalf("workflow template path escape error = %v", err)
	}
}

func TestPrepareWorkflowProductionConfigRejectsRuntimeOutputEscape(t *testing.T) {
	fixture := newProductionTestFixture(t)
	authorityRoot, templatePath := workflowConfigTemplate(t, fixture.config)
	runtimeRoot := workflowPrivateDirectory(t)
	err := prepareWorkflowProductionConfig(
		templatePath,
		authorityRoot,
		runtimeRoot,
		filepath.Join(workflowPrivateDirectory(t), workflowGeneratedConfigFile),
	)
	if err == nil || !strings.Contains(err.Error(), "inside the shared runtime root") {
		t.Fatalf("workflow runtime output escape error = %v", err)
	}
}

func workflowConfigTemplate(t *testing.T, config productionCoordinatorConfig) (string, string) {
	t.Helper()
	authorityRoot := workflowPrivateDirectory(t)
	for _, file := range []struct {
		source string
		name   string
	}{
		{source: config.BootstrapRootFile, name: "bootstrap-root.json"},
		{source: config.BootstrapControllerFile, name: "bootstrap-controller"},
		{source: config.BootstrapControllerKeyFile, name: "bootstrap-controller-key.json"},
		{source: config.SeccompProfile, name: "seccomp.json"},
		{source: config.PromotionSigner.PrivateKeyFile, name: "promotion-private.key"},
		{source: config.ResultReceiptAuthority.PrivateKeyFile, name: "receipt-private.json"},
		{source: config.ActionGrantAuthority.PrivateKeyFile, name: "action-grant-private.json"},
	} {
		copyWorkflowAuthorityFile(t, file.source, filepath.Join(authorityRoot, file.name))
	}
	config.AcceptedImageRoot = "@runtime/accepted"
	config.CandidateStateRoot = "@runtime/candidate-state"
	config.CandidateBuildRoot = "@runtime/candidate-build"
	config.TrustedSourceRoot = "@runtime/trusted-source"
	config.TrustedRepository = "@runtime/trusted.git"
	config.BootstrapRootFile = "@authority/bootstrap-root.json"
	config.BootstrapControllerFile = "@authority/bootstrap-controller"
	config.BootstrapControllerKeyFile = "@authority/bootstrap-controller-key.json"
	config.SeccompProfile = "@authority/seccomp.json"
	config.PromotionSigner.PrivateKeyFile = "@authority/promotion-private.key"
	config.ResultReceiptAuthority.PrivateKeyFile = "@authority/receipt-private.json"
	config.ActionGrantAuthority.PrivateKeyFile = "@authority/action-grant-private.json"
	templatePath := filepath.Join(authorityRoot, workflowConfigTemplateFile)
	writeWorkflowConfigTemplate(t, templatePath, config)
	return authorityRoot, templatePath
}

func copyWorkflowAuthorityFile(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func writeWorkflowConfigTemplate(t *testing.T, path string, config productionCoordinatorConfig) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func workflowPrivateDirectory(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
