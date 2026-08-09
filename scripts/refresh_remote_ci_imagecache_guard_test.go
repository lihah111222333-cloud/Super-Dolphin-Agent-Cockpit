package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRemoteCIImageCacheRefreshScriptLocksTheExternalOperatorBoundary 验证刷新入口不接触 SQLite 或本地镜像构建器。
func TestRemoteCIImageCacheRefreshScriptLocksTheExternalOperatorBoundary(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test path")
	}
	path := filepath.Join(filepath.Dir(current), "refresh_remote_ci_imagecache.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ToLower(string(content))
	for _, required := range []string{"goproxy=off", "gocache=/tmp/go-build", "gomodcache=/tmp/gomod", "redact_cloud_error", "--imagesnapshotid", "--plainhttpregistry", "--retentiondays", "mutates_sqlite:false", "vSwitchRandom", "--if-older-than-hours", "refreshed_at_unix_sec", "baseline-refresh/receipts/current.json", "--AutoMatchImageCache true", "--EliminationStrategy LRU", "--ClientToken", "ErrImageNeverPull", "readonly image_cache_size_gib=30"} {
		if !strings.Contains(source, strings.ToLower(required)) {
			t.Errorf("refresh script is missing %q", required)
		}
	}
	for _, forbidden := range []string{"sqlite3", "update ci_remote_baseline_state", "docker build", "docker push", "buildx", "acr"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("refresh script contains forbidden boundary %q", forbidden)
		}
	}
}

// TestRemoteCIImageCacheRefreshCreationReusesOnlyNonAuthoritativeLayers 锁定候选制作可复用旧缓存层，但正常运行仍只能绑定精确快照。
func TestRemoteCIImageCacheRefreshCreationReusesOnlyNonAuthoritativeLayers(t *testing.T) {
	content, err := os.ReadFile("refresh_remote_ci_imagecache.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	createStart := strings.Index(source, "create_image_cache() {")
	verifyStart := strings.Index(source, "verify_image_cache() {")
	if createStart < 0 || verifyStart <= createStart {
		t.Fatal("refresh script ImageCache creation boundary is unavailable")
	}
	createSource := source[createStart:verifyStart]
	for _, required := range []string{"--AutoMatchImageCache true", "--EliminationStrategy LRU", "--ClientToken \"$client_token\"", "--VSwitchId \"$vswitch_csv\"", "--ImageCacheSize \"$image_cache_size_gib\""} {
		if !strings.Contains(createSource, required) {
			t.Errorf("refresh ImageCache creation is missing %q", required)
		}
	}
	if strings.Contains(createSource, "--AutoMatchImageCache false") {
		t.Fatal("refresh ImageCache creation disables service-side layer reuse")
	}
	if strings.Contains(createSource, "--Flash true") {
		t.Fatal("refresh ImageCache creation uses a zone-local flash snapshot for multi-zone verification")
	}
}

// TestRemoteCIImageCacheRefreshHookIsNonBlockingAndExactTree 锁定 hook 只后台调度 pushed tree 的候选维护。
func TestRemoteCIImageCacheRefreshHookIsNonBlockingAndExactTree(t *testing.T) {
	hook, err := os.ReadFile(filepath.Join("..", ".githooks", "pre-push"))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := os.ReadFile("dispatch_remote_ci_imagecache_refresh.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"git -C \"$repo_root\" cat-file blob", "nohup env", "-u SUPER_DOLPHIN_CI_AGENT_TOKEN", "\"$dispatcher\"", "--if-older-than-hours 24", "</dev/null", ">>\"$log_file\" 2>&1 &"} {
		if !strings.Contains(string(hook), required) {
			t.Errorf("pre-push hook is missing %q", required)
		}
	}
	for _, required := range []string{"shlock -p $$", "refresh.lock", "--source-ref \"$source_commit\""} {
		if !strings.Contains(string(dispatcher), required) {
			t.Errorf("refresh dispatcher is missing %q", required)
		}
	}
	preCommit, err := os.ReadFile(filepath.Join("..", ".githooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(preCommit), "dispatch_remote_ci_imagecache_refresh") {
		t.Error("pre-commit must not refresh an uncommitted or parent tree")
	}
}

// TestRemoteCIImageCacheRefreshScriptKeepsModuleFilesImmutable 锁定依赖解析只能在临时源码归档中执行。
func TestRemoteCIImageCacheRefreshScriptKeepsModuleFilesImmutable(t *testing.T) {
	content, err := os.ReadFile("refresh_remote_ci_imagecache.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, required := range []string{
		`module_root="$temp_root/module-root"`,
		`tar -xzf "$source_archive" -C "$module_root"`,
		`find "$module_root" -type f -name go.mod`,
		`GOWORK=off go mod download`,
		`go mod download -json "github.com/kelindar/event@${event_version}"`,
		`gzip -n`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("refresh script does not isolate module resolution with %q", required)
		}
	}
	if strings.Contains(source, "\n  go mod download\n") {
		t.Error("refresh script resolves modules in the repository worktree")
	}
}

// TestRemoteCIImageCacheRefreshScriptDoesNotEmbedCredentials 防止签名 URL 或云密钥进入仓库。
func TestRemoteCIImageCacheRefreshScriptDoesNotEmbedCredentials(t *testing.T) {
	content, err := os.ReadFile("refresh_remote_ci_imagecache.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"OSSAccessKeyId=", "security-token=", "Signature=", "SUPER_DOLPHIN_CI_GHCR_TOKEN"} {
		if strings.Contains(string(content), forbidden) {
			t.Errorf("refresh script embeds credential marker %q", forbidden)
		}
	}
}

// TestRemoteCIImageCacheRefreshVerificationCannotPullFromBuilder 锁定验收前先移除临时 registry 且禁止回源。
func TestRemoteCIImageCacheRefreshVerificationCannotPullFromBuilder(t *testing.T) {
	content, err := os.ReadFile("refresh_remote_ci_imagecache.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	retire := strings.Index(source, "  retire_builder\n")
	verify := strings.Index(source, "  verify_image_cache\n")
	if retire < 0 || verify < 0 || retire >= verify {
		t.Fatal("refresh script does not retire the temporary registry before verification")
	}
	if !strings.Contains(source, "--Container.1.ImagePullPolicy Never") {
		t.Fatal("refresh verifier can still pull from the temporary registry")
	}
	for _, required := range []string{
		"cp -a /opt/super-dolphin-gate/frontend-embed /tmp/src/cmd/agent-terminal/web-dist",
		"chmod -R a-w /tmp/overlay/opt/super-dolphin/cache/go-build /tmp/overlay/opt/super-dolphin-gate/runtime/go-mod-cache",
		"find /opt/super-dolphin/cache/go-build /opt/super-dolphin-gate/runtime/go-mod-cache -perm /222",
		"SUPER_DOLPHIN_TEST_BACKEND=remote-worker",
		"go list -deps github.com/kelindar/event",
		"third_party/kelindar-event",
		"GOOS=windows GOARCH=amd64 go list -deps -test ./internal/devtools/gate",
		"./scripts/test_with_guard.sh --ci-compile-package \"$package\"",
		"refresh-builder-package-failed package=%s",
		"/super-dolphin-gate worker go-module-overlay /opt/super-dolphin-gate/runtime/go-mod-cache /tmp/gomod",
		"go list -deps -test ./... >/dev/null",
		"CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/verified-gate",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("refresh verifier does not preserve the read-only runtime cache contract: missing %q", required)
		}
	}
}
