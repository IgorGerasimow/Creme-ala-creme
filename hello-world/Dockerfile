# Build stage
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Produce a static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/app ./main.go

# Runtime stage
FROM gcr.io/distroless/static
USER 65532:65532
WORKDIR /
COPY --from=build /out/app /app
# Migrations (if used at runtime)
COPY migrations /migrations
EXPOSE 8080
# Hardened runtime: no shell, non-root, read-only fs
ENTRYPOINT ["/app"]
