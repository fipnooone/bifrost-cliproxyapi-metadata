#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
mkdir -p .build/upstream
base="https://raw.githubusercontent.com/maximhq/bifrost/$BIFROST_SOURCE/transports"
fetch_verified() {
 local file=$1 expected=$2 target=".build/upstream/$1"
 if [[ -f "$target" && $(git hash-object "$target") == "$expected" ]]; then return; fi
 local temp; temp=$(mktemp ".build/upstream/.${file}.XXXXXX")
 if ! curl --fail --silent --show-error --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 120 "$base/$file" -o "$temp"; then
  rm -f "$temp"; fail "Could not fetch pinned upstream $file (network required on first preparation)."
 fi
 if [[ $(git hash-object "$temp") != "$expected" ]]; then rm -f "$temp"; fail "Upstream $file does not match its pinned Git blob hash."; fi
 mv "$temp" "$target"
}
fetch_verified go.mod "$HOST_MOD_BLOB"
fetch_verified go.sum "$HOST_SUM_BLOB"
[[ $(awk '$1=="go"{print $2}' .build/upstream/go.mod) == "$GO_VERSION" ]] || fail "Go version differs from target.env."
[[ $(awk '$1=="github.com/maximhq/bifrost/core"{print $2}' .build/upstream/go.mod) == "$CORE_VERSION" ]] || fail "Core version differs from target.env."
! grep -Eq '^[[:space:]]*replace([[:space:]]|$)' .build/upstream/go.mod || fail "Upstream added replace directives; re-audit before building."
# Keep EVERY host requirement, including indirect requirements. MVS can select
# different common-package versions when only core is pinned. No go mod tidy.
sed 's|^module github.com/maximhq/bifrost/transports/v2$|module bifrost-cliproxyapi-metadata|' .build/upstream/go.mod > .build/host.mod.new
if [[ $(head -n 1 .build/host.mod.new) != 'module bifrost-cliproxyapi-metadata' ]]; then
 rm -f .build/host.mod.new
 fail "Unexpected upstream module identity; re-audit before building."
fi
mv .build/host.mod.new .build/host.mod
cp .build/upstream/go.sum .build/host.sum
# Check the small editor/module facade, or regenerate it during a deliberate upgrade.
if [[ ${1:-} == --update-facade ]]; then
 cat > go.mod <<MOD
// Generated compatibility facade; run make prepare before using Go commands.
// Production builds use the full host graph in .build/host.mod, not just core.
module bifrost-cliproxyapi-metadata

go $GO_VERSION

require github.com/maximhq/bifrost/core $CORE_VERSION
MOD
 grep -E "^github.com/maximhq/bifrost/core ${CORE_VERSION//./\.}(/go.mod)? " .build/upstream/go.sum > go.sum
fi
[[ $(awk '$1=="go"{print $2}' go.mod) == "$GO_VERSION" ]] || fail "Regenerate go.mod with scripts/prepare.sh --update-facade."
[[ $(awk '$1=="require" && $2=="github.com/maximhq/bifrost/core"{print $3}' go.mod) == "$CORE_VERSION" ]] || fail "go.mod core pin drifted."
printf 'Prepared Bifrost %s / core %s / Go %s host dependency graph.\n' "$BIFROST_VERSION" "$CORE_VERSION" "$GO_VERSION"
