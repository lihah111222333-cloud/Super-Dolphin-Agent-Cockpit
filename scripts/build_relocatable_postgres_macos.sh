#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
version="${POSTGRES_VERSION:-16.14}"
goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
platform="${goos}-${goarch}"
postgres_macos_min_version="${SUPER_DOLPHIN_MACOS_MIN_VERSION:-13.0}"

if [[ "$goos" != "darwin" ]]; then
  echo "build_relocatable_postgres_macos.sh must run on macOS; current GOOS=$goos" >&2
  exit 1
fi
if [[ ! "$postgres_macos_min_version" =~ ^[0-9]+([.][0-9]+){1,2}$ ]]; then
  echo "SUPER_DOLPHIN_MACOS_MIN_VERSION must be a dotted numeric version such as 13.0" >&2
  exit 1
fi

cache="$root/.build-cache/postgres-build"
src="$cache/postgresql-$version"
tarball="$cache/postgresql-$version.tar.bz2"
prefix="${SUPER_DOLPHIN_POSTGRES_RELOCATABLE_PREFIX:-$root/.build-cache/postgres/$version/$platform}"
url="${POSTGRES_SOURCE_URL:-https://ftp.postgresql.org/pub/source/v$version/postgresql-$version.tar.bz2}"

mkdir -p "$cache" "$(dirname "$prefix")"

if [[ ! -f "$tarball" ]]; then
  curl -fL "$url" -o "$tarball"
fi

if [[ ! -d "$src" ]]; then
  tar -xjf "$tarball" -C "$cache"
fi

rm -rf "$prefix"

(
  cd "$src"
  make distclean >/dev/null 2>&1 || true
  deployment_cflags="${CFLAGS:-}"
  deployment_ldflags="${LDFLAGS:-}"
  deployment_cflags="${deployment_cflags:+$deployment_cflags }-mmacosx-version-min=$postgres_macos_min_version"
  deployment_ldflags="${deployment_ldflags:+$deployment_ldflags }-mmacosx-version-min=$postgres_macos_min_version -Wl,-headerpad_max_install_names"
  MACOSX_DEPLOYMENT_TARGET="$postgres_macos_min_version" \
  CFLAGS="$deployment_cflags" \
  LDFLAGS="$deployment_ldflags" \
    ./configure \
    --prefix="$prefix" \
    --without-icu \
    --without-readline \
    --without-zlib
  make -j"$(sysctl -n hw.ncpu)"
  make install
)

"$prefix/bin/postgres" --version
"$prefix/bin/initdb" --version
"$prefix/bin/pg_ctl" --version

share_found=0
for share in \
  "$prefix/share" \
  "$prefix/share/postgresql@16" \
  "$prefix/share/postgresql"; do
  if [[ -f "$share/postgres.bki" ]]; then
    share_found=1
    break
  fi
done
if [[ "$share_found" != "1" ]]; then
  echo "PostgreSQL share files missing: expected postgres.bki under $prefix/share, $prefix/share/postgresql@16, or $prefix/share/postgresql" >&2
  exit 1
fi

echo "SUPER_DOLPHIN_POSTGRES_DIST=$prefix"
