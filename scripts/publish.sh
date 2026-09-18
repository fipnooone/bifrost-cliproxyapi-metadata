#!/usr/bin/env bash
# Uses only the workflow's built-in GitHub token, never a CLIProxyAPI credential.
set -euo pipefail
source "$(dirname "$0")/common.sh"
version=${1:-}
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "Invalid release version."
[[ -n ${GITHUB_REPOSITORY:-} ]] || fail "GITHUB_REPOSITORY must be owner/repo."
[[ $(git rev-parse HEAD) == "$(git rev-parse "refs/tags/$version^{commit}")" ]] || fail "Checkout does not match the release tag."
[[ $(gh repo view "$GITHUB_REPOSITORY" --json isPrivate --jq .isPrivate) == false ]] || fail "Direct HTTPS distribution requires a public repository."
./scripts/release-files.sh "$version"
asset=$(cat dist/ASSET_NAME)
files=("$asset" SHA256SUMS BUILDINFO.txt host-smoke.json)
if gh release view "$version" --repo "$GITHUB_REPOSITORY" >/dev/null 2>&1; then
 # Safe reruns only: verify every existing asset; never use --clobber.
 temp=$(mktemp -d); trap 'rm -rf "$temp"' EXIT
 for file in "${files[@]}"; do
  gh release download "$version" --repo "$GITHUB_REPOSITORY" --pattern "$file" --dir "$temp"
  cmp "dist/$file" "$temp/$file" || fail "Existing release asset differs. Publish a new tag; do not overwrite."
 done
else
 assets=();for file in "${files[@]}"; do assets+=("dist/$file");done
 # A public prerelease allows exercising unauthenticated HTTPS download before
 # this release is marked stable. All binaries have ALREADY passed local-image tests.
 gh release create "$version" "${assets[@]}" --repo "$GITHUB_REPOSITORY" --verify-tag \
  --prerelease --title "$version — Bifrost $BIFROST_VERSION ($GOOS/$GOARCH)" --notes-file dist/RELEASE_NOTES.md
fi
export PLUGIN_URL="https://github.com/$GITHUB_REPOSITORY/releases/download/$version/$asset"
python3 scripts/smoke.py
# Only a successful public-URL load is promoted to a stable release.
gh release edit "$version" --repo "$GITHUB_REPOSITORY" --prerelease=false
printf 'Published and URL-tested: %s\n' "$PLUGIN_URL"
