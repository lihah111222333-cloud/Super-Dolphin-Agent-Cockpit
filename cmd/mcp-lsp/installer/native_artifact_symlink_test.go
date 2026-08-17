package installer

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeArtifactInstallerAllowsSafeInternalTarSymlinksWhenManifestOptsIn(t *testing.T) {
	archive := buildTarGzArtifact(t, []tarArtifactEntry{
		{name: "bin/native-lsp", kind: tar.TypeReg, content: []byte("binary")},
		{name: "bin/alias", kind: tar.TypeSymlink, link: "native-lsp"},
	})
	server := newTLSArtifactServer(t, archive)
	t.Cleanup(server.Close)
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer := mustNativeInstaller(t, root, server.Client())
	result, err := installer.InstallArtifact(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "symlink", URL: server.URL,
		SHA256: sha256Hex(archive), Format: NativeArtifactFormatTarGz,
		BinaryPath: "bin/native-lsp", LauncherName: "native-lsp", AllowSymlinks: true,
	})
	if err != nil {
		if win32Code, authorizationRequired := nativeArtifactSymlinkAuthorizationRequired(err); authorizationRequired {
			t.Logf("safe symlink requires host authorization on this Windows token: win32=%d", win32Code)
			return
		}
		t.Fatalf("InstallArtifact with safe internal symlink: %v", err)
	}
	linkPath := filepath.Join(result.InstallDir, "payload", "bin", "alias")
	link, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("read installed internal symlink: %v", err)
	}
	if link != "native-lsp" {
		t.Fatalf("installed symlink target = %q, want native-lsp", link)
	}
}

func TestNativeArtifactInstallerRejectsEscapingTarSymlinkEvenWhenManifestOptsIn(t *testing.T) {
	archive := buildTarGzArtifact(t, []tarArtifactEntry{
		{name: "bin/native-lsp", kind: tar.TypeReg, content: []byte("binary")},
		{name: "bin/escape", kind: tar.TypeSymlink, link: "../../outside"},
	})
	server := newTLSArtifactServer(t, archive)
	t.Cleanup(server.Close)
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer := mustNativeInstaller(t, root, server.Client())
	_, err := installer.InstallArtifact(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "escape", URL: server.URL,
		SHA256: sha256Hex(archive), Format: NativeArtifactFormatTarGz,
		BinaryPath: "bin/native-lsp", AllowSymlinks: true,
	})
	if err == nil || !strings.Contains(err.Error(), "escapes payload") {
		t.Fatalf("escaping symlink error = %v, want payload escape rejection", err)
	}
	assertNoPublishedInstall(t, filepath.Join(root, "native", "escape"))
}
