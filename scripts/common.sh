#!/usr/bin/env bash
# shellcheck shell=bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"
set -a
# shellcheck source=../target.env
source "$ROOT/target.env"
set +a
export GOWORK=off GOTOOLCHAIN=local GOEXPERIMENT=
export GOFLAGS="-modfile=$ROOT/.build/host.mod -mod=readonly -trimpath -tags=$BUILD_TAGS"
if [[ -n "${BIFROST_IMAGE_DIGEST:-}" ]]; then
  BIFROST_IMAGE="${BIFROST_IMAGE_REPO}@${BIFROST_IMAGE_DIGEST}"
else
  BIFROST_IMAGE="${BIFROST_IMAGE_REPO}:${BIFROST_IMAGE_TAG}"
fi
export BIFROST_IMAGE
fail() { printf '%s\n' "$*" >&2; exit 1; }
check_go() {
 [[ $(go env GOVERSION) == "go${GO_VERSION}" ]] || fail "Exact Go ${GO_VERSION} required; installed: $(go env GOVERSION). Use the pinned build container."
 [[ $(go env GOHOSTOS)/$(go env GOHOSTARCH) == "$GOOS/$GOARCH" ]] || fail "Native $GOOS/$GOARCH builder required."
}
