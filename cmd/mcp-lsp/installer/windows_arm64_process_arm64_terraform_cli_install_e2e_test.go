//go:build windows && arm64 && e2e

package installer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const windowsTerraformCLIInstallE2EEnv = "MCP_LSP_WINDOWS_ARM64_TERRAFORM_CLI_INSTALL_E2E"
const windowsTerraformCLIInstallE2EEvidenceEnv = "MCP_LSP_WINDOWS_ARM64_TERRAFORM_CLI_EVIDENCE_DIR"

// TestWindowsARM64TerraformCLIInstallE2E 只物化锁定 Terraform CLI companion；不注册或启动 LSP。
func TestWindowsARM64TerraformCLIInstallE2E(t *testing.T) {
	if os.Getenv(windowsTerraformCLIInstallE2EEnv) != "1" {
		t.Skipf("set %s=1 for the bounded Terraform CLI install proof", windowsTerraformCLIInstallE2EEnv)
	}
	root := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_ARM64_TERRAFORM_CLI_ROOT"))
	if root == "" || !filepath.IsAbs(root) {
		t.Fatal("MCP_LSP_WINDOWS_ARM64_TERRAFORM_CLI_ROOT must be an absolute existing product root")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("validate Terraform CLI product root: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	path, err := EnsureWindowsTerraformCLI(ctx, root, nil)
	if err != nil {
		t.Fatalf("EnsureWindowsTerraformCLI: %v", err)
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateWindowsTerraformCLIExecutable(path, platform.NativeArch); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(path)), "payload.zip"))
	if err != nil {
		t.Fatalf("read locked Terraform CLI payload: %v", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "02820bcae3725c9c4e91deb6656e9b96ca8af9f395fc5faccc0820dd3295d6e0" {
		t.Fatalf("Terraform CLI payload SHA256=%s", got)
	}
	if evidence := strings.TrimSpace(os.Getenv(windowsTerraformCLIInstallE2EEvidenceEnv)); evidence != "" {
		if !filepath.IsAbs(evidence) {
			t.Fatal("Terraform CLI evidence directory must be absolute")
		}
		if err := os.MkdirAll(evidence, 0o700); err != nil {
			t.Fatalf("create Terraform CLI evidence directory: %v", err)
		}
		receipt := []byte(fmt.Sprintf("proof_kind=SETUP_NON_FORMAL\nstatus=NON_PASS_setup_only\nversion=%s\nnative_arch=%s\nprocess_arch=%s\npayload_sha256=%x\npayload_bytes=%d\npe_architecture=%s\nhttp_requests=1_or_cache_hit\nformat_probe=not_run\n", windowsTerraformCLIVersion, platform.NativeArch, platform.ProcessArch, sha256.Sum256(data), len(data), platform.NativeArch))
		if err := os.WriteFile(filepath.Join(evidence, "terraform-cli-install.receipt"), receipt, 0o600); err != nil {
			t.Fatalf("write Terraform CLI receipt: %v", err)
		}
	}
	t.Logf("Terraform CLI install PASS version=%s native_arch=%s binary=%s payload_bytes=%d", windowsTerraformCLIVersion, platform.NativeArch, filepath.Base(path), len(data))
}
