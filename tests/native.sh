#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
if [[ $(uname -s) != Linux || ${BARTL_FORGEJO_LAB:-} != hetzner ]]; then
  printf '%s\n' '{"event":"refused","reason":"Linux and BARTL_FORGEJO_LAB=hetzner are required"}' >&2
  exit 2
fi
if [[ $# != 1 || $1 != /* ]]; then
  printf '%s\n' '{"event":"refused","reason":"usage: tests/native.sh /absolute/empty/remote/workdir"}' >&2
  exit 2
fi
for prerequisite in node curl git git-lfs ssh ssh-keygen ssh-keyscan tar sha256sum; do
  if ! command -v "$prerequisite" >/dev/null; then
    printf '{"event":"missing-prerequisite","command":"%s"}\n' "$prerequisite" >&2
    exit 2
  fi
done
exec node "$(dirname "${BASH_SOURCE[0]}")/native-fixture.mjs" "$1"
