# Official multi-platform image digests, verified 2026-10-10.
ARG GO_IMAGE=golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61
ARG RUNTIME_IMAGE=debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG VERSION=container-dev
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
RUN go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" -o /out/sloplex ./cmd/sloplex

FROM ${RUNTIME_IMAGE}
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg curl ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /config /data/transcode /media \
    && chown -R 65532:65532 /data
COPY --from=build /out/sloplex /usr/local/bin/sloplex
COPY deploy/sloplex.container.json /config/sloplex.json
# A safe image default, not a required NAS UID. Compose requires an explicit UID/GID.
USER 65532:65532
WORKDIR /data
ENV TMPDIR=/tmp
EXPOSE 32400/tcp
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD curl --fail --silent --output /dev/null http://127.0.0.1:32400/identity || exit 1
# Last, so a new version reuses the cached package layer.
ARG VERSION=container-dev
ARG REVISION=unknown
ARG CREATED
LABEL org.opencontainers.image.title="Sloplex" \
      org.opencontainers.image.description="A self-hosted media server for Plex-compatible apps" \
      org.opencontainers.image.source="https://github.com/mtdewwolf/sloplex" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"
ENTRYPOINT ["/usr/local/bin/sloplex"]
CMD ["-config", "/config/sloplex.json"]
