# OmniPing image for amd64, arm64 (Raspberry Pi 3/4/5 with a 64-bit OS) and
# arm/v7 (32-bit Raspberry Pi OS). GitHub Actions builds and publishes it; to
# build it yourself:
#   docker build -t omniping .
#   docker buildx build --platform linux/arm64 -t omniping .

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/omniping .

FROM alpine:3.24
# ca-certificates for HTTPS notifications (ntfy, Telegram, ...), tzdata for
# local times in alerts, tini to pass on stop signals and reap script processes
RUN apk add --no-cache ca-certificates tzdata tini \
 && addgroup -g 1000 omniping \
 && adduser -D -H -u 1000 -G omniping -s /sbin/nologin omniping \
 && mkdir /data && chown omniping:omniping /data
COPY --from=build /out/omniping /usr/local/bin/omniping

# OmniPing starts as root only to open its ping sockets and port, hands /data
# to PUID:PGID and then runs as that user. PUID=0 keeps it running as root.
ENV OMNIPING_DATA=/data \
    OMNIPING_PORT=8080 \
    PUID=1000 \
    PGID=1000
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s \
  CMD ["/usr/local/bin/omniping", "healthcheck"]
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/omniping"]

ARG VERSION=dev
LABEL org.opencontainers.image.title="OmniPing" \
      org.opencontainers.image.description="Pings many hosts at once and graphs the results live in your browser" \
      org.opencontainers.image.source="https://github.com/sausje/omniping" \
      org.opencontainers.image.version="$VERSION"
