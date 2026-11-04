# SPDX-License-Identifier: Apache-2.0
FROM docker.io/library/golang:1.25.4-bookworm AS builder
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY cmd ./cmd
COPY api ./api
COPY internal ./internal
RUN --mount=type=cache,id=bartl-forgejo-go-build,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/controller ./cmd/controller \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/forgejo-worker ./cmd/worker
RUN mkdir -p /out/licenses \
 && find /go/pkg/mod -type f \( -iname 'LICENSE*' -o -iname 'NOTICE*' -o -iname 'COPYING*' \) -exec cp --parents '{}' /out/licenses/ \;

FROM docker.io/library/debian:bookworm-slim
ARG TARGETARCH=amd64
RUN test "$TARGETARCH" = amd64 \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git curl bzip2 \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 1000 git \
 && useradd --uid 1000 --gid 1000 --home-dir /data/home --no-create-home --shell /bin/sh git \
 && mkdir -p /usr/local/lib/forgejo/16.0.7 /usr/local/lib/forgejo/17.0.1 /usr/local/share/licenses/forgejo /usr/local/share/forgejo-source \
 && curl -fL --retry 3 https://codeberg.org/forgejo/forgejo/releases/download/v16.0.7/forgejo-16.0.7-linux-amd64 -o /usr/local/lib/forgejo/16.0.7/forgejo \
 && echo 'ca8ecb37da9210d5730c878308bf31e25b6b526a7294b263d07f6c8718af07b7  /usr/local/lib/forgejo/16.0.7/forgejo' | sha256sum -c - \
 && curl -fL --retry 3 https://codeberg.org/forgejo/forgejo/releases/download/v17.0.1/forgejo-17.0.1-linux-amd64 -o /usr/local/lib/forgejo/17.0.1/forgejo \
 && echo '51a8cdf4a1252e79f4b3855d7a7891af6c6635105420768c460e2f0690385d6f  /usr/local/lib/forgejo/17.0.1/forgejo' | sha256sum -c - \
 && chmod 0755 /usr/local/lib/forgejo/*/forgejo \
 && curl -fL --retry 3 https://github.com/restic/restic/releases/download/v0.19.1/restic_0.19.1_linux_amd64.bz2 -o /tmp/restic.bz2 \
 && echo 'f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c  /tmp/restic.bz2' | sha256sum -c - \
 && bzip2 -dc /tmp/restic.bz2 > /usr/local/bin/restic \
 && chmod 0755 /usr/local/bin/restic \
 && rm /tmp/restic.bz2 \
 && for version in 16.0.7 17.0.1; do \
      curl -fL --retry 3 "https://codeberg.org/forgejo/forgejo/raw/tag/v$version/LICENSE" -o "/usr/local/share/licenses/forgejo/LICENSE-$version" \
      && curl -fL --retry 3 "https://codeberg.org/forgejo/forgejo/archive/v$version.tar.gz" -o "/usr/local/share/forgejo-source/forgejo-$version.tar.gz" || exit 1; \
    done \
 && mkdir -p /usr/local/share/licenses/restic \
 && curl -fL --retry 3 https://raw.githubusercontent.com/restic/restic/v0.19.1/LICENSE -o /usr/local/share/licenses/restic/LICENSE \
 && curl -fL --retry 3 https://github.com/restic/restic/releases/download/v0.19.1/restic-0.19.1.tar.gz -o /usr/local/share/licenses/restic/restic-0.19.1.tar.gz \
 && echo 'bb9b1a19040744d26d8a79be029d4e6b189c45ccc9d8831d7fe367d3c33df725  /usr/local/share/licenses/restic/restic-0.19.1.tar.gz' | sha256sum -c - \
 && mkdir -p /data /staging \
 && chown 1000:1000 /data /staging
COPY --from=builder /out/controller /out/forgejo-worker /usr/local/bin/
COPY --from=builder /out/licenses/ /usr/local/share/licenses/go-modules/
COPY LICENSE NOTICE /usr/local/share/licenses/bartl-forgejo/
ARG SOURCE_REVISION
ARG SOURCE_SHA256
LABEL org.opencontainers.image.title="bartl-forgejo" \
      org.opencontainers.image.revision="$SOURCE_REVISION" \
      sh.bartl.source.archive.sha256="$SOURCE_SHA256" \
      org.opencontainers.image.licenses="Apache-2.0 AND GPL-3.0-or-later"
ENV HOME=/data/home DATA_DIR=/data STAGING_DIR=/staging CONFIG_FILE=/configuration/app.ini
USER 1000:1000
WORKDIR /data
EXPOSE 3000 2222
ENTRYPOINT ["/usr/local/bin/forgejo-worker"]
CMD ["serve"]
