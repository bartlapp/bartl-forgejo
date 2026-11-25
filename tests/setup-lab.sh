#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
if [[ $(uname -s) != Linux || ${BARTL_FORGEJO_LAB:-} != hetzner || ${BARTL_FORGEJO_DISPOSABLE_CLUSTER:-} != yes ]]; then
  printf '%s\n' 'Refused: Linux, BARTL_FORGEJO_LAB=hetzner and BARTL_FORGEJO_DISPOSABLE_CLUSTER=yes required.' >&2
  exit 2
fi
if [[ $# != 0 ]]; then
  printf '%s\n' 'Usage: BARTL_FORGEJO_LAB=hetzner BARTL_FORGEJO_DISPOSABLE_CLUSTER=yes BARTL_FORGEJO_HOST=<forgejo-DNS> BARTL_FORGEJO_TARGET_HOST=<target-DNS> BARTL_KEYCLOAK_HOST=<keycloak-DNS> BARTL_LAB_IPV4=<public-IPv4> [LAB_QUALIFICATION_DIR=/absolute/remote/path] bash tests/setup-lab.sh' >&2
  exit 2
fi
: "${BARTL_FORGEJO_HOST:?Set the public Forgejo DNS name}"
: "${BARTL_FORGEJO_TARGET_HOST:?Set the distinct nonproductive target Forgejo DNS name}"
: "${BARTL_KEYCLOAK_HOST:?Set the public Keycloak DNS name}"
: "${BARTL_LAB_IPV4:?Set the disposable lab public IPv4 address}"
for prerequisite in node kubectl helm yq openssl dig; do
  command -v "$prerequisite" >/dev/null || { printf 'Missing prerequisite: %s\n' "$prerequisite" >&2; exit 2; }
done
umask 077
exec node "$(dirname "${BASH_SOURCE[0]}")/setup-lab.mjs"
