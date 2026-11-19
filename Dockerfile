# ARM64 (TrimUI tg5040) build environment of the pak, as in Cheat Manager.
# See scripts/setup-toolchain.sh.
#   docker build --platform linux/amd64 -t reminislink-build .
FROM debian:bullseye
WORKDIR /src
COPY go.mod scripts/setup-toolchain.sh /tmp/toolchain/
RUN cd /tmp/toolchain && sh ./setup-toolchain.sh && rm -rf /tmp/toolchain
ENV GOTOOLCHAIN=local
