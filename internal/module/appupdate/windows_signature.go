package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

type windowsInstallerSignatureVerifier func(path, publisher, thumbprint string) error

type authenticodeSignature struct {
	Status     string `json:"Status"`
	Subject    string `json:"Subject"`
	Thumbprint string `json:"Thumbprint"`
	Issuer     string `json:"Issuer"`
}

// verifyWindowsInstallerSignatureWithPowerShell 校验 Windows 安装器的 Authenticode 状态、发布者和证书指纹。
// 安装前必须通过这一步；非 Windows 平台没有可靠 Authenticode API，因此直接 fail-fast。
func verifyWindowsInstallerSignatureWithPowerShell(path, publisher, thumbprint string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("Windows app update installer path is required")
	}
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return fmt.Errorf("Windows app update installer must be an .exe: %s", path)
	}
	publisher = strings.TrimSpace(publisher)
	if publisher == "" {
		return fmt.Errorf("%s is required for Windows app update installation", envUpdateWindowsPublisher)
	}
	if err := validateWindowsThumbprint(thumbprint); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return errors.New("Windows app update installer Authenticode verification requires Windows runtime")
	}

	ctx, cancel := platformconfig.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	script := `
$sig = Get-AuthenticodeSignature -LiteralPath $args[0]
$cert = $sig.SignerCertificate
$subject = ''
$thumbprint = ''
$issuer = ''
if ($null -ne $cert) {
  $subject = [string]$cert.Subject
  $thumbprint = [string]$cert.Thumbprint
  $issuer = [string]$cert.Issuer
}
[pscustomobject]@{
  Status = [string]$sig.Status
  Subject = $subject
  Thumbprint = $thumbprint
  Issuer = $issuer
} | ConvertTo-Json -Compress
`
	cmd := exec.CommandContext(
		ctx,
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-Command",
		script,
		path,
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("verify Windows installer Authenticode signature timed out: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("verify Windows installer Authenticode signature: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var sig authenticodeSignature
	if err := json.Unmarshal(output, &sig); err != nil {
		return fmt.Errorf("decode Windows installer Authenticode signature: %w", err)
	}
	return validateAuthenticodeSignature(sig, publisher, thumbprint)
}

// validateAuthenticodeSignature 比对 PowerShell 返回的签名状态和证书字段。
func validateAuthenticodeSignature(sig authenticodeSignature, publisher, thumbprint string) error {
	if !strings.EqualFold(strings.TrimSpace(sig.Status), "Valid") {
		return fmt.Errorf("Windows installer Authenticode status = %q, want Valid", sig.Status)
	}
	if !strings.Contains(
		strings.ToLower(strings.TrimSpace(sig.Subject)),
		strings.ToLower(strings.TrimSpace(publisher)),
	) {
		return fmt.Errorf("Windows installer publisher %q does not match expected publisher %q", sig.Subject, publisher)
	}
	actualThumbprint := normalizeCertificateThumbprint(sig.Thumbprint)
	expectedThumbprint := normalizeCertificateThumbprint(thumbprint)
	if actualThumbprint == "" || !strings.EqualFold(actualThumbprint, expectedThumbprint) {
		return fmt.Errorf("Windows installer certificate thumbprint = %q, want %q", sig.Thumbprint, thumbprint)
	}
	return nil
}

// normalizeCertificateThumbprint 允许配置中包含空格或冒号，比较前统一为大写十六进制。
func normalizeCertificateThumbprint(value string) string {
	value = strings.NewReplacer(" ", "", ":", "").Replace(strings.TrimSpace(value))
	return strings.ToUpper(value)
}
