#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
check_go
[[ -f /etc/alpine-release ]] || fail "Release target uses musl/Alpine; use make build for the pinned container."
[[ -f .build/host.mod ]] || fail "Run make prepare first."
mkdir -p dist
# Deliberately do NOT copy the host's -extldflags '-static': a plugin must be
# a dynamically loadable shared object. The host's static-linking risk is
# separately and mandatorily checked by the official-image smoke test.
go build -buildmode=plugin -buildvcs=false -ldflags='-s -w -buildid=' -o dist/bifrost-cliproxyapi-metadata.tmp.so .
mv dist/bifrost-cliproxyapi-metadata.tmp.so dist/bifrost-cliproxyapi-metadata.so
go version -m dist/bifrost-cliproxyapi-metadata.so > dist/plugin-buildinfo.txt
{
 printf 'Bifrost=%s\nSource=%s\nCore=%s\nGo=%s\nGOOS=%s\nGOARCH=%s\nGOAMD64=%s\nCGO_ENABLED=%s\nTags=%s\nBuilder=%s\n' "$BIFROST_VERSION" "$BIFROST_SOURCE" "$CORE_VERSION" "$GO_VERSION" "$GOOS" "$GOARCH" "$GOAMD64" "$CGO_ENABLED" "$BUILD_TAGS" "$GO_BUILDER"
 gcc --version | head -1
 apk info -v gcc musl musl-dev binutils || true
 sha256sum .build/host.mod .build/host.sum
} > dist/build-environment.txt
printf 'Built %s\n' "$ROOT/dist/bifrost-cliproxyapi-metadata.so"
