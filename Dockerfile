# Build in the specified golang:1.22 image; the whole service runs in a
# single container with a local file repository (no database process).
FROM golang:1.22 AS build
WORKDIR /src

# Cache dependencies first (the module currently has no external deps).
COPY go.mod ./
RUN go mod download

COPY . .
# Static binary so the final image needs no libc toolchain.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/cpm-server ./cmd/cpm-server

FROM golang:1.22
WORKDIR /app
COPY --from=build /out/cpm-server /app/cpm-server

# Job files live here; mount a volume to persist across container restarts.
# Owned by the unprivileged runtime user (named volumes inherit this owner).
RUN mkdir -p /data && chown nobody:nogroup /data
ENV CPM_ADDR=:8080
ENV CPM_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

USER nobody
ENTRYPOINT ["/app/cpm-server"]
