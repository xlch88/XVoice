#!/usr/bin/env bash
# Builds dist/xvoice-<os>-<arch>-<version>[.exe]. Pure Go: no cgo, no C compiler.
#
#   ./build.sh                 # version from git, default targets
#   ./build.sh v1.2.0          # explicit version
#   TARGETS="windows/amd64" ./build.sh
set -euo pipefail
cd "$(dirname "$0")"

version="${1:-$(git describe --tags --always --dirty)}"
# Only Windows has a platform layer so far.
targets="${TARGETS:-windows/amd64 windows/arm64}"

export CGO_ENABLED=0
go vet ./...
mkdir -p dist
for target in $targets; do
	os="${target%/*}" arch="${target#*/}"
	output="dist/xvoice-$os-$arch-$version" flags="-s -w"
	if [ "$os" = windows ]; then
		output+=".exe"
		flags+=" -H windowsgui" # No console window.
	fi
	GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$flags" -o "$output" ./cmd/xvoice
	echo "$output"
done
