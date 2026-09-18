#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
version=${1:-}
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "Provide a version such as v0.1.0."
[[ -f dist/bifrost-cliproxyapi-metadata.so && -f dist/host-smoke.json ]] || fail "Run make validate successfully before packaging a release."
# A stale smoke result must not authorize publishing a newer/different binary.
python3 - <<'PY'
import hashlib,json
from pathlib import Path
root=Path('.')
record=json.loads((root/'dist/host-smoke.json').read_text())
sha=hashlib.sha256((root/'dist/bifrost-cliproxyapi-metadata.so').read_bytes()).hexdigest()
target=dict(line.split('=',1) for line in (root/'target.env').read_text().splitlines() if line and not line.startswith('#'))
if record.get('status')!='passed' or record.get('plugin_sha256')!=sha or record.get('target')!=target:
    raise SystemExit('Smoke result is not a passing result for this exact binary and target.')
PY
asset="bifrost-cliproxyapi-metadata-${version}-bifrost-${BIFROST_VERSION}-${GOOS}-${GOARCH}.so"
cp dist/bifrost-cliproxyapi-metadata.so "dist/$asset"
{
 printf 'Plugin: %s\nBifrost: %s\nCore: %s\nGo: %s\nOS: %s\nArch: %s\nSource: %s\n' "$version" "$BIFROST_VERSION" "$CORE_VERSION" "$GO_VERSION" "$GOOS" "$GOARCH" "$BIFROST_SOURCE"
 cat dist/build-environment.txt dist/plugin-buildinfo.txt dist/host-buildinfo.txt
} > dist/BUILDINFO.txt
(cd dist && sha256sum "$asset" BUILDINFO.txt host-smoke.json > SHA256SUMS)
cat > dist/RELEASE_NOTES.md <<NOTES
## Compatibility

Plugin: **$version**  
Target Bifrost: **$BIFROST_VERSION** (core **$CORE_VERSION**)  
Go: **$GO_VERSION**  
Platform: **$GOOS/$GOARCH**, CGO=1, musl, GOAMD64=$GOAMD64  
Build tags: **$BUILD_TAGS**  
Bifrost source: \`$BIFROST_SOURCE\`

This artifact passed unit tests, race tests, and an actual-image native-catalog
smoke test before upload. See host-smoke.json for the tested image identity;
BUILDINFO.txt records shared-module/toolchain build information. This is not a
universal binary for other Bifrost versions, Enterprise builds or libc variants.

Verify SHA256SUMS before adoption. Use the immutable URL for this version,
not a latest-download URL. Restart Bifrost after updating the plugin path.
NOTES
printf '%s\n' "$asset" > dist/ASSET_NAME
printf 'Packaged %s\n' "$asset"
