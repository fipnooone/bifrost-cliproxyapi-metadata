#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"
check_go
[[ -f .build/host.mod && -f .build/host.sum ]] || fail "Run make prepare first."
exec go "$@"
