# syntax=docker/dockerfile:1

# VARIANT selects the server build: "pure" (default, CGO_ENABLED=0) or "native"
# (links the opus_mlow SMPL encoder via the nativemlow build tag, static CGO).
# Stage selection happens at FROM level so a pure build never clones or compiles
# the native toolchain.

ARG VARIANT=pure

# ── Stage 1: web client ──────────────────────────────────────────────
FROM node:22-bookworm-slim AS web
WORKDIR /web
COPY client/package.json client/package-lock.json ./
RUN npm ci
COPY client/ ./
RUN npm run build

# ── Stage 2: build opus_mlow from source (only used by native) ───────
FROM golang:1.26-bookworm AS libopusmlow
ARG OPUS_MLOW_SHA=93e91a74c0a2af610d8313a85e2c811081a73f93
RUN apt-get update \
    && apt-get install -y --no-install-recommends git cmake ninja-build gcc g++ \
    && rm -rf /var/lib/apt/lists/*
RUN git clone https://github.com/edgardmessias/opus_mlow.git /opus_mlow \
    && cd /opus_mlow && git checkout "${OPUS_MLOW_SHA}" \
    && cmake -B build -G Ninja -DCMAKE_BUILD_TYPE=Release \
    && cmake --build build \
    && mkdir -p /opus/lib /opus/include \
    && cp build/libopus.a /opus/lib/ \
    && cp -r include/* /opus/include/

# ── Stage 3: shared Go source (both variants) ────────────────────────
FROM golang:1.26-bookworm AS srcbase
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /web/dist ./client/dist

# ── Stage 4a: pure-Go build (CGO_ENABLED=0) ──────────────────────────
FROM srcbase AS build-pure
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/wacalls ./cmd/server

# ── Stage 4b: native CGO build with opus_mlow (static link) ─────────
FROM srcbase AS build-native
ARG VERSION=docker
COPY --from=libopusmlow /opus /opus
RUN apt-get update && apt-get install -y --no-install-recommends gcc g++ && rm -rf /var/lib/apt/lists/*
RUN CGO_ENABLED=1 \
    CGO_CFLAGS="-I/opus/include" \
    CGO_LDFLAGS="-L/opus/lib" \
    go build -trimpath -tags nativemlow \
    -ldflags "-s -w -X main.version=${VERSION} -extldflags '-static'" \
    -o /out/wacalls ./cmd/server

# ── Stage 5: select the right build ──────────────────────────────────
FROM build-${VARIANT} AS build

# ── Stage 6: runtime ─────────────────────────────────────────────────
FROM debian:bookworm-slim
ARG VERSION=docker
LABEL org.opencontainers.image.source="https://github.com/whazing/WaCalls" \
      org.opencontainers.image.title="WaCalls" \
      org.opencontainers.image.description="WhatsApp voice calls server" \
      org.opencontainers.image.version="${VERSION}"
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -d /app -s /sbin/nologin app \
    && mkdir -p /data && chown app:app /data
WORKDIR /app
COPY --from=build /out/wacalls ./wacalls
COPY --from=web /web/dist ./client/dist
USER app
EXPOSE 8080 5000
VOLUME ["/data"]
ENTRYPOINT ["/app/wacalls"]
CMD ["-addr", ":8080", "-db", "/data/wacalls.db", "-static", "client/dist"]
