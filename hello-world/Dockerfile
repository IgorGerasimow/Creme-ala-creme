# Build stage
FROM golang:1.26 AS build
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Produce a static binary with version injected at build time.
# TARGETOS/TARGETARCH are populated by `docker buildx` for multi-arch builds.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/app .

# Runtime stage
FROM gcr.io/distroless/static:nonroot
USER 65532:65532
WORKDIR /
COPY --from=build /out/app /app
# Migrations (if used at runtime)
COPY migrations /migrations
EXPOSE 8080
# Hardened runtime: no shell, non-root, read-only fs
ENTRYPOINT ["/app"]
