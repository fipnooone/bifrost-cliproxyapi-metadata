#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
command -v docker >/dev/null || fail "Docker is required for the pinned build environment. No Docker is needed on the Bifrost server for plugin compilation."
[[ $# -gt 0 ]] || fail "Usage: scripts/container.sh make native-test [native-test-race native-build]"
mkdir -p .build/gomod .build/gocache
builder="bifrost-cliproxyapi-builder:${GO_VERSION}-${GOARCH}"
docker build --platform "$GOOS/$GOARCH" --build-arg "GO_BUILDER=$GO_BUILDER" -f scripts/Dockerfile.builder -t "$builder" .
# Run as the invoking user so CI artifacts can be cleaned/cached without sudo.
docker run --rm --platform "$GOOS/$GOARCH" --user "$(id -u):$(id -g)" \
 --mount "type=bind,src=$ROOT,dst=/workspace" \
 -e HOME=/tmp -e GOMODCACHE=/workspace/.build/gomod -e GOCACHE=/workspace/.build/gocache \
 "$builder" "$@"
