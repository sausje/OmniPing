#!/bin/sh
# Builds the Linux program for each architecture and packs it as a .deb and
# an .rpm (packaging/linux/nfpm.yaml). The files have no version in their
# names, so the newest release always has the same download links.
# Usage: .github/build-packages.sh 1.2.3 amd64 arm64 arm7
set -eu
v=$1
shift
mkdir -p build dist
GOBIN="$PWD/build/bin" go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0

for arch in "$@"; do
	case $arch in
	amd64) goarch=amd64 deb=amd64 rpm=x86_64 ;;
	arm64) goarch=arm64 deb=arm64 rpm=aarch64 ;;
	arm7) goarch=arm deb=armhf rpm=armv7hl ;;
	*) echo "unknown architecture $arch" >&2; exit 1 ;;
	esac
	CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOARM=7 \
		go build -trimpath -ldflags="-s -w -X main.version=$v" -o build/omniping .
	ARCH=$arch VERSION=$v build/bin/nfpm package --config packaging/linux/nfpm.yaml --packager deb --target "dist/omniping_$deb.deb"
	ARCH=$arch VERSION=$v build/bin/nfpm package --config packaging/linux/nfpm.yaml --packager rpm --target "dist/omniping.$rpm.rpm"
done
