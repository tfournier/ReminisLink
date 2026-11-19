# Image of "make screenshots": the desktop UI under Xvfb (native SDL2 of
# Debian 11, linux/arm64 or linux/amd64).
FROM debian:bullseye
ARG GO_VERSION
RUN printf 'deb http://archive.debian.org/debian bullseye main\ndeb http://archive.debian.org/debian-security bullseye-security main\n' > /etc/apt/sources.list && \
    rm -f /etc/apt/sources.list.d/* && \
    apt-get -o Acquire::Check-Valid-Until=false update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates curl gcc libc6-dev pkg-config git \
        libsdl2-dev libsdl2-ttf-dev libsdl2-image-dev libsdl2-gfx-dev \
        xvfb xdotool imagemagick && \
    rm -rf /var/lib/apt/lists/*
RUN arch="$(dpkg --print-architecture)" && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" | tar -C /usr/local -xz && \
    ln -s /usr/local/go/bin/go /usr/local/bin/go
ENV GOTOOLCHAIN=local
