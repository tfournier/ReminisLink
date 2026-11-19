# ARM64 (TrimUI tg5040) build environment of the pak, as in Cheat Manager.
# See scripts/setup-toolchain.sh.
#   docker build --platform linux/amd64 -t reminislink-build .
# debian:bullseye, pinned by digest (multi-architecture index).
FROM debian:bullseye@sha256:6f519a81440354a85eb592c5f32109ab80605f6b892455983a6f618bf87fabe9
WORKDIR /src
COPY go.mod scripts/setup-toolchain.sh /tmp/toolchain/
RUN cd /tmp/toolchain && sh ./setup-toolchain.sh && rm -rf /tmp/toolchain
ENV GOTOOLCHAIN=local
