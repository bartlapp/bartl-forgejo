#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "$ROOT"
[[ $(uname -s) == Linux && $(uname -m) == x86_64 && ${BARTL_FORGEJO_LAB:-} == hetzner ]] || {
  printf '%s\n' 'Build only on the authorized Hetzner Linux amd64 lab (BARTL_FORGEJO_LAB=hetzner).' >&2
  exit 1
}
for tool in git podman skopeo helm yq sha256sum tar; do
  command -v "$tool" >/dev/null || { printf 'Missing tool: %s\n' "$tool" >&2; exit 1; }
done
[[ $(yq --version) == *'version v4.'* ]] || { printf '%s\n' 'Mike Farah yq v4 is required.' >&2; exit 1; }
# Snapshot tracked and nonignored working files without changing the user's index
# or pretending an uncommitted implementation is a verified commit.
BASE_REVISION=$(git rev-parse HEAD)
SNAPSHOT=$(mktemp -d)
trap 'rm -rf "$SNAPSHOT"' EXIT
export GIT_INDEX_FILE="$SNAPSHOT/index"
git read-tree HEAD
git add --all
SOURCE_TREE=$(git write-tree)
rm "$GIT_INDEX_FILE"
unset GIT_INDEX_FILE
git archive --format=tar "$SOURCE_TREE" | tar -xf - -C "$SNAPSHOT"
RELEASE=$(yq -r '.version' "$SNAPSHOT/chart/Chart.yaml")
IMAGE_REPOSITORY=${IMAGE_REPOSITORY:-ghcr.io/bartlapp/bartl-forgejo-controller}
CANDIDATE="$IMAGE_REPOSITORY:$RELEASE-$SOURCE_TREE"
OUT=${OUT:-$ROOT/dist}
# An existing destination is never rebuilt or overwritten. Retain it through qualification/publication.
mkdir "$OUT"
OUT=$(cd "$OUT" && pwd)
printf '%s\n' "$BASE_REVISION" > "$OUT/base-revision"
printf '%s\n' "$SOURCE_TREE" > "$OUT/source-tree"
printf '%s\n' "$RELEASE" > "$OUT/package-version"
git archive --format=tar.gz --output="$OUT/source.tar.gz" "$SOURCE_TREE"
SOURCE_SHA256=$(sha256sum "$OUT/source.tar.gz")
SOURCE_SHA256=${SOURCE_SHA256%% *}
printf '%s\n' "$SOURCE_SHA256" > "$OUT/source.sha256"
podman build --platform linux/amd64 --format oci --build-arg "SOURCE_REVISION=$SOURCE_TREE" \
  --build-arg "SOURCE_SHA256=$SOURCE_SHA256" -t "$CANDIDATE" "$SNAPSHOT"
skopeo copy --preserve-digests --digestfile "$OUT/image.digest" "containers-storage:$CANDIDATE" "oci:$OUT/image:candidate"
DIGEST=$(< "$OUT/image.digest")
export PINNED_IMAGE="$IMAGE_REPOSITORY@$DIGEST"
printf '%s\n' "$PINNED_IMAGE" > "$OUT/image-reference"
cp -R "$SNAPSHOT/chart" "$OUT/chart"
yq -i '.controller.image = strenv(PINNED_IMAGE)' "$OUT/chart/defaults.yaml"
yq '{"services": {"forgejo": {"controller": {"image": .controller.image}}}}' "$OUT/chart/defaults.yaml" > "$OUT/image-values.yaml"
helm package "$OUT/chart" --destination "$OUT"
(cd "$OUT" && sha256sum -- *.tgz source.tar.gz base-revision source-tree source.sha256 package-version image.digest image-reference image-values.yaml > SHA256SUMS)
printf 'Retained immutable candidate: %s\n' "$OUT"
printf '%s\n' 'Qualify this OCI image and chart. Retain qualification alongside SHA256SUMS; publish by digest-preserving copy, never rebuild.'
