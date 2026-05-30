# A13: Linux Packaging Parity

**Goal:** Linux package guard/verifier 与 macOS 关键治理同步；若 Linux 不在本次 release scope，必须显式标注 out-of-scope，不能静默缺失。

**Files:**
- Modify: `scripts/package_linux.sh`
- Modify: `scripts/package_linux_local.sh`
- Add/Modify: `scripts/verify_packaged_app_linux.sh`
- Test: `scripts/package_linux_guard_test.go`
- Test: `scripts/*verify*_test.go`

**Steps:**
- [ ] Write red test: Linux package script cannot contain private URL/path/key prompt.
- [ ] Write red test: Linux package must emit or validate runtime manifest fields equivalent to macOS critical fields.
- [ ] Add `scripts/verify_packaged_app_linux.sh`; input must be either the unpacked Linux stage directory (`dist/package/linux/<app_name>-<version>-linux-<arch>`) or the generated `.tar.gz` artifact, extracting tarballs to a temp directory before verification.
- [ ] Linux verifier must check required executables, `runtime-manifest.json`, Codex manifest/digest, LSP manifest/digests, embedded PG binaries/share/inventory, permissions, broken symlinks, and package-root ownership/escape rules equivalent to the macOS verifier where platform semantics allow.
- [ ] If Linux is explicitly out of release scope, A13 must record owner/date/reason/re-enable condition in A14 execution notes and A15 must mark Linux release readiness as Deferred/Not ready; a missing Linux verifier is not a silent pass.
- [ ] Keep env names aligned with macOS script governance.

**Validation:**
```bash
./scripts/test_with_guard.sh ./scripts -run 'TestPackageLinux|Test.*Linux.*Verifier' -count=1
bash -n scripts/package_linux.sh scripts/package_linux_local.sh scripts/verify_packaged_app_linux.sh
# Run the package/verifier smoke on a Linux runner where `go env GOOS` is `linux`.
scripts/package_linux.sh
scripts/verify_packaged_app_linux.sh "dist/package/linux/${APP_NAME:-super-dolphin}-${VERSION:-0.1.0}-linux-$(go env GOARCH)"
```

If Linux is not in this release scope, replace the two package/verifier commands above only with an explicit out-of-scope gate in A14 notes containing owner, date, reason, and re-enable condition; A15 must then report Linux release as Deferred/Not ready.
