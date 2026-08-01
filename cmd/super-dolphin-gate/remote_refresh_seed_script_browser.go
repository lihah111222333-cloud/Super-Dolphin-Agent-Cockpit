package main

const remoteBaselineSeedScriptBrowser = `  playwright_modules=$payload_root/runtime/frontend/node_modules
  playwright_browsers=$playwright_modules/.cache/ms-playwright
  playwright_cli=$playwright_modules/.bin/playwright
  test -x "$playwright_cli" || {
    echo "runtime frontend dependencies are missing the Playwright CLI" >&2
    exit 1
  }
  resolve_playwright_chromium() {
    PLAYWRIGHT_BROWSERS_PATH="$playwright_browsers" PLAYWRIGHT_MODULE="$playwright_modules/playwright" \
      node -e 'const { chromium } = require(process.env.PLAYWRIGHT_MODULE); process.stdout.write(chromium.executablePath())'
  }
  chromium_executable=$(resolve_playwright_chromium)
  if test ! -x "$chromium_executable"; then
    run_logged playwright-chromium-install env PLAYWRIGHT_BROWSERS_PATH="$playwright_browsers" \
      "$playwright_cli" install chromium
    chromium_executable=$(resolve_playwright_chromium)
  fi
  test -x "$chromium_executable" || {
    echo "Playwright Chromium install did not produce an executable" >&2
    exit 1
  }
  chromium_real=${chromium_executable}.super-dolphin-real
  if test ! -x "$chromium_real"; then
    mv "$chromium_executable" "$chromium_real"
    cat > "$chromium_executable" <<'EOF'
#!/bin/sh
set -eu
runtime_root=${SUPER_DOLPHIN_RUNTIME_ROOT:-/opt/super-dolphin-gate/runtime}
test -f "$runtime_root/multiarch"
IFS= read -r multiarch < "$runtime_root/multiarch"
case "$multiarch" in
  x86_64-linux-gnu|aarch64-linux-gnu) ;;
  *) echo "unsupported runtime multiarch: $multiarch" >&2; exit 1 ;;
esac
system_root=$runtime_root/rootfs
test -f "$system_root/etc/fonts/fonts.conf"
LD_LIBRARY_PATH=$system_root/usr/lib/$multiarch:$system_root/lib/$multiarch:$system_root/usr/lib:$system_root/lib
FONTCONFIG_SYSROOT=$system_root
FONTCONFIG_FILE=fonts.conf
FONTCONFIG_PATH=$system_root/etc/fonts
XDG_DATA_DIRS=$system_root/usr/local/share:$system_root/usr/share
GSETTINGS_SCHEMA_DIR=$system_root/usr/share/glib-2.0/schemas
export LD_LIBRARY_PATH FONTCONFIG_SYSROOT FONTCONFIG_FILE FONTCONFIG_PATH XDG_DATA_DIRS GSETTINGS_SCHEMA_DIR
real=$0.super-dolphin-real
test -x "$real"
exec "$real" "$@"
EOF
    chmod 0755 "$chromium_executable"
  fi
  test -x "$chromium_real" || {
    echo "Playwright Chromium wrapper is missing its real executable" >&2
    exit 1
  }
  run_logged playwright-chromium-probe env PLAYWRIGHT_BROWSERS_PATH="$playwright_browsers" \
    PLAYWRIGHT_MODULE="$playwright_modules/playwright" node -e \
    'const { chromium } = require(process.env.PLAYWRIGHT_MODULE); chromium.launch({ headless: true }).then(async (browser) => { const page = await browser.newPage(); await page.setContent("<main data-testid=runtime-probe>ready</main>"); const text = await page.textContent("[data-testid=runtime-probe]"); if (text !== "ready") throw new Error("unexpected Chromium probe text: " + text); await page.screenshot(); await browser.close(); }).catch((error) => { console.error(error); process.exit(1); });'
  printf 'runtime dependency cache ready: Playwright Chromium\n'
`
