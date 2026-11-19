#!/bin/sh
# Installs the ARM64 cross-compilation toolchain of the pak on Debian 11
# (bullseye) amd64. Copied from scripts/setup-toolchain.sh of
# github.com/tfournier/nextui-cheat-manager (same author).
#
# Debian 11 is used on purpose: its glibc and SDL2 versions are old enough for
# the binary to run on the TrimUI firmware (this is what other NextUI paks do).
# Used by the Dockerfile and by the GitHub Actions build job.
#
#   GO_VERSION   Go version to install (default: the "go" line of go.mod)
set -eu

GO_VERSION="${GO_VERSION:-}"
if [ -z "$GO_VERSION" ] && [ -f go.mod ]; then
    GO_VERSION="$(sed -n 's/^go \([0-9.]*\)$/\1/p' go.mod)"
fi
if [ -z "$GO_VERSION" ]; then
    echo "GO_VERSION is not set and go.mod has no go line" >&2
    exit 1
fi

# Debian 11 is end-of-life: its packages now live on archive.debian.org.
cat >/etc/apt/sources.list <<SOURCES
deb http://archive.debian.org/debian bullseye main
deb http://archive.debian.org/debian bullseye-updates main
deb http://archive.debian.org/debian-security bullseye-security main
SOURCES
rm -f /etc/apt/sources.list.d/*

dpkg --add-architecture arm64
apt-get -o Acquire::Check-Valid-Until=false update
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates curl make zip git pkg-config \
    crossbuild-essential-arm64 \
    libsdl2-dev:arm64 libsdl2-ttf-dev:arm64 libsdl2-image-dev:arm64 libsdl2-gfx-dev:arm64
rm -rf /var/lib/apt/lists/*

curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
ln -sf /usr/local/go/bin/go /usr/local/bin/go
ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
go version
