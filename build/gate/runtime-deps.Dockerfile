ARG GO_IMAGE
ARG NODE_IMAGE
ARG SQRUFF_ARCHIVE_URL_AMD64
ARG SQRUFF_ARCHIVE_SHA256_AMD64
ARG SQRUFF_ARCHIVE_URL_ARM64
ARG SQRUFF_ARCHIVE_SHA256_ARM64
ARG BUILD_SOURCE_TREE
ARG RUNTIME_DEPS_INPUT_DIGEST
ARG TOOLCHAIN_DIGEST
ARG TARGET_PLATFORM

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go-build-base
FROM ${GO_IMAGE} AS go-target-base
FROM ${NODE_IMAGE} AS node-target-base

FROM go-build-base AS repository-module-cache
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/kelindar-event/ ./third_party/kelindar-event/
COPY build/gate/runtime-proxy/go.mod build/gate/runtime-proxy/go.sum ./build/gate/runtime-proxy/
RUN set -eu; \
    retry_command() { \
      attempts=0; \
      until "$@"; do \
        attempts=$((attempts + 1)); \
        if [ "$attempts" -ge 5 ]; then return 1; fi; \
        sleep $((attempts * 3)); \
      done; \
    }; \
    retry_command env GOWORK=off GOTOOLCHAIN=local go mod download all; \
    cd /src/build/gate/runtime-proxy && \
    retry_command env GOWORK=off GOTOOLCHAIN=local go mod download all && \
    test -f "$(go env GOMODCACHE)/cache/download/github.com/kelindar/event/@v/v1.5.2.info" && \
    test -f "$(go env GOMODCACHE)/cache/download/github.com/kelindar/event/@v/v1.5.2.mod" && \
    test -f "$(go env GOMODCACHE)/cache/download/github.com/kelindar/event/@v/v1.5.2.zip" && \
    grep -Fxq v1.5.2 "$(go env GOMODCACHE)/cache/download/github.com/kelindar/event/@v/list" && \
    mkdir -p /out/go-proxy && \
    cp -a "$(go env GOMODCACHE)/cache/download/." /out/go-proxy/ && \
    mkdir -p /out/ready && touch /out/ready/repository

FROM node-target-base AS frontend-seed
WORKDIR /src/frontend-app
COPY --from=repository-module-cache /out/ready/repository /tmp/dependency-order/repository
COPY frontend-app/package.json frontend-app/package-lock.json ./
RUN NPM_CONFIG_CACHE=/out/frontend/npm-cache npm ci --ignore-scripts --no-audit --no-fund && \
    rm -rf node_modules && \
    NPM_CONFIG_CACHE=/out/frontend/npm-cache npm ci --ignore-scripts --no-audit --no-fund --offline && \
    PLAYWRIGHT_BROWSERS_PATH=/src/frontend-app/node_modules/.cache/ms-playwright \
    ./node_modules/.bin/playwright install chromium && \
    mkdir -p /out/frontend /out/ready && mv node_modules /out/frontend/node_modules && \
    chmod -R a+rX /out/frontend/node_modules/.cache/ms-playwright && \
    touch /out/ready/frontend

FROM node-target-base AS lsp-seed
WORKDIR /src/runtime-lsp
COPY --from=frontend-seed /out/ready/frontend /tmp/dependency-order/frontend
COPY build/gate/runtime-lsp/package.json build/gate/runtime-lsp/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund && mkdir -p /out/lsp /out/ready && \
    mv node_modules /out/lsp/node_modules && touch /out/ready/lsp

FROM node-target-base AS ripgrep-seed
COPY --from=lsp-seed /out/ready/lsp /tmp/dependency-order/lsp
RUN apt-get update && apt-get install -y --no-install-recommends ripgrep=13.0.0-4+b2 && \
    mkdir -p /out/bin /out/ready && cp /usr/bin/rg /out/bin/rg && touch /out/ready/ripgrep

FROM go-target-base AS sqruff-seed
ARG TARGETARCH
ARG SQRUFF_ARCHIVE_URL_AMD64
ARG SQRUFF_ARCHIVE_SHA256_AMD64
ARG SQRUFF_ARCHIVE_URL_ARM64
ARG SQRUFF_ARCHIVE_SHA256_ARM64
COPY --from=ripgrep-seed /out/ready/ripgrep /tmp/dependency-order/ripgrep
RUN case "${TARGETARCH}" in \
		amd64) archive_url="${SQRUFF_ARCHIVE_URL_AMD64}"; archive_sha256="${SQRUFF_ARCHIVE_SHA256_AMD64}" ;; \
		arm64) archive_url="${SQRUFF_ARCHIVE_URL_ARM64}"; archive_sha256="${SQRUFF_ARCHIVE_SHA256_ARM64}" ;; \
		*) echo "unsupported Sqruff target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
	esac; \
		test -n "${archive_url}" && test -n "${archive_sha256}" && \
		curl -fsSL --retry 2 --connect-timeout 15 --max-time 180 -o /tmp/sqruff.tar.gz "${archive_url}" && \
		echo "${archive_sha256}  /tmp/sqruff.tar.gz" | sha256sum -c - && \
    mkdir -p /out/bin /out/ready && tar -xzf /tmp/sqruff.tar.gz -C /out/bin && \
    test "$(/out/bin/sqruff --version)" = "sqruff 0.38.0" && touch /out/ready/sqruff

FROM go-target-base AS tool-seed
WORKDIR /src/runtime-tools
COPY --from=sqruff-seed /out/ready/sqruff /tmp/dependency-order/sqruff
COPY build/gate/runtime-tools/go.mod build/gate/runtime-tools/go.sum build/gate/runtime-tools/tools.go ./
RUN set -eu; \
    retry_command() { \
      attempts=0; \
      until "$@"; do \
        attempts=$((attempts + 1)); \
        if [ "$attempts" -ge 5 ]; then return 1; fi; \
        sleep $((attempts * 3)); \
      done; \
    }; \
    retry_command env GOWORK=off GOTOOLCHAIN=local go mod download && \
		CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local go build -trimpath -buildvcs=false -o /out/actionlint github.com/rhysd/actionlint/cmd/actionlint && \
    CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local go build -trimpath -buildvcs=false -o /out/gopls golang.org/x/tools/gopls && \
    CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local go build -trimpath -buildvcs=false -o /out/sqlc github.com/sqlc-dev/sqlc/cmd/sqlc && \
    mkdir -p /out/ready && touch /out/ready/tool

FROM go-target-base AS manifest-builder
WORKDIR /src
COPY --from=tool-seed /out/ready/tool /tmp/dependency-order/tool
COPY . .
COPY --from=repository-module-cache /go/pkg/mod /go/pkg/mod
RUN GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
    go build -mod=mod -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate

FROM go-target-base
USER root
ARG BUILD_SOURCE_TREE
ARG RUNTIME_DEPS_INPUT_DIGEST
ARG TOOLCHAIN_DIGEST
ARG TARGET_PLATFORM
LABEL org.super-dolphin.source-tree-sha="${BUILD_SOURCE_TREE}" \
      org.super-dolphin.runtime-deps-input-digest="${RUNTIME_DEPS_INPUT_DIGEST}" \
      org.super-dolphin.toolchain-digest="${TOOLCHAIN_DIGEST}" \
      org.super-dolphin.platform="${TARGET_PLATFORM}" \
      org.super-dolphin.schema-version="1"
COPY --from=lsp-seed /usr/local/bin/node /usr/local/bin/node
COPY --from=lsp-seed /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
COPY --from=repository-module-cache /out/go-proxy /opt/super-dolphin-gate/runtime/go-proxy
COPY --from=repository-module-cache /go/pkg/mod /opt/super-dolphin-gate/runtime/go-mod-cache
COPY --from=frontend-seed /out/frontend/node_modules /opt/super-dolphin-gate/runtime/frontend/node_modules
COPY --from=frontend-seed /out/frontend/npm-cache /opt/super-dolphin-gate/runtime/frontend/npm-cache
COPY --from=lsp-seed /out/lsp/node_modules /opt/super-dolphin-gate/runtime/lsp/node_modules
COPY --from=manifest-builder /out/super-dolphin-gate /tmp/super-dolphin-gate
COPY --from=tool-seed /out/gopls /usr/local/bin/gopls
COPY --from=tool-seed /out/actionlint /opt/super-dolphin-gate/runtime/bin/actionlint
COPY --from=tool-seed /out/sqlc /opt/super-dolphin-gate/runtime/bin/sqlc
COPY --from=ripgrep-seed /out/bin/rg /opt/super-dolphin-gate/runtime/bin/rg
COPY --from=sqruff-seed /out/bin/sqruff /opt/super-dolphin-gate/runtime/bin/sqruff
RUN set -eu; \
    retry_command() { \
      attempts=0; \
      until "$@"; do \
        attempts=$((attempts + 1)); \
        if [ "$attempts" -ge 5 ]; then return 1; fi; \
        sleep $((attempts * 3)); \
      done; \
    }; \
    ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm; \
    ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx; \
    ln -s /opt/super-dolphin-gate/runtime/lsp/node_modules/.bin/typescript-language-server /usr/local/bin/typescript-language-server; \
    ln -s /opt/super-dolphin-gate/runtime/lsp/node_modules/.bin/vscode-css-language-server /usr/local/bin/vscode-css-language-server; \
    ln -s /opt/super-dolphin-gate/runtime/lsp/node_modules/.bin/pyright-langserver /usr/local/bin/pyright-langserver; \
    ln -s /opt/super-dolphin-gate/runtime/lsp/node_modules/.bin/bash-language-server /usr/local/bin/bash-language-server; \
	ln -s /opt/super-dolphin-gate/runtime/bin/actionlint /usr/local/bin/actionlint; \
    printf 'Acquire::Retries "10";\nAcquire::http::No-Cache "true";\nAcquire::http::Pipeline-Depth "0";\n' > /etc/apt/apt.conf.d/80-super-dolphin-retries; \
    if [ -f /etc/apt/sources.list ]; then sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list; fi; \
    find /etc/apt/sources.list.d -type f \( -name '*.list' -o -name '*.sources' \) -exec sed -i 's|http://deb.debian.org|https://deb.debian.org|g' {} +; \
    retry_command env PLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright \
      /opt/super-dolphin-gate/runtime/frontend/node_modules/.bin/playwright install-deps chromium; \
    retry_command sh -c 'apt-get update && apt-get install -y --no-install-recommends fontconfig fonts-liberation libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev pkg-config procps xauth xvfb'; \
    rm -rf /var/lib/apt/lists/*; \
    pkg-config --exists gtk+-3.0 webkit2gtk-4.1 gio-unix-2.0 libsoup-3.0; \
    ln -s /opt/super-dolphin-gate/runtime/bin/rg /usr/local/bin/rg; \
    ln -s /opt/super-dolphin-gate/runtime/bin/sqruff /usr/local/bin/sqruff; \
    test -x /usr/bin/git && test -x /usr/bin/make && test -x /usr/bin/python3 && test -x /usr/bin/ps && test -x /usr/bin/Xvfb && test -x /usr/bin/xauth && test -x /usr/bin/xvfb-run && test -x /usr/local/bin/node && test -x /usr/local/bin/npm; \
    test -f /etc/fonts/fonts.conf && test -d /usr/share/fonts && test -x /usr/local/go/bin/go && test -x /usr/local/bin/gopls && test -x /opt/super-dolphin-gate/runtime/bin/actionlint && test -x /opt/super-dolphin-gate/runtime/bin/sqlc && test -x /opt/super-dolphin-gate/runtime/bin/rg && test -x /opt/super-dolphin-gate/runtime/bin/sqruff; \
    test "$(rg --version | head -n 1)" = "ripgrep 13.0.0" && test "$(sqruff --version)" = "sqruff 0.38.0"
COPY go.sum /tmp/runtime-manifest-source/go.sum
COPY build/gate/runtime-proxy/go.sum /tmp/runtime-manifest-source/build/gate/runtime-proxy/go.sum
COPY frontend-app/package-lock.json /tmp/runtime-manifest-source/frontend-app/package-lock.json
RUN --network=none mkdir -p /opt/super-dolphin-gate/runtime/frontend/vite-cache && \
    /tmp/super-dolphin-gate worker runtime-seed write /tmp/runtime-manifest-source /opt/super-dolphin-gate/runtime && \
    rm /tmp/super-dolphin-gate && \
    rm -rf /tmp/runtime-manifest-source && \
    test -s /opt/super-dolphin-gate/runtime/manifest.json && \
    /opt/super-dolphin-gate/runtime/bin/sqlc version >/dev/null
RUN --network=none mkdir -p /opt/super-dolphin/cache/go-build && \
    chmod 0555 /opt/super-dolphin/cache /opt/super-dolphin/cache/go-build
ENV PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin \
    PLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright \
    GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
USER 65532:65532
RUN --network=none xvfb-run -a sh -ec 'test -n "$DISPLAY"'
RUN --network=none node -e 'const { chromium } = require("/opt/super-dolphin-gate/runtime/frontend/node_modules/playwright"); chromium.launch({headless:true}).then(async browser => { const page = await browser.newPage(); await page.setContent("<main data-testid=runtime-probe>ready</main>"); const text = await page.textContent("[data-testid=runtime-probe]"); if (text !== "ready") throw new Error(`unexpected Chromium probe text: ${text}`); await page.screenshot(); await browser.close(); }).catch(error => { console.error(error); process.exit(1); });'
RUN --network=none sqruff --version | grep -Fx 'sqruff 0.38.0'
RUN --network=none test "$(actionlint -version | head -n 1)" = "v1.7.12"
